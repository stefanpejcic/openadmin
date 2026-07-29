package handlers

import (
	"html/template"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

var NotificationsLogPath = "/var/log/openpanel/admin/notifications.log"

// Notifications bundles the /notifications handlers (the
// /settings/notifications config-form handlers are in
// notification_settings.go).
type Notifications struct {
	Sessions *auth.Manager
}

type notificationsPageData struct {
	webtemplates.Chrome
	Notifications []notificationRow
	Flashes       []auth.Flash
}

// notificationRow is the parsed form of one raw log line ("<date> <time>
// <STATUS> <title...> MESSAGE: <message>"). Kind selects which of the
// message body's special renderings (RAM/CPU/OOM/disk usage, or a plain
// message possibly containing a "Log file:"/"detailed report:" link)
// applies.
type notificationRow struct {
	Index  int
	Time   string
	Status string
	Title  string

	Kind         string
	Plain        string
	Percent      string
	UsedOf       string
	ProcessLines string
	OOMSystem    []string
	OOMUsers     []oomUserGroup
	DiskValue    string
	DiskDetail   template.HTML
	Before       string
	LinkHref     string
	LinkText     string
}

type oomUserGroup struct {
	Username string
	Entries  []string
}

// unescapeNewlines undoes sentinel.sh's `sed 's/\n/\\n/g'`, which flattens
// multi-line process/partition listings to literal "\n" so each
// notification stays on one line in the log file.
func unescapeNewlines(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

// parseNotificationRow parses one raw log line plus its message-body
// special-casing.
func parseNotificationRow(raw string, index int) notificationRow {
	row := notificationRow{Index: index}

	parts := strings.SplitN(raw, " MESSAGE: ", 2)
	head := strings.SplitN(parts[0], " ", 5)
	if len(head) > 1 {
		row.Time = head[0] + " " + head[1]
	}
	if len(head) > 2 {
		row.Status = head[2]
	}
	if len(head) > 3 {
		row.Title = strings.ReplaceAll(strings.Join(head[3:], " "), "MESSAGE:", "")
	}
	message := ""
	if len(parts) > 1 {
		message = parts[1]
	}

	switch {
	case strings.HasPrefix(message, "Used RAM:"):
		row.Kind = "ram"
		msgParts := strings.SplitN(message, "|", 2)
		ratio := strings.TrimSpace(msgParts[0])
		if len(msgParts) > 1 {
			row.ProcessLines = unescapeNewlines(strings.TrimSpace(msgParts[1]))
		}
		ramPart := ""
		if idx := strings.Index(ratio, ":"); idx != -1 {
			ramPart = strings.TrimSpace(ratio[idx+1:])
		}
		ramSplit := strings.SplitN(ramPart, "/", 2)
		usedRAM, totalRAM := "N/A", "N/A"
		if len(ramSplit) > 0 {
			usedRAM = strings.TrimSpace(ramSplit[0])
		}
		if len(ramSplit) > 1 {
			totalRAM = strings.TrimSpace(strings.SplitN(ramSplit[1], "(", 2)[0])
		}
		row.Percent = "0"
		if idx := strings.Index(ramPart, "("); idx != -1 {
			row.Percent = strings.TrimSpace(strings.ReplaceAll(ramPart[idx+1:], "%)", ""))
		}
		row.UsedOf = usedRAM + " of " + totalRAM

	case strings.HasPrefix(message, "CPU:"):
		row.Kind = "cpu"
		usageLine := strings.SplitN(message, "|", 2)[0]
		row.Percent = "0"
		if idx := strings.Index(usageLine, ":"); idx != -1 {
			row.Percent = strings.ReplaceAll(strings.TrimSpace(usageLine[idx+1:]), "%", "")
		}
		if idx := strings.Index(message, "|"); idx != -1 {
			row.ProcessLines = unescapeNewlines(strings.TrimSpace(message[idx+1:]))
		}

	case strings.Contains(message, "killed by OOM"):
		row.Kind = "oom"
		segments := strings.Split(message, " | ")
		seenUser := map[string]bool{}
		for _, seg := range segments {
			s := strings.TrimSpace(seg)
			if s == "" {
				continue
			}
			if (s[0] >= '0' && s[0] <= '9') || strings.HasPrefix(s, "20") {
				row.OOMSystem = append(row.OOMSystem, s)
			}
		}
		var usernames []string
		for _, seg := range segments {
			s := strings.TrimSpace(seg)
			if s == "" || (s[0] >= '0' && s[0] <= '9') || !strings.Contains(s, ":") {
				continue
			}
			uname := strings.TrimSpace(strings.SplitN(s, ":", 2)[0])
			if !seenUser[uname] {
				seenUser[uname] = true
				usernames = append(usernames, uname)
			}
		}
		for _, uname := range usernames {
			group := oomUserGroup{Username: uname}
			for _, seg := range segments {
				s := strings.TrimSpace(seg)
				if strings.HasPrefix(s, uname+":") {
					group.Entries = append(group.Entries, strings.TrimSpace(s[len(uname)+1:]))
				}
			}
			row.OOMUsers = append(row.OOMUsers, group)
		}

	case strings.Contains(strings.ToLower(message), "disk usage:"):
		row.Kind = "disk"
		msgParts := strings.SplitN(message, "| Partitions:", 2)
		beforeDisk := strings.TrimSpace(msgParts[0])
		if len(msgParts) > 1 {
			escaped := template.HTMLEscapeString(strings.TrimSpace(msgParts[1]))
			row.DiskDetail = template.HTML(strings.ReplaceAll(escaped, `\n`, "<br>"))
		}
		row.DiskValue = "0"
		if idx := strings.Index(beforeDisk, ":"); idx != -1 {
			row.DiskValue = strings.TrimSpace(beforeDisk[idx+1:])
		}

	default:
		if idx := strings.Index(message, "Log file:"); idx != -1 {
			row.Kind = "logfile"
			row.Before = message[:idx]
			rest := strings.TrimSpace(message[idx+len("Log file:"):])
			row.LinkText = rest
			segs := strings.Split(rest, "/")
			row.LinkHref = "/settings/updates/log/?log_name=" + segs[len(segs)-1]
		} else if idx := strings.Index(message, "detailed report:"); idx != -1 {
			row.Kind = "report"
			row.Before = message[:idx]
			rest := strings.TrimSpace(message[idx+len("detailed report:"):])
			row.LinkText = rest
			segs := strings.Split(rest, "/")
			row.LinkHref = "/services/crashlogs/log/?log_name=" + segs[len(segs)-1]
		} else {
			row.Kind = "plain"
			row.Plain = message
		}
	}

	return row
}

// ServeView handles GET /notifications.
func (n *Notifications) ServeView(w http.ResponseWriter, r *http.Request) {
	lines, err := readNotificationLines()
	if err != nil {
		http.Error(w, "NOTIFICATIONS - Error loading notifications: "+err.Error(), http.StatusOK)
		return
	}

	// newest-first: lines are appended chronologically, so a plain
	// reverse-string-sort of already-timestamp-prefixed lines yields
	// newest-first order
	sorted := append([]string(nil), lines...)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, sorted)
		return
	}

	rows := make([]notificationRow, len(sorted))
	for i, l := range sorted {
		// Index is 1-based from the top of this already-newest-first list,
		// matching HandleDelete/HandleMarkAsRead's own "1-indexed from the
		// newest entry" line-number convention.
		rows[i] = parseNotificationRow(l, i+1)
	}

	webtemplates.Render(w, "notifications.html", notificationsPageData{
		Chrome:        buildChrome(r, "Notifications"),
		Notifications: rows,
		Flashes:       auth.PopFlashes(w, r, n.Sessions),
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
	lines, err := readNotificationLines()
	if err != nil {
		http.Error(w, "Log file not found", http.StatusBadRequest)
		return
	}

	if r.FormValue("command") == "mark_all_as_read" {
		for i, l := range lines {
			lines[i] = strings.ReplaceAll(l, "UNREAD", "READ")
		}
	} else if lineNumber >= 1 && lineNumber <= len(lines) {
		idx := len(lines) - lineNumber
		lines[idx] = strings.ReplaceAll(lines[idx], "UNREAD", "READ")
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
