// Package handlers holds the admin panel's HTTP handlers.
package handlers

import (
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/csrf"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/panelinfo"
	"openadmin/internal/webtemplates"
)

func init() {
	gob.Register(webauthn.SessionData{})
}

// Login log paths.
var (
	LoginLogPath       = "/var/log/openpanel/admin/login.log"
	FailedLoginLogPath = "/var/log/openpanel/admin/failed_login.log"
	ErrorLogPath       = "/var/log/openpanel/admin/error.log"
)

// Login bundles the login/logout/2FA/passkey handlers and their shared
// dependencies.
type Login struct {
	DB       *admindb.DB
	Sessions *auth.Manager
	Limiter  *auth.PerIPLimiter

	// Display data injected once at startup: public IP, server hostname,
	// force-domain value, panel version, and license type, all of which
	// every rendered template needs.
	PublicIP         string
	ServerHostname   string
	ForceDomainValue string
	PanelVersion     string
	LicenseType      string

	// BlockLimit mirrors admin.ini's [PANEL] login_blocklimit: the number
	// of rate-limit violations from one IP before it's temporarily
	// CSF-blocked.
	BlockLimit int

	mu             sync.Mutex
	failedAttempts map[string]int // in-memory, cleared on restart
}

func NewLogin(db *admindb.DB, sessions *auth.Manager, limiter *auth.PerIPLimiter, blockLimit int) *Login {
	return &Login{
		DB:             db,
		Sessions:       sessions,
		Limiter:        limiter,
		BlockLimit:     blockLimit,
		failedAttempts: map[string]int{},
	}
}

type loginPageData struct {
	ForceDomainValue string
	ServerHostname   string
	PublicIP         string
	PanelVersion     string
	LicenseType      string
	CSRFToken        string
	Next             string
	Flashes          []auth.Flash
	OpenpanelDomain  string
	OpenpanelPort    string
}

func (l *Login) renderLoginPage(w http.ResponseWriter, r *http.Request, status int) {
	w.WriteHeader(status)
	webtemplates.Render(w, "login.html", loginPageData{
		ForceDomainValue: l.ForceDomainValue,
		ServerHostname:   l.ServerHostname,
		PublicIP:         l.PublicIP,
		PanelVersion:     l.PanelVersion,
		LicenseType:      l.LicenseType,
		CSRFToken:        csrf.Token(r),
		Next:             r.URL.Query().Get("next"),
		Flashes:          auth.PopFlashes(w, r, l.Sessions),
		OpenpanelDomain:  panelinfo.Domain(),
		OpenpanelPort:    panelinfo.Port(),
	})
}

// ServeLoginPage handles GET /login (and GET /login/).
func (l *Login) ServeLoginPage(w http.ResponseWriter, r *http.Request) {
	l.renderLoginPage(w, r, http.StatusOK)
}

// HandleLoginSubmit handles POST /login (and POST /login/).
func (l *Login) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	if !l.Limiter.Allow(ip) {
		l.handleRateLimitExceeded(w, r, ip)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := l.DB.UserByUsername(username)
	if err != nil || !auth.CheckPasswordHash(user.PasswordHash, password) {
		auth.AddFlash(w, r, l.Sessions, "Login failed. Please check your credentials.", "danger")
		l.renderLoginPage(w, r, http.StatusOK)
		return
	}

	if !user.IsActive {
		auth.AddFlash(w, r, l.Sessions, "Login failed. User is not active.", "danger")
		l.renderLoginPage(w, r, http.StatusOK)
		return
	}

	next := firstNonEmpty(r.FormValue("next"), r.URL.Query().Get("next"))

	if user.TOTPEnabled {
		sess, _ := l.Sessions.Get(r)
		sess.Values["2fa_user_id"] = user.ID
		sess.Values["2fa_next"] = next
		sess.Save(r, w)
		http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		return
	}

	l.finalizeLogin(w, r, user, ip, next)
}

func (l *Login) finalizeLogin(w http.ResponseWriter, r *http.Request, user *admindb.User, ip, next string) {
	appendLogLine(LoginLogPath, fmt.Sprintf("%s %s %s", time.Now().Format("2006-01-02 15:04:05"), user.Username, ip))

	if err := auth.LoginUser(w, r, l.Sessions, user, ip); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	l.mu.Lock()
	delete(l.failedAttempts, ip)
	l.mu.Unlock()

	if user.Role == "reseller" {
		auth.AddFlash(w, r, l.Sessions, "Login successful", "success")
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	// next is always a same-origin relative path here (see auth.RequireLogin's
	// doc comment: it can't point off-domain by construction), so it's safe
	// to redirect to directly without an extra origin check.
	if next != "" && strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (l *Login) handleRateLimitExceeded(w http.ResponseWriter, r *http.Request, ip string) {
	appendLogLine(FailedLoginLogPath, fmt.Sprintf("%s Rate limit for login exceeded from IP: %s", time.Now().Format("2006-01-02 15:04:05"), ip))

	l.mu.Lock()
	l.failedAttempts[ip]++
	count := l.failedAttempts[ip]
	l.mu.Unlock()

	if l.BlockLimit > 0 && count > l.BlockLimit {
		blockIPTemporarily(ip, l.BlockLimit)
	}

	auth.AddFlash(w, r, l.Sessions, "Too many failed login attempts. Please try again later.", "danger")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// blockIPTemporarily shells out to CSF. Best-effort -- errors are logged,
// never fatal to the request.
func blockIPTemporarily(ip string, blockLimit int) {
	if err := exec.Command("csf", "-v").Run(); err != nil {
		appendLogLine(ErrorLogPath, fmt.Sprintf("%s - Failed to block IP %s on Firewall: csf not available: %v", time.Now(), ip, err))
		return
	}
	msg := fmt.Sprintf("Too many failed login attempts on OpenAdmin")
	if err := exec.Command("csf", "-td", ip, msg).Run(); err != nil {
		appendLogLine(ErrorLogPath, fmt.Sprintf("%s - Failed to block IP %s on Firewall: %v", time.Now(), ip, err))
		return
	}
	appendLogLine(FailedLoginLogPath, fmt.Sprintf("%s IP: %s temporary blocked on CSF due to %d failed logins.", time.Now().Format("2006-01-02 15:04:05"), ip, blockLimit))
}

// --- 2FA ---

type twoFAPageData struct {
	Username         string
	CSRFToken        string
	Flashes          []auth.Flash
	ForceDomainValue string
	ServerHostname   string
	PublicIP         string
}

// ServeTwoFAPage handles GET /login/2fa.
func (l *Login) ServeTwoFAPage(w http.ResponseWriter, r *http.Request) {
	user := l.pending2FAUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	webtemplates.Render(w, "login_2fa.html", twoFAPageData{
		Username:         user.Username,
		CSRFToken:        csrf.Token(r),
		Flashes:          auth.PopFlashes(w, r, l.Sessions),
		ForceDomainValue: l.ForceDomainValue,
		ServerHostname:   l.ServerHostname,
		PublicIP:         l.PublicIP,
	})
}

// HandleTwoFASubmit handles POST /login/2fa.
func (l *Login) HandleTwoFASubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !l.Limiter.Allow(ip) {
		l.handleRateLimitExceeded(w, r, ip)
		return
	}

	user := l.pending2FAUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	valid, _ := totp.ValidateCustom(code, user.TOTPSecret.String, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})

	if !valid {
		auth.AddFlash(w, r, l.Sessions, "Invalid authentication code. Please try again.", "danger")
		l.ServeTwoFAPage(w, r)
		return
	}

	sess, _ := l.Sessions.Get(r)
	next, _ := sess.Values["2fa_next"].(string)
	delete(sess.Values, "2fa_user_id")
	delete(sess.Values, "2fa_next")
	sess.Save(r, w)

	l.finalizeLogin(w, r, user, ip, next)
}

func (l *Login) pending2FAUser(r *http.Request) *admindb.User {
	sess, err := l.Sessions.Get(r)
	if err != nil {
		return nil
	}
	uid, ok := sess.Values["2fa_user_id"]
	if !ok {
		return nil
	}
	id, ok := toInt64(uid)
	if !ok {
		return nil
	}
	user, err := l.DB.UserByID(id)
	if err != nil || !user.TOTPEnabled {
		return nil
	}
	return user
}

// --- Passkeys (WebAuthn) ---
//
// NOTE: this passkey implementation uses the go-webauthn library. It has
// not been exercised against a real browser/authenticator ceremony (this
// environment has no way to drive navigator.credentials.get()) -- treat it
// as needing manual verification with a real passkey before relying on it.
// Password + TOTP login (above) is the fully tested path.

const webauthnSessionKey = "passkey_login_session"

var webauthnInvalidHostError = "Passkeys require OpenAdmin to be accessed over a domain name (not an IP address). Set up a custom domain to use this feature."

func webauthnRPID(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	host, _, found := strings.Cut(host, ":")
	if !found {
		host = strings.TrimSuffix(host, ":")
	}
	return host
}

func webauthnOrigin(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func isWebauthnCapableHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if isIPAddress(host) {
		return false
	}
	return strings.Contains(host, ".")
}

func isIPAddress(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		allNumeric := true
		for _, p := range parts {
			for _, c := range p {
				if c < '0' || c > '9' {
					allNumeric = false
				}
			}
		}
		if allNumeric {
			return true
		}
	}
	return strings.Contains(host, ":") // crude IPv6 check
}

// HandlePasskeyBegin handles POST /login/passkey/begin.
func (l *Login) HandlePasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !l.Limiter.Allow(clientIP(r)) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
		return
	}

	rpID := webauthnRPID(r)
	if !isWebauthnCapableHost(rpID) {
		writeJSONError(w, http.StatusBadRequest, webauthnInvalidHostError)
		return
	}

	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "OpenAdmin",
		RPOrigins:     []string{webauthnOrigin(r)},
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not initialize passkey login")
		return
	}

	assertion, sessionData, err := wa.BeginDiscoverableLogin()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start passkey login")
		return
	}

	sess, _ := l.Sessions.Get(r)
	sess.Values[webauthnSessionKey] = *sessionData
	sess.Save(r, w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assertion.Response)
}

// HandlePasskeyComplete handles POST /login/passkey/complete.
func (l *Login) HandlePasskeyComplete(w http.ResponseWriter, r *http.Request) {
	if !l.Limiter.Allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "rate limited")
		return
	}

	sess, _ := l.Sessions.Get(r)
	sd, ok := sess.Values[webauthnSessionKey].(webauthn.SessionData)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Passkey login session expired, please try again.")
		return
	}
	delete(sess.Values, webauthnSessionKey)
	sess.Save(r, w)

	parsed, err := protocol.ParseCredentialRequestResponseBody(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request.")
		return
	}

	credentialID := parsed.ID // base64url string
	stored, err := l.DB.CredentialByCredentialID(credentialID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "This passkey is not registered.")
		return
	}

	user, err := l.DB.UserByID(stored.UserID)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, "Login failed. User is not active.")
		return
	}

	rpID := webauthnRPID(r)
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "OpenAdmin",
		RPOrigins:     []string{webauthnOrigin(r)},
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not verify passkey")
		return
	}

	waUser := &webauthnUser{user: user, credential: stored}
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		return waUser, nil
	}

	cred, err := wa.ValidateDiscoverableLogin(handler, sd, parsed)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not verify passkey.")
		return
	}

	l.DB.UpdateCredentialSignCount(stored.CredentialID, uint32(cred.Authenticator.SignCount))

	if !user.IsActive {
		writeJSONError(w, http.StatusForbidden, "Login failed. User is not active.")
		return
	}

	ip := clientIP(r)
	next := r.URL.Query().Get("next")

	appendLogLine(LoginLogPath, fmt.Sprintf("%s %s %s", time.Now().Format("2006-01-02 15:04:05"), user.Username, ip))
	if err := auth.LoginUser(w, r, l.Sessions, user, ip); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	l.mu.Lock()
	delete(l.failedAttempts, ip)
	l.mu.Unlock()

	redirect := "/dashboard"
	if user.Role != "reseller" && next != "" && strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		redirect = next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"redirect": redirect})
}

// webauthnUser adapts admindb's User/WebauthnCredential to go-webauthn's
// User interface. Only the single credential involved in this login
// ceremony is exposed: it's looked up by the asserted credential ID, so
// there's only ever one to expose.
type webauthnUser struct {
	user       *admindb.User
	credential *admindb.WebauthnCredential
}

func (u *webauthnUser) WebAuthnID() []byte          { return []byte(fmt.Sprintf("%d", u.user.ID)) }
func (u *webauthnUser) WebAuthnName() string        { return u.user.Username }
func (u *webauthnUser) WebAuthnDisplayName() string { return u.user.Username }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	pubKey, err := base64.RawURLEncoding.DecodeString(u.credential.PublicKey)
	if err != nil {
		return nil
	}
	credID, err := base64.RawURLEncoding.DecodeString(u.credential.CredentialID)
	if err != nil {
		return nil
	}
	c := webauthn.Credential{
		ID:        credID,
		PublicKey: pubKey,
	}
	c.Authenticator.SignCount = u.credential.SignCount
	return []webauthn.Credential{c}
}

// --- Logout ---

// HandleLogout handles GET /logout.
func (l *Login) HandleLogout(w http.ResponseWriter, r *http.Request) {
	auth.LogoutUser(w, r, l.Sessions)
	auth.AddFlash(w, r, l.Sessions, "Logged out successfully", "success")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- shared helpers ---

func clientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func appendLogLine(path, line string) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// HandleTourComplete handles POST /api/tour/complete: creates
// ChromeTourSkipFilePath (chrome.go) if it doesn't already exist, so
// buildChrome's TourShow never fires again.
func (l *Login) HandleTourComplete(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(ChromeTourSkipFilePath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(ChromeTourSkipFilePath), 0755); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Could not save tour state.")
			return
		}
		if err := os.WriteFile(ChromeTourSkipFilePath, nil, 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Could not save tour state.")
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}
