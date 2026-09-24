package handlers

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
)

// Notification is one line of NotificationsLogPath, written by opencli's lib/notifications.sh as JSON.
type Notification struct {
	ID         string               `json:"id"`
	Time       string               `json:"time"`
	LastSeen   string               `json:"last_seen,omitempty"`
	Count      int                  `json:"count,omitempty"`
	Status     string               `json:"status"`
	Severity   string               `json:"severity"`
	Category   string               `json:"category,omitempty"`
	Source     string               `json:"source,omitempty"`
	Title      string               `json:"title"`
	Message    string               `json:"message"`
	Details    *NotificationDetails `json:"details,omitempty"`
	ResolvedAt string               `json:"resolved_at,omitempty"`
}

// NotificationDetails is the structured data behind the special renderings on the Notifications page.
type NotificationDetails struct {
	Kind       string             `json:"kind,omitempty"`
	Percent    float64            `json:"percent,omitempty"`
	UsedMB     int                `json:"used_mb,omitempty"`
	TotalMB    int                `json:"total_mb,omitempty"`
	Processes  string             `json:"processes,omitempty"`
	Partitions string             `json:"partitions,omitempty"`
	Load       string             `json:"load,omitempty"`
	Crashlog   string             `json:"crashlog,omitempty"`
	LogFile    string             `json:"log_file,omitempty"`
	Action     string             `json:"action,omitempty"`
	System     []string           `json:"system,omitempty"`
	Users      []NotificationUser `json:"users,omitempty"`
}

type NotificationUser struct {
	Username string   `json:"username"`
	Entries  []string `json:"entries"`
}

func (n Notification) Unread() bool { return n.Status == "unread" }

// parseNotification reads a JSON line, lines from before 2.0.12 ("<date> <time> <STATUS> <title> MESSAGE: <message>") become plain entries
func parseNotification(raw string) Notification {
	if strings.HasPrefix(raw, "{") {
		var n Notification
		if json.Unmarshal([]byte(raw), &n) == nil {
			if n.Severity == "" {
				n.Severity = "info"
			}
			return n
		}
	}

	n := Notification{Severity: "info"}
	head, message, _ := strings.Cut(raw, " MESSAGE: ")
	fields := strings.SplitN(head, " ", 4)
	if len(fields) > 1 {
		n.Time = fields[0] + " " + fields[1]
	}
	if len(fields) > 2 {
		n.Status = strings.ToLower(fields[2])
	}
	if len(fields) > 3 {
		n.Title = strings.TrimSpace(fields[3])
	}
	n.Message = message
	return n
}

// markNotificationLineRead sets status to read without touching fields this code doesn't know about
func markNotificationLineRead(raw string) string {
	if strings.HasPrefix(raw, "{") {
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &m) == nil {
			m["status"] = json.RawMessage(`"read"`)
			if out, err := json.Marshal(m); err == nil {
				return string(out)
			}
		}
	}
	return strings.Replace(raw, " UNREAD ", " READ ", 1)
}

// lockNotifications takes the same flock opencli uses around notifications.log writes
func lockNotifications() func() {
	f, err := os.OpenFile(NotificationsLogPath+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}
