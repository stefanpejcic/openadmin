// This file implements the JSON REST API's host-introspection surface:
// docker/podman info, the dedicated-IP-per-user listing, and the
// system/cpu/memory/disk snapshots also used by the dashboard's live
// resource-usage widgets.
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
)

// APISystem bundles the /api/docker/info, /api/ips, /api/system, and
// /api/usage/* handlers.
type APISystem struct {
	MySQL *sql.DB
}

// apiDockerInfoRun runs `podman info --format json`. Injectable so tests
// never shell out to a real podman binary.
var apiDockerInfoRun = func() ([]byte, error) {
	cmd, err := podman.Command("default", "info", "--format", "json")
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}

// ServeDockerInfo handles GET /api/docker/info.
func (s *APISystem) ServeDockerInfo(w http.ResponseWriter, r *http.Request) {
	out, err := apiDockerInfoRun()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var data interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, data)
}

// apiIPFileBaseDir is the per-user account directory each ip.json lives
// under (shared in spirit with AutologinTokenBaseDir in autologin.go, kept
// as its own independent constant since the two paths can be configured
// separately).
var apiIPFileBaseDir = "/etc/openpanel/openpanel/core/users"

// ServeIPs handles GET /api/ips: every user's dedicated IP, read from each
// account's own ip.json (accounts with no such file, or an unparseable
// one, are simply omitted).
func (s *APISystem) ServeIPs(w http.ResponseWriter, r *http.Request) {
	users, err := paneldb.GetAllUsers(s.MySQL, "")
	if err != nil {
		// The user list is never expected to fail to load here; a caller
		// iterating over it unconditionally would simply crash, so this
		// does too rather than papering over a broken user table with an
		// empty response.
		panic(err)
	}

	usersIPs := map[string]interface{}{}
	for _, u := range users {
		username, _ := u["username"].(string)
		if username == "" {
			continue
		}
		raw, err := os.ReadFile(apiIPFileBaseDir + "/" + username + "/ip.json")
		if err != nil {
			continue
		}
		var data map[string]interface{}
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		usersIPs[username] = data["ip"]
	}

	writeJSON(w, usersIPs)
}

// ServeSystemInfo handles GET /api/system. Deliberately mirrors
// (*Dashboard).ServeSystemInfo's field-by-field construction (dashboard.go)
// rather than sharing a helper across packages-internal boundaries; both
// delegate to the same underlying osPrettyName/cpuModelName/
// packageUpdatesCount primitives.
func (s *APISystem) ServeSystemInfo(w http.ResponseWriter, r *http.Request) {
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

// ServeCPUUsage handles GET /api/usage/cpu: a 1-second blocking per-core
// sample, identical to the dashboard's /json/cpu widget.
func (s *APISystem) ServeCPUUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, cpuUsageSnapshot())
}

// ServeMemoryUsage handles GET /api/usage/memory. Same total/available/used
// figures as the dashboard's /json/memory widget (ramUsageSnapshot in
// dashboard.go), but reshaped into this endpoint's own (narrower, no swap)
// two-key response.
func (s *APISystem) ServeMemoryUsage(w http.ResponseWriter, r *http.Request) {
	mem := readMemInfo()
	total := mem["MemTotal"]
	available := mem["MemAvailable"]
	used := uint64(0)
	if total > available {
		used = total - available
	}
	percent := 0.0
	if total > 0 {
		percent = math.Round(float64(used)/float64(total)*1000) / 10
	}

	writeJSON(w, map[string]interface{}{
		"ram_info": map[string]interface{}{
			"total": total, "available": available, "used": used, "percent": percent,
		},
		"human_readable_info": map[string]interface{}{
			"total":     humanReadableGB(total),
			"available": humanReadableGB(available),
			"used":      humanReadableGB(used),
			"percent":   fmt.Sprintf("%v%%", percent),
		},
	})
}

// ServeDiskUsage handles GET /api/usage/server: every mounted filesystem
// (skipping pseudo-filesystems and /snap), each with a statfs-derived usage
// summary. Same total/used/free/percent formula as the dashboard's
// /json/disk widget (diskUsageSnapshot in dashboard.go), but without that
// widget's extra /boot and /etc/bind exclusions, matching this endpoint's
// own narrower filter.
func (s *APISystem) ServeDiskUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, apiDiskPartitionsUsage())
}

func apiDiskPartitionsUsage() []map[string]interface{} {
	raw, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return []map[string]interface{}{}
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
		if seen[mountpoint] || strings.HasPrefix(mountpoint, "/snap") || strings.HasPrefix(mountpoint, "/var/lib/containers/storage") {
			continue
		}
		if diskUsagePseudoFstypes[fstype] {
			continue
		}
		seen[mountpoint] = true
		mounts = append(mounts, mount{device, mountpoint, fstype})
	}

	out := make([]map[string]interface{}, 0, len(mounts))
	for _, m := range mounts {
		var st syscall.Statfs_t
		if err := syscall.Statfs(m.mountpoint, &st); err != nil {
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
