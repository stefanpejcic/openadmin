package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"openadmin/internal/activity"
	"openadmin/internal/auth"
)

// ImunifyScan bundles the per-user scan route used by /websites
type ImunifyScan struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// imunifyAgentRun runs imunify360-agent with --json, a var so tests don't need imunify
var imunifyAgentRun = func(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "imunify360-agent", append(args, "--json")...).Output()
}

func imunifyInstalled() bool { return imunifyCommandAvailableRun("imunify360-agent") }

// ImunifyInstalled is imunifyInstalled for main.go's bulk route wiring
func ImunifyInstalled() bool { return imunifyInstalled() }

// imunifyUser is one row of malware user list, User is the system user, the panel user's context
type imunifyUser struct {
	User          string  `json:"user"`
	Home          string  `json:"home"`
	Infected      int     `json:"infected"`
	InfectedDB    int     `json:"infected_db"`
	ScanID        string  `json:"scan_id"`
	ScanDate      int64   `json:"scan_date"`
	ScanStatus    string  `json:"scan_status"`
	CleanupStatus *string `json:"cleanup_status"`
}

func (u imunifyUser) Scanning() bool {
	return u.ScanStatus == "running" || u.ScanStatus == "queued" || u.ScanStatus == "pending"
}

func (u imunifyUser) Total() int { return u.Infected + u.InfectedDB }

// ScanAgo is the last scan as "2 days ago", empty when the user was never scanned
func (u imunifyUser) ScanAgo() string {
	if u.ScanDate == 0 {
		return ""
	}
	return timeSince(time.Unix(u.ScanDate, 0).Format("2006-01-02 15:04:05"), time.Now())
}

// imunifyItems decodes the items field every agent --json reply wraps its data in
func imunifyItems(out []byte, items any) error {
	var reply struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return err
	}
	if len(reply.Items) == 0 || string(reply.Items) == "null" {
		return nil
	}
	return json.Unmarshal(reply.Items, items)
}

func fetchImunifyUsers() (map[string]imunifyUser, error) {
	out, err := imunifyAgentRun("malware", "user", "list", "--limit", "100000")
	if err != nil {
		return nil, err
	}
	var items []imunifyUser
	if err := imunifyItems(out, &items); err != nil {
		return nil, err
	}
	users := make(map[string]imunifyUser, len(items))
	for _, u := range items {
		users[u.User] = u
	}
	return users, nil
}

var (
	imunifyUsersMu    sync.Mutex
	imunifyUsersCache map[string]imunifyUser
	imunifyUsersUntil time.Time
)

// cachedImunifyUsers is the per-user scan state for /websites, the agent takes over a second per call so it's kept for 2 minutes and refreshed in the background
func cachedImunifyUsers() map[string]imunifyUser {
	imunifyUsersMu.Lock()
	defer imunifyUsersMu.Unlock()
	if time.Now().Before(imunifyUsersUntil) {
		return imunifyUsersCache
	}
	imunifyUsersUntil = time.Now().Add(2 * time.Minute)
	if imunifyUsersCache == nil {
		users, err := fetchImunifyUsers()
		if err != nil {
			// keep an empty list so a broken agent doesn't stall /websites for a second on every load, the background refresh retries it
			users = map[string]imunifyUser{}
		}
		imunifyUsersCache = users
		return imunifyUsersCache
	}
	go func() {
		if users, err := fetchImunifyUsers(); err == nil {
			setImunifyUsersCache(users)
		}
	}()
	return imunifyUsersCache
}

func setImunifyUsersCache(users map[string]imunifyUser) {
	imunifyUsersMu.Lock()
	imunifyUsersCache, imunifyUsersUntil = users, time.Now().Add(2*time.Minute)
	imunifyUsersMu.Unlock()
}

// markImunifyQueued shows a freshly queued scan right away instead of after the next refresh
func markImunifyQueued(userContext string) {
	imunifyUsersMu.Lock()
	defer imunifyUsersMu.Unlock()
	if u, ok := imunifyUsersCache[userContext]; ok {
		u.ScanStatus = "queued"
		imunifyUsersCache[userContext] = u
	}
}

var imunifyUserRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// imunifyScanRun queues a scan of a user's home, the same scan imunify runs per user on its own schedule
var imunifyScanRun = func(userContext string) error {
	_, err := imunifyAgentRun("malware", "on-demand", "queue", "put", "/home/"+userContext)
	return err
}

// HandleScanUser handles POST /security/imunify/{user}, user is the panel username
func (ir *ImunifyScan) HandleScanUser(w http.ResponseWriter, r *http.Request) {
	back := "/websites"
	fail := func(msg string) {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ir.Sessions, msg, "error")
		http.Redirect(w, r, back, http.StatusSeeOther)
	}

	username := r.PathValue("user")
	if !imunifyUserRE.MatchString(username) {
		fail("Invalid username.")
		return
	}
	if !imunifyInstalled() {
		fail("ImunifyAV is not installed.")
		return
	}
	userContext, err := queryContextByUsername(ir.MySQL, username)
	if err != nil || !imunifyUserRE.MatchString(userContext) {
		fail("User \"" + username + "\" not found.")
		return
	}
	if err := imunifyScanRun(userContext); err != nil {
		fail("Failed to queue the ImunifyAV scan for " + username + ": " + err.Error())
		return
	}
	markImunifyQueued(userContext)
	activity.Describe(r.Context(), "Queued ImunifyAV scan for "+username)
	auth.AddFlash(w, r, ir.Sessions, "ImunifyAV scan queued for "+username+", the result shows up here once it finishes.", "info")
	http.Redirect(w, r, back, http.StatusSeeOther)
}
