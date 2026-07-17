package handlers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Dashboard bundles the dashboard handlers and their dependencies.
type Dashboard struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

type dashboardAdminData struct {
	ForceDomain       string
	UserCount         int
	PlanCount         int
	SiteCount         int
	DomainCount       int
	RunningContainers int
	MailCount         int
	ServerCount       int
}

type dashboardResellerData struct {
	Username        string
	LastIP          string
	LastLogin       string
	MaxAccounts     string
	CurrentAccounts string
	AccountsPercent string
	CurrentDiskGB   string
	MaxDiskGB       string
	DiskPercent     string
	AllowedPlans    []string
}

// dashboardPageData is the combined shape webtemplates/dashboard.html
// renders against: the shared chrome plus every field either the admin or
// reseller branch might use (the branch not taken leaves its half zeroed).
type dashboardPageData struct {
	webtemplates.Chrome
	dashboardAdminData
	dashboardResellerData
	Flashes []auth.Flash
}

// blocksToGB converts a 1024-byte block count to GiB, rounded to 2
// decimals; returns "N/A" if it isn't a number.
func blocksToGB(blocks interface{}) string {
	var f float64
	switch v := blocks.(type) {
	case float64:
		f = v
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "N/A"
		}
		f = parsed
	default:
		return "N/A"
	}
	gb := (f * 1024) / (1024 * 1024 * 1024)
	return strconv.FormatFloat(gb, 'f', 2, 64)
}

// dashboardPercent computes current/max*100, rounded to 1 decimal, or 0 if
// either side is zero/not a number (e.g. max == "unlimited").
func dashboardPercent(current, max string) string {
	c, cerr := strconv.ParseFloat(current, 64)
	m, merr := strconv.ParseFloat(max, 64)
	if cerr != nil || merr != nil || c == 0 || m == 0 {
		return "0"
	}
	pct := math.Round((c/m)*100*10) / 10
	return strconv.FormatFloat(pct, 'f', -1, 64)
}

// ServeDashboard handles GET "/" and GET "/dashboard". Renders the admin
// summary for admin/user roles, or the reseller-scoped summary for
// resellers.
func (d *Dashboard) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	user := auth.CurrentUser(r)
	wantJSON := r.URL.Query().Get("output") == "json"

	if user.Role == "reseller" {
		d.serveResellerDashboard(w, r, user, wantJSON)
		return
	}
	d.serveAdminDashboard(w, r, wantJSON)
}

func (d *Dashboard) serveAdminDashboard(w http.ResponseWriter, r *http.Request, wantJSON bool) {
	forceDomain := config.Openpanel().Get("DEFAULT", "force_domain", "")

	counts, err := paneldb.GetCounts(d.MySQL)
	if err != nil {
		d.serveAdminDashboardError(w, r, wantJSON)
		return
	}
	dockerContexts, err := paneldb.DockerContexts(d.MySQL)
	if err != nil {
		dockerContexts = 1
	}

	data := dashboardAdminData{
		ForceDomain:       forceDomain,
		UserCount:         counts.UserCount,
		PlanCount:         counts.PlanCount,
		SiteCount:         counts.SiteCount,
		DomainCount:       counts.DomainCount,
		RunningContainers: localContainerCount(),
		MailCount:         emailCount(),
		ServerCount:       dockerContexts,
	}

	if wantJSON {
		writeJSON(w, data)
		return
	}
	webtemplates.Render(w, "dashboard.html", dashboardPageData{
		Chrome:             buildChrome(r, "Dashboard"),
		dashboardAdminData: data,
		Flashes:            auth.PopFlashes(w, r, d.Sessions),
	})
}

func (d *Dashboard) serveAdminDashboardError(w http.ResponseWriter, r *http.Request, wantJSON bool) {
	data := dashboardAdminData{ServerCount: 1}
	if wantJSON {
		writeJSON(w, data)
		return
	}
	webtemplates.Render(w, "dashboard.html", dashboardPageData{
		Chrome:             buildChrome(r, "Dashboard"),
		dashboardAdminData: data,
		Flashes:            auth.PopFlashes(w, r, d.Sessions),
	})
}

func (d *Dashboard) serveResellerDashboard(w http.ResponseWriter, r *http.Request, user *admindb.User, wantJSON bool) {
	resellerFile := "/etc/openpanel/openadmin/resellers/" + user.Username + ".json"
	maxAccounts, currentAccounts, allowedPlans := "unlimited", "0", []string{}
	var currentDiskBlocks, maxDiskBlocks interface{} = float64(0), "unlimited"

	if raw, err := os.ReadFile(resellerFile); err == nil {
		var parsed struct {
			MaxAccounts       interface{} `json:"max_accounts"`
			CurrentAccounts   interface{} `json:"current_accounts"`
			AllowedPlans      []string    `json:"allowed_plans"`
			CurrentDiskBlocks interface{} `json:"current_disk_blocks"`
			MaxDiskBlocks     interface{} `json:"max_disk_blocks"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			if parsed.MaxAccounts != nil {
				maxAccounts = toDisplayString(parsed.MaxAccounts)
			}
			if parsed.CurrentAccounts != nil {
				currentAccounts = toDisplayString(parsed.CurrentAccounts)
			}
			if parsed.AllowedPlans != nil {
				allowedPlans = parsed.AllowedPlans
			}
			if parsed.CurrentDiskBlocks != nil {
				currentDiskBlocks = parsed.CurrentDiskBlocks
			}
			if parsed.MaxDiskBlocks != nil {
				maxDiskBlocks = parsed.MaxDiskBlocks
			}
		}
	}

	maxDiskGB := "unlimited"
	if toDisplayString(maxDiskBlocks) != "unlimited" {
		maxDiskGB = blocksToGB(maxDiskBlocks)
	}

	lastIP, lastLogin := lastLoginFor(user.Username)

	currentDiskGB := blocksToGB(currentDiskBlocks)
	data := dashboardResellerData{
		Username:        user.Username,
		LastIP:          lastIP,
		LastLogin:       lastLogin,
		MaxAccounts:     maxAccounts,
		CurrentAccounts: currentAccounts,
		AccountsPercent: dashboardPercent(currentAccounts, maxAccounts),
		CurrentDiskGB:   currentDiskGB,
		MaxDiskGB:       maxDiskGB,
		DiskPercent:     dashboardPercent(currentDiskGB, maxDiskGB),
		AllowedPlans:    allowedPlans,
	}

	if wantJSON {
		writeJSON(w, data)
		return
	}
	webtemplates.Render(w, "dashboard.html", dashboardPageData{
		Chrome:                buildChrome(r, "Reseller Dashboard"),
		dashboardResellerData: data,
		Flashes:               auth.PopFlashes(w, r, d.Sessions),
	})
}

// lastLoginFor scans login.log for a reseller's most recent IP/timestamp.
func lastLoginFor(username string) (ip, login string) {
	f, err := os.Open(LoginLogPath)
	if err != nil {
		return "N/A", "N/A"
	}
	defer f.Close()

	var lastMatch []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 4 && parts[2] == username {
			lastMatch = parts
		}
	}
	if lastMatch == nil {
		return "N/A", "N/A"
	}
	return lastMatch[3], lastMatch[0] + " " + lastMatch[1]
}

func toDisplayString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

// localContainerCount counts containers in the local (root/"default")
// podman context only. Per-user remote podman contexts are not yet
// handled here -- see the backlog -- so this undercounts on installs with
// per-user rootless podman stacks.
func localContainerCount() int {
	out, err := exec.Command("podman", "container", "ls", "-a", "-q").Output()
	if err != nil {
		return 0
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	return len(lines)
}

// emailCount uses opencli's email list, without the postfix-accounts.cf
// fallback file parsing (see backlog).
func emailCount() int {
	out, err := exec.Command("opencli", "email-setup", "email", "list").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "*") {
			count++
		}
	}
	return count
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// --- /json/system ---

type systemInfo struct {
	Hostname         string `json:"hostname"`
	OS               string `json:"os"`
	Time             string `json:"time"`
	Kernel           string `json:"kernel"`
	CPU              string `json:"cpu"`
	OpenpanelVersion string `json:"openpanel_version"`
	Uptime           string `json:"uptime"`
	RunningProcesses string `json:"running_processes"`
	PackageUpdates   string `json:"package_updates"`
}

// osPrettyName reads the PRETTY_NAME= line from /etc/os-release, the
// standard cross-distro source for a human-readable OS name (Ubuntu,
// Debian, RHEL, ... all provide it).
func osPrettyName() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Unavailable"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if name, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(name), `"`)
		}
	}
	return "Unavailable"
}

// cpuModelName detects the CPU model via lscpu.
//
// LC_ALL/LANG are forced to "C" here: on a non-English locale (e.g.
// LANG=sr_RS.UTF-8) lscpu prints localized field labels ("Назив модела:"
// instead of "Model name:"), which would otherwise make this always fall
// through to "Unavailable" on such a host. Forcing the C locale avoids
// that.
func cpuModelName() string {
	cmd := exec.Command("lscpu")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return "Unavailable"
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Model name:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "Unavailable"
}

// packageUpdatesCount checks for pending package updates via apt, dnf, or
// yum, in that priority order.
func packageUpdatesCount() string {
	if _, err := exec.LookPath("apt"); err == nil {
		out, err := exec.Command("apt", "list", "--upgradable").Output()
		if err != nil {
			return "Unavailable"
		}
		count := strings.Count(string(out), "\n") - 1
		if count < 0 {
			count = 0
		}
		return strconv.Itoa(count)
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		out, err := exec.Command("dnf", "check-update").Output()
		if err != nil {
			return "Unavailable"
		}
		return strconv.Itoa(countUpdateLines(string(out), "Last metadata"))
	}
	if _, err := exec.LookPath("yum"); err == nil {
		out, err := exec.Command("yum", "check-update").Output()
		if err != nil {
			return "Unavailable"
		}
		return strconv.Itoa(countUpdateLines(string(out), "Loaded plugins"))
	}
	return "Unavailable"
}

func countUpdateLines(output, skipPrefix string) int {
	n := 0
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, skipPrefix) || strings.HasPrefix(trimmed, "Obsoleting") {
			continue
		}
		n++
	}
	return n
}

// ServeSystemInfo handles GET /json/system.
func (d *Dashboard) ServeSystemInfo(w http.ResponseWriter, r *http.Request) {
	info := systemInfo{
		CPU: "Unavailable", Uptime: "Unavailable", RunningProcesses: "Unavailable",
		OS: osPrettyName(), Time: time.Now().Format("2006-01-02 15:04:05"),
		OpenpanelVersion: chromeSite.PanelVersion, PackageUpdates: packageUpdatesCount(),
	}

	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info.Kernel = strings.TrimSpace(string(release))
	}
	info.CPU = cpuModelName()
	if uptime, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(uptime))
		if len(fields) > 0 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				info.Uptime = strconv.Itoa(int(secs))
			}
		}
	}
	if out, err := exec.Command("ps", "-e").Output(); err == nil {
		info.RunningProcesses = strconv.Itoa(strings.Count(string(out), "\n"))
	}

	writeJSON(w, info)
}

// --- /json/user_activity_status ---

// ServeUserActivityStatus handles GET /json/user_activity_status. This is
// always computed fresh, never cached.
func (d *Dashboard) ServeUserActivityStatus(w http.ResponseWriter, r *http.Request) {
	status, err := paneldb.ActiveUserSessions(d.MySQL)
	if err != nil {
		status = map[string]string{}
	}
	writeJSON(w, status)
}

// --- /json/combined_activity ---

// ServeCombinedActivity handles GET /json/combined_activity for the admin
// (non-reseller) path -- reseller-scoped username filtering is not yet
// implemented (see backlog).
func (d *Dashboard) ServeCombinedActivity(w http.ResponseWriter, r *http.Request) {
	const logsDir = "/etc/openpanel/openpanel/core/users"

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		writeJSON(w, map[string][]string{"combined_logs": {}})
		return
	}

	var logs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logPath := logsDir + "/" + e.Name() + "/activity.log"
		lines := tailLines(logPath, 20)
		logs = append(logs, lines...)
	}
	if len(logs) > 20 {
		logs = logs[:20]
	}

	writeJSON(w, map[string][]string{"combined_logs": logs})
}

// --- /json/{resource} ---

// ServeResourceUsage handles GET /json/{resource}: live cpu/memory/load/
// disk snapshots for the dashboard header's resource-usage widget. "io"
// and "network" resources aren't implemented -- see the backlog -- they're
// not called by any page this Go build serves.
func (d *Dashboard) ServeResourceUsage(w http.ResponseWriter, r *http.Request) {
	resource := r.PathValue("resource")
	switch resource {
	case "disk":
		writeJSON(w, diskUsageSnapshot())
	case "memory":
		writeJSON(w, ramUsageSnapshot())
	case "load":
		writeJSON(w, loadUsageSnapshot())
	case "cpu":
		writeJSON(w, cpuUsageSnapshot())
	default:
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid resource '%s' requested", resource))
	}
}

// cpuStatSample is one line of /proc/stat: idle is the sum of the idle and
// iowait fields, and total is the sum of all the CPU-time fields.
type cpuStatSample struct {
	idle, total uint64
}

func readCPUStatPerCore() map[string]cpuStatSample {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	out := map[string]cpuStatSample{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] == "cpu" || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		var sum uint64
		vals := make([]uint64, 0, len(fields)-1)
		for _, f := range fields[1:] {
			v, _ := strconv.ParseUint(f, 10, 64)
			vals = append(vals, v)
			sum += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		out[fields[0]] = cpuStatSample{idle: idle, total: sum}
	}
	return out
}

// cpuUsageSnapshot takes a 1-second blocking sample of per-core CPU
// percent.
func cpuUsageSnapshot() map[string]float64 {
	first := readCPUStatPerCore()
	time.Sleep(1 * time.Second)
	second := readCPUStatPerCore()

	result := map[string]float64{}
	if first == nil || second == nil {
		return result
	}

	names := make([]string, 0, len(first))
	for name := range first {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ni, _ := strconv.Atoi(strings.TrimPrefix(names[i], "cpu"))
		nj, _ := strconv.Atoi(strings.TrimPrefix(names[j], "cpu"))
		return ni < nj
	})

	for i, name := range names {
		s2, ok := second[name]
		if !ok {
			continue
		}
		s1 := first[name]
		totalDelta := float64(s2.total - s1.total)
		idleDelta := float64(s2.idle - s1.idle)
		percent := 0.0
		if totalDelta > 0 {
			percent = (1 - idleDelta/totalDelta) * 100
		}
		result[fmt.Sprintf("core_%d", i)] = math.Round(percent*10) / 10
	}
	return result
}

// loadUsageSnapshot reads the 1/5/15-minute load averages from /proc/loadavg.
func loadUsageSnapshot() map[string]float64 {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return map[string]float64{"load1min": 0, "load5min": 0, "load15min": 0}
	}
	fields := strings.Fields(string(raw))
	get := func(i int) float64 {
		if i >= len(fields) {
			return 0
		}
		v, _ := strconv.ParseFloat(fields[i], 64)
		return v
	}
	return map[string]float64{"load1min": get(0), "load5min": get(1), "load15min": get(2)}
}

func readMemInfo() map[string]uint64 {
	raw, err := os.ReadFile("/proc/meminfo")
	out := map[string]uint64{}
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		out[parts[0]] = kb * 1024
	}
	return out
}

func humanReadableGB(bytesVal uint64) string {
	return fmt.Sprintf("%.2f GB", float64(bytesVal)/(1024*1024*1024))
}

// ramUsageSnapshot returns RAM/swap totals plus a human-readable summary,
// as a three-key JSON shape.
func ramUsageSnapshot() map[string]interface{} {
	mem := readMemInfo()
	total := mem["MemTotal"]
	available := mem["MemAvailable"]
	used := uint64(0)
	if total > available {
		used = total - available
	}
	ramPercent := 0.0
	if total > 0 {
		ramPercent = math.Round(float64(used)/float64(total)*1000) / 10
	}

	swapTotal := mem["SwapTotal"]
	swapFree := mem["SwapFree"]
	swapUsed := uint64(0)
	if swapTotal > swapFree {
		swapUsed = swapTotal - swapFree
	}
	swapPercent := 0.0
	if swapTotal > 0 {
		swapPercent = math.Round(float64(swapUsed)/float64(swapTotal)*1000) / 10
	}

	vmstat := map[string]uint64{}
	if raw, err := os.ReadFile("/proc/vmstat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				vmstat[fields[0]] = v
			}
		}
	}
	const pageSize = 4096
	sin := vmstat["pswpin"] * pageSize
	sout := vmstat["pswpout"] * pageSize

	return map[string]interface{}{
		"ram_info": map[string]interface{}{
			"total": total, "available": available, "used": used, "percent": ramPercent,
		},
		"swap_info": map[string]interface{}{
			"total": swapTotal, "free": swapFree, "used": swapUsed, "percent": swapPercent,
			"sin": sin, "sout": sout,
		},
		"human_readable": map[string]interface{}{
			"ram": map[string]interface{}{
				"total": humanReadableGB(total), "available": humanReadableGB(available),
				"used": humanReadableGB(used), "percent": fmt.Sprintf("%v%%", ramPercent),
			},
			"swap": map[string]interface{}{
				"total": humanReadableGB(swapTotal), "free": humanReadableGB(swapFree),
				"used": humanReadableGB(swapUsed), "percent": fmt.Sprintf("%v%%", swapPercent),
				"sin": fmt.Sprintf("%d B", sin), "sout": fmt.Sprintf("%d B", sout),
			},
		},
	}
}

var diskUsagePseudoFstypes = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "devtmpfs": true,
	"devpts": true, "tmpfs": true, "mqueue": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true, "rpc_pipefs": true,
	"binfmt_misc": true, "hugetlbfs": true, "configfs": true, "fusectl": true,
}

// diskUsageSnapshot lists mounted filesystems (skipping pseudo-filesystems
// and the /snap, /boot, /etc/bind prefixes), each with a statfs-derived
// usage summary (used = total - free-including-reserved-blocks; percent =
// used / (used + free-available-to-non-root) * 100).
func diskUsageSnapshot() []map[string]interface{} {
	raw, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}

	type mount struct{ device, mountpoint, fstype string }
	var mounts []mount
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device, mountpoint, fstype := fields[0], fields[1], fields[2]
		if seen[mountpoint] {
			continue
		}
		if strings.HasPrefix(mountpoint, "/snap") || strings.HasPrefix(mountpoint, "/boot") || strings.HasPrefix(mountpoint, "/etc/bind") {
			continue
		}
		if diskUsagePseudoFstypes[fstype] && !strings.Contains(fstype, "sshfs") {
			continue
		}
		seen[mountpoint] = true
		mounts = append(mounts, mount{device, mountpoint, fstype})
	}

	out := make([]map[string]interface{}, 0, len(mounts))
	for _, m := range mounts {
		var st syscall.Statfs_t
		if err := syscall.Statfs(m.mountpoint, &st); err != nil {
			out = append(out, map[string]interface{}{
				"device": m.device, "mountpoint": m.mountpoint, "fstype": m.fstype,
				"error": err.Error(),
			})
			continue
		}
		bsize := uint64(st.Bsize)
		total := st.Blocks * bsize
		bavail := st.Bavail * bsize
		bfree := st.Bfree * bsize
		used := uint64(0)
		if total > bfree {
			used = total - bfree
		}
		percent := 0.0
		if used+bavail > 0 {
			percent = math.Round(float64(used)/float64(used+bavail)*1000) / 10
		}
		out = append(out, map[string]interface{}{
			"device": m.device, "mountpoint": m.mountpoint, "fstype": m.fstype,
			"total": total, "used": used, "free": bavail, "percent": percent,
		})
	}
	return out
}

func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var all []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			all = append(all, line)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// --- /server/resource-usage, /server/resource-usage/history(/data) ---

// ResourceUsageSnapshotFile is the sentinel JSONL snapshot log the
// resource-usage history reads from.
var ResourceUsageSnapshotFile = "/var/log/openpanel/admin/sentinel_snapshots.jsonl"

// resourceUsagePeriodLabels maps a lookback window in minutes to its
// display label.
var resourceUsagePeriodLabels = map[int]string{
	5: "5 minutes", 15: "15 minutes", 30: "30 minutes", 60: "1 hour",
	180: "3 hours", 360: "6 hours", 720: "12 hours", 1440: "24 hours",
	2880: "2 days", 10080: "7 days", 43200: "30 days",
}

func resourceUsagePeriodLabel(minutes int) string {
	if label, ok := resourceUsagePeriodLabels[minutes]; ok {
		return label
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// loadResourceUsageSnapshots reads the sentinel JSONL file, finds the
// latest "ts" scanning from the end, and returns every row whose "ts" is
// >= (latest - minutes), oldest-first (the file's natural top-to-bottom
// order). Rows that fail to parse are skipped.
func loadResourceUsageSnapshots(minutes int) []json.RawMessage {
	raw, err := os.ReadFile(ResourceUsageSnapshotFile)
	if err != nil {
		return []json.RawMessage{}
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return []json.RawMessage{}
	}

	const tsLayout = "2006-01-02 15:04:05"
	var latestTS time.Time
	found := false
	for i := len(lines) - 1; i >= 0; i-- {
		var row struct {
			TS string `json:"ts"`
		}
		if json.Unmarshal([]byte(lines[i]), &row) != nil || row.TS == "" {
			continue
		}
		ts, err := time.Parse(tsLayout, row.TS)
		if err != nil {
			continue
		}
		latestTS = ts
		found = true
		break
	}
	if !found {
		return []json.RawMessage{}
	}

	cutoff := latestTS.Add(-time.Duration(minutes) * time.Minute)
	snapshots := []json.RawMessage{}
	for _, l := range lines {
		var row struct {
			TS string `json:"ts"`
		}
		if json.Unmarshal([]byte(l), &row) != nil || row.TS == "" {
			continue
		}
		ts, err := time.Parse(tsLayout, row.TS)
		if err != nil {
			continue
		}
		if !ts.Before(cutoff) {
			snapshots = append(snapshots, json.RawMessage(l))
		}
	}
	return snapshots
}

// ServeResourceUsagePage handles GET /server/resource-usage.
func (d *Dashboard) ServeResourceUsagePage(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "system_resources.html", mergeChrome(map[string]interface{}{
		"Flashes": auth.PopFlashes(w, r, d.Sessions),
	}, r, "Resource Usage"))
}

// ServeResourceUsageHistory handles GET /server/resource-usage/history.
func (d *Dashboard) ServeResourceUsageHistory(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "line"
	}
	minutes := 60
	if m, err := strconv.Atoi(r.URL.Query().Get("minutes")); err == nil {
		minutes = m
	}
	snapshots := loadResourceUsageSnapshots(minutes)

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, snapshots)
		return
	}

	snapshotsJSON, err := json.Marshal(snapshots)
	if err != nil {
		snapshotsJSON = []byte("[]")
	}

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	webtemplates.Render(w, "server_resource_usage_history.html", mergeChrome(map[string]interface{}{
		"SnapshotsJSON": template.JS(snapshotsJSON),
		"Minutes":       minutes,
		"PeriodLabel":   resourceUsagePeriodLabel(minutes),
		"View":          view,
		"Flashes":       auth.PopFlashes(w, r, d.Sessions),
	}, r, "Resource Usage History"))
}

// ServeResourceUsageHistoryData handles GET
// /server/resource-usage/history/data.
func (d *Dashboard) ServeResourceUsageHistoryData(w http.ResponseWriter, r *http.Request) {
	minutes := 60
	if m, err := strconv.Atoi(r.URL.Query().Get("minutes")); err == nil {
		minutes = m
	}
	snapshots := loadResourceUsageSnapshots(minutes)
	writeJSON(w, map[string]interface{}{
		"minutes":   minutes,
		"count":     len(snapshots),
		"snapshots": snapshots,
	})
}
