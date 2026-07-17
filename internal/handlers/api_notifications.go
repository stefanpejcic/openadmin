// This file implements the JSON REST API's notifications surface (mirrors
// the log-file-line model behind the HTML /notifications page in
// notifications.go, but as its own independent read/write path so this
// endpoint's line-numbering -- which must count every raw line, blank ones
// included -- isn't affected by that page's blank-line-filtering behavior)
// plus the per-user disk/inode quota listing.
package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// APINotifications bundles the /api/notifications* and /api/usage/disk
// handlers.
type APINotifications struct{}

// ServeNotifications handles GET /api/notifications.
func (n *APINotifications) ServeNotifications(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(NotificationsLogPath)
	if err != nil {
		if os.IsNotExist(err) {
			os.WriteFile(NotificationsLogPath, nil, 0644)
			writeJSON(w, map[string]interface{}{"success": true, "notifications": []string{}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Error loading notifications from " + NotificationsLogPath,
			"details": err.Error(),
		})
		return
	}

	var notifications []string
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			notifications = append(notifications, t)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(notifications)))
	if notifications == nil {
		notifications = []string{}
	}
	writeJSON(w, map[string]interface{}{"success": true, "notifications": notifications})
}

// readRawNotificationLines reads NotificationsLogPath as raw, unfiltered
// lines (blank lines included); a trailing newline at end-of-file does not
// produce a synthetic empty final element.
func readRawNotificationLines() ([]string, error) {
	raw, err := os.ReadFile(NotificationsLogPath)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	lines := strings.Split(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func writeRawNotificationLines(lines []string) error {
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(NotificationsLogPath, []byte(content), 0644)
}

// notificationCommandParam reads the "command" query param, falling back
// to a "command" field in a (silently-ignored-if-invalid) JSON body.
func notificationCommandParam(r *http.Request) string {
	if c := r.URL.Query().Get("command"); c != "" {
		return c
	}
	var body struct {
		Command string `json:"command"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	return body.Command
}

// HandleMarkRead handles POST /api/notifications/{line_number}/read.
// line_number is 1-indexed from the newest (bottom-of-file) entry, the same
// convention as the HTML page's HandleMarkAsRead in notifications.go.
func (n *APINotifications) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	// A non-numeric path segment never reaches this handler in the first
	// place under a typed route parameter, so it 404s here too.
	lineNumber, convErr := strconv.Atoi(r.PathValue("line_number"))
	if convErr != nil {
		writeJSONError(w, http.StatusNotFound, "Not Found")
		return
	}
	command := notificationCommandParam(r)

	lines, err := readRawNotificationLines()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "Log file not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Error marking notification as read: "+err.Error())
		return
	}

	switch {
	case command == "mark_all_as_read":
		for i, l := range lines {
			lines[i] = strings.ReplaceAll(l, "UNREAD", "READ")
		}
	case lineNumber >= 1 && lineNumber <= len(lines):
		idx := len(lines) - lineNumber
		lines[idx] = strings.ReplaceAll(lines[idx], "UNREAD", "READ")
	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid line number")
		return
	}

	if err := writeRawNotificationLines(lines); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error marking notification as read: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// HandleDelete handles DELETE /api/notifications/{line_number}.
func (n *APINotifications) HandleDelete(w http.ResponseWriter, r *http.Request) {
	lineNumber, convErr := strconv.Atoi(r.PathValue("line_number"))
	if convErr != nil {
		writeJSONError(w, http.StatusNotFound, "Not Found")
		return
	}
	command := notificationCommandParam(r)

	lines, err := readRawNotificationLines()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "Log file not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Error deleting notification: "+err.Error())
		return
	}

	switch {
	case command == "delete_all":
		lines = nil
	case lineNumber >= 1 && lineNumber <= len(lines):
		idx := len(lines) - lineNumber
		lines = append(lines[:idx], lines[idx+1:]...)
	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid line number")
		return
	}

	if err := writeRawNotificationLines(lines); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error deleting notification: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// apiQuotaGenerateRun runs `opencli user-quota` to (re)generate
// QuotaReportPath when it's missing. Injectable so tests never shell out.
var apiQuotaGenerateRun = func() error {
	return exec.Command("opencli", "user-quota").Run()
}

// quotaFieldOrZero defaults a genuinely absent key to 0; a present-but-null
// value still reads back as null.
func quotaFieldOrZero(m map[string]interface{}, key string) interface{} {
	if v, ok := m[key]; ok {
		return v
	}
	return 0
}

// ServeDiskUsage handles GET /api/usage/disk: per-user disk/inode usage
// from QuotaReportPath (shared with users.go's readDiskUsageAll), generating
// the report on demand via `opencli user-quota` if it doesn't exist yet.
func (n *APINotifications) ServeDiskUsage(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(QuotaReportPath); err != nil {
		if genErr := apiQuotaGenerateRun(); genErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to generate quota report.")
			return
		}
		if _, err := os.Stat(QuotaReportPath); err != nil {
			writeJSONError(w, http.StatusNotFound, "Quota file not found after generation.")
			return
		}
	}

	raw, err := os.ReadFile(QuotaReportPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Unable to read quota file.")
		return
	}
	var parsed struct {
		Users []map[string]interface{} `json:"users"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		writeJSONError(w, http.StatusInternalServerError, "Unable to read quota file.")
		return
	}

	results := map[string]interface{}{}
	for _, u := range parsed.Users {
		username, _ := u["username"].(string)
		if username == "" {
			continue
		}
		results[username] = map[string]interface{}{
			"diskusage":   quotaFieldOrZero(u, "disk_used"),
			"disklimit":   quotaFieldOrZero(u, "disk_hard"),
			"inodesusage": quotaFieldOrZero(u, "inodes_used"),
			"inodeslimit": quotaFieldOrZero(u, "inodes_hard"),
			"bwusage":     0,
			"bwlimit":     0,
		}
	}
	writeJSON(w, results)
}
