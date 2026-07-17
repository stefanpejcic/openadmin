package auth

import (
	"net/http"
	"net/url"

	"openadmin/internal/server"
)

// Options bundles the config-driven knobs the middleware needs, injected
// explicitly (rather than reaching into a global config singleton) so
// behavior is easy to unit test and easy to see at the call site.
type Options struct {
	// DemoMode disables non-GET requests while the panel is running in demo mode.
	DemoMode bool
	// ValidateSessionIP enables the IP-pinning check that terminates a
	// session if the client's IP changes mid-session.
	ValidateSessionIP bool
}

// demoModeBlocked rejects non-GET requests with a flash message while demo
// mode is on, redirecting back to the referring page (or /dashboard).
func demoModeBlocked(w http.ResponseWriter, r *http.Request, mgr *Manager, opts Options) bool {
	if !opts.DemoMode || r.Method == http.MethodGet {
		return false
	}
	AddFlash(w, r, mgr, "This functionality is disabled in demo mode.", "warning")
	referer := r.Referer()
	if referer == "" {
		referer = "/dashboard"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
	return true
}

// RequireLogin redirects anonymous requests to /login?next=<relative path>.
// Using a relative path (rather than an absolute URL) makes the redirect
// inherently same-origin, so there's no need for a separate same-domain
// guard on next -- a relative path can't point off-domain.
func RequireLogin(mgr *Manager, opts Options, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthenticated(r) {
			redirectToLogin(w, r)
			return
		}
		if demoModeBlocked(w, r, mgr, opts) {
			return
		}
		next(w, r)
	}
}

// RequireAdmin requires an authenticated session (like RequireLogin) plus
// rejects the "reseller" role with a 403.
func RequireAdmin(mgr *Manager, opts Options, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthenticated(r) {
			redirectToLogin(w, r)
			return
		}
		if demoModeBlocked(w, r, mgr, opts) {
			return
		}
		if CurrentUser(r).Role == "reseller" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.RequestURI()
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
}

// ValidateSessionIPMiddleware terminates a session if the client's current
// IP no longer matches the IP the session was established from. Skipped for
// /api, /login, /send_email, and /static, and gated by opts.ValidateSessionIP.
func ValidateSessionIPMiddleware(mgr *Manager, opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !opts.ValidateSessionIP || shouldSkipIPValidation(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if IsAuthenticated(r) {
				currentIP := server.GetClientIP(r)
				sessionIP, ok := SessionUserIP(mgr, r)
				if ok && sessionIP != "" && sessionIP != currentIP {
					LogoutUser(w, r, mgr)
					AddFlash(w, r, mgr, "Your session has expired due to an IP address change. Please log in again.", "danger")
					http.Redirect(w, r, "/login", http.StatusSeeOther)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func shouldSkipIPValidation(path string) bool {
	switch {
	case path == "/login", path == "/send_email":
		return true
	case len(path) >= 4 && path[:4] == "/api":
		return true
	case len(path) >= 8 && path[:8] == "/static/":
		return true
	default:
		return false
	}
}
