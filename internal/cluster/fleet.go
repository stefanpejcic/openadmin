package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	updateUnit      = "openadmin-cluster-update"
	updateLog       = "/var/log/openpanel/admin/cluster_update.log"
	latestCacheTTL  = 6 * time.Hour
	updateGrace     = 35 * time.Minute // opencli update kills itself after 30min
	searchLimit     = 10
	notificationCap = 20
)

// statusArgs is how a slave learns who its master is, so a promotion reaches servers that were offline at the time
type statusArgs struct {
	ClusterSize   int    `json:"cluster_size"`
	MasterURL     string `json:"master_url"`
	MasterName    string `json:"master_name"`
	MasterSPKI    string `json:"master_spki"`
	MasterTLSName string `json:"master_tls_name"`
}

func (m *Manager) applyStatusArgs(raw json.RawMessage) {
	var a statusArgs
	if len(raw) == 0 || json.Unmarshal(raw, &a) != nil || a.MasterURL == "" || a.MasterSPKI == "" {
		return
	}
	st := m.State()
	if st.Role != RoleSlave {
		return
	}
	if st.ClusterSize == a.ClusterSize && st.MasterURL == a.MasterURL && st.MasterSPKI == a.MasterSPKI && st.MasterName == a.MasterName && st.MasterTLSName == a.MasterTLSName {
		return
	}
	_ = m.update(func(s *State) error {
		s.ClusterSize, s.MasterURL, s.MasterSPKI, s.MasterName, s.MasterTLSName = a.ClusterSize, a.MasterURL, a.MasterSPKI, a.MasterName, a.MasterTLSName
		return nil
	})
}

// ClusterCount is the number of servers in this server's cluster, 1 when standalone
func (m *Manager) ClusterCount() int {
	st := m.State()
	switch st.Role {
	case RoleMaster:
		return 1 + len(st.Nodes)
	case RoleSlave:
		if st.ClusterSize > 0 {
			return st.ClusterSize
		}
	}
	return 1
}

type promoteArgs struct {
	Seed       string            `json:"seed"`
	SelfURL    string            `json:"self_url"`
	Nodes      []Node            `json:"nodes"`
	LastSynced map[string]string `json:"last_synced"`
}

// Promote hands the master role to a slave and turns this server into a slave of it, the signing key moves along so the other slaves keep trusting it
func (m *Manager) Promote(id string) error {
	if !m.IsMaster() {
		return errors.New("only a licensed master can hand over its role")
	}
	st := m.State()
	n, ok := m.nodeByID(id)
	if !ok {
		return errors.New("unknown server")
	}
	if n.Paused {
		return errors.New("resume the server first")
	}
	for _, j := range st.Jobs {
		if j.Status == JobRunning {
			return fmt.Errorf("move of %s is still running", j.Account)
		}
	}
	var h NodeHealth
	if err := m.rpc(n, "status", nil, &h); err != nil {
		return fmt.Errorf("%s is not reachable: %w", n.Name, err)
	}
	if !h.License {
		return fmt.Errorf("%s has no valid Enterprise license", n.Name)
	}
	if st.SelfURL == "" {
		return errors.New("this server's own address is unknown")
	}
	selfSPKI, selfName, err := probeCert("127.0.0.1:" + m.AdminPort)
	if err != nil {
		return fmt.Errorf("could not read this server's certificate: %w", err)
	}

	self := Node{ID: randomHex(8), Name: m.displayName(), URL: st.SelfURL, SPKI: selfSPKI, TLSName: selfName, Secret: randomHex(32), AddedAt: time.Now()}
	nodes := []Node{self}
	for _, x := range st.Nodes {
		if x.ID != id {
			nodes = append(nodes, x)
		}
	}
	if err := m.rpc(n, "promote", promoteArgs{Seed: st.SeedHex, SelfURL: n.URL, Nodes: nodes, LastSynced: st.LastSynced}, nil); err != nil {
		return fmt.Errorf("%s refused: %w", n.Name, err)
	}

	pub, err := publicFromSeed(st.SeedHex)
	if err != nil {
		return err
	}
	m.sessMu.Lock()
	m.sessions = nil
	m.sessMu.Unlock()
	Notify("Master changed", n.Name+" is the master now, this server became one of its slaves.", "info")
	return m.update(func(s *State) error {
		*s = State{
			Role: RoleSlave, NodeID: self.ID, NodeSecret: self.Secret, MasterName: n.Name, MasterURL: n.URL, MasterSPKI: n.SPKI, MasterTLSName: n.TLSName,
			MasterPubHex: pub, Mode: ModeManual, Groups: []string{GroupCore}, ClusterSize: len(nodes) + 1,
		}
		return nil
	})
}

func (m *Manager) acceptPromotion(raw json.RawMessage) error {
	var a promoteArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Seed == "" || a.SelfURL == "" {
		return errors.New("invalid promotion")
	}
	if m.Licensed == nil || !m.Licensed() {
		return errors.New("no valid Enterprise license")
	}
	err := m.update(func(s *State) error {
		if s.Role != RoleSlave {
			return errors.New("this server is not a slave")
		}
		*s = State{Role: RoleMaster, SeedHex: a.Seed, SelfURL: a.SelfURL, Nodes: a.Nodes, LastSynced: a.LastSynced}
		return nil
	})
	if err == nil {
		Notify("This server is the master now", "The cluster is managed from this server.", "info")
	}
	return err
}

// StartUpdate runs opencli update on a server in the background
func (m *Manager) StartUpdate(id string) error {
	if err := m.call(id, "update_start", nil, nil); err != nil {
		return err
	}
	m.markUpdating(id)
	Notify("Server update started", m.serverName(id)+" is updating OpenPanel.", "info")
	return nil
}

// updateLock is held by every opencli update run, also the ones from cron or ssh
var updateLock = "/var/lock/openpanel_update.lock"

func updateRunning() bool {
	out, _ := exec.Command("systemctl", "is-active", updateUnit).Output()
	if s := strings.TrimSpace(string(out)); s == "activating" || s == "active" {
		return true
	}
	return lockHeld(updateLock, "/proc/locks")
}

// lockHeld looks the file up in /proc/locks, taking the lock ourselves could make a starting update bail out
func lockHeld(path, locks string) bool {
	var st syscall.Stat_t
	if syscall.Stat(path, &st) != nil {
		return false
	}
	raw, err := os.ReadFile(locks)
	if err != nil {
		return false
	}
	major := (st.Dev>>8)&0xfff | (st.Dev>>32)&^0xfff
	minor := st.Dev&0xff | (st.Dev>>12)&^0xff
	want := fmt.Sprintf("%02x:%02x:%d", major, minor, st.Ino)
	for _, line := range strings.Split(string(raw), "\n") {
		for _, f := range strings.Fields(line) {
			if f == want {
				return true
			}
		}
	}
	return false
}

// markUpdating lets a server drop off for the length of an update without counting as offline
func (m *Manager) markUpdating(id string) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	if m.updating == nil {
		m.updating = map[string]time.Time{}
	}
	m.updating[id] = time.Now()
}

func startUpdate() error {
	if updateRunning() {
		return errors.New("an update is already running")
	}
	script := `exec timeout 900s opencli update --force >"$1" 2>&1`
	if out, err := exec.Command("systemd-run", "--unit="+updateUnit, "--collect", "--quiet", "/bin/bash", "-c", script, "_", updateLog).CombinedOutput(); err != nil {
		return fmt.Errorf("could not start the update: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

var latest struct {
	sync.Mutex
	version string
	at      time.Time
}

// LatestVersion refreshes in the background, a slow registry shouldn't hold up the servers page
func (m *Manager) LatestVersion() string {
	latest.Lock()
	defer latest.Unlock()
	if m.LatestSource != nil && time.Since(latest.at) >= latestCacheTTL {
		latest.at = time.Now()
		go func() {
			v := m.LatestSource()
			if v == "" {
				return
			}
			latest.Lock()
			latest.version = v
			latest.Unlock()
		}()
	}
	return latest.version
}

// versionNewer reports a > b for dotted numeric versions, anything unparsable is never newer
func versionNewer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		var err error
		if i < len(as) {
			if x, err = strconv.Atoi(as[i]); err != nil {
				return false
			}
		}
		if i < len(bs) {
			if y, err = strconv.Atoi(bs[i]); err != nil {
				return false
			}
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// SearchHit is one result of a search across all servers
type SearchHit struct {
	Server string `json:"server"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Link   string `json:"link"`
}

func likeArg(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func (m *Manager) searchLocal(q string) ([]SearchHit, error) {
	hits := []SearchHit{}
	rows, err := m.MySQL.Query(`SELECT username, email FROM users WHERE username LIKE ? OR email LIKE ? ORDER BY username LIMIT ?`, likeArg(q), likeArg(q), searchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u, email string
		if err := rows.Scan(&u, &email); err != nil {
			return nil, err
		}
		hits = append(hits, SearchHit{Kind: "account", Label: u + " (" + email + ")", Link: "/users/" + u})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	domains, err := m.MySQL.Query(`SELECT d.domain_url, u.username FROM domains d JOIN users u ON d.user_id = u.id WHERE d.domain_url LIKE ? ORDER BY d.domain_url LIMIT ?`, likeArg(q), searchLimit)
	if err != nil {
		return nil, err
	}
	defer domains.Close()
	for domains.Next() {
		var d, u string
		if err := domains.Scan(&d, &u); err != nil {
			return nil, err
		}
		hits = append(hits, SearchHit{Kind: "domain", Label: d + " (" + u + ")", Link: "/users/" + u})
	}
	return hits, domains.Err()
}

// each runs fn for every other server at the same time, a dead one just adds nothing
func (m *Manager) eachServer(fn func(id, name string)) {
	var wg sync.WaitGroup
	run := func(id, name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(id, name)
		}()
	}
	run(LocalNode, m.displayName())
	for _, n := range m.LiveNodes() {
		run(n.ID, n.Name)
	}
	wg.Wait()
}

func (m *Manager) handleSearch(w http.ResponseWriter, r *http.Request) {
	u := m.sessionUser(r)
	if u == nil || u.Role == "reseller" || !m.IsMaster() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []SearchHit{}
	if len(q) >= 2 {
		var mu sync.Mutex
		ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
		defer cancel()
		m.eachServer(func(id, name string) {
			var hits []SearchHit
			if m.callContext(ctx, id, "search", q, &hits) != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, h := range hits {
				h.Server, h.Name = id, name
				out = append(out, h)
			}
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// NotificationBrief is a notification as other servers see it
type NotificationBrief struct {
	Time     string `json:"time"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Unread   bool   `json:"unread"`
}

type serverNotifications struct {
	Unread int                 `json:"unread"`
	Items  []NotificationBrief `json:"items"`
}

func (m *Manager) localNotifications() serverNotifications {
	out := serverNotifications{Items: []NotificationBrief{}}
	if m.Notifications == nil {
		return out
	}
	items := m.Notifications()
	for _, n := range items {
		if n.Unread {
			out.Unread++
			if len(out.Items) < notificationCap {
				out.Items = append(out.Items, n)
			}
		}
	}
	return out
}
