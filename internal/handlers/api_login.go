// This file implements POST /api/login and GET /login/sso/{token}: a way
// for external integrations (e.g. the WHMCS module's "Login to Panel"
// button) to open a real OpenAdmin browser session for an admin/reseller
// user without posting credentials directly to the CSRF-protected /login
// form, which auto-submitted cross-origin forms (Origin: null) can't get
// past. POST /api/login checks credentials and hands back a one-time
// token; GET /login/sso/{token} redeems it for a session cookie.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

// ssoTokenTTL is deliberately short: the token only needs to survive the
// round trip from POST /api/login to the admin clicking the resulting link.
const ssoTokenTTL = 2 * time.Minute

type ssoTokenEntry struct {
	username string
	expires  time.Time
}

// APILogin bundles POST /api/login and GET /login/sso/{token}, which share
// the in-memory one-time token store between them.
type APILogin struct {
	DB       *admindb.DB
	Sessions *auth.Manager
	Limiter  *auth.PerIPLimiter

	mu     sync.Mutex
	tokens map[string]ssoTokenEntry
}

func newSSOToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HandleAPILogin handles POST /api/login. Same credential check as POST
// /api/ (no TOTP check -- password auth is enough for API access here too,
// same as the JWT login), but issues a single-use token good for a real
// session at GET /login/sso/{token} instead of a JWT.
func (a *APILogin) HandleAPILogin(w http.ResponseWriter, r *http.Request) {
	if !a.Limiter.Allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "Too many login attempts. Please try again later.")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	user, err := a.DB.UserByUsername(body.Username)
	if err != nil || !auth.CheckPasswordHash(user.PasswordHash, body.Password) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if !user.IsActive {
		writeJSONError(w, http.StatusForbidden, "User is not active")
		return
	}

	token, err := newSSOToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Could not issue token")
		return
	}

	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = make(map[string]ssoTokenEntry)
	}
	a.tokens[token] = ssoTokenEntry{username: user.Username, expires: time.Now().Add(ssoTokenTTL)}
	a.mu.Unlock()

	writeJSON(w, map[string]interface{}{
		"token":      token,
		"login_path": "/login/sso/" + token,
		"expires_in": int(ssoTokenTTL.Seconds()),
	})
}

// HandleSSOLogin handles GET /login/sso/{token}. The token is popped on
// first use whether or not it turns out valid, so a link that leaked into
// a proxy or browser history log can't be replayed.
func (a *APILogin) HandleSSOLogin(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	a.mu.Lock()
	entry, ok := a.tokens[token]
	delete(a.tokens, token)
	a.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		http.Error(w, "This login link is invalid or has expired.", http.StatusUnauthorized)
		return
	}

	user, err := a.DB.UserByUsername(entry.username)
	if err != nil || !user.IsActive {
		http.Error(w, "This login link is invalid or has expired.", http.StatusUnauthorized)
		return
	}

	ip := clientIP(r)
	if err := auth.LoginUser(w, r, a.Sessions, user, ip); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	appendLogLine(LoginLogPath, fmt.Sprintf("%s %s %s (sso)", time.Now().Format("2006-01-02 15:04:05"), user.Username, ip))

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
