package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"openadmin/internal/config"
)

const (
	GroupCore        = "core"
	GroupSecrets     = "secrets"
	GroupNameservers = "nameservers"

	filePrefix = "file:"
	followPoll = 5 * time.Minute
	keepBackup = 10
)

// ini files are synced key by key because they mix shared values with per-server ones like the license
var (
	iniCoreSections = map[string]map[string]bool{
		"openpanel": {"USERS": true, "LOGS": true, "STATS": true, "APPS": true, "DOCKER": true, "SECURITY": true, "FILES": true, "DATABASES": true},
		"admin":     {"USERS": true, "RESELLERS": true, "PANEL": true},
	}
	iniCoreKeys = map[string]bool{
		"openpanel:DEFAULT.dir": true, "openpanel:DEFAULT.brand_name": true, "openpanel:DEFAULT.logo": true, "openpanel:DEFAULT.favicon": true,
		"openpanel:DEFAULT.logout_url": true, "openpanel:DEFAULT.enabled_modules": true,
		"openpanel:PANEL.autoupdate": true, "openpanel:PANEL.autopatch": true, "openpanel:PANEL.template": true, "openpanel:PANEL.admin_template": true,
		"openpanel:PANEL.screenshots": true, "openpanel:PANEL.favicons": true, "openpanel:PANEL.temporary_links": true, "openpanel:PANEL.favorites_items": true,
		"openpanel:PANEL.terminal_timeout": true, "openpanel:PANEL.menu_style": true, "openpanel:PANEL.applications_dashboard_items": true,
	}
	iniSecretSections = map[string]map[string]bool{
		"openpanel": {"SMTP": true, "CAPTCHA": true, "AI": true},
		"admin":     {"CLOUDFLARE": true},
	}
	iniNever = map[string]bool{"openpanel:SMTP.mail_security_token": true}
	iniNS    = map[string]bool{"openpanel:DEFAULT.ns1": true, "openpanel:DEFAULT.ns2": true, "openpanel:DEFAULT.ns3": true, "openpanel:DEFAULT.ns4": true}

	fileGlobs = []string{
		"/etc/openpanel/openpanel/features/*.txt",
		"/etc/openpanel/openpanel/custom_code/*",
		"/etc/openpanel/openpanel/conf/blacklist_useragents.txt",
		"/etc/openpanel/openpanel/conf/domain_restriction.txt",
		"/etc/openpanel/openadmin/config/forbidden_usernames.txt",
		"/etc/openpanel/openadmin/config/notifications.ini",
		"/etc/openpanel/bind9/zone_template.txt",
		"/etc/openpanel/bind9/zone_template_ipv6.txt",
	}
)

func iniPath(file string) string {
	if file == "admin" {
		return config.AdminConfigPath
	}
	return config.OpenpanelConfigPath
}

// classify says which group a key belongs to, "" means it never leaves this server, apply checks it too so a bad master can't write anywhere else
func classify(key string) string {
	if path, ok := strings.CutPrefix(key, filePrefix); ok {
		if filepath.Clean(path) != path {
			return ""
		}
		for _, g := range fileGlobs {
			if ok, _ := filepath.Match(g, path); ok {
				return GroupCore
			}
		}
		return ""
	}
	file, rest, ok := strings.Cut(key, ":")
	if !ok || (file != "openpanel" && file != "admin") {
		return ""
	}
	section, _, ok := strings.Cut(rest, ".")
	if !ok || iniNever[key] {
		return ""
	}
	switch {
	case iniNS[key]:
		return GroupNameservers
	case iniCoreKeys[key], iniCoreSections[file][section]:
		return GroupCore
	case iniSecretSections[file][section]:
		return GroupSecrets
	}
	return ""
}

// Snapshot is every shareable value of a server
type Snapshot struct {
	Rev    string            `json:"rev"`
	Values map[string]string `json:"values"`
}

func BuildSnapshot() (*Snapshot, error) {
	values := map[string]string{}
	for _, file := range []string{"openpanel", "admin"} {
		for section, kv := range config.Load(iniPath(file)) {
			for k, v := range kv {
				key := file + ":" + section + "." + k
				if classify(key) != "" {
					values[key] = v
				}
			}
		}
	}
	for _, g := range fileGlobs {
		matches, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			values[filePrefix+path] = string(raw)
		}
	}
	return &Snapshot{Rev: snapshotRev(values), Values: values}, nil
}

func snapshotRev(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s\x00%s\x00", k, values[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type Change struct {
	Key    string `json:"key"`
	Group  string `json:"group"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Pinned bool   `json:"pinned,omitempty"`
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// diff lists what applying remote would change, keys missing on the master are left alone
func diff(local, remote map[string]string, groups, pinned []string) []Change {
	changes := []Change{}
	for k, v := range remote {
		group := classify(k)
		if group == "" || !hasString(groups, group) || local[k] == v {
			continue
		}
		c := Change{Key: k, Group: group, Old: local[k], New: v, Pinned: hasString(pinned, k)}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes
}

// masked keeps secrets out of the preview, showing that one changed is enough
func masked(changes []Change) []Change {
	for i := range changes {
		c := &changes[i]
		if c.Group == GroupSecrets {
			if c.Old != "" {
				c.Old = "********"
			}
			if c.New != "" {
				c.New = "********"
			}
		}
		if strings.HasPrefix(c.Key, filePrefix) {
			c.Old = fmt.Sprintf("(%d bytes)", len(c.Old))
			c.New = fmt.Sprintf("(%d bytes)", len(c.New))
		}
	}
	return changes
}

func (m *Manager) fetchMasterSnapshot() (*Snapshot, error) {
	st := m.State()
	if st.Role != RoleSlave {
		return nil, errors.New("this server is not a slave")
	}
	req, err := http.NewRequest(http.MethodGet, st.MasterURL+"/_cluster/snapshot", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Cluster "+st.NodeID+":"+st.NodeSecret)
	resp, err := m.client(m.masterNode(st)).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the master: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRPCBody*4))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("master answered: %s", strings.TrimSpace(string(body)))
	}
	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return nil, errors.New("master sent an unreadable snapshot")
	}
	return &snap, nil
}

// PreviewPull shows what a sync from the master would change on this slave
func (m *Manager) PreviewPull(groups []string) ([]Change, error) {
	remote, err := m.fetchMasterSnapshot()
	if err != nil {
		return nil, err
	}
	local, err := BuildSnapshot()
	if err != nil {
		return nil, err
	}
	st := m.State()
	if len(groups) == 0 {
		groups = st.Groups
	}
	return masked(diff(local.Values, remote.Values, groups, st.Pinned)), nil
}

type SyncResult struct {
	Applied int    `json:"applied"`
	Skipped int    `json:"skipped"`
	Message string `json:"message"`
}

// Pull applies the master's settings here, pinned keys stay as they are
func (m *Manager) Pull(groups []string) (SyncResult, error) {
	st := m.State()
	if len(groups) == 0 {
		groups = st.Groups
	}
	remote, err := m.fetchMasterSnapshot()
	if err != nil {
		return SyncResult{}, m.recordSync(st.LastRev, err)
	}
	local, err := BuildSnapshot()
	if err != nil {
		return SyncResult{}, m.recordSync(st.LastRev, err)
	}
	changes := diff(local.Values, remote.Values, groups, st.Pinned)
	res := SyncResult{}
	var apply []Change
	for _, c := range changes {
		if c.Pinned {
			res.Skipped++
		} else {
			apply = append(apply, c)
		}
	}
	if len(apply) > 0 {
		if err := applyChanges(apply); err != nil {
			return SyncResult{}, m.recordSync(st.LastRev, err)
		}
		restartServices()
	}
	res.Applied = len(apply)
	res.Message = fmt.Sprintf("%d setting(s) applied, %d pinned and kept", res.Applied, res.Skipped)
	if res.Applied > 0 {
		Notify("Settings synced from the master", res.Message+".", "info")
	}
	_ = m.update(func(s *State) error {
		s.LastRev, s.LastSyncAt, s.LastSyncMsg = remote.Rev, time.Now(), res.Message
		return nil
	})
	return res, nil
}

func (m *Manager) recordSync(rev string, cause error) error {
	Notify("Settings sync from the master failed", cause.Error(), "warning")
	_ = m.update(func(s *State) error {
		s.LastRev, s.LastSyncAt, s.LastSyncMsg = rev, time.Now(), "failed: "+cause.Error()
		return nil
	})
	return cause
}

func backupDir() string { return filepath.Join(Dir, "backups") }

// backup copies every file a change touches, a file that didn't exist yet is listed so rollback removes it
func backup(paths []string) error {
	dir := filepath.Join(backupDir(), time.Now().Format("20060102-150405.000"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var missing []string
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, p)
			continue
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, "files", p)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, raw, 0600); err != nil {
			return err
		}
	}
	if err := writeJSONFile(filepath.Join(dir, "missing.json"), missing); err != nil {
		return err
	}
	entries, err := os.ReadDir(backupDir())
	if err == nil && len(entries) > keepBackup {
		for _, e := range entries[:len(entries)-keepBackup] {
			os.RemoveAll(filepath.Join(backupDir(), e.Name()))
		}
	}
	return nil
}

func applyChanges(changes []Change) error {
	touched := map[string]bool{}
	for _, c := range changes {
		if classify(c.Key) == "" {
			return fmt.Errorf("refusing to sync %q", c.Key)
		}
		if path, ok := strings.CutPrefix(c.Key, filePrefix); ok {
			touched[path] = true
		} else {
			file, _, _ := strings.Cut(c.Key, ":")
			touched[iniPath(file)] = true
		}
	}
	paths := make([]string, 0, len(touched))
	for p := range touched {
		paths = append(paths, p)
	}
	if err := backup(paths); err != nil {
		return fmt.Errorf("could not back up current settings: %w", err)
	}

	loaded := map[string]config.Data{}
	for _, c := range changes {
		if path, ok := strings.CutPrefix(c.Key, filePrefix); ok {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(c.New), 0644); err != nil {
				return err
			}
			continue
		}
		file, rest, _ := strings.Cut(c.Key, ":")
		section, key, _ := strings.Cut(rest, ".")
		if loaded[file] == nil {
			loaded[file] = config.Load(iniPath(file))
		}
		loaded[file].Set(section, key, c.New)
	}
	for file, data := range loaded {
		if err := config.Save(iniPath(file), data); err != nil {
			return err
		}
	}
	return nil
}

// Rollback restores the newest backup
func (m *Manager) Rollback() (string, error) {
	entries, err := os.ReadDir(backupDir())
	if err != nil || len(entries) == 0 {
		return "", errors.New("there is no backup to restore")
	}
	latest := filepath.Join(backupDir(), entries[len(entries)-1].Name())
	root := filepath.Join(latest, "files")
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(strings.TrimPrefix(p, root), raw, 0644)
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if raw, err := os.ReadFile(filepath.Join(latest, "missing.json")); err == nil {
		var missing []string
		_ = json.Unmarshal(raw, &missing)
		for _, p := range missing {
			if classify(filePrefix+p) != "" {
				os.Remove(p)
			}
		}
	}
	if err := os.RemoveAll(latest); err != nil {
		return "", err
	}
	_ = m.update(func(s *State) error {
		s.LastRev, s.LastSyncAt, s.LastSyncMsg = "", time.Now(), "last sync undone"
		return nil
	})
	restartServices()
	return filepath.Base(latest), nil
}

// restartServices runs detached and delayed so the http response that triggered it still goes out
func restartServices() {
	// a restart can hang with the container stuck in "Stopping", then it's removed and brought up again
	script := `sleep 2; timeout 90 podman restart openpanel >/dev/null 2>&1 || { timeout 60 podman rm -f openpanel >/dev/null 2>&1; cd /root && timeout 180 podman-compose up -d openpanel >/dev/null 2>&1; }; systemctl restart admin`
	if path, err := exec.LookPath("systemd-run"); err == nil {
		unit := fmt.Sprintf("openadmin-cluster-restart-%d", time.Now().UnixNano())
		if exec.Command(path, "--unit="+unit, "--collect", "--quiet", "/bin/bash", "-c", script).Run() == nil {
			return
		}
	}
	cmd := exec.Command("/bin/bash", "-c", script)
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// UnsyncedCount is how many shared values changed on the master since the last push
func (m *Manager) UnsyncedCount() int {
	st := m.State()
	if st.Role != RoleMaster || st.LastSynced == nil {
		return 0
	}
	snap, err := BuildSnapshot()
	if err != nil {
		return 0
	}
	return len(diff(st.LastSynced, snap.Values, []string{GroupCore}, nil))
}

// MarkSynced records the current values as the baseline once a push went through
func (m *Manager) MarkSynced() {
	snap, err := BuildSnapshot()
	if err != nil {
		return
	}
	_ = m.update(func(s *State) error {
		s.LastSynced = snap.Values
		return nil
	})
}

// SetSlaveSettings saves this slave's sync mode and pinned keys
func (m *Manager) SetSlaveSettings(mode string, pinned, groups []string) error {
	if mode != ModeManual && mode != ModeFollow {
		return errors.New("unknown sync mode")
	}
	return m.update(func(s *State) error {
		if s.Role != RoleSlave {
			return errors.New("this server is not a slave")
		}
		s.Mode, s.Pinned = mode, pinned
		if len(groups) > 0 {
			s.Groups = groups
		}
		return nil
	})
}

// RunBackground keeps node health fresh on a master and pulls settings on a slave in follow mode
func (m *Manager) RunBackground() {
	go func() {
		lastFollow := time.Time{}
		for {
			st := m.State()
			switch st.Role {
			case RoleMaster:
				if m.IsMaster() {
					m.refreshHealth()
				}
			case RoleSlave:
				if st.Mode == ModeFollow && time.Since(lastFollow) >= followPoll {
					lastFollow = time.Now()
					if remote, err := m.fetchMasterSnapshot(); err == nil && remote.Rev != st.LastRev {
						_, _ = m.Pull(nil)
					}
				}
			}
			time.Sleep(30 * time.Second)
		}
	}()
}
