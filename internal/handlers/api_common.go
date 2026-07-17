// This file implements the shared plumbing for the JSON REST API under
// /api/*: JWT issuance/validation, the three role-gating middlewares used
// by every endpoint, and the "is the API even reachable right now" gate.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"openadmin/internal/admindb"
	"openadmin/internal/config"
	"openadmin/internal/paneldb"
)

// APIJWTExpiry is how long an issued access token stays valid.
const APIJWTExpiry = 15 * time.Minute

// apiJWTClaims is deliberately minimal: only the subject (username) is
// used by anything downstream of validation.
type apiJWTClaims struct {
	jwt.RegisteredClaims
}

// createAPIToken signs a new HS256 JWT for username, valid for
// APIJWTExpiry, using secretKey as the signing key.
func createAPIToken(username, secretKey string) (string, error) {
	now := time.Now()
	claims := apiJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(APIJWTExpiry)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secretKey))
}

// parseAPIToken validates the bearer token from the Authorization header
// and returns the username (JWT subject) it was issued for.
func parseAPIToken(r *http.Request, secretKey string) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	tokenString, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || tokenString == "" {
		return "", false
	}

	claims := &apiJWTClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(secretKey), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid || claims.Subject == "" {
		return "", false
	}
	return claims.Subject, true
}

// APIAuth bundles the dependencies every /api/* handler and its
// role-gating middleware need.
type APIAuth struct {
	DB        *admindb.DB
	MySQL     *sql.DB
	SecretKey string
}

// actingAPIUser resolves the bearer token to a real, currently-active
// admindb user, or nil if the token is missing/invalid/for an unknown user.
func (a *APIAuth) actingAPIUser(r *http.Request) *admindb.User {
	username, ok := parseAPIToken(r, a.SecretKey)
	if !ok {
		return nil
	}
	user, err := a.DB.UserByUsername(username)
	if err != nil {
		return nil
	}
	return user
}

// ActingAPIUserOr404 resolves the caller the same way the role-gating
// middlewares below do, writing the same 404 body they use when the
// token's user no longer exists. A handful of bare-@jwt_required() routes
// (no shared role decorator) do this same acting-user lookup inline
// instead of going through a role-gating middleware; this lets their Go
// handlers do the same without duplicating the JSON body.
func (a *APIAuth) ActingAPIUserOr404(w http.ResponseWriter, r *http.Request) (*admindb.User, bool) {
	user := a.actingAPIUser(r)
	if user == nil {
		writeJSONError(w, http.StatusNotFound, "User not found")
		return nil, false
	}
	return user, true
}

type apiUserContextKey struct{}
type apiUsernameContextKey struct{}

// withAPIUser stashes the resolved acting user on the request context so
// handlers can read it back via APIUserFromContext instead of re-parsing
// the bearer token themselves.
func withAPIUser(r *http.Request, user *admindb.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), apiUserContextKey{}, user))
}

// withAPIUsername stashes the raw JWT subject on the request context,
// available even when the acting user's DB row is gone (see
// RequireAPIToken below) or was never looked up at all.
func withAPIUsername(r *http.Request, username string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), apiUsernameContextKey{}, username))
}

// APIUserFromContext returns the acting user resolved by one of the
// role-gating RequireAPI* middlewares below. Guaranteed non-nil under
// those; under plain RequireAPIToken it's nil if the token's user no
// longer exists in the DB -- use APIUsernameFromContext if only the raw
// identity is needed.
func APIUserFromContext(r *http.Request) *admindb.User {
	user, _ := r.Context().Value(apiUserContextKey{}).(*admindb.User)
	return user
}

// APIUsernameFromContext returns the JWT subject of the current request,
// set by any of the RequireAPI* middlewares. Always non-empty once one of
// them has run.
func APIUsernameFromContext(r *http.Request) string {
	username, _ := r.Context().Value(apiUsernameContextKey{}).(string)
	return username
}

// RequireAPIToken only validates that the bearer token itself is present
// and well-formed -- no acting-user DB lookup and no role check. This
// mirrors a bare @jwt_required() route: most of them (e.g. /api/whoami)
// never touch the acting user's DB row at all, just the JWT's identity
// claim, so requiring the row to still exist here would reject requests
// Python happily serves. Handlers that do need the full acting user (a
// minority of the bare-decorated routes) resolve it themselves via
// ActingAPIUserOr404, same as their Python counterparts do inline.
func (a *APIAuth) RequireAPIToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := parseAPIToken(r, a.SecretKey)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "Missing or invalid token")
			return
		}
		user, _ := a.DB.UserByUsername(username)
		next(w, withAPIUser(withAPIUsername(r, username), user))
	}
}

// RequireAPIAdmin allows the "admin" and "user" roles, blocking "reseller"
// -- the JWT-API equivalent of RequireAdmin for session-based routes.
func (a *APIAuth) RequireAPIAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := a.actingAPIUser(r)
		if user == nil {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}
		if user.Role == "reseller" {
			writeJSONError(w, http.StatusForbidden, "Forbidden: administrator role required")
			return
		}
		next(w, withAPIUser(withAPIUsername(r, user.Username), user))
	}
}

// RequireAPISuperAdmin only allows role == "admin", for reboot/root-password/
// impersonation-class actions.
func (a *APIAuth) RequireAPISuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := a.actingAPIUser(r)
		if user == nil || user.Role != "admin" {
			writeJSONError(w, http.StatusForbidden, "Only the Super Admin can perform this action.")
			return
		}
		next(w, withAPIUser(withAPIUsername(r, user.Username), user))
	}
}

// RequireAPIOwnerOrAdmin allows "admin"/"user" roles through unconditionally;
// a "reseller" must own the account named by the {pathParam} path value.
func (a *APIAuth) RequireAPIOwnerOrAdmin(pathParam string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := a.actingAPIUser(r)
		if user == nil {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}
		target := r.PathValue(pathParam)
		if target != "" && !paneldb.CheckIfOwnerForUser(a.MySQL, target, user.Username, user.Role) {
			writeJSONError(w, http.StatusForbidden, "Forbidden: you do not own this account")
			return
		}
		next(w, withAPIUser(withAPIUsername(r, user.Username), user))
	}
}

// apiFeatureEnabled reports whether the JSON REST API is actually
// reachable right now: a license key must be configured (any non-empty
// string -- not necessarily a validated Enterprise license) AND [PANEL]
// api must be "on". If either is false, every /api/* route -- including
// /api/ itself -- behaves as if it were never registered at all. Both
// checks are read fresh (not through config.Openpanel()'s process-lifetime
// cache), same as apiEnabled() above, so toggling either takes effect on
// the next request rather than requiring a restart.
func apiFeatureEnabled() bool {
	licenseKey := config.Load(config.OpenpanelConfigPath).Get("LICENSE", "key", "")
	return licenseKey != "" && apiEnabled()
}

// RequireAPIFeatureEnabled wraps a handler so it returns the same
// "API access is disabled" JSON body used by the 404 fallback whenever the
// API isn't currently reachable, instead of ever running the real handler.
func RequireAPIFeatureEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiFeatureEnabled() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "API access is disabled! To enable api access OpenAdmin > Settings",
			})
			return
		}
		next(w, r)
	}
}
