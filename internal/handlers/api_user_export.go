// This file implements the JSON REST API's user-export endpoints: the
// JSON counterpart to user_export.go's HTML Export tab (the "Generate
// full account backup" option) on /users/{username}. Reuses the exact
// same plumbing -- only the request/response shape differs.
package handlers

import (
	"net/http"

	"openadmin/internal/auth"
)

// APIUserExport bundles the /api/users/{username}/export/* handlers.
type APIUserExport struct {
	Users *Users
}

// withActingUser stashes the request's already-resolved acting API user
// (see api_common.go's APIUserFromContext, set by RequireAPIOwnerOrAdmin)
// onto r in the shape auth.CurrentUser expects -- user_export.go's
// handlers call auth.CurrentUser(r) internally, which only ever sees a
// session-cookie-loaded user, never a JWT bearer token. Without this, that
// call returns nil and the handler panics on currentUser.Username.
func withActingUser(r *http.Request) *http.Request {
	return auth.WithCurrentUser(r, APIUserFromContext(r))
}

// ServeStatus handles GET /api/users/{username}/export/status.
func (a *APIUserExport) ServeStatus(w http.ResponseWriter, r *http.Request) {
	a.Users.ServeUserExportStatus(w, withActingUser(r))
}

// ServeCreate handles POST /api/users/{username}/export/create: fires
// `opencli user-backup --account <username>` in the background; poll
// ServeStatus for progress and the resulting archive.
func (a *APIUserExport) ServeCreate(w http.ResponseWriter, r *http.Request) {
	a.Users.ServeUserExportCreate(w, withActingUser(r))
}

// ServeDownload handles GET
// /api/users/{username}/export/download/{filename...}.
func (a *APIUserExport) ServeDownload(w http.ResponseWriter, r *http.Request) {
	a.Users.ServeUserExportDownload(w, withActingUser(r))
}

// ServeDelete handles POST /api/users/{username}/export/delete (JSON body
// {"filename": "..."}).
func (a *APIUserExport) ServeDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filename string `json:"filename"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	// user_export.go's ServeUserExportDelete reads "filename" from a POST
	// form value; pre-populating both r.Form and r.PostForm makes its
	// r.ParseForm() call a no-op, so this value survives it intact --
	// same technique as api_server_swap.go's ServeSwapAction.
	values := map[string][]string{"filename": {body.Filename}}
	r.Form = values
	r.PostForm = values

	a.Users.ServeUserExportDelete(w, withActingUser(r))
}
