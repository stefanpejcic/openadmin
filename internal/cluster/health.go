package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"openadmin/internal/config"
)

// NodeHealth is one server's row on the dashboard widget and the servers page
type NodeHealth struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Address    string    `json:"address"`
	Master     bool      `json:"master,omitempty"`
	Paused     bool      `json:"paused,omitempty"`
	Online     bool      `json:"online"`
	Error      string    `json:"error,omitempty"`
	Version    string    `json:"version,omitempty"`
	Users      int       `json:"users"`
	Domains    int       `json:"domains"`
	Load       float64   `json:"load"`
	CPUs       int       `json:"cpus,omitempty"`
	MemPct     int       `json:"mem_pct"`
	DiskPct    int       `json:"disk_pct"`
	Down       []string  `json:"down,omitempty"`
	License    bool      `json:"license"`
	Mode       string    `json:"mode,omitempty"`
	LastSync   string    `json:"last_sync,omitempty"`
	Unread     int       `json:"unread,omitempty"`
	Updating   bool      `json:"updating,omitempty"`
	Latest     string    `json:"latest,omitempty"`
	LastSyncAt string    `json:"last_sync_at,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

var coreServices = []string{"openpanel", "openpanel_mysql", "openpanel_redis", "caddy", "openpanel_dns"}

func (m *Manager) localHealth() NodeHealth {
	st := m.State()
	h := NodeHealth{ID: LocalNode, Name: m.displayName(), Address: m.PublicIP, Online: true, Version: m.Version, CheckedAt: time.Now(), Mode: st.Mode}
	h.License = m.Licensed != nil && m.Licensed()
	h.Updating = updateRunning()
	h.Unread = m.localNotifications().Unread
	if !st.LastSyncAt.IsZero() {
		h.LastSync = st.LastSyncMsg
		h.LastSyncAt = st.LastSyncAt.Format("2006-01-02 15:04")
	}
	if m.MySQL != nil {
		_ = m.MySQL.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&h.Users)
		_ = m.MySQL.QueryRow(`SELECT COUNT(*) FROM domains`).Scan(&h.Domains)
	}
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			h.Load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if out, err := exec.Command("nproc").Output(); err == nil {
		h.CPUs, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail float64
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "MemTotal:":
				total, _ = strconv.ParseFloat(f[1], 64)
			case "MemAvailable:":
				avail, _ = strconv.ParseFloat(f[1], 64)
			}
		}
		if total > 0 {
			h.MemPct = int((total - avail) * 100 / total)
		}
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/", &fs) == nil && fs.Blocks > 0 {
		h.DiskPct = int((fs.Blocks - fs.Bavail) * 100 / fs.Blocks)
	}
	if out, err := exec.Command("podman", "ps", "--format", "{{.Names}}").Output(); err == nil {
		running := strings.Fields(string(out))
		for _, svc := range coreServices {
			// bind only gets started with the first domain
			if svc == "openpanel_dns" && h.Domains == 0 {
				continue
			}
			if !hasString(running, svc) {
				h.Down = append(h.Down, svc)
			}
		}
	}
	return h
}

func (m *Manager) refreshHealth() {
	st := m.State()
	args := statusArgs{ClusterSize: len(st.Nodes) + 1, MasterURL: m.selfURL(st), MasterName: m.displayName()}
	args.MasterSPKI, args.MasterTLSName, _ = probeCert("127.0.0.1:" + m.AdminPort)
	fresh := map[string]NodeHealth{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range st.Nodes {
		if n.Paused {
			continue
		}
		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			h := NodeHealth{}
			ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
			err := m.rpcContext(ctx, n, "status", args, &h)
			cancel()
			h.ID, h.Name, h.Address, h.Online, h.CheckedAt = n.ID, n.Name, hostOnly(n.URL), err == nil, time.Now()
			if err != nil {
				h.Error = err.Error()
			}
			mu.Lock()
			fresh[n.ID] = h
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	m.healthMu.Lock()
	old := m.health
	m.health = fresh
	m.healthMu.Unlock()
	for id, h := range fresh {
		prev, seen := old[id]
		switch {
		case !h.Online && (!seen || prev.Online):
			Notify("Server offline", h.Name+" ("+h.Address+") doesn't answer the master: "+h.Error, "warning")
		case h.Online && seen && !prev.Online:
			Notify("Server back online", h.Name+" ("+h.Address+") answers the master again.", "info")
		}
	}
}

func (m *Manager) nodeHealth(id string) (NodeHealth, bool) {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()
	h, ok := m.health[id]
	return h, ok
}

// Health lists the master first, then every slave from the last poll
func (m *Manager) Health() []NodeHealth {
	local := m.localHealth()
	local.Master = true
	out := []NodeHealth{local}
	newest := m.LatestVersion()
	defer func() {
		for i := range out {
			if out[i].Online && versionNewer(newest, out[i].Version) {
				out[i].Latest = newest
			}
		}
	}()
	for _, n := range m.State().Nodes {
		if n.Paused {
			out = append(out, NodeHealth{ID: n.ID, Name: n.Name, Address: hostOnly(n.URL), Paused: true})
			continue
		}
		if h, ok := m.nodeHealth(n.ID); ok {
			out = append(out, h)
		} else {
			out = append(out, NodeHealth{ID: n.ID, Name: n.Name, Address: hostOnly(n.URL), Error: "waiting for the first check"})
		}
	}
	return out
}

// SSH access for a transfer is one key per source server, it's only authorized on the destination while the job runs
func sshKeyPath() string { return filepath.Join(Dir, "ssh", "id_ed25519") }

const authorizedKeys = "/root/.ssh/authorized_keys"

var (
	tagRe     = regexp.MustCompile(`^[a-f0-9]{8,32}$`)
	accountRe = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)
	ipRe      = regexp.MustCompile(`^[0-9a-fA-F:.]{2,45}$`)
	sshPubRe  = regexp.MustCompile(`^ssh-ed25519 [A-Za-z0-9+/=]+$`)
)

type sshAuthorizeArgs struct {
	Pub  string `json:"pub"`
	From string `json:"from"`
	Tag  string `json:"tag"`
}

type accountArgs struct {
	Account string `json:"account"`
	Job     string `json:"job,omitempty"`
	DestIP  string `json:"dest_ip,omitempty"`
	HostKey string `json:"host_key,omitempty"`
	Live    bool   `json:"live,omitempty"`
}

type accountInfo struct {
	Exists      bool     `json:"exists"`
	Owner       string   `json:"owner,omitempty"`
	DedicatedIP bool     `json:"dedicated_ip,omitempty"`
	Domains     []string `json:"domains,omitempty"`
}

type transferState struct {
	Done bool   `json:"done"`
	RC   int    `json:"rc"`
	Log  string `json:"log"`
}

func transferLog(job string) string {
	return "/var/log/openpanel/admin/transfers/cluster_" + job + ".log"
}
func transferRC(job string) string { return filepath.Join(Dir, "jobs", job+".rc") }

// handleAction is everything the master can ask a server to do, the master runs the same code for itself
func (m *Manager) handleAction(action string, raw json.RawMessage) (any, error) {
	switch action {
	case "status":
		m.applyStatusArgs(raw)
		return m.localHealth(), nil
	case "promote":
		return map[string]bool{"ok": true}, m.acceptPromotion(raw)
	case "update_start":
		return map[string]bool{"ok": true}, startUpdate()
	case "plan_name":
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		return m.planNameByID(id)
	case "plan_id":
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, err
		}
		return m.planIDByName(name)
	case "dash_counts":
		if m.DashCounts == nil {
			return nil, errors.New("counts are not available")
		}
		return m.DashCounts(), nil
	case "reseller_export":
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, err
		}
		return m.exportReseller(name)
	case "reseller_import":
		var r resellerExport
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, m.importReseller(r)
	case "admin_role":
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, err
		}
		return m.adminRole(name), nil
	case "account_owner":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		_, err := m.MySQL.Exec(`UPDATE users SET owner = NULL WHERE username = ?`, a.Account)
		return map[string]bool{"ok": true}, err
	case "summaries":
		if m.Summaries == nil {
			return nil, errors.New("summaries are not available")
		}
		return m.Summaries(), nil
	case "search":
		var q string
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		return m.searchLocal(q)
	case "leave":
		return map[string]bool{"ok": true}, m.Leave()
	case "opencli":
		return runOpencli(raw)
	case "has_account":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		return m.hasAccount(a.Account)
	case "sync_diff":
		var groups []string
		_ = json.Unmarshal(raw, &groups)
		return m.PreviewPull(groups)
	case "sync_now":
		var groups []string
		_ = json.Unmarshal(raw, &groups)
		return m.Pull(groups)
	case "accounts":
		return m.listAccounts()
	case "account_info":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		return m.accountInfo(a.Account)
	case "domains_taken":
		var domains []string
		if err := json.Unmarshal(raw, &domains); err != nil {
			return nil, err
		}
		return m.domainsTaken(domains)
	case "ssh_key":
		return sshPublicKey()
	case "ssh_hostkey":
		return sshHostKey()
	case "mail_sync":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		return map[string]bool{"ok": true}, m.mailSync(a)
	case "ssh_authorize":
		var a sshAuthorizeArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, sshAuthorize(a)
	case "ssh_revoke":
		var a sshAuthorizeArgs
		if err := json.Unmarshal(raw, &a); err != nil || !tagRe.MatchString(a.Tag) {
			return nil, errors.New("invalid tag")
		}
		return map[string]bool{"ok": true}, sshRevoke(a.Tag)
	case "transfer_start":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, transferStart(a)
	case "transfer_state":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !tagRe.MatchString(a.Job) {
			return nil, errors.New("invalid job")
		}
		return readTransferState(a.Job), nil
	case "account_restore":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		return map[string]bool{"ok": true}, m.restoreSource(a.Account)
	case "account_shared_ip":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		// only an account that came with a dedicated ip has one to drop
		if _, err := os.Stat("/etc/openpanel/openpanel/core/users/" + a.Account + "/ip.json"); err != nil {
			return map[string]bool{"ok": true}, nil
		}
		out, err := exec.Command("opencli", "user-ip", a.Account, "delete", "-y").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("user-ip delete failed: %s", strings.TrimSpace(string(out)))
		}
		return map[string]bool{"ok": true}, nil
	case "account_delete":
		var a accountArgs
		if err := json.Unmarshal(raw, &a); err != nil || !accountRe.MatchString(a.Account) {
			return nil, errors.New("invalid account")
		}
		out, err := exec.Command("opencli", "user-delete", a.Account, "-y").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("user-delete failed: %s", strings.TrimSpace(string(out)))
		}
		return map[string]bool{"ok": true}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

func (m *Manager) listAccounts() ([]string, error) {
	rows, err := m.MySQL.Query(`SELECT username FROM users WHERE username NOT LIKE 'SUSPENDED\_%' ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (m *Manager) accountInfo(account string) (accountInfo, error) {
	info := accountInfo{}
	var owner *string
	var id int64
	err := m.MySQL.QueryRow(`SELECT id, owner FROM users WHERE username = ?`, account).Scan(&id, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	info.Exists = true
	if owner != nil {
		info.Owner = *owner
	}
	if raw, err := os.ReadFile("/etc/openpanel/openpanel/core/users/" + account + "/ip.json"); err == nil {
		var ip struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal(raw, &ip) == nil && ip.IP != "" {
			info.DedicatedIP = true
		}
	}
	rows, err := m.MySQL.Query(`SELECT domain_url FROM domains WHERE user_id = ?`, id)
	if err != nil {
		return info, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return info, err
		}
		info.Domains = append(info.Domains, d)
	}
	return info, rows.Err()
}

func (m *Manager) domainsTaken(domains []string) ([]string, error) {
	taken := []string{}
	for _, d := range domains {
		var n int
		if err := m.MySQL.QueryRow(`SELECT COUNT(*) FROM domains WHERE domain_url = ?`, d).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			taken = append(taken, d)
		}
	}
	return taken, nil
}

func sshPublicKey() (string, error) {
	path := sshKeyPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "openadmin-cluster", "-f", path).CombinedOutput(); err != nil {
			return "", fmt.Errorf("ssh-keygen failed: %s", strings.TrimSpace(string(out)))
		}
	}
	raw, err := os.ReadFile(path + ".pub")
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(raw))
	if len(f) < 2 {
		return "", errors.New("unreadable public key")
	}
	return f[0] + " " + f[1], nil
}

func sshAuthorize(a sshAuthorizeArgs) error {
	if !sshPubRe.MatchString(a.Pub) || !ipRe.MatchString(a.From) || !tagRe.MatchString(a.Tag) {
		return errors.New("invalid ssh authorization")
	}
	if err := os.MkdirAll(filepath.Dir(authorizedKeys), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(authorizedKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\nfrom=\"%s\",no-agent-forwarding,no-X11-forwarding,no-port-forwarding %s openadmin-cluster-%s\n", a.From, a.Pub, a.Tag)
	return err
}

func sshRevoke(tag string) error {
	raw, err := os.ReadFile(authorizedKeys)
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasSuffix(strings.TrimSpace(line), "openadmin-cluster-"+tag) {
			kept = append(kept, line)
		}
	}
	return os.WriteFile(authorizedKeys, []byte(strings.Join(kept, "\n")), 0600)
}

func transferStart(a accountArgs) error {
	if !accountRe.MatchString(a.Account) || !ipRe.MatchString(a.DestIP) || !tagRe.MatchString(a.Job) {
		return errors.New("invalid transfer request")
	}
	logPath, rcPath := transferLog(a.Job), transferRC(a.Job)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rcPath), 0700); err != nil {
		return err
	}
	args := []string{"user-transfer", "--account", a.Account, "--host", a.DestIP, "--username", "root", "--identity", sshKeyPath()}
	if a.HostKey != "" {
		known, err := writeKnownHosts(a.DestIP, a.HostKey)
		if err != nil {
			return err
		}
		args = append(args, "--known-hosts", known)
	}
	if a.Live {
		args = append(args, "--live-transfer")
	}
	script := `log="$1"; rc="$2"; shift 2; opencli "$@" >"$log" 2>&1; echo $? >"$rc"`
	full := append([]string{"--unit=openadmin-cluster-transfer-" + a.Job, "--collect", "--quiet", "/bin/bash", "-c", script, "_", logPath, rcPath}, args...)
	if out, err := exec.Command("systemd-run", full...).CombinedOutput(); err != nil {
		return fmt.Errorf("could not start the transfer: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func readTransferState(job string) transferState {
	st := transferState{}
	if raw, err := os.ReadFile(transferLog(job)); err == nil {
		if len(raw) > 16<<10 {
			raw = raw[len(raw)-16<<10:]
		}
		st.Log = string(raw)
	}
	if raw, err := os.ReadFile(transferRC(job)); err == nil {
		st.Done = true
		st.RC, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	return st
}

// restoreSource undoes a live move's cutover, suspend kept the original vhosts and the transfer kept the original zones
func (m *Manager) restoreSource(account string) error {
	rows, err := m.MySQL.Query(`SELECT d.domain_url FROM domains d JOIN users u ON d.user_id = u.id WHERE u.username = ? OR u.username LIKE ?`, account, `SUSPENDED\_%\_`+account)
	if err != nil {
		return err
	}
	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return err
		}
		domains = append(domains, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range domains {
		kept := "/etc/openpanel/caddy/suspended_domains/" + d + ".conf"
		if raw, err := os.ReadFile(kept); err == nil {
			if err := os.WriteFile("/etc/openpanel/caddy/domains/"+d+".conf", raw, 0644); err != nil {
				return err
			}
			// suspend won't overwrite an existing copy, a stale one would come back on the next suspend
			os.Remove(kept)
		}
		zone := "/etc/bind/zones/" + d + ".zone"
		if raw, err := os.ReadFile(zone + ".pre-move"); err == nil {
			if err := os.WriteFile(zone, raw, 0644); err != nil {
				return err
			}
			os.Remove(zone + ".pre-move")
		}
	}
	out, err := exec.Command("opencli", "user-unsuspend", account).CombinedOutput()
	if err != nil {
		return fmt.Errorf("user-unsuspend failed: %s", strings.TrimSpace(string(out)))
	}
	_ = exec.Command("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Run()
	_ = exec.Command("podman", "exec", "openpanel_dns", "rndc", "reload").Run()
	return nil
}

var (
	sshHostKeyPaths = []string{"/etc/ssh/ssh_host_ed25519_key.pub", "/etc/ssh/ssh_host_ecdsa_key.pub", "/etc/ssh/ssh_host_rsa_key.pub"}
	hostKeyRe       = regexp.MustCompile(`^(ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa) [A-Za-z0-9+/=]+$`)
)

// sshHostKey lets the master tell a source server who it is about to connect to, not every server has every key type
func sshHostKey() (string, error) {
	for _, path := range sshHostKeyPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if f := strings.Fields(string(raw)); len(f) >= 2 {
			return f[0] + " " + f[1], nil
		}
	}
	return "", errors.New("this server has no ssh host key")
}

func writeKnownHosts(ip, key string) (string, error) {
	if !ipRe.MatchString(ip) || !hostKeyRe.MatchString(key) {
		return "", errors.New("invalid ssh host key")
	}
	path := filepath.Join(Dir, "ssh", "known_hosts_"+strings.NewReplacer(":", "_", ".", "_").Replace(ip))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(ip+" "+key+"\n"), 0600)
}

// mailSync copies mail that arrived after the move, it runs right before the source copy is deleted
func (m *Manager) mailSync(a accountArgs) error {
	if !ipRe.MatchString(a.DestIP) {
		return errors.New("invalid destination")
	}
	known, err := writeKnownHosts(a.DestIP, a.HostKey)
	if err != nil {
		return err
	}
	var context string
	rows, err := m.MySQL.Query(`SELECT u.server, d.domain_url FROM domains d JOIN users u ON d.user_id = u.id WHERE u.username = ? OR u.username LIKE ?`, a.Account, `SUSPENDED\_%\_`+a.Account)
	if err != nil {
		return err
	}
	defer rows.Close()
	store := strings.TrimSpace(config.Load(config.AdminConfigPath).Get("EMAIL", "email_storage_location", ""))
	remoteShell := "ssh -i " + sshKeyPath() + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + known
	for rows.Next() {
		var domain string
		if err := rows.Scan(&context, &domain); err != nil {
			return err
		}
		dir := filepath.Join("/home", context, "mail", domain)
		if strings.HasPrefix(store, "/") {
			dir = filepath.Join(store, domain)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if out, err := exec.Command("rsync", "-azHAX", "--numeric-ids", "-e", remoteShell, dir+"/", "root@"+a.DestIP+":"+dir+"/").CombinedOutput(); err != nil {
			return fmt.Errorf("mail sync for %s failed: %s", domain, strings.TrimSpace(string(out)))
		}
	}
	return rows.Err()
}
