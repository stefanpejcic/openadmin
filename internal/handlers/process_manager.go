// This file implements the system process list (with sortable columns),
// kill action, and a live strace stream.
package handlers

import (
	"bufio"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// ProcessManager bundles the /server/processes* handlers.
type ProcessManager struct {
	Sessions *auth.Manager
}

type processInfo struct {
	PID           int     `json:"pid"`
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	Priority      int     `json:"priority"`
	Owner         string  `json:"owner"`
	Command       string  `json:"command"`
}

// processManagerSortCriteria maps a sort key to its (field, reverse) pair.
var processManagerSortCriteria = map[string]struct {
	field   string
	reverse bool
}{
	"cpu": {"cpu_percent", true}, "-cpu": {"cpu_percent", false},
	"memory": {"memory_percent", true}, "-memory": {"memory_percent", false},
	"priority": {"priority", true}, "-priority": {"priority", false},
	"name": {"name", true}, "-name": {"name", false},
	"owner": {"owner", true}, "-owner": {"owner", false},
	"command": {"command", true}, "-command": {"command", false},
	"pid": {"pid", false}, "-pid": {"pid", true},
}

// processCPUSample / processCPUCache implement delta-based CPU percent
// sampling: cpu percent is computed by comparing a process's CPU time to
// the last time we happened to sample it (returning 0 the first time any
// given pid is observed), rather than a fixed-interval blocking sample --
// using a package-level cache across requests within this Go process's
// lifetime.
type processCPUSample struct {
	cpuTime   float64
	wallClock time.Time
}

var (
	processCPUCacheMu sync.Mutex
	processCPUCache   = map[int]processCPUSample{}
)

// processManagerUserHZ mirrors the kernel's USER_HZ clock tick rate used to
// convert /proc/[pid]/stat's jiffies fields to seconds. Virtually always
// 100 on Linux x86/ARM; querying the real sysconf(_SC_CLK_TCK) value isn't
// easily done from pure Go without cgo, so this is a documented assumption
// rather than a query.
const processManagerUserHZ = 100.0

// listAllProcesses enumerates /proc/[pid], computing cpu_percent via the
// delta-since-last-sample cache above.
func listAllProcesses() []processInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	totalMemKB := readTotalMemKB()
	now := time.Now()

	processCPUCacheMu.Lock()
	defer processCPUCacheMu.Unlock()

	var list []processInfo
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		info, ok := readProcessInfo(pid, totalMemKB)
		if !ok {
			continue
		}

		if last, seen := processCPUCache[pid]; seen {
			wallDelta := now.Sub(last.wallClock).Seconds()
			if wallDelta > 0 {
				info.CPUPercent = math.Round((info.cpuTimeRaw-last.cpuTime)/wallDelta*100*1000) / 1000
			}
		}
		processCPUCache[pid] = processCPUSample{cpuTime: info.cpuTimeRaw, wallClock: now}

		list = append(list, info.processInfo)
	}
	return list
}

// procInfoWithRawCPU carries the raw CPU-seconds value alongside the
// public processInfo, needed only to update the cache above.
type procInfoWithRawCPU struct {
	processInfo
	cpuTimeRaw float64
}

func readTotalMemKB() uint64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				return n
			}
		}
	}
	return 0
}

func readProcessInfo(pid int, totalMemKB uint64) (procInfoWithRawCPU, bool) {
	statRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procInfoWithRawCPU{}, false
	}
	statStr := string(statRaw)

	openParen := strings.Index(statStr, "(")
	closeParen := strings.LastIndex(statStr, ")")
	if openParen == -1 || closeParen == -1 || closeParen < openParen {
		return procInfoWithRawCPU{}, false
	}
	name := statStr[openParen+1 : closeParen]
	rest := strings.Fields(statStr[closeParen+1:])
	// rest[0] = state, [1] = ppid, ... indices below are 0-based within
	// `rest`, i.e. field N in `man proc` is rest[N-3].
	if len(rest) < 17 {
		return procInfoWithRawCPU{}, false
	}
	utime, _ := strconv.ParseFloat(rest[11], 64) // field 14
	stime, _ := strconv.ParseFloat(rest[12], 64) // field 15
	nice, _ := strconv.Atoi(rest[16])            // field 19
	cpuTimeSeconds := (utime + stime) / processManagerUserHZ

	cmdlineRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	command := strings.TrimRight(strings.ReplaceAll(string(cmdlineRaw), "\x00", " "), " ")

	var rssKB uint64
	var uid string
	statusRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err == nil {
		for _, line := range strings.Split(string(statusRaw), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					rssKB, _ = strconv.ParseUint(fields[1], 10, 64)
				}
			} else if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					uid = fields[1]
				}
			}
		}
	}

	owner := uid
	if u, err := user.LookupId(uid); err == nil {
		owner = u.Username
	}

	var memPercent float64
	if totalMemKB > 0 {
		memPercent = float64(rssKB) / float64(totalMemKB) * 100
	}

	return procInfoWithRawCPU{
		processInfo: processInfo{
			PID: pid, Name: name, MemoryPercent: memPercent,
			Priority: nice, Owner: owner, Command: command,
		},
		cpuTimeRaw: cpuTimeSeconds,
	}, true
}

// sortProcesses dispatches on sortBy, defaulting to cpu-descending for
// an unrecognized value.
func sortProcesses(list []processInfo, sortBy string) {
	crit, ok := processManagerSortCriteria[sortBy]
	field, reverse := "cpu_percent", true
	if ok {
		field, reverse = crit.field, crit.reverse
	}

	less := func(i, j int) bool {
		var lt bool
		switch field {
		case "cpu_percent":
			lt = list[i].CPUPercent < list[j].CPUPercent
		case "memory_percent":
			lt = list[i].MemoryPercent < list[j].MemoryPercent
		case "priority":
			lt = list[i].Priority < list[j].Priority
		case "name":
			lt = list[i].Name < list[j].Name
		case "owner":
			lt = list[i].Owner < list[j].Owner
		case "command":
			lt = list[i].Command < list[j].Command
		case "pid":
			lt = list[i].PID < list[j].PID
		}
		return lt
	}
	sort.SliceStable(list, func(i, j int) bool {
		if reverse {
			return less(j, i)
		}
		return less(i, j)
	})
}

// ServeProcesses handles GET /server/processes.
func (p *ProcessManager) ServeProcesses(w http.ResponseWriter, r *http.Request) {
	sortProvided := r.URL.Query().Has("sort")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "cpu"
	}

	processes := listAllProcesses()
	sortProcesses(processes, sortBy)

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, processes)
		return
	}

	title := "Process Manager"
	if sortProvided {
		title = fmt.Sprintf("Process Manager (sort by %s)", sortBy)
	}

	webtemplates.Render(w, "system_processes.html", mergeChrome(map[string]interface{}{
		"Processes": processes,
		"SortBy":    sortBy,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, p.Sessions),
	}, r, title))
}

// straceRun is injectable so tests never shell out to a real strace binary.
var straceRun = func(pid int) (*exec.Cmd, error) {
	cmd := exec.Command("strace", "-p", strconv.Itoa(pid))
	return cmd, nil
}

// ServeProcessAction handles GET /server/processes/{pid}/{action}.
func (p *ProcessManager) ServeProcessAction(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	action := r.PathValue("action")

	switch action {
	case "strace":
		if r.URL.Query().Get("output") == "stream" {
			p.streamStrace(w, r, pid)
			return
		}
		webtemplates.Render(w, "system_strace.html", mergeChrome(map[string]interface{}{
			"PID":       pid,
			"CSRFToken": csrf.Token(r),
			"Flashes":   auth.PopFlashes(w, r, p.Sessions),
		}, r, "Process Manager"))

	case "kill":
		if err := killProcess(pid); err != nil {
			auth.AddFlash(w, r, p.Sessions, "Error killing process: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, p.Sessions, fmt.Sprintf("Process with PID %d killed successfully", pid), "success")
		}
		http.Redirect(w, r, "/server/processes", http.StatusSeeOther)

	default:
		auth.AddFlash(w, r, p.Sessions, "Invalid action: "+action, "error")
		http.Redirect(w, r, "/server/processes", http.StatusSeeOther)
	}
}

func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGKILL)
}

// streamStrace streams live `strace -p PID` output as Server-Sent
// Events, killing the child process if the client disconnects.
func (p *ProcessManager) streamStrace(w http.ResponseWriter, r *http.Request, pid int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	cmd, err := straceRun(pid)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			cmd.Process.Kill()
			cmd.Wait()
		case <-done:
		}
	}()
	defer close(done)

	// Content-Type: text/event-stream but plain `line + "\n"` writes, no
	// "data: "/blank-line SSE framing. The template (see
	// system_strace.html) doesn't actually rely on EventSource.onmessage
	// to read this -- it consumes the stream via a separate fetch() +
	// ReadableStream reader, which just wants raw bytes and doesn't care
	// about SSE framing. The template's own `EventSource` object is
	// created but never read from at all, meaning each page load opens a
	// second, wasted strace subprocess purely to be ignored; kept as-is
	// rather than removing that redundant object, since fixing the
	// template is out of scope here.
	w.Header().Set("Content-Type", "text/event-stream;charset=utf-8")
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		fmt.Fprintf(w, "%s\n", scanner.Text())
		flusher.Flush()
	}
	cmd.Wait()
}
