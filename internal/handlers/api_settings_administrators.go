// This file implements the JSON REST API's administrator-account
// management endpoint: listing administrator accounts and running the
// same create/suspend/unsuspend/delete/rename/reset-password/disable-2fa/
// disable-passkeys actions the HTML /administrators page exposes, reusing
// its underlying admindb/opencli plumbing directly (administrators.go).
package handlers

import (
	"net/http"
	"strings"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/license"
)

// APISettingsAdministrators bundles the /api/settings/administrators
// handler.
type APISettingsAdministrators struct {
	DB             *admindb.DB
	LicenseChecker *license.Checker // nil on Community
}

var apiAdminActions = map[string]bool{
	"create": true, "reset_password": true, "rename_user": true, "suspend": true,
	"unsuspend": true, "delete": true, "disable_2fa": true, "disable_passkeys": true,
}

type apiAdminRow struct {
	Username        string `json:"username"`
	IsActive        bool   `json:"is_active"`
	Role            string `json:"role"`
	LastIP          string `json:"last_ip"`
	LastLogin       string `json:"last_login"`
	TOTPEnabled     bool   `json:"totp_enabled"`
	PasskeysEnabled bool   `json:"passkeys_enabled"`
}

type apiAdminActionBody struct {
	Action      string `json:"action"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	NewPassword string `json:"new_password"`
	NewUsername string `json:"new_username"`
}

// opencliResultMessage picks the message shown for an opencli-backed
// action: the command's own first output line when it produced one,
// otherwise a generic "OK"/"Failed" fallback. A command that exits
// successfully with no stdout at all is treated as "OK" rather than
// surfaced as an error -- most admin commands do print a confirmation
// line, so this only matters for the rare command that doesn't.
func opencliResultMessage(ok bool, message string) string {
	if message != "" {
		return message
	}
	if ok {
		return "OK"
	}
	return "Failed"
}

// ServeSettingsAdministrators handles GET/POST /api/settings/administrators.
// Wrap with (*APIAuth).RequireAPIAdmin.
func (a *APISettingsAdministrators) ServeSettingsAdministrators(w http.ResponseWriter, r *http.Request) {
	actingUser := APIUserFromContext(r)

	if r.Method == http.MethodPost {
		a.handlePost(w, r, actingUser)
		return
	}

	users, err := a.DB.AllUsers()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lastLogin := parseLoginLog()

	rows := []apiAdminRow{}
	for _, u := range users {
		if strings.EqualFold(u.Role, "reseller") {
			continue
		}
		info := lastLogin[u.Username]
		credCount, _ := a.DB.CredentialCountByUserID(u.ID)
		rows = append(rows, apiAdminRow{
			Username:        u.Username,
			IsActive:        u.IsActive,
			Role:            u.Role,
			LastIP:          orNA(info.ip),
			LastLogin:       orNA(info.login),
			TOTPEnabled:     u.TOTPEnabled,
			PasskeysEnabled: credCount > 0,
		})
	}
	writeJSON(w, rows)
}

func (a *APISettingsAdministrators) handlePost(w http.ResponseWriter, r *http.Request, actingUser *admindb.User) {
	var body apiAdminActionBody
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	if body.Username != "" && !adminUsernameRe.MatchString(body.Username) {
		writeJSONError(w, http.StatusBadRequest, "Username can only contain letters, numbers, and underscores.")
		return
	}
	if body.Password != "" && !adminPasswordRe.MatchString(body.Password) {
		writeJSONError(w, http.StatusBadRequest, "Password must be 6-30 characters and contain no spaces.")
		return
	}

	if !apiAdminActions[body.Action] || body.Username == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing required fields.")
		return
	}

	admins := &Administrators{DB: a.DB, LicenseChecker: a.LicenseChecker}

	var ok bool
	var message string
	switch body.Action {
	case "reset_password":
		ok = admins.updatePasswordForUser(body.Username, body.NewPassword, actingUser)
		if ok {
			message = "Password changed for admin user: " + body.Username
		} else {
			message = "Failed changing password for admin user: " + body.Username
		}

	case "disable_2fa":
		ok = admins.disable2FAForUser(body.Username, actingUser)
		if ok {
			message = "Two-factor authentication disabled for admin user: " + body.Username
		} else {
			message = "Failed disabling two-factor authentication. Only the Super Administrator can do this."
		}

	case "disable_passkeys":
		ok = admins.disablePasskeysForUser(body.Username, actingUser)
		if ok {
			message = "Passkeys disabled for admin user: " + body.Username
		} else {
			message = "Failed disabling passkeys. Only the Super Administrator can do this."
		}

	case "create":
		if admins.LicenseChecker == nil || !admins.LicenseChecker.Valid() {
			message = "Community edition supports only one Administrator account."
		} else if hash, err := auth.GeneratePasswordHash(body.Password); err != nil {
			message = "Failed creating a new admin user: " + body.Username
		} else if err := admins.DB.CreateUser(body.Username, hash, "user"); err != nil {
			message = "Failed creating a new admin user: " + body.Username
		} else {
			notifySentinel("admin_create", "Administrator created", "Administrator account '"+body.Username+"' has been created.")
			ok = true
			message = "Successfully created a new admin user: " + body.Username
		}

	case "rename_user":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "rename", body.Username, body.NewUsername)
		message = opencliResultMessage(ok, out)

	case "suspend":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "suspend", body.Username)
		message = opencliResultMessage(ok, out)

	case "unsuspend":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "unsuspend", body.Username)
		message = opencliResultMessage(ok, out)

	case "delete":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "delete", body.Username)
		message = opencliResultMessage(ok, out)
	}

	if ok {
		writeJSON(w, map[string]interface{}{"success": true, "message": message})
		return
	}
	writeJSONFailure(w, http.StatusBadRequest, message)
}
