package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

// where the account slices live, swapped in tests
var userSliceRoot = "/sys/fs/cgroup/user.slice"

// userTasks is the account's process ceiling, opencli sets the slice TasksMax from plan RAM unless /home/<ctx>/TasksMax overrides it
type userTasks struct {
	// what the edit fields start with, the account's current cpu cores and ram GB
	CPUNow, RAMNow string
	Known          bool
	Current        int
	Max            int // 0 when the slice has no limit
	Override       int // 0 when the formula applies
	Default        int // what the formula gives for this plan
	Pct            string
	Offset         string
	Color          string
	MaxHuman       string
	DefaultGB      string
}

func readIntFile(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, string(raw))
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

func tasksOverridePath(context string) string {
	return filepath.Join(containerHomeBase, context, "TasksMax")
}

// tasksDefault mirrors opencli's podman.TasksMax: 1000 per GB of plan RAM, at least 1000, an unlimited plan counts its 90% of RAM ceiling
func tasksDefault(planRAM string) (int, string) {
	ram := strings.TrimRight(strings.TrimSpace(planRAM), "gGbB")
	gb, _ := strconv.Atoi(ram)
	label := strconv.Itoa(gb) + " GB"
	if gb == 0 && !strings.Contains(ram, ".") {
		mb := int(readMemInfo()["MemTotal"] / 1024 / 1024)
		gb = mb * 90 / 100 / 1024
		label = "the 90% RAM ceiling of an unlimited plan, " + strconv.Itoa(gb) + " GB"
	}
	tasks := gb * 1000
	if tasks < 1000 {
		tasks = 1000
	}
	return tasks, label
}

// userSliceLive is what the account's cgroup says right now, the page prefers it over the hourly docker-collect_stats line
type userSliceLive struct {
	MemCur, MemMax   int64   // MemMax 0 = no limit
	CPUCores         float64 // 0 = no limit
	CPUUsed          float64 // cores, sampled over sliceCPUSample
	PidsCur, PidsMax int     // PidsMax 0 = no limit
}

// how long the cpu.stat sample takes, short enough not to slow the page down
var sliceCPUSample = 200 * time.Millisecond

func readInt64File(path string) (int64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n, err == nil
}

func sliceCPUUsec(slice string) (int64, bool) {
	raw, err := os.ReadFile(filepath.Join(slice, "cpu.stat"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "usage_usec" {
			n, err := strconv.ParseInt(f[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// readUserSlice reads the account slice, ok is false when it isn't on this server
func readUserSlice(context string) (userSliceLive, bool) {
	var l userSliceLive
	st, err := os.Stat(filepath.Join(containerHomeBase, context))
	if err != nil {
		return l, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return l, false
	}
	slice := filepath.Join(userSliceRoot, "user-"+strconv.Itoa(int(sys.Uid))+".slice")
	if l.MemCur, ok = readInt64File(filepath.Join(slice, "memory.current")); !ok {
		return l, false
	}
	l.MemMax, _ = readInt64File(filepath.Join(slice, "memory.max"))
	if raw, err := os.ReadFile(filepath.Join(slice, "cpu.max")); err == nil {
		if f := strings.Fields(string(raw)); len(f) == 2 {
			q, qerr := strconv.ParseFloat(f[0], 64)
			p, perr := strconv.ParseFloat(f[1], 64)
			if qerr == nil && perr == nil && p > 0 {
				l.CPUCores = q / p
			}
		}
	}
	pc, _ := readInt64File(filepath.Join(slice, "pids.current"))
	pm, _ := readInt64File(filepath.Join(slice, "pids.max"))
	l.PidsCur, l.PidsMax = int(pc), int(pm)
	if u1, ok := sliceCPUUsec(slice); ok {
		t1 := time.Now()
		time.Sleep(sliceCPUSample)
		if u2, ok := sliceCPUUsec(slice); ok {
			l.CPUUsed = float64(u2-u1) / float64(time.Since(t1).Microseconds())
		}
	}
	return l, true
}

func humanBinary(b float64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(b/(1<<30), 'f', 1, 64) + "G"
	case b >= 1<<20:
		return strconv.FormatFloat(b/(1<<20), 'f', 1, 64) + "M"
	}
	return strconv.FormatFloat(b/(1<<10), 'f', 1, 64) + "K"
}

// withLive puts the cgroup's CPU and memory over the ones from the usage file, a slice without a limit counts the whole server like docker-collect_stats does
func (w detailStatsWidget) withLive(l userSliceLive) detailStatsWidget {
	memMax := float64(l.MemMax)
	if memMax <= 0 {
		memMax = float64(readMemInfo()["MemTotal"])
	}
	memPct := 0.0
	if memMax > 0 {
		memPct = float64(l.MemCur) / memMax * 100
	}
	w.RAMPercentRounded = strconv.FormatFloat(round2(memPct), 'f', -1, 64)
	w.RAMOffset = detailRingOffset(math.Min(memPct, 100))
	w.RAMColor = usageColorClass(memPct)
	w.RAMHumanUsage = humanBinary(float64(l.MemCur))
	w.RAMHumanTotal = humanBinary(memMax)

	cores := l.CPUCores
	if cores <= 0 {
		cores = float64(runtime.NumCPU())
	}
	cpuPct := l.CPUUsed / cores * 100
	w.CPURatioRounded = strconv.FormatFloat(round2(cpuPct), 'f', -1, 64)
	w.CPUOffset = detailRingOffset(math.Min(cpuPct, 100))
	w.CPUColor = usageColorClass(cpuPct)
	w.CPUHumanUsage = fmt.Sprintf("%.1f cores", l.CPUUsed)
	w.CPUHumanTotal = strconv.FormatFloat(cores, 'f', -1, 64) + " cores"
	return w
}

func hasTaskStats(stats map[string]interface{}) bool {
	tasks, _ := stats["tasks"].(map[string]interface{})
	_, ok := tasks["current"]
	return ok
}

// readUserTasks is the process ring, from the live slice or else the last usage line
func readUserTasks(context, planRAM string, live *userSliceLive, stats map[string]interface{}) userTasks {
	t := userTasks{}
	t.Default, t.DefaultGB = tasksDefault(planRAM)
	t.Override, _ = readIntFile(tasksOverridePath(context))
	switch {
	case live != nil:
		t.Known, t.Current, t.Max = true, live.PidsCur, live.PidsMax
	case hasTaskStats(stats):
		t.Known = true
		t.Current = int(statFloat(stats, "tasks", "current"))
		t.Max = int(statFloat(stats, "tasks", "limit"))
	default:
		return t
	}
	pct := 0.0
	t.MaxHuman = "∞"
	if t.Max > 0 {
		pct = float64(t.Current) / float64(t.Max) * 100
		t.MaxHuman = strconv.Itoa(t.Max)
	}
	t.Pct = strconv.Itoa(int(math.Min(pct, 100) + 0.5))
	t.Offset = detailRingOffset(math.Min(pct, 100))
	t.Color = usageColorClass(pct)
	return t
}

// ServeTasksMax handles POST /users/{username}/tasks-max, a number writes the override, 0 or empty removes it so the plan's RAM decides again
func (u *Users) ServeTasksMax(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	back := "/users/" + username + "#stats"
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil || userData == nil || userData.Context == "" || strings.ContainsAny(userData.Context, "/.") {
		auth.AddFlash(w, r, u.Sessions, "Error: User "+username+" not found", "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	value := strings.TrimSpace(r.FormValue("value"))
	path := tasksOverridePath(userData.Context)
	msg := ""
	if value == "" || value == "0" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			auth.AddFlash(w, r, u.Sessions, "Failed to remove "+path+": "+err.Error(), "error")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		msg = "Process limit for " + username + " follows the plan's RAM again."
	} else {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 4194304 {
			auth.AddFlash(w, r, u.Sessions, "The process limit has to be a whole number from 1 to 4194304, or 0 to use the plan's default.", "error")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		// root owned, opencli reads it as root
		if err := os.WriteFile(path, []byte(strconv.Itoa(n)+"\n"), 0644); err != nil {
			auth.AddFlash(w, r, u.Sessions, "Failed to write "+path+": "+err.Error(), "error")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		os.Chown(path, 0, 0)
		msg = fmt.Sprintf("Process limit for %s set to %d.", username, n)
	}
	// only TasksMax, going through plan-apply --ram would also put back a memory limit set outside the plan
	tasks, _ := tasksDefault(planRAMFor(u.MySQL, userData.PlanID))
	if n, ok := readIntFile(path); ok && n > 0 {
		tasks = n
	}
	uid, ok := homeUID(userData.Context)
	if !ok {
		auth.AddFlash(w, r, u.Sessions, msg+" Applying it failed: /home/"+userData.Context+" not found", "error")
	} else if out, err := userSliceSetRun(uid, fmt.Sprintf("TasksMax=%d", tasks)); err != nil {
		auth.AddFlash(w, r, u.Sessions, msg+" Applying it failed: "+strings.TrimSpace(out), "error")
	} else {
		auth.AddFlash(w, r, u.Sessions, msg, "success")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// homeUID is the uid owning /home/<ctx>, the account slice is named after it
func homeUID(context string) (string, bool) {
	st, err := os.Stat(filepath.Join(containerHomeBase, context))
	if err != nil {
		return "", false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return strconv.Itoa(int(sys.Uid)), true
}

// planRAMFor is the ram column of a plan, like "6g"
var planRAMFor = func(db *sql.DB, planID int64) string {
	if db == nil {
		return ""
	}
	var ram string
	db.QueryRow("SELECT ram FROM plans WHERE id = ?", planID).Scan(&ram)
	return ram
}

// IntPct shows a ring percent without decimals, the cards are too narrow for 45.06%
func (w detailStatsWidget) IntPct(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return strconv.Itoa(int(math.Round(f)))
}

// CPUShort is "0.4 of 4 cores", short enough for the card
func (w detailStatsWidget) CPUShort() string {
	return strings.TrimSuffix(w.CPUHumanUsage, " cores") + " of " + w.CPUHumanTotal
}

// userSliceSetRun sets a property on the account slice, systemctl keeps it across reboots in system.control
var userSliceSetRun = func(uid string, props ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"set-property", "user-" + uid + ".slice"}, props...)...).CombinedOutput()
	return string(out), err
}

// userPlanApplyRun puts the plan's cpu or ram back on the account, the same way a plan change does
var userPlanApplyRun = func(planID int64, part, username string) (string, error) {
	out, err := exec.Command("opencli", "plan-apply", strconv.FormatInt(planID, 10), "--"+part, username).CombinedOutput()
	return string(out), err
}

// ServeSliceLimit handles POST /users/{username}/slice-limit/{kind}, cpu in cores or ram in GB for the whole account outside its plan,
// 0 or empty puts the plan's value back, the limits of the user's own services aren't touched
func (u *Users) ServeSliceLimit(w http.ResponseWriter, r *http.Request) {
	username, kind := r.PathValue("username"), r.PathValue("kind")
	back := "/users/" + username + "#stats"
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if kind != "cpu" && kind != "ram" {
		http.Error(w, "Invalid limit", http.StatusBadRequest)
		return
	}
	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil || userData == nil || userData.Context == "" || strings.ContainsAny(userData.Context, "/.") {
		auth.AddFlash(w, r, u.Sessions, "Error: User "+username+" not found", "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	label := map[string]string{"cpu": "CPU", "ram": "Memory"}[kind]
	value := strings.TrimSpace(r.FormValue("value"))
	if value == "" || value == "0" {
		if out, err := userPlanApplyRun(userData.PlanID, kind, username); err != nil {
			auth.AddFlash(w, r, u.Sessions, "Failed to put the plan's "+label+" limit back: "+strings.TrimSpace(out), "error")
		} else {
			auth.AddFlash(w, r, u.Sessions, label+" limit for "+username+" follows the plan again.", "success")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n <= 0 || n > 4096 {
		auth.AddFlash(w, r, u.Sessions, label+" has to be a number above 0, or 0 to use the plan's limit.", "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	uid, ok := homeUID(userData.Context)
	if !ok {
		auth.AddFlash(w, r, u.Sessions, "Error: /home/"+userData.Context+" not found", "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	var props []string
	var msg string
	if kind == "cpu" {
		props = []string{fmt.Sprintf("CPUQuota=%d%%", int(math.Round(n*100)))}
		msg = fmt.Sprintf("CPU limit for %s set to %s cores.", username, strconv.FormatFloat(n, 'f', -1, 64))
	} else {
		props = []string{"MemoryHigh=infinity", fmt.Sprintf("MemoryMax=%dM", int(math.Round(n*1024)))}
		msg = fmt.Sprintf("Memory limit for %s set to %s GB.", username, strconv.FormatFloat(n, 'f', -1, 64))
	}
	if out, err := userSliceSetRun(uid, props...); err != nil {
		auth.AddFlash(w, r, u.Sessions, "Failed to set the "+label+" limit: "+strings.TrimSpace(out), "error")
	} else {
		auth.AddFlash(w, r, u.Sessions, msg, "success")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// withNow fills the current cpu and ram for the edit fields, from the slice or else the plan
func (t userTasks) withNow(live *userSliceLive, planCPU, planRAM string) userTasks {
	t.CPUNow = strings.TrimSpace(planCPU)
	t.RAMNow = strings.TrimRight(strings.TrimSpace(planRAM), "gGbB")
	if live != nil {
		if live.CPUCores > 0 {
			t.CPUNow = strconv.FormatFloat(round2(live.CPUCores), 'f', -1, 64)
		}
		if live.MemMax > 0 {
			t.RAMNow = strconv.FormatFloat(round2(float64(live.MemMax)/(1<<30)), 'f', -1, 64)
		}
	}
	return t
}
