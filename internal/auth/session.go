package auth

import (
	"context"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/gorilla/sessions"

	"openadmin/internal/admindb"
)

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "OPENADMIN"

func init() {
	// gorilla/sessions' default gob-based serializer needs every concrete
	// type stored in the Values map (map[interface{}]interface{}) to be
	// registered, including named struct types like Flash.
	gob.Register(Flash{})
	gob.Register(int64(0))
}

// Manager wraps the cookie session store, configured with HttpOnly and
// SameSite=Lax cookies.
type Manager struct {
	store *sessions.CookieStore
}

// NewManager derives a session signing key from the on-disk secret (see
// deriveKey). useTLS controls the cookie's Secure flag.
func NewManager(secret string, useTLS bool) *Manager {
	store := sessions.NewCookieStore(deriveKey(secret, "session"))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   int((31 * 24 * time.Hour).Seconds()), // 31-day session lifetime
		HttpOnly: true,
		Secure:   useTLS,
		SameSite: http.SameSiteLaxMode,
	}
	return &Manager{store: store}
}

func (m *Manager) Get(r *http.Request) (*sessions.Session, error) {
	return m.store.Get(r, SessionCookieName)
}

type contextKey int

const (
	userContextKey contextKey = iota
	flashContextKey
)

// WithUserLoader loads the logged-in user (if any) from the session into
// the request context on every request.
func WithUserLoader(mgr *Manager, db *admindb.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sess, err := mgr.Get(r); err == nil {
				if uid, ok := sessionInt64(sess.Values["user_id"]); ok {
					if u, err := db.UserByID(uid); err == nil {
						r = r.WithContext(context.WithValue(r.Context(), userContextKey, u))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sessionInt64 handles both int64 (set within this process) and float64
// (what gob round-trips a plain "int" through on some platforms) so a
// session value survives a process restart cleanly either way.
func sessionInt64(v interface{}) (int64, bool) {
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

// CurrentUser returns the logged-in user for an authenticated request;
// returns nil if the request is anonymous.
func CurrentUser(r *http.Request) *admindb.User {
	u, _ := r.Context().Value(userContextKey).(*admindb.User)
	return u
}

func IsAuthenticated(r *http.Request) bool {
	return CurrentUser(r) != nil
}

// LoginUser stores the user id and the IP the session was established from
// (see ValidateSessionIP). mgr.Get's error is deliberately ignored: per
// gorilla/sessions, Get() still returns a usable new session (just with
// IsNew=true) when the browser's existing cookie fails to decode -- e.g. a
// stale cookie from before a secret rotation, or one a client tampered
// with. That's the expected path for anyone logging back in with a bad
// cookie, not a fatal error; the fresh session's Save below overwrites it.
func LoginUser(w http.ResponseWriter, r *http.Request, mgr *Manager, u *admindb.User, clientIP string) error {
	sess, _ := mgr.Get(r)
	sess.Values["user_id"] = u.ID
	sess.Values["user_ip"] = clientIP
	return sess.Save(r, w)
}

// LogoutUser clears the session and expires the cookie.
func LogoutUser(w http.ResponseWriter, r *http.Request, mgr *Manager) error {
	sess, err := mgr.Get(r)
	if err != nil {
		return err
	}
	for k := range sess.Values {
		delete(sess.Values, k)
	}
	sess.Options.MaxAge = -1
	return sess.Save(r, w)
}

// SessionUserIP returns the IP the current session was established from
// (session['user_ip']), for the IP-pinning check in ValidateSessionIP.
func SessionUserIP(mgr *Manager, r *http.Request) (string, bool) {
	sess, err := mgr.Get(r)
	if err != nil {
		return "", false
	}
	ip, ok := sess.Values["user_ip"].(string)
	return ip, ok
}

// Flash is a single flash message: (category, message).
type Flash struct {
	Category string
	Message  string
}

// AddFlash queues a flash message under the given category.
func AddFlash(w http.ResponseWriter, r *http.Request, mgr *Manager, message, category string) error {
	sess, err := mgr.Get(r)
	if err != nil {
		return err
	}
	sess.AddFlash(Flash{Category: category, Message: message})
	return sess.Save(r, w)
}

// PopFlashes reads and clears pending flashes. Call once per request that
// renders a template (typically the handler that follows a redirect).
func PopFlashes(w http.ResponseWriter, r *http.Request, mgr *Manager) []Flash {
	sess, err := mgr.Get(r)
	if err != nil {
		return nil
	}
	raw := sess.Flashes()
	if len(raw) > 0 {
		_ = sess.Save(r, w)
	}
	flashes := make([]Flash, 0, len(raw))
	for _, v := range raw {
		if f, ok := v.(Flash); ok {
			flashes = append(flashes, f)
		}
	}
	return flashes
}
