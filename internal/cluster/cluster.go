// Package cluster is the Enterprise multi-server feature: one OpenAdmin (master) proxies the admin panels of the others (slaves), every server stays a standalone install.
package cluster

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

const (
	RoleMaster = "master"
	RoleSlave  = "slave"
	ModeManual = "manual"
	ModeFollow = "follow"

	joinCodePrefix = "opjoin-"
	joinCodeTTL    = 30 * time.Minute
	readTimeout    = 10 * time.Second // most a list or status read waits on one server, writes keep the client's own limit
	claimTTL       = 60 * time.Second
	offlineAfter   = 3               // failed health polls in a row before a server counts as offline
	probeTimeout   = 3 * time.Second // status answers in time even while mysql or podman hang
)

// Dir holds all cluster state, var so tests can point it at a scratch dir
var Dir = "/etc/openpanel/openadmin/cluster"

type Node struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	SPKI    string    `json:"spki"`
	TLSName string    `json:"tls_name,omitempty"`
	Secret  string    `json:"secret"`
	AddedAt time.Time `json:"added_at"`
	// a paused server stays in the cluster but the master leaves it out of everything until it's resumed
	Paused bool `json:"paused,omitempty"`
}

type State struct {
	Role string `json:"role,omitempty"`

	// master side
	SeedHex    string            `json:"seed,omitempty"`
	SelfURL    string            `json:"self_url,omitempty"`
	Nodes      []Node            `json:"nodes,omitempty"`
	LastSynced map[string]string `json:"last_synced,omitempty"`
	Jobs       []*Job            `json:"jobs,omitempty"`

	// slave side
	NodeID        string    `json:"node_id,omitempty"`
	NodeSecret    string    `json:"node_secret,omitempty"`
	MasterName    string    `json:"master_name,omitempty"`
	MasterURL     string    `json:"master_url,omitempty"`
	MasterSPKI    string    `json:"master_spki,omitempty"`
	MasterTLSName string    `json:"master_tls_name,omitempty"`
	MasterPubHex  string    `json:"master_pub,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	Pinned        []string  `json:"pinned,omitempty"`
	Groups        []string  `json:"groups,omitempty"`
	LastRev       string    `json:"last_rev,omitempty"`
	LastSyncAt    time.Time `json:"last_sync_at,omitempty"`
	LastSyncMsg   string    `json:"last_sync_msg,omitempty"`
	ClusterSize   int       `json:"cluster_size,omitempty"`
}

// Manager is the one cluster instance of this process
type Manager struct {
	Sessions *auth.Manager
	DB       *admindb.DB
	MySQL    *sql.DB
	Licensed func() bool
	// DashboardLayout is the admin's saved dashboard layout as json, "null" when there's none
	DashboardLayout   func(userID int64) string
	AdminPort         string
	PublicIP          string
	Hostname          string
	Version           string
	ValidateSessionIP bool
	Notifications     func() []NotificationBrief
	LatestSource      func() string
	Summaries         func() any
	DashCounts        func() any

	mu     sync.Mutex
	state  State
	loaded bool

	seenMu sync.Mutex
	seen   map[string]time.Time

	sessMu   sync.Mutex
	sessions map[string]*nodeSession

	healthMu sync.RWMutex
	health   map[string]NodeHealth
	fails    map[string]int

	clientMu sync.Mutex
	clients  map[string]*http.Client
	// rpcTimeout overrides the 30s rpc timeout, opencli runs from a shell need much longer
	rpcTimeout time.Duration

	joinMu sync.Mutex
}

func statePath() string { return filepath.Join(Dir, "cluster.json") }
func joinPath() string  { return filepath.Join(Dir, "join.json") }

// State returns a copy of the current state so callers never touch it without the lock
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	return m.state
}

func (m *Manager) loadLocked() {
	if m.loaded {
		return
	}
	m.loaded = true
	raw, err := os.ReadFile(statePath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, &m.state)
}

// update applies fn under the lock and persists the result, fn returning an error leaves disk untouched
func (m *Manager) update(fn func(*State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	if err := fn(&m.state); err != nil {
		return err
	}
	return writeJSONFile(statePath(), m.state)
}

func writeJSONFile(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsMaster is true only with a valid license, an expired one turns the proxy off and leaves the local panel working
func (m *Manager) IsMaster() bool {
	st := m.State()
	return st.Role == RoleMaster && len(st.Nodes) > 0 && m.Licensed != nil && m.Licensed()
}

func (m *Manager) nodeByID(id string) (Node, bool) {
	for _, n := range m.State().Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// liveNodeByID is nodeByID for anything acting on a server, a paused one isn't found
func (m *Manager) liveNodeByID(id string) (Node, bool) {
	n, ok := m.nodeByID(id)
	if !ok || n.Paused {
		return Node{}, false
	}
	return n, true
}

// selfURL is this master's address on the port it runs on now, so slaves follow a port change on the next status check
func (m *Manager) selfURL(st State) string {
	u, err := url.Parse(st.SelfURL)
	if err != nil || u.Host == "" || m.AdminPort == "" {
		return st.SelfURL
	}
	u.Host = net.JoinHostPort(u.Hostname(), m.AdminPort)
	return u.String()
}

// LiveNodes are the servers the master works with, paused ones left out
func (m *Manager) LiveNodes() []Node {
	var out []Node
	for _, n := range m.State().Nodes {
		if !n.Paused {
			out = append(out, n)
		}
	}
	return out
}

// SetPaused pauses or resumes a server
func (m *Manager) SetPaused(id string, paused bool) error {
	n, ok := m.nodeByID(id)
	if !ok {
		return errors.New("unknown server")
	}
	if paused {
		m.dropNodeSessions(id)
	}
	err := m.update(func(s *State) error {
		for i := range s.Nodes {
			if s.Nodes[i].ID == id {
				s.Nodes[i].Paused = paused
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if paused {
		Notify("Server paused", n.Name+" is left out of lists and of settings applied to all servers until it's resumed.", "info")
	} else {
		Notify("Server resumed", n.Name+" is managed from the master again.", "info")
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func spkiPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// caName returns a DNS name the cert is publicly valid for, so a renewed Let's Encrypt cert doesn't break the pin
func caName(certs []*x509.Certificate) string {
	if len(certs) == 0 {
		return ""
	}
	leaf := certs[0]
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	for _, name := range leaf.DNSNames {
		if _, err := leaf.Verify(x509.VerifyOptions{Intermediates: inter, DNSName: name}); err == nil {
			return name
		}
	}
	return ""
}

// pinnedTLS trusts the pinned key, or a publicly valid cert for the name recorded at join
func pinnedTLS(spki, tlsName string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // VerifyConnection below is the real check
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no peer certificate")
			}
			leaf := cs.PeerCertificates[0]
			if subtle.ConstantTimeCompare([]byte(spkiPin(leaf)), []byte(spki)) == 1 {
				return nil
			}
			if tlsName != "" {
				inter := x509.NewCertPool()
				for _, c := range cs.PeerCertificates[1:] {
					inter.AddCert(c)
				}
				if _, err := leaf.Verify(x509.VerifyOptions{Intermediates: inter, DNSName: tlsName}); err == nil {
					return nil
				}
			}
			return errors.New("server certificate does not match the one pinned when it joined")
		},
	}
}

func pinnedTransport(spki, tlsName string) *http.Transport {
	return &http.Transport{
		TLSClientConfig:     pinnedTLS(spki, tlsName),
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 8 * time.Second,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
	}
}

// probeCert dials addr without trusting anything, the caller compares the result to a known pin
func probeCert(addr string) (spki, tlsName string, err error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return "", "", err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", "", errors.New("no certificate")
	}
	return spkiPin(certs[0]), caName(certs), nil
}

type joinFile struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// GenerateJoinCode prints a one-time code carrying this server's cert pin, so the master never has to trust first use
func GenerateJoinCode(adminPort string) (string, error) {
	if adminPort == "" {
		adminPort = "2087"
	}
	spki, _, err := probeCert("127.0.0.1:" + adminPort)
	if err != nil {
		return "", fmt.Errorf("OpenAdmin must serve HTTPS on port %s to join a cluster: %w", adminPort, err)
	}
	pin, err := hex.DecodeString(spki)
	if err != nil {
		return "", err
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	if err := writeJSONFile(joinPath(), joinFile{Hash: hashSecret(hex.EncodeToString(secret)), Expires: time.Now().Add(joinCodeTTL)}); err != nil {
		return "", err
	}
	return joinCodePrefix + base64.RawURLEncoding.EncodeToString(append(secret, pin...)), nil
}

func parseJoinCode(code string) (secret, spki string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(code), joinCodePrefix))
	if err != nil || len(raw) != 48 {
		return "", "", errors.New("invalid join code")
	}
	return hex.EncodeToString(raw[:16]), hex.EncodeToString(raw[16:]), nil
}

// Claims is what the master signs for every call it makes to a slave
type Claims struct {
	Aud    string          `json:"aud"`
	Action string          `json:"act"`
	Sub    string          `json:"sub,omitempty"`
	Role   string          `json:"role,omitempty"`
	IP     string          `json:"ip,omitempty"`
	Iat    int64           `json:"iat"`
	Exp    int64           `json:"exp"`
	Jti    string          `json:"jti"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type envelope struct {
	Body []byte `json:"b"`
	Sig  []byte `json:"s"`
}

func signWith(seedHex string, body []byte) ([]byte, error) {
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("cluster key is missing or corrupt")
	}
	return ed25519.Sign(ed25519.NewKeyFromSeed(seed), body), nil
}

func publicFromSeed(seedHex string) (string, error) {
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", errors.New("cluster key is missing or corrupt")
	}
	return hex.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)), nil
}

func (m *Manager) sign(c Claims) ([]byte, error) {
	now := time.Now()
	c.Iat, c.Exp, c.Jti = now.Unix(), now.Add(claimTTL).Unix(), randomHex(12)
	body, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	sig, err := signWith(m.State().SeedHex, body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Body: body, Sig: sig})
}

// verify checks signature, audience, expiry and replay of a call claiming to come from our master
func (m *Manager) verify(raw []byte) (*Claims, error) {
	st := m.State()
	if st.Role != RoleSlave || st.MasterPubHex == "" {
		return nil, errors.New("this server is not a cluster member")
	}
	pub, err := hex.DecodeString(st.MasterPubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("master key is corrupt")
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, errors.New("bad envelope")
	}
	if !ed25519.Verify(pub, env.Body, env.Sig) {
		return nil, errors.New("bad signature")
	}
	var c Claims
	if err := json.Unmarshal(env.Body, &c); err != nil {
		return nil, errors.New("bad claims")
	}
	now := time.Now()
	if c.Aud != st.NodeID || now.Unix() > c.Exp || c.Iat > now.Add(claimTTL).Unix() {
		return nil, errors.New("claims expired or for another server")
	}
	m.seenMu.Lock()
	defer m.seenMu.Unlock()
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	for k, exp := range m.seen {
		if now.After(exp) {
			delete(m.seen, k)
		}
	}
	if _, dup := m.seen[c.Jti]; dup {
		return nil, errors.New("replayed call")
	}
	m.seen[c.Jti] = time.Unix(c.Exp, 0).Add(claimTTL)
	return &c, nil
}

func (m *Manager) client(n Node) *http.Client {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.clients == nil {
		m.clients = map[string]*http.Client{}
	}
	key := n.ID + "|" + n.SPKI + "|" + n.TLSName
	if c, ok := m.clients[key]; ok {
		return c
	}
	timeout := 30 * time.Second
	if m.rpcTimeout > 0 {
		timeout = m.rpcTimeout
	}
	c := &http.Client{Transport: pinnedTransport(n.SPKI, n.TLSName), Timeout: timeout}
	m.clients[key] = c
	return c
}

// rpc signs and sends action to node, out may be nil
func (m *Manager) rpc(n Node, action string, data, out any) error {
	return m.rpcContext(context.Background(), n, action, data, out)
}

// rpcContext is rpc that gives up when ctx does, reads use it so a stuck server can't hold up a page
func (m *Manager) rpcContext(ctx context.Context, n Node, action string, data, out any) error {
	var rawData json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		rawData = b
	}
	body, err := m.sign(Claims{Aud: n.ID, Action: action, Data: rawData})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL+"/_cluster/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client(n).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", n.Name, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

// AddNode enrolls a slave from its join code, this is what turns the server into a master
func (m *Manager) AddNode(rawURL, name, code string) (Node, error) {
	return m.addNode(rawURL, name, code, "")
}

// addNode with a sync mode for a slave that has none yet, servers added from the install command follow the master
func (m *Manager) addNode(rawURL, name, code, mode string) (Node, error) {
	if m.Licensed == nil || !m.Licensed() {
		return Node{}, errors.New("multi-server requires a valid Enterprise license")
	}
	st := m.State()
	if st.Role == RoleSlave {
		return Node{}, errors.New("this server is a slave, leave the cluster before adding servers")
	}
	u, err := normalizeURL(rawURL, m.AdminPort)
	if err != nil {
		return Node{}, err
	}
	secret, spki, err := parseJoinCode(code)
	if err != nil {
		return Node{}, err
	}
	if st.SeedHex == "" {
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return Node{}, err
		}
		selfURL := "https://" + net.JoinHostPort(m.PublicIP, m.AdminPort)
		if err := m.update(func(s *State) error {
			s.SeedHex, s.SelfURL = hex.EncodeToString(seed), selfURL
			return nil
		}); err != nil {
			return Node{}, err
		}
		st = m.State()
	}
	selfSPKI, selfName, err := probeCert("127.0.0.1:" + m.AdminPort)
	if err != nil {
		return Node{}, fmt.Errorf("this server must serve HTTPS itself: %w", err)
	}

	host := strings.TrimPrefix(u, "https://")
	seenSPKI, tlsName, err := probeCert(host)
	if err != nil {
		return Node{}, fmt.Errorf("could not reach %s: %w", u, err)
	}
	if seenSPKI != spki {
		return Node{}, fmt.Errorf("%s presented a different certificate than the join code expects", u)
	}

	seed, err := hex.DecodeString(st.SeedHex)
	if err != nil {
		return Node{}, err
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	body, err := json.Marshal(joinRequest{
		Secret: secret, MasterURL: m.selfURL(st), MasterName: m.displayName(),
		MasterPub: hex.EncodeToString(pub), MasterSPKI: selfSPKI, MasterTLSName: selfName, MasterVersion: m.Version, Mode: mode,
	})
	if err != nil {
		return Node{}, err
	}
	client := &http.Client{Transport: pinnedTransport(spki, tlsName), Timeout: 20 * time.Second}
	resp, err := client.Post(u+"/_cluster/join", "application/json", bytes.NewReader(body))
	if err != nil {
		return Node{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return Node{}, fmt.Errorf("%s runs an OpenPanel version without multi-server, update it to %s or newer first", u, MinVersion)
	}
	if resp.StatusCode != http.StatusOK {
		return Node{}, fmt.Errorf("server refused to join: %s", strings.TrimSpace(string(respBody)))
	}
	var jr joinResponse
	if err := json.Unmarshal(respBody, &jr); err != nil {
		return Node{}, errors.New("unexpected join response, is that server on the same OpenAdmin version?")
	}
	if name = strings.TrimSpace(name); name == "" {
		name = jr.Name
	}
	node := Node{ID: jr.NodeID, Name: name, URL: u, SPKI: spki, TLSName: tlsName, Secret: jr.Secret, AddedAt: time.Now()}
	baseline, _ := BuildSnapshot()
	err = m.update(func(s *State) error {
		s.Role = RoleMaster
		kept := s.Nodes[:0]
		for _, n := range s.Nodes {
			if n.URL != node.URL {
				kept = append(kept, n)
			}
		}
		s.Nodes = append(kept, node)
		if s.LastSynced == nil && baseline != nil {
			s.LastSynced = baseline.Values
		}
		return nil
	})
	if err == nil {
		Notify("Server added to the cluster", node.Name+" ("+hostOnly(u)+") joined as a slave.", "info")
	}
	return node, err
}

func normalizeURL(raw, defaultPort string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("server address is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("invalid server address")
	}
	if u.Scheme != "https" {
		return "", errors.New("server address must use https")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	return "https://" + net.JoinHostPort(u.Hostname(), port), nil
}

// RemoveNode tells the slave to forget us when it's reachable, a dead server can still be removed
func (m *Manager) RemoveNode(id string) error {
	n, ok := m.nodeByID(id)
	if !ok {
		return errors.New("unknown server")
	}
	rpcErr := m.rpc(n, "leave", nil, nil)
	m.dropNodeSessions(id)
	err := m.update(func(s *State) error {
		kept := s.Nodes[:0]
		for _, x := range s.Nodes {
			if x.ID != id {
				kept = append(kept, x)
			}
		}
		s.Nodes = kept
		if len(s.Nodes) == 0 {
			s.Role = ""
		}
		return nil
	})
	if err != nil {
		return err
	}
	Notify("Server removed from the cluster", n.Name+" is standalone again, its accounts and settings stay on it.", "info")
	return rpcErr
}

// Leave makes a slave standalone again, the local admin keeps working
func (m *Manager) Leave() error {
	master := m.State().MasterName
	err := m.update(func(s *State) error {
		if s.Role != RoleSlave {
			return errors.New("this server is not a slave")
		}
		*s = State{}
		return nil
	})
	if err == nil {
		Notify("Left the cluster", "This server is no longer managed by "+master+".", "info")
	}
	return err
}

func (m *Manager) displayName() string {
	if m.Hostname != "" {
		return m.Hostname
	}
	return m.PublicIP
}

func (m *Manager) masterNode(st State) Node {
	return Node{ID: "master", Name: st.MasterName, URL: st.MasterURL, SPKI: st.MasterSPKI, TLSName: st.MasterTLSName}
}
