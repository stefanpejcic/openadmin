// This file implements the global 404/403/CSRF/unhandled-panic error
// handlers and the API/error log setup.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// ErrorLogDirs lists the log directories to ensure exist. ErrorLogPath
// itself (and the appendLogLine helper used to write to it) already exist
// in login.go, reused here rather than redeclared.
var ErrorLogDirs = []string{"/var/log/openpanel/admin", "/var/log/openpanel/user"}

// EnsureErrorLogDirs creates ErrorLogDirs if they don't already exist.
// Safe to call multiple times.
func EnsureErrorLogDirs() {
	for _, dir := range ErrorLogDirs {
		os.MkdirAll(dir, 0755)
	}
}

// logError appends to ErrorLogPath (login.go) with no size/backup-count
// based log rotation -- Go's stdlib has no built-in log rotation, and
// nothing else here depends on the file staying under a particular size,
// so this is a deliberate simplification.
func logError(format string, args ...interface{}) {
	appendLogLine(ErrorLogPath, fmt.Sprintf("%s - "+format, append([]interface{}{time.Now()}, args...)...))
}

// apiEnabled checks whether [PANEL] api is "on" in openpanel.config. Reads
// fresh (not through config.Openpanel()'s process-lifetime cache),
// consistent with every other config read in this file.
func apiEnabled() bool {
	return strings.EqualFold(config.Load(config.OpenpanelConfigPath).Get("PANEL", "api", "off"), "on")
}

func renderErrorPage(w http.ResponseWriter, r *http.Request, errorMessage string, errorCode int) {
	w.WriteHeader(errorCode)
	webtemplates.Render(w, "system_error.html", mergeChrome(map[string]interface{}{
		"ErrorMessage": errorMessage,
		"ErrorCode":    errorCode,
	}, r, "Error"))
}

// NotFoundHandler wraps a ServeMux so unmatched routes get this instead of
// Go's plain-text default, with /api/ requests branching to a JSON error
// body and everything else to an HTML page, based on whether the API is
// enabled in openpanel.config.
func NotFoundHandler(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			if apiEnabled() {
				json.NewEncoder(w).Encode(map[string]string{
					"error": "This api route does not exist. Please check the documentation: https://openpanel.com/docs/articles/dev-experience/openadmin-api",
				})
			} else {
				json.NewEncoder(w).Encode(map[string]string{
					"error": "API access is disabled! To enable api access OpenAdmin > Settings",
				})
			}
			return
		}

		renderErrorPage(w, r, "Page not found", http.StatusNotFound)
	})
}

// RecoverMiddleware recovers from a panic in a handler, logs it, and
// renders the HTML error page rather than a JSON body -- the JSON body is
// reserved for a deliberate error response, not a genuine crash.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logError("Unhandled exception: %v\n%s", rec, debug.Stack())
				message := fmt.Sprintf("%v", rec)
				if message == "" {
					message = "An unexpected error occurred"
				}
				renderErrorPage(w, r, message, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CSRFErrorHandler returns a JSON 400 body instead of gorilla/csrf's
// default plain-text 403 response.
func CSRFErrorHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "CSRF error",
			"message": "CSRF token missing or incorrect.",
		})
	})
}
