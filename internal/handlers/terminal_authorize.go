package handlers

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"openadmin/internal/auth"
)

// The web terminal opens a root PTY, so an XSS in the admin origin that reaches
// /ws/terminal would get a root shell. Opening it now needs a fresh password:
// the admin posts it to /terminal/authorize, which hands back a short-lived
// token the page passes to the websocket. Injected JS can't mint that token
// without knowing the password, so it can't open the terminal on its own.

type terminalGrant struct {
	userID  int64
	expires time.Time
}

var (
	terminalGrantsMu sync.Mutex
	terminalGrants   = map[string]terminalGrant{}
	// a terminal page that reconnects reuses its token for this long before the admin is asked again
	terminalGrantTTL = 15 * time.Minute
)

func mintTerminalGrant(userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := cryptorand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	now := time.Now()
	terminalGrantsMu.Lock()
	for k, g := range terminalGrants {
		if now.After(g.expires) {
			delete(terminalGrants, k)
		}
	}
	terminalGrants[tok] = terminalGrant{userID: userID, expires: now.Add(terminalGrantTTL)}
	terminalGrantsMu.Unlock()
	return tok, nil
}

// validTerminalGrant is whether tok was minted for userID and hasn't expired, it isn't consumed so the socket can reconnect
func validTerminalGrant(tok string, userID int64) bool {
	if tok == "" {
		return false
	}
	terminalGrantsMu.Lock()
	defer terminalGrantsMu.Unlock()
	g, ok := terminalGrants[tok]
	if !ok || g.userID != userID || time.Now().After(g.expires) {
		return false
	}
	return true
}

// a logged-in admin brute forcing their own password here is slow enough to matter, so cap the tries
var (
	terminalAuthTriesMu sync.Mutex
	terminalAuthTries   = map[int64][]time.Time{}
)

func terminalAuthAllowed(userID int64) bool {
	cutoff := time.Now().Add(-time.Minute)
	terminalAuthTriesMu.Lock()
	defer terminalAuthTriesMu.Unlock()
	kept := terminalAuthTries[userID][:0]
	for _, t := range terminalAuthTries[userID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	terminalAuthTries[userID] = kept
	if len(kept) >= 5 {
		return false
	}
	terminalAuthTries[userID] = append(terminalAuthTries[userID], time.Now())
	return true
}

// ServeTerminalAuthorize handles POST /terminal/authorize. The admin re-enters
// their password and gets a short-lived token to open the web terminal with.
func (t *Terminal) ServeTerminalAuthorize(w http.ResponseWriter, r *http.Request) {
	if t.terminalDisabled(w, r) {
		return
	}
	u := auth.CurrentUser(r)
	if u == nil {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "Not signed in."})
		return
	}
	if !terminalAuthAllowed(u.ID) {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]interface{}{"error": "Too many attempts, wait a minute and try again."})
		return
	}
	password := r.FormValue("password")
	if password == "" || u.PasswordHash == "" || !auth.CheckPasswordHash(u.PasswordHash, password) {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "Wrong password."})
		return
	}
	tok, err := mintTerminalGrant(u.ID)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "Could not start a terminal session."})
		return
	}
	writeJSON(w, map[string]interface{}{"token": tok})
}

// terminalGrantOK gates a websocket on a valid grant for the current admin, it writes the error itself
func (t *Terminal) terminalGrantOK(w http.ResponseWriter, r *http.Request) bool {
	u := auth.CurrentUser(r)
	if u == nil || !validTerminalGrant(r.URL.Query().Get("token"), u.ID) {
		http.Error(w, "Enter your password to open the terminal.", http.StatusForbidden)
		return false
	}
	return true
}
