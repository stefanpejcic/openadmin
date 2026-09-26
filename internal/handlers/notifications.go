package handlers

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

var (
	NotificationsLogPath = "/var/log/openpanel/admin/notifications.log"
	// NotificationsPauseFlagPath holds the unix timestamp (as text) that
	// sentinel.sh reads to decide whether to skip sending email/webhook
	// alerts. It's just a flag file, not config -- sentinel.sh deletes it
	// itself once the timestamp has passed.
	NotificationsPauseFlagPath = "/tmp/openpanel_notifications_paused"
	// sentinel appends one line here at the end of every full cron run
	SentinelSnapshotsPath = "/var/log/openpanel/admin/sentinel_snapshots.jsonl"
)

// sentinel runs every 5 min, so 3 missed runs means the cron is likely broken
const sentinelStaleAfter = 15 * time.Minute

type sentinelLastCheck struct {
	Ran   bool
	At    string
	Ago   string
	Stale bool
}

// lastSentinelCheck uses the snapshot file's mtime as the last run time
func lastSentinelCheck(now time.Time) sentinelLastCheck {
	info, err := os.Stat(SentinelSnapshotsPath)
	if err != nil || info.Size() == 0 {
		return sentinelLastCheck{}
	}
	mod := info.ModTime()
	return sentinelLastCheck{
		Ran:   true,
		At:    mod.Format("Jan 2, 15:04"),
		Ago:   humanizeAgo(mod, now),
		Stale: now.Sub(mod) > sentinelStaleAfter,
	}
}

// humanizeAgo formats the time since t as "just now", "Xm ago", "Xh Ym ago" or "Xd Yh ago"
func humanizeAgo(t, now time.Time) string {
	d := now.Sub(t)
	if d < time.Minute {
		return "just now"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh ago", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm ago", hours, minutes)
	default:
		return fmt.Sprintf("%dm ago", minutes)
	}
}

// notificationsPauseDurations maps the pause dropdown's option values to
// how long that option pauses notifications for.
var notificationsPauseDurations = map[string]time.Duration{
	"10m": 10 * time.Minute,
	"30m": 30 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"1d":  24 * time.Hour,
}

// currentNotificationsPause reports whether notifications are currently
// paused and, if so, until when. An expired flag file is cleaned up here
// too, same as sentinel.sh does on its own next run.
func currentNotificationsPause() (time.Time, bool) {
	raw, err := os.ReadFile(NotificationsPauseFlagPath)
	if err != nil {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	until := time.Unix(ts, 0)
	if !until.After(time.Now()) {
		os.Remove(NotificationsPauseFlagPath)
		return time.Time{}, false
	}
	return until, true
}

// notificationSnoozeFlagPath returns the per-title snooze flag file for a
// notification: sentinel.sh writes every alert with the same title to the
// same flag, so hashing the title (the same convention sentinel.sh's own
// dedup already keys on -- see is_unread_message_present) gives every
// occurrence of "this specific alert" a stable, filesystem-safe name.
func notificationSnoozeFlagPath(title string) string {
	return fmt.Sprintf("/tmp/openpanel_notification_snooze_%x", md5.Sum([]byte(title)))
}

// currentNotificationSnooze reports whether alerts with this title are
// currently snoozed and, if so, until when. Same expire-and-clean-up-here
// behavior as currentNotificationsPause.
func currentNotificationSnooze(title string) (time.Time, bool) {
	path := notificationSnoozeFlagPath(title)
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	until := time.Unix(ts, 0)
	if !until.After(time.Now()) {
		os.Remove(path)
		return time.Time{}, false
	}
	return until, true
}

// Notifications bundles the /notifications handlers (the
// /settings/notifications config-form handlers are in
// notification_settings.go).
type Notifications struct {
	Sessions *auth.Manager
}

type notificationsPageData struct {
	webtemplates.Chrome
	Notifications            []notificationRow
	Counts                   notificationCounts
	Categories               []string
	NotificationsPaused      bool
	NotificationsPausedUntil string
	LastCheck                sentinelLastCheck
	Flashes                  []auth.Flash
}

// notificationCounts feeds the numbers next to the page's filter options
type notificationCounts struct {
	All, Unread, Resolved, Critical, Warning, Info int
}

type notificationRow struct {
	Notification
	Index         int
	Kind          string
	Snoozed       bool
	SnoozedUntil  string
	ResolvedAfter string
	Summary       string
	More          string
	LinkHref      string
	LinkText      string
}

// newNotificationRow adds what the template needs on top of the stored entry
func newNotificationRow(n Notification, index int) notificationRow {
	row := notificationRow{Notification: n, Index: index}
	row.Summary, row.More, _ = strings.Cut(strings.TrimSpace(n.Message), "\n")
	row.More = strings.TrimSpace(row.More)
	if n.ResolvedAt != "" {
		start, err1 := time.ParseInLocation("2006-01-02 15:04:05", n.Time, time.Local)
		end, err2 := time.ParseInLocation("2006-01-02 15:04:05", n.ResolvedAt, time.Local)
		if err1 == nil && err2 == nil {
			if end.Sub(start) < time.Minute {
				row.ResolvedAfter = "under a minute"
			} else {
				row.ResolvedAfter = strings.TrimSuffix(humanizeAgo(start, end), " ago")
			}
		}
	}
	if d := n.Details; d != nil {
		switch d.Kind {
		case "ram", "cpu", "disk", "oom":
			row.Kind = d.Kind
		}
		switch {
		case d.Crashlog != "":
			row.LinkText = "View crashlog"
			row.LinkHref = "/services/crashlogs/log/?log_name=" + filepath.Base(d.Crashlog)
		case d.LogFile != "":
			row.LinkText = "View update log"
			row.LinkHref = "/settings/updates/log/?log_name=" + filepath.Base(d.LogFile)
		}
	}
	if until, snoozed := currentNotificationSnooze(n.Title); snoozed {
		row.Snoozed = true
		row.SnoozedUntil = until.Format("Jan 2, 15:04")
	}
	return row
}

// readNotifications returns the parsed entries newest first, the same order the 1-indexed line numbers count in
func readNotifications() ([]Notification, error) {
	lines, err := readNotificationLines()
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		out = append(out, parseNotification(lines[i]))
	}
	return out, nil
}

// ServeView handles GET /notifications.
func (n *Notifications) ServeView(w http.ResponseWriter, r *http.Request) {
	entries, err := readNotifications()
	if err != nil {
		http.Error(w, "NOTIFICATIONS - Error loading notifications: "+err.Error(), http.StatusOK)
		return
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, entries)
		return
	}

	rows := make([]notificationRow, len(entries))
	var counts notificationCounts
	seenCategory := map[string]bool{}
	var categories []string
	for i, e := range entries {
		rows[i] = newNotificationRow(e, i+1)
		counts.All++
		if e.Unread() {
			counts.Unread++
		}
		if e.ResolvedAt != "" {
			counts.Resolved++
		}
		switch e.Severity {
		case "critical":
			counts.Critical++
		case "warning":
			counts.Warning++
		default:
			counts.Info++
		}
		if e.Category != "" && !seenCategory[e.Category] {
			seenCategory[e.Category] = true
			categories = append(categories, e.Category)
		}
	}
	sort.Strings(categories)

	pausedUntil, isPaused := currentNotificationsPause()
	pausedUntilLabel := ""
	if isPaused {
		pausedUntilLabel = pausedUntil.Format("Jan 2, 15:04")
	}

	chrome := buildChrome(r, "Notifications")
	chrome.BulkActions = NotificationsBulkActions()
	webtemplates.Render(w, "notifications.html", notificationsPageData{
		Chrome:                   chrome,
		Notifications:            rows,
		Counts:                   counts,
		Categories:               categories,
		NotificationsPaused:      isPaused,
		NotificationsPausedUntil: pausedUntilLabel,
		LastCheck:                lastSentinelCheck(time.Now()),
		Flashes:                  auth.PopFlashes(w, r, n.Sessions),
	})
}

func readNotificationLines() ([]string, error) {
	raw, err := os.ReadFile(NotificationsLogPath)
	if err != nil {
		if os.IsNotExist(err) {
			os.WriteFile(NotificationsLogPath, nil, 0644)
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func writeNotificationLines(lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return os.WriteFile(NotificationsLogPath, []byte(b.String()), 0644)
}

// HandleDelete handles POST /notifications/delete/{line_number}.
// line_number is 1-indexed from the newest (bottom-of-file) entry,
// indexing into the file's raw (chronological) line order from the end.
func (n *Notifications) HandleDelete(w http.ResponseWriter, r *http.Request) {
	lineNumber, _ := strconv.Atoi(r.PathValue("line_number"))
	unlock := lockNotifications()
	defer unlock()
	lines, err := readNotificationLines()
	if err != nil {
		http.Error(w, "Log file not found", http.StatusBadRequest)
		return
	}

	if r.FormValue("command") == "delete_all" {
		lines = nil
	} else if lineNumber >= 1 && lineNumber <= len(lines) {
		idx := len(lines) - lineNumber
		lines = append(lines[:idx], lines[idx+1:]...)
	} else {
		http.Error(w, "Invalid line number", http.StatusBadRequest)
		return
	}

	if err := writeNotificationLines(lines); err != nil {
		http.Error(w, "Error deleting notification: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// HandleMarkAsRead handles POST /notifications/mark_as_read/{line_number}.
func (n *Notifications) HandleMarkAsRead(w http.ResponseWriter, r *http.Request) {
	lineNumber, _ := strconv.Atoi(r.PathValue("line_number"))
	unlock := lockNotifications()
	defer unlock()
	lines, err := readNotificationLines()
	if err != nil {
		http.Error(w, "Log file not found", http.StatusBadRequest)
		return
	}

	if r.FormValue("command") == "mark_all_as_read" {
		for i, l := range lines {
			lines[i] = markNotificationLineRead(l)
		}
	} else if lineNumber >= 1 && lineNumber <= len(lines) {
		idx := len(lines) - lineNumber
		lines[idx] = markNotificationLineRead(lines[idx])
	} else {
		http.Error(w, "Invalid line number", http.StatusBadRequest)
		return
	}

	if err := writeNotificationLines(lines); err != nil {
		http.Error(w, "Error marking notification as read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// PauseNotifications handles POST /notifications/pause: writes the flag
// file sentinel.sh checks before sending email/webhook alerts.
func (n *Notifications) PauseNotifications(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	duration, ok := notificationsPauseDurations[r.PostFormValue("duration")]
	if !ok {
		auth.AddFlash(w, r, n.Sessions, "Invalid pause duration.", "error")
		http.Redirect(w, r, "/notifications", http.StatusSeeOther)
		return
	}
	until := time.Now().Add(duration).Unix()
	if err := os.WriteFile(NotificationsPauseFlagPath, []byte(strconv.FormatInt(until, 10)), 0644); err != nil {
		auth.AddFlash(w, r, n.Sessions, "Error pausing notifications.", "error")
	} else {
		auth.AddFlash(w, r, n.Sessions, "Notifications paused.", "success")
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// ResumeNotifications handles POST /notifications/resume: removes the
// pause flag file so sentinel.sh resumes sending alerts immediately.
func (n *Notifications) ResumeNotifications(w http.ResponseWriter, r *http.Request) {
	os.Remove(NotificationsPauseFlagPath)
	auth.AddFlash(w, r, n.Sessions, "Notifications resumed.", "success")
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// notificationTitleForLine looks up the title of the notification at
// line_number (1-indexed from the newest entry, same convention as
// HandleDelete/HandleMarkAsRead), so a snooze/unsnooze request can be
// keyed off it without the browser having to round-trip the raw title.
func notificationTitleForLine(lineNumber int) (string, bool) {
	lines, err := readNotificationLines()
	if err != nil || lineNumber < 1 || lineNumber > len(lines) {
		return "", false
	}
	idx := len(lines) - lineNumber
	return parseNotification(lines[idx]).Title, true
}

// HandleSnooze handles POST /notifications/snooze/{line_number}: snoozes
// future alerts sharing this notification's title (sentinel.sh's own
// dedup already treats identical titles as "the same alert", so this is
// the natural granularity -- see notificationSnoozeFlagPath).
func (n *Notifications) HandleSnooze(w http.ResponseWriter, r *http.Request) {
	lineNumber, _ := strconv.Atoi(r.PathValue("line_number"))
	title, ok := notificationTitleForLine(lineNumber)
	if !ok {
		http.Error(w, "Invalid line number", http.StatusBadRequest)
		return
	}

	r.ParseForm()
	duration, ok := notificationsPauseDurations[r.PostFormValue("duration")]
	if !ok {
		auth.AddFlash(w, r, n.Sessions, "Invalid snooze duration.", "error")
		http.Redirect(w, r, "/notifications", http.StatusSeeOther)
		return
	}
	until := time.Now().Add(duration).Unix()
	if err := os.WriteFile(notificationSnoozeFlagPath(title), []byte(strconv.FormatInt(until, 10)), 0644); err != nil {
		auth.AddFlash(w, r, n.Sessions, "Error snoozing this alert.", "error")
	} else {
		auth.AddFlash(w, r, n.Sessions, "This alert type is snoozed.", "success")
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// HandleUnsnooze handles POST /notifications/unsnooze/{line_number}:
// removes the per-title snooze flag so this alert can fire again
// immediately.
func (n *Notifications) HandleUnsnooze(w http.ResponseWriter, r *http.Request) {
	lineNumber, _ := strconv.Atoi(r.PathValue("line_number"))
	title, ok := notificationTitleForLine(lineNumber)
	if !ok {
		http.Error(w, "Invalid line number", http.StatusBadRequest)
		return
	}
	os.Remove(notificationSnoozeFlagPath(title))
	auth.AddFlash(w, r, n.Sessions, "Alert unsnoozed.", "success")
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}
