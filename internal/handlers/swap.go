// This file implements the Server > Swap page: viewing current swap usage,
// resizing/creating the swap file, and dropping (clearing) swap the same
// way opencli's sentinel.sh does it (swapoff -a; swapon -a).
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

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
}

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
	threshold, _ := strconv.Atoi(config.Load(NotificationsConfigPath).Get("DEFAULT", "swap", "85"))

	_, statErr := os.Stat(SwapFilePath)

	return swapStatus{
		TotalMB:           total,
		UsedMB:            used,
		FreeMB:            total - used,
		UsedPercent:       percent,
		Devices:           parseSwapDevices(),
		ThresholdPercent:  threshold,
		ManagedFileExists: statErr == nil,
	}
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
		"Swap":         status,
		"SwapFilePath": SwapFilePath,
		"TotalGB":      totalGB,
		"CSRFToken":    csrf.Token(r),
		"Flashes":      auth.PopFlashes(w, r, s.Sessions),
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

// swapResizeRun creates or resizes the managed swap file to sizeMB and
// activates it, updating /etc/fstab so it persists across reboots.
// Injectable for tests.
var swapResizeRun = func(sizeMB int64) error {
	_ = exec.Command("swapoff", SwapFilePath).Run() // no-op if not active

	if err := exec.Command("fallocate", "-l", fmt.Sprintf("%dM", sizeMB), SwapFilePath).Run(); err != nil {
		// Some filesystems (e.g. some btrfs/overlay setups) don't support
		// fallocate for swap files -- dd is slower but always works.
		ddCmd := exec.Command("dd", "if=/dev/zero", "of="+SwapFilePath, "bs=1M", fmt.Sprintf("count=%d", sizeMB))
		if ddErr := ddCmd.Run(); ddErr != nil {
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
	if strings.Contains(string(raw), SwapFilePath) {
		return
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
	} else if action != "drop" {
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
			successMsg = fmt.Sprintf("Swap resized to %d MB.", sizeMB)
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
