package cluster

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"openadmin/internal/auth"
)

type joinRequest struct {
	Secret        string `json:"secret"`
	MasterURL     string `json:"master_url"`
	MasterName    string `json:"master_name"`
	MasterPub     string `json:"master_pub"`
	MasterSPKI    string `json:"master_spki"`
	MasterTLSName string `json:"master_tls_name"`
	MasterVersion string `json:"master_version,omitempty"`
	// Mode is the sync mode for a server joining for the first time, empty keeps manual
	Mode string `json:"mode,omitempty"`
}

// MinVersion is the first OpenPanel release with multi-server, older servers can't join or be a master
const MinVersion = "2.0.20"

// versionProblem says why these two versions can't be in one cluster, a slave newer than its master could get pages the master doesn't know
func versionProblem(slave, master string) string {
	switch {
	case master == "":
		return "the master didn't send its version, update it to " + MinVersion + " or newer first"
	case versionNewer(MinVersion, master):
		return "the master runs OpenPanel " + master + ", multi-server needs " + MinVersion + " or newer, update the master first"
	case versionNewer(MinVersion, slave):
		return "this server runs OpenPanel " + slave + ", multi-server needs " + MinVersion + " or newer, update it first"
	case versionNewer(slave, master):
		return "this server runs OpenPanel " + slave + ", newer than the master's " + master + ", update the master first"
	}
	return ""
}

type joinResponse struct {
	NodeID string `json:"node_id"`
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

const maxRPCBody = 8 << 20

func (m *Manager) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_cluster/join", m.handleJoin)
	mux.HandleFunc("POST /_cluster/enroll", m.handleEnroll)
	mux.HandleFunc("POST /_cluster/sso", m.handleSSO)
	mux.HandleFunc("POST /_cluster/rpc", m.handleRPC)
	mux.HandleFunc("GET /_cluster/snapshot", m.handleSnapshot)
	mux.HandleFunc("GET /_cluster/switcher.js", m.handleSwitcherJS)
	mux.HandleFunc("GET /_cluster/state", m.handleState)
	mux.HandleFunc("POST /_cluster/switch", m.handleSwitch)
	mux.HandleFunc("GET /_cluster/search", m.handleSearch)
	mux.HandleFunc("GET /_cluster/open", m.handleOpen)
	mux.HandleFunc("POST /_cluster/move", m.handleMove)
	return mux
}

func (m *Manager) handleSwitcherJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(switcherJS)
}

var joinAttempts struct {
	sync.Mutex
	last  time.Time
	count int
}

// joinAllowed caps guesses at the code, ten a minute is plenty for a human pasting it
func joinAllowed() bool {
	joinAttempts.Lock()
	defer joinAttempts.Unlock()
	if time.Since(joinAttempts.last) > time.Minute {
		joinAttempts.last, joinAttempts.count = time.Now(), 0
	}
	joinAttempts.count++
	return joinAttempts.count <= 10
}

func (m *Manager) handleJoin(w http.ResponseWriter, r *http.Request) {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()
	if !joinAllowed() {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var req joinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw, err := os.ReadFile(joinPath())
	if err != nil {
		http.Error(w, "no join code was generated on this server", http.StatusForbidden)
		return
	}
	var jf joinFile
	if json.Unmarshal(raw, &jf) != nil || time.Now().After(jf.Expires) ||
		subtle.ConstantTimeCompare([]byte(jf.Hash), []byte(hashSecret(req.Secret))) != 1 {
		http.Error(w, "join code is invalid or expired", http.StatusForbidden)
		return
	}
	st := m.State()
	if st.Role == RoleMaster && len(st.Nodes) > 0 {
		http.Error(w, "this server is a master with its own servers", http.StatusConflict)
		return
	}
	if st.Role == RoleSlave && st.MasterPubHex != req.MasterPub {
		http.Error(w, "this server already belongs to another master, leave that cluster first", http.StatusConflict)
		return
	}
	if req.MasterPub == "" || req.MasterURL == "" || req.MasterSPKI == "" {
		http.Error(w, "incomplete join request", http.StatusBadRequest)
		return
	}
	if problem := versionProblem(m.Version, req.MasterVersion); problem != "" {
		http.Error(w, problem, http.StatusConflict)
		return
	}
	resp := joinResponse{NodeID: st.NodeID, Secret: st.NodeSecret, Name: m.displayName()}
	if resp.NodeID == "" {
		resp.NodeID, resp.Secret = randomHex(8), randomHex(32)
	}
	err = m.update(func(s *State) error {
		mode, groups := s.Mode, s.Groups
		if mode == "" && req.Mode == ModeFollow {
			mode = ModeFollow
		}
		if mode == "" {
			mode = ModeManual
		}
		if len(groups) == 0 {
			groups = []string{GroupCore}
		}
		*s = State{
			Role: RoleSlave, NodeID: resp.NodeID, NodeSecret: resp.Secret, MasterName: req.MasterName, MasterURL: req.MasterURL,
			MasterSPKI: req.MasterSPKI, MasterTLSName: req.MasterTLSName, MasterPubHex: req.MasterPub,
			Mode: mode, Groups: groups, Pinned: s.Pinned, LastRev: s.LastRev, LastSyncAt: s.LastSyncAt, LastSyncMsg: s.LastSyncMsg,
		}
		return nil
	})
	if err != nil {
		http.Error(w, "could not save cluster state", http.StatusInternalServerError)
		return
	}
	os.Remove(joinPath())
	Notify("Joined a cluster", "This server is now a slave of "+req.MasterName+".", "info")
	json.NewEncoder(w).Encode(resp)
}

// handleSSO opens a real local session for a master admin, each one gets a passwordless local account so the activity log names them
func (m *Manager) handleSSO(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c, err := m.verify(raw)
	if err != nil || c.Action != "sso" || c.Sub == "" {
		http.Error(w, "invalid login", http.StatusForbidden)
		return
	}
	role := "user"
	if c.Role == "admin" {
		role = "admin"
	}
	name := "cluster_" + c.Sub
	u, err := m.DB.UserByUsername(name)
	if err == nil && u.Role != role {
		m.DB.DeleteUser(name)
		err = os.ErrNotExist
	}
	if err != nil {
		hash, hashErr := auth.GeneratePasswordHash(randomHex(32))
		if hashErr != nil || m.DB.CreateUser(name, hash, role) != nil {
			http.Error(w, "could not create the cluster admin", http.StatusInternalServerError)
			return
		}
		if u, err = m.DB.UserByUsername(name); err != nil {
			http.Error(w, "could not load the cluster admin", http.StatusInternalServerError)
			return
		}
	}
	if !u.IsActive {
		http.Error(w, "cluster admin is disabled on this server", http.StatusForbidden)
		return
	}
	if err := auth.LoginUser(w, r, m.Sessions, u, c.IP); err != nil {
		http.Error(w, "could not open a session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) handleRPC(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c, err := m.verify(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if c.Action == "opencli" {
		// the server's 30s write timeout would cut off opencli update and friends
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(opencliTimeout + time.Minute))
	}
	out, err := m.handleAction(c.Action, c.Data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleSnapshot gives a slave the master's shared settings, it authenticates with the secret from its join
func (m *Manager) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if !m.IsMaster() {
		http.Error(w, "not a master", http.StatusForbidden)
		return
	}
	id, secret, ok := strings.Cut(strings.TrimPrefix(r.Header.Get("Authorization"), "Cluster "), ":")
	n, known := m.nodeByID(id)
	if !ok || !known || subtle.ConstantTimeCompare([]byte(n.Secret), []byte(secret)) != 1 {
		http.Error(w, "unknown server", http.StatusForbidden)
		return
	}
	if n.Paused {
		http.Error(w, "this server is paused on the master", http.StatusConflict)
		return
	}
	snap, err := BuildSnapshot()
	if err != nil {
		http.Error(w, "could not read configuration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(snap)
}

type serverEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	Online  *bool  `json:"online,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
	Down    bool   `json:"down,omitempty"`
	Unread  int    `json:"unread,omitempty"`
}

func (m *Manager) handleState(w http.ResponseWriter, r *http.Request) {
	u := m.sessionUser(r)
	if u == nil || u.Role == "reseller" || !m.IsMaster() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	st := m.State()
	active := LocalNode
	if n, ok := m.activeNode(r); ok {
		active = n.ID
	}
	servers := []serverEntry{{ID: LocalNode, Name: m.displayName(), Address: m.PublicIP}}
	for _, n := range st.Nodes {
		e := serverEntry{ID: n.ID, Name: n.Name, Address: hostOnly(n.URL), Paused: n.Paused}
		if h, ok := m.nodeHealth(n.ID); ok && !n.Paused {
			online := h.Online
			e.Online = &online
			e.Down = len(h.Down) > 0
			e.Unread = h.Unread
		}
		servers = append(servers, e)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	applied, pending := applyTake(u.ID)
	json.NewEncoder(w).Encode(map[string]any{"active": active, "servers": servers, "unsynced": m.UnsyncedCount(), "applied": applied, "apply_pending": pending})
}

// handleMove starts a move from an account's own page, which can be on any server so it can't use the master's form token
func (m *Manager) handleMove(w http.ResponseWriter, r *http.Request) {
	u := m.sessionUser(r)
	if u == nil || u.Role != "admin" || !m.IsMaster() || r.Header.Get("X-Cluster-Switch") == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if ou, err := url.Parse(r.Header.Get("Origin")); err != nil || ou.Host != r.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Account  string `json:"account"`
		From     string `json:"from"`
		To       string `json:"to"`
		Live     bool   `json:"live"`
		Reseller string `json:"reseller"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := m.StartMove(body.Account, body.From, body.To, body.Live, body.Reseller); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSwitch only needs the custom header and matching origin, a cross-site page can send neither
func (m *Manager) handleSwitch(w http.ResponseWriter, r *http.Request) {
	u := m.sessionUser(r)
	if u == nil || u.Role == "reseller" || !m.IsMaster() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Header.Get("X-Cluster-Switch") == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" {
		if ou, err := url.Parse(o); err != nil || ou.Host != r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.To != LocalNode {
		n, ok := m.nodeByID(body.To)
		if !ok {
			http.Error(w, "unknown server", http.StatusNotFound)
			return
		}
		if n.Paused {
			http.Error(w, "server is paused", http.StatusConflict)
			return
		}
	}
	sess, err := m.Sessions.Get(r)
	if err != nil {
		http.Error(w, "no session", http.StatusForbidden)
		return
	}
	sess.Values[sessionKeyNode] = body.To
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "could not save session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Hostname()
}
