package cluster

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	invitePrefix = "opinvite-"
	inviteTTL    = 24 * time.Hour
)

func invitesPath() string { return filepath.Join(Dir, "invites.json") }

// invite is what the install script gets, the master's address and cert pin plus a one-time secret
type invite struct {
	URL    string `json:"u"`
	SPKI   string `json:"p"`
	Secret string `json:"s"`
}

type enrollRequest struct {
	Secret string `json:"secret"`
	URL    string `json:"url"`
	Name   string `json:"name"`
	Code   string `json:"code"`
}

// EnrollResult tells the new server how joining and the first sync went
type EnrollResult struct {
	Name    string `json:"name"`
	Master  string `json:"master"`
	Synced  string `json:"synced,omitempty"`
	SyncErr string `json:"sync_error,omitempty"`
}

func (m *Manager) readInvites() []joinFile {
	var list []joinFile
	if raw, err := os.ReadFile(invitesPath()); err == nil {
		json.Unmarshal(raw, &list)
	}
	live := list[:0]
	for _, inv := range list {
		if time.Now().Before(inv.Expires) {
			live = append(live, inv)
		}
	}
	return live
}

// GenerateInvite makes a one-time code a fresh install uses to add itself to this master
func (m *Manager) GenerateInvite() (string, error) {
	if m.Licensed == nil || !m.Licensed() {
		return "", errors.New("multi-server requires a valid Enterprise license")
	}
	st := m.State()
	if st.Role == RoleSlave {
		return "", errors.New("this server is a slave, only the master can invite servers")
	}
	spki, _, err := probeCert("127.0.0.1:" + m.AdminPort)
	if err != nil {
		return "", fmt.Errorf("this server must serve HTTPS itself: %w", err)
	}
	self := m.selfURL(st)
	if self == "" {
		self = "https://" + net.JoinHostPort(m.PublicIP, m.AdminPort)
	}
	secret := randomHex(16)
	m.joinMu.Lock()
	defer m.joinMu.Unlock()
	list := append(m.readInvites(), joinFile{Hash: hashSecret(secret), Expires: time.Now().Add(inviteTTL)})
	if err := writeJSONFile(invitesPath(), list); err != nil {
		return "", err
	}
	raw, err := json.Marshal(invite{URL: self, SPKI: spki, Secret: secret})
	if err != nil {
		return "", err
	}
	return invitePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// takeInvite uses up a matching invite so the same code can't add a second server
func (m *Manager) takeInvite(secret string) bool {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()
	list := m.readInvites()
	want := []byte(hashSecret(secret))
	for i, inv := range list {
		if subtle.ConstantTimeCompare([]byte(inv.Hash), want) == 1 {
			list = append(list[:i], list[i+1:]...)
			return writeJSONFile(invitesPath(), list) == nil
		}
	}
	return false
}

// handleEnroll is called by a new server with an invite, the master then adds it the usual way with its join code
func (m *Manager) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !joinAllowed() {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var req enrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Secret == "" || req.Code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !m.takeInvite(req.Secret) {
		http.Error(w, "invite is invalid, expired or already used", http.StatusForbidden)
		return
	}
	// a server that doesn't know its public address is reached on the one it called from
	if req.URL == "" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		req.URL = host
	}
	n, err := m.addNode(req.URL, req.Name, req.Code, ModeFollow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	res := EnrollResult{Name: n.Name, Master: m.displayName()}
	ok, failed := m.PushSettings([]string{n.ID}, nil)
	if msg, bad := failed[n.Name]; bad {
		res.SyncErr = msg
	} else if s, done := ok[n.Name]; done {
		res.Synced = s.Message
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// Enroll is run on a fresh server, it makes a join code and hands it to the master named in the invite
func Enroll(code, adminPort, selfURL, name string) (EnrollResult, error) {
	var res EnrollResult
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(code), invitePrefix))
	if err != nil {
		return res, errors.New("invalid invite")
	}
	var inv invite
	if json.Unmarshal(raw, &inv) != nil || inv.URL == "" || inv.SPKI == "" || inv.Secret == "" {
		return res, errors.New("invalid invite")
	}
	if _, err := hex.DecodeString(inv.SPKI); err != nil {
		return res, errors.New("invalid invite")
	}
	joinCode, err := GenerateJoinCode(adminPort)
	if err != nil {
		return res, err
	}
	body, err := json.Marshal(enrollRequest{Secret: inv.Secret, URL: selfURL, Name: name, Code: joinCode})
	if err != nil {
		return res, err
	}
	// the master syncs settings to us before answering, that can take a while
	client := &http.Client{Transport: pinnedTransport(inv.SPKI, ""), Timeout: 5 * time.Minute}
	resp, err := client.Post(strings.TrimRight(inv.URL, "/")+"/_cluster/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		os.Remove(joinPath())
		return res, fmt.Errorf("could not reach the master at %s: %w", inv.URL, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		os.Remove(joinPath())
		return res, fmt.Errorf("the master runs an OpenPanel version without multi-server, update it to %s or newer first", MinVersion)
	}
	if resp.StatusCode != http.StatusOK {
		os.Remove(joinPath())
		return res, fmt.Errorf("the master refused: %s", strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return res, errors.New("unexpected answer from the master, is it on the same OpenAdmin version?")
	}
	return res, nil
}
