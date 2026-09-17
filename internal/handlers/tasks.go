// This file implements the /tasks page: a single list of opencli commands
// that are either currently running on the server or scheduled to run via
// cron (internal/handlers/cronjobs.go). Running tasks are detected by
// scanning /proc the same way the process manager does (process_manager.go),
// filtered to processes whose argv[0] is opencli. Clicking a running task
// links to the process manager's existing strace view for that PID.
package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/robfig/cron/v3"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Tasks bundles the /tasks handlers.
type Tasks struct {
	Sessions *auth.Manager
}

type taskInfo struct {
	PID           int     `json:"pid"`
	Command       string  `json:"command"`
	Owner         string  `json:"owner"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	StartedAt     string  `json:"started_at"`
	Elapsed       string  `json:"elapsed"`
}

// isOpencliCommand reports whether a /proc cmdline invokes the opencli
// entrypoint. In practice opencli is a bash script, so the top-level
// invocation shows up as e.g. "/bin/bash /usr/local/bin/opencli sentinel"
// rather than argv[0] being "opencli" -- so every field is checked, not
// just argv[0]. The subcommand scripts it shells out to (e.g. sentinel.sh)
// run as their own processes without "opencli" in their cmdline at all, so
// this naturally only matches the top-level command the admin invoked,
// not its internal child processes.
func isOpencliCommand(command string) bool {
	for _, field := range strings.Fields(command) {
		if filepath.Base(field) == "opencli" {
			return true
		}
	}
	return false
}

// displayOpencliCommand strips any leading interpreter/path before the
// opencli entrypoint (e.g. "/bin/bash /usr/local/bin/opencli sentinel"),
// leaving just "opencli sentinel" for display.
func displayOpencliCommand(command string) string {
	fields := strings.Fields(command)
	for i, field := range fields {
		if filepath.Base(field) == "opencli" {
			return strings.Join(append([]string{"opencli"}, fields[i+1:]...), " ")
		}
	}
	return command
}

// listOpencliTasks reuses the process manager's /proc snapshot, filtered to
// opencli invocations, with start time added.
func listOpencliTasks() []taskInfo {
	var tasks []taskInfo
	for _, p := range listAllProcesses() {
		if !isOpencliCommand(p.Command) {
			continue
		}
		startedAt, elapsed := processStartedAgo(p.PID)
		tasks = append(tasks, taskInfo{
			PID:           p.PID,
			Command:       displayOpencliCommand(p.Command),
			Owner:         p.Owner,
			CPUPercent:    p.CPUPercent,
			MemoryPercent: p.MemoryPercent,
			StartedAt:     startedAt,
			Elapsed:       elapsed,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].PID < tasks[j].PID })
	return tasks
}

type scheduledTaskInfo struct {
	LineNumber int    `json:"line_number"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	NextRun    string `json:"next_run"`
	In         string `json:"in"`
}

// cronScheduleParser matches the standard 5-field cron syntax used by
// /etc/cron.d/openpanel (isValidCronLine already excludes "@..." lines).
var cronScheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// listScheduledOpencliTasks reads the same crontab readCronJobs() (see
// cronjobs.go) already parses for /server/crons, filtered to opencli
// commands, with each entry's next run time computed from its schedule.
func listScheduledOpencliTasks() []scheduledTaskInfo {
	jobs, fileMissing := readCronJobs()
	if fileMissing {
		return nil
	}

	now := time.Now()
	var scheduled []scheduledTaskInfo
	for _, job := range jobs {
		if !isOpencliCommand(job.Command) {
			continue
		}
		schedule, err := cronScheduleParser.Parse(job.Schedule)
		if err != nil {
			continue
		}
		next := schedule.Next(now)
		if next.IsZero() {
			// e.g. a schedule like "31 2 *" (Feb 31st) that can never
			// actually match a real calendar date -- skip it rather than
			// showing a confusing "never" row.
			continue
		}
		scheduled = append(scheduled, scheduledTaskInfo{
			LineNumber: job.LineNumber,
			Schedule:   job.Schedule,
			Command:    job.Command,
			NextRun:    next.Format("2006-01-02 15:04:05"),
			In:         humanizeUntil(next, now),
		})
	}
	sort.Slice(scheduled, func(i, j int) bool { return scheduled[i].NextRun < scheduled[j].NextRun })
	return scheduled
}

// humanizeUntil formats the time between now and t as "in Xd Yh", "in Xh
// Ym", "in Xm Ys", or "in Xs".
func humanizeUntil(t, now time.Time) string {
	d := t.Sub(now).Round(time.Second)
	if d <= 0 {
		return "due now"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("in %dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("in %dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("in %dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("in %ds", seconds)
	}
}

// systemBootTime reads the kernel's boot time from /proc/uptime.
func systemBootTime() (time.Time, bool) {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return time.Time{}, false
	}
	uptimeSeconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Now().Add(-time.Duration(uptimeSeconds * float64(time.Second))), true
}

// processStartedAgo reads a process's start time from /proc/[pid]/stat
// (field 22, in clock ticks since boot) and returns it as a formatted
// timestamp plus a human elapsed duration.
func processStartedAgo(pid int) (string, string) {
	statRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", ""
	}
	closeParen := strings.LastIndex(string(statRaw), ")")
	if closeParen == -1 {
		return "", ""
	}
	rest := strings.Fields(string(statRaw)[closeParen+1:])
	if len(rest) < 20 {
		return "", ""
	}
	startTicks, err := strconv.ParseFloat(rest[19], 64) // field 22
	if err != nil {
		return "", ""
	}
	boot, ok := systemBootTime()
	if !ok {
		return "", ""
	}
	started := boot.Add(time.Duration(startTicks / processManagerUserHZ * float64(time.Second)))
	return started.Format("2006-01-02 15:04:05"), time.Since(started).Round(time.Second).String()
}

// taskRow is one row of the unified /tasks table: either a currently
// running opencli process or an opencli command scheduled via cron. Status
// distinguishes the two; PID is only set for running rows (used for the
// strace/kill actions), LineNumber only for scheduled rows (used for the
// edit-schedule action, linking to its row on /server/crons).
type taskRow struct {
	Status     string `json:"status"` // "running" or "scheduled"
	Command    string `json:"command"`
	PID        int    `json:"pid,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
	Time       string `json:"time"` // elapsed ("4s") for running, countdown ("in 4h 26m") for scheduled
}

// listTasks combines currently running opencli processes with opencli
// commands scheduled via cron into one list: running tasks first, then
// scheduled ones soonest-first (both source lists already sorted that way).
func listTasks() []taskRow {
	rows := make([]taskRow, 0, 8)
	for _, t := range listOpencliTasks() {
		rows = append(rows, taskRow{Status: "running", Command: t.Command, PID: t.PID, Time: t.Elapsed})
	}
	for _, s := range listScheduledOpencliTasks() {
		rows = append(rows, taskRow{Status: "scheduled", Command: s.Command, LineNumber: s.LineNumber, Time: s.In})
	}
	return rows
}

// ServeTasks handles GET /tasks.
func (t *Tasks) ServeTasks(w http.ResponseWriter, r *http.Request) {
	tasks := listTasks()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, tasks)
		return
	}

	webtemplates.Render(w, "tasks.html", mergeChrome(map[string]interface{}{
		"Tasks":     tasks,
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, t.Sessions),
	}, r, "Tasks"))
}
