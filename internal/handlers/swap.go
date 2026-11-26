// This file implements the Server > Swap page: viewing current swap usage,
// resizing/creating the swap file, and dropping (clearing) swap the same
// way opencli's sentinel.sh does it (swapoff -a; swapon -a).
package handlers

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// Swap bundles the /server/swap* handlers.
type Swap struct {
	Sessions *auth.Manager
}

// SwapFilePath is where the managed swap file lives. Sentinel and most
// OpenPanel installs default to this path, matching /etc/fstab.
var SwapFilePath = "/swapfile"

type swapDeviceInfo struct {
	Name     string
	Type     string
	SizeMB   int64
	UsedMB   int64
	Priority string
}

type swapStatus struct {
	TotalMB           int64
	UsedMB            int64
	FreeMB            int64
	UsedPercent       int
	Devices           []swapDeviceInfo
	ThresholdPercent  int
	ManagedFileExists bool
	// size and usage of /swapfile itself
	ManagedMB     int64
	ManagedUsedMB int64
	AvailRAMMB    int64
	DiskFreeMB    int64
	// biggest size the form takes, what's managed now plus free disk minus a reserve
	MaxMB int64
	Top   []swapProc
}

const (
	swapDiskReserveMB = 1024
	swapRAMReserveMB  = 256
)

func (s swapStatus) MaxGB() string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(s.MaxMB)/1024, 'f', 2, 64), "0"), ".")
}
func (s swapStatus) AvailRAMGB() string { return mbToGB(s.AvailRAMMB) }
func (s swapStatus) DiskFreeGB() string { return mbToGB(s.DiskFreeMB) }

// CanDrop is false when the swapped out pages wouldn't fit in free RAM
func (s swapStatus) CanDrop() bool { return s.UsedMB+swapRAMReserveMB <= s.AvailRAMMB }

// swapRoomRun returns available RAM and free disk where the swap file lives, injectable for tests
var swapRoomRun = func() (ramMB, diskMB int64) {
	ramMB = int64(readMemInfo()["MemAvailable"] >> 20)
	var st syscall.Statfs_t
	if syscall.Statfs(filepath.Dir(SwapFilePath), &st) == nil {
		diskMB = int64(st.Bavail * uint64(st.Bsize) >> 20)
	}
	return
}

type swapProc struct {
	PID       int
	Name      string
	Owner     string
	PanelUser string
	SwapMB    int64
}

func (p swapProc) SwapGB() string { return mbToGB(p.SwapMB) }

var swapProcDir = "/proc"

// topSwapProcs reads VmSwap from every /proc/<pid>/status, one small read per process so it's cheap enough for page load
func topSwapProcs(limit int) []swapProc {
	dirs, _ := os.ReadDir(swapProcDir)
	var out []swapProc
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(swapProcDir, d.Name(), "status"))
		if err != nil {
			continue
		}
		p := swapProc{PID: pid}
		for _, line := range strings.Split(string(raw), "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			f := strings.Fields(v)
			if len(f) == 0 {
				continue
			}
			switch k {
			case "Name":
				p.Name = f[0]
			case "Uid":
				p.Owner = f[0]
			case "VmSwap":
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				p.SwapMB = kb >> 10
			}
		}
		if p.SwapMB > 0 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SwapMB > out[j].SwapMB })
	if len(out) > limit {
		out = out[:limit]
	}
	ranges := readSubuidRanges(SubuidPath)
	for i := range out {
		if u, err := user.LookupId(out[i].Owner); err == nil {
			out[i].Owner = u.Username
		}
		out[i].PanelUser = panelUserFor(out[i].Owner, LocalesUsersDir, ranges)
	}
	return out
}

// mbToGB formats MB as GB with 2 decimals, matching the GB resize input
func mbToGB(mb int64) string { return fmt.Sprintf("%.2f GB", float64(mb)/1024) }

func (d swapDeviceInfo) SizeGB() string { return mbToGB(d.SizeMB) }
func (d swapDeviceInfo) UsedGB() string { return mbToGB(d.UsedMB) }
func (s swapStatus) TotalGB() string    { return mbToGB(s.TotalMB) }
func (s swapStatus) UsedGB() string     { return mbToGB(s.UsedMB) }
func (s swapStatus) FreeGB() string     { return mbToGB(s.FreeMB) }

// swapFreeRun/swapShowRun are injectable so tests never actually shell out.
var (
	swapFreeRun = func() (string, error) {
		out, err := exec.Command("free", "-m").Output()
		return string(out), err
	}
	swapShowRun = func() (string, error) {
		out, err := exec.Command("swapon", "--show=NAME,TYPE,SIZE,USED,PRIO", "--noheadings", "--bytes").Output()
		return string(out), err
	}
)

var swapFreeLineRe = regexp.MustCompile(`^Swap:\s+(\d+)\s+(\d+)\s+(\d+)`)

// parseSwapFree returns (totalMB, usedMB) from `free -m`'s Swap: line.
func parseSwapFree() (int64, int64) {
	out, err := swapFreeRun()
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		if m := swapFreeLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			total, _ := strconv.ParseInt(m[1], 10, 64)
			used, _ := strconv.ParseInt(m[2], 10, 64)
			return total, used
		}
	}
	return 0, 0
}

// parseSwapDevices returns every active swap device/file, as reported by
// `swapon --show`. Sizes are converted from bytes to MB.
func parseSwapDevices() []swapDeviceInfo {
	devices := []swapDeviceInfo{}
	out, err := swapShowRun()
	if err != nil {
		return devices
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		sizeBytes, _ := strconv.ParseInt(fields[2], 10, 64)
		usedBytes, _ := strconv.ParseInt(fields[3], 10, 64)
		devices = append(devices, swapDeviceInfo{
			Name:     fields[0],
			Type:     fields[1],
			SizeMB:   sizeBytes / 1024 / 1024,
			UsedMB:   usedBytes / 1024 / 1024,
			Priority: fields[4],
		})
	}
	return devices
}

// getSwapStatus assembles the current swap view.
func getSwapStatus() swapStatus {
	total, used := parseSwapFree()
	percent := 0
	if total > 0 {
		percent = int(used * 100 / total)
	}
	threshold, _ := strconv.Atoi(config.Load(NotificationsConfigPath).Get("DEFAULT", "swap", "80"))

	_, statErr := os.Stat(SwapFilePath)

	devs := parseSwapDevices()
	st := swapStatus{
		TotalMB:           total,
		UsedMB:            used,
		FreeMB:            total - used,
		UsedPercent:       percent,
		Devices:           devs,
		ThresholdPercent:  threshold,
		ManagedFileExists: statErr == nil,
	}
	if info, err := os.Stat(SwapFilePath); err == nil {
		st.ManagedMB = info.Size() >> 20
	}
	for _, d := range devs {
		if d.Name == SwapFilePath {
			st.ManagedUsedMB = d.UsedMB
		}
	}
	st.AvailRAMMB, st.DiskFreeMB = swapRoomRun()
	st.MaxMB = max(st.ManagedMB+st.DiskFreeMB-swapDiskReserveMB, st.ManagedMB)
	return st
}

// swapResizeCheck says why a resize can't run, or "" when it can
func swapResizeCheck(st swapStatus, sizeMB int64) string {
	if sizeMB > st.MaxMB {
		return fmt.Sprintf("Not enough disk space: %s GB needs %s more, only %s is free (1 GB is kept in reserve).",
			strconv.FormatFloat(float64(sizeMB)/1024, 'f', 2, 64), mbToGB(sizeMB-st.ManagedMB), mbToGB(st.DiskFreeMB))
	}
	// the swap file is turned off to recreate it, so what's in it has to fit in RAM
	if st.ManagedUsedMB+swapRAMReserveMB > st.AvailRAMMB {
		return fmt.Sprintf("Not enough free RAM: %s is in the swap file and only %s RAM is available, it has to move back into RAM first.",
			mbToGB(st.ManagedUsedMB), mbToGB(st.AvailRAMMB))
	}
	return ""
}

// ServeSwap handles GET /server/swap.
func (s *Swap) ServeSwap(w http.ResponseWriter, r *http.Request) {
	status := getSwapStatus()

	// The "Change allocation" field is labeled in GB, so its prefilled
	// value must be converted from status.TotalMB (which is in MB) --
	// pre-filling the raw MB number there previously showed e.g. "1024"
	// next to a "GB" label instead of "1".
	totalGB := "1"
	if status.TotalMB > 0 {
		totalGB = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(status.TotalMB)/1024, 'f', 2, 64), "0"), ".")
	}

	webtemplates.Render(w, "server_swap.html", mergeChrome(map[string]interface{}{
		"Swap":          status,
		"SwapFilePath":  SwapFilePath,
		"TotalGB":       totalGB,
		"RecommendedGB": recommendedSwapGB(float64(readMemInfo()["MemTotal"]) / (1024 * 1024 * 1024)),
		"SwapTop":       topSwapProcs(10),
		"CSRFToken":     csrf.Token(r),
		"Flashes":       auth.PopFlashes(w, r, s.Sessions),
	}, r, "Swap"))
}

// swapActionResult tracks progress for the resize/drop actions, matching
// the mutex-guarded single-pointer + goroutine + polling pattern used
// throughout this package (see podman.go).
type swapActionResult struct {
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var (
	pendingSwapActionMu sync.Mutex
	pendingSwapAction   *swapActionResult
)

// swapResizeRun recreates the managed swap file at sizeMB and activates it, updating /etc/fstab so it persists across reboots.
// Injectable for tests.
var swapResizeRun = func(sizeMB int64) error {
	_ = exec.Command("swapoff", SwapFilePath).Run() // no-op if not active
	// fallocate never shrinks an existing file, so start from scratch
	_ = os.Remove(SwapFilePath)

	if err := exec.Command("fallocate", "-l", fmt.Sprintf("%dM", sizeMB), SwapFilePath).Run(); err != nil {
		// some filesystems (btrfs, overlay) can't fallocate a swap file, dd is slower but always works
		name, args := lowPrio(false, "dd", "if=/dev/zero", "of="+SwapFilePath, "bs=1M", fmt.Sprintf("count=%d", sizeMB))
		if ddErr := exec.Command(name, args...).Run(); ddErr != nil {
			return fmt.Errorf("failed to allocate swap file: %w", ddErr)
		}
	}
	if err := os.Chmod(SwapFilePath, 0600); err != nil {
		return fmt.Errorf("failed to chmod swap file: %w", err)
	}
	if err := exec.Command("mkswap", SwapFilePath).Run(); err != nil {
		return fmt.Errorf("mkswap failed: %w", err)
	}
	if err := exec.Command("swapon", SwapFilePath).Run(); err != nil {
		return fmt.Errorf("swapon failed: %w", err)
	}

	ensureSwapFstabEntry()
	return nil
}

// ensureSwapFstabEntry adds the managed swap file to /etc/fstab if it isn't
// already there, so the swap survives a reboot.
func ensureSwapFstabEntry() {
	raw, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == SwapFilePath {
			return
		}
	}
	entry := SwapFilePath + " none swap sw 0 0\n"
	content := string(raw)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	_ = os.WriteFile("/etc/fstab", []byte(content+entry), 0644)
}

// swapDropRun mirrors sentinel.sh's cleanup: swapoff -a; swapon -a, which
// flushes swapped-out pages back to RAM and re-enables every fstab swap
// entry. Injectable for tests.
var swapDropRun = func() error {
	if err := exec.Command("swapoff", "-a").Run(); err != nil {
		return fmt.Errorf("swapoff -a failed: %w", err)
	}
	if err := exec.Command("swapon", "-a").Run(); err != nil {
		return fmt.Errorf("swapon -a failed: %w", err)
	}
	return nil
}

// ServeSwapAction handles POST /server/swap/action/{action}: "resize"
// (form value size_mb) creates/resizes the managed swap file; "drop"
// clears swap the same way sentinel.sh does.
func (s *Swap) ServeSwapAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	r.ParseForm()

	var sizeMB int64
	if action == "resize" {
		var err error
		sizeMB, err = strconv.ParseInt(r.PostFormValue("size_mb"), 10, 64)
		if err != nil || sizeMB < 128 {
			writeJSONError(w, http.StatusBadRequest, "Invalid swap size. Minimum is 128 MB.")
			return
		}
		if msg := swapResizeCheck(getSwapStatus(), sizeMB); msg != "" {
			writeJSONError(w, http.StatusBadRequest, msg)
			return
		}
	} else if action == "drop" {
		if st := getSwapStatus(); !st.CanDrop() {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Not enough free RAM: %s is in swap and only %s RAM is available.", st.UsedGB(), st.AvailRAMGB()))
			return
		}
	} else {
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use resize or drop.")
		return
	}

	result := &swapActionResult{}
	pendingSwapActionMu.Lock()
	pendingSwapAction = result
	pendingSwapActionMu.Unlock()

	go func() {
		var err error
		var successMsg string
		switch action {
		case "resize":
			err = swapResizeRun(sizeMB)
			successMsg = fmt.Sprintf("Swap set to %s.", mbToGB(sizeMB))
		case "drop":
			err = swapDropRun()
			successMsg = "Swap cleared successfully."
		}

		pendingSwapActionMu.Lock()
		result.Done = true
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.Success = true
			result.Message = successMsg
		}
		pendingSwapActionMu.Unlock()
	}()

	writeJSON(w, map[string]bool{"scheduled": true})
}

// ServeSwapActionStatus handles GET /server/swap/action-status.
func (s *Swap) ServeSwapActionStatus(w http.ResponseWriter, r *http.Request) {
	pendingSwapActionMu.Lock()
	defer pendingSwapActionMu.Unlock()
	if pendingSwapAction == nil {
		writeJSON(w, swapActionResult{Done: true, Message: "No action has run yet."})
		return
	}
	writeJSON(w, *pendingSwapAction)
}

// recommendedSwapGB: 2x RAM on small servers, then the square root of RAM since big servers don't need swap to match it
func recommendedSwapGB(ramGB float64) float64 {
	if ramGB <= 0 {
		return 0
	}
	if ramGB <= 2 {
		return math.Ceil(ramGB * 2)
	}
	return math.Max(2, math.Round(math.Sqrt(ramGB)))
}
