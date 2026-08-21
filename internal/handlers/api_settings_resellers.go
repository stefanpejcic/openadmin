// This file implements the JSON REST API's reseller-account management
// endpoint. It's the JSON counterpart to resellers.go's HTML page and
// reuses the same admindb/opencli plumbing (including Administrators'
// role-gated password/2FA/passkey helpers) -- only the request/response
// shape differs.
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

// APISettingsResellers bundles the /api/settings/resellers handler.
type APISettingsResellers struct {
	DB   *admindb.DB
	Auth *APIAuth
}

var apiResellerActions = map[string]bool{
	"create": true, "reset_password": true, "rename_user": true, "update": true,
	"suspend": true, "unsuspend": true, "delete": true, "disable_2fa": true, "disable_passkeys": true,
	"update_branding": true,
}

type apiResellerActionBody struct {
	Action        string `json:"action"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	NewPassword   string `json:"new_password"`
	NewUsername   string `json:"new_username"`
	AllowedPlans  string `json:"allowed_plans"`
	MaxAccounts   string `json:"max_accounts"`
	MaxDiskBlocks string `json:"max_disk_blocks"`
	LogoURL       string `json:"logo_url"`
}

// ServeSettingsResellers handles GET/POST /api/settings/resellers. Wrap
// with (*APIAuth).RequireAPIToken -- this handler resolves the acting user
// itself (a reseller is allowed to call this endpoint to manage their own
// password, unlike most admin-only settings routes).
func (a *APISettingsResellers) ServeSettingsResellers(w http.ResponseWriter, r *http.Request) {
	actingUser, ok := a.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	if r.Method == http.MethodPost {
		a.handlePost(w, r, actingUser)
		return
	}

	if actingUser.Role == "reseller" {
		writeJSONError(w, http.StatusForbidden, "Resellers cannot list other resellers. Use GET /api/whoami and manage your own account.")
		return
	}

	users, err := a.DB.AllUsers()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lastLogin := parseLoginLog()

	rows := []resellerRow{}
	for _, u := range users {
		if !strings.EqualFold(u.Role, "reseller") {
			continue
		}
		info := lastLogin[u.Username]
		credCount, _ := a.DB.CredentialCountByUserID(u.ID)
		rd := readResellerData(u.Username)
		rows = append(rows, resellerRow{
			Username:          u.Username,
			IsActive:          u.IsActive,
			Role:              u.Role,
			LastIP:            orNA(info.ip),
			LastLogin:         orNA(info.login),
			TOTPEnabled:       u.TOTPEnabled,
			PasskeysEnabled:   credCount > 0,
			MaxAccounts:       rd.MaxAccounts,
			CurrentAccounts:   rd.CurrentAccounts,
			CurrentDiskBlocks: rd.CurrentDiskBlocks,
			MaxDiskBlocks:     rd.MaxDiskBlocks,
			AllowedPlans:      rd.AllowedPlans,
			LogoURL:           rd.LogoURL,
		})
	}
	writeJSON(w, rows)
}

func (a *APISettingsResellers) handlePost(w http.ResponseWriter, r *http.Request, actingUser *admindb.User) {
	var body apiResellerActionBody
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	action := body.Action
	username := body.Username

	// A reseller caller can only ever reset their own password or set
	// their own branding through this endpoint -- any other action/
	// username submitted is overridden before anything else runs.
	if actingUser.Role == "reseller" {
		if action == "update_branding" {
			username = actingUser.Username
		} else {
			action = "reset_password"
			username = actingUser.Username
		}
	}

	if !apiResellerActions[action] || username == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing required fields.")
		return
	}

	admins := &Administrators{DB: a.DB}

	var ok bool
	var message string
	switch action {
	case "reset_password":
		ok = admins.updatePasswordForUser(username, body.NewPassword, actingUser)
		if ok {
			message = "Password changed for reseller user: " + username
		} else {
			message = "Failed changing password for reseller user: " + username
		}

	case "disable_2fa":
		ok = admins.disable2FAForUser(username, actingUser)
		if ok {
			message = "Two-factor authentication disabled for reseller: " + username
		} else {
			message = "Failed disabling two-factor authentication. Only the Super Administrator can do this."
		}

	case "disable_passkeys":
		ok = admins.disablePasskeysForUser(username, actingUser)
		if ok {
			message = "Passkeys disabled for reseller: " + username
		} else {
			message = "Failed disabling passkeys. Only the Super Administrator can do this."
		}

	case "create":
		// Unlike administrators' "create" action, reseller account
		// creation isn't gated by license tier.
		if hash, err := auth.GeneratePasswordHash(body.Password); err != nil {
			message = "Failed creating a new reseller user: " + username
		} else if err := a.DB.CreateUser(username, hash, "reseller"); err != nil {
			message = "Failed creating a new reseller user: " + username
		} else {
			ok = true
			message = "Successfully created a new reseller user: " + username
		}

	case "rename_user":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "rename", username, body.NewUsername)
		message = opencliResultMessage(ok, out)

	case "update":
		args := []string{"opencli", "admin", "update", username}
		if body.AllowedPlans != "" {
			args = append(args, "--allowed_plans="+body.AllowedPlans)
		}
		if body.MaxAccounts != "" {
			args = append(args, "--max_accounts="+body.MaxAccounts)
		}
		if body.MaxDiskBlocks != "" {
			args = append(args, "--max_disk_blocks="+body.MaxDiskBlocks)
		}
		var out string
		ok, out = runOpenCLI(adminCommandError, args...)
		message = opencliResultMessage(ok, out)

	case "update_branding":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "update", username, "--logo_url="+body.LogoURL)
		if ok {
			message = "Branding updated for " + username + "."
		} else {
			message = opencliResultMessage(ok, out)
		}

	case "suspend":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "suspend", username)
		message = opencliResultMessage(ok, out)

	case "unsuspend":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "unsuspend", username)
		message = opencliResultMessage(ok, out)

	case "delete":
		var out string
		ok, out = runOpenCLI(adminCommandError, "opencli", "admin", "delete", username)
		message = opencliResultMessage(ok, out)
	}

	if ok {
		writeJSON(w, map[string]interface{}{"success": true, "message": message})
		return
	}
	writeJSONFailure(w, http.StatusBadRequest, message)
}

// ServeSettingsResellersEnabled handles GET/POST
// /api/settings/resellers/enabled: the master on/off switch gating whether
// reseller accounts can be created at all. Turning it off is refused
// while any reseller account still exists -- they have to be deleted
// first. JSON counterpart to resellers.go's handleToggleResellers.
func (a *APISettingsResellers) ServeSettingsResellersEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, map[string]interface{}{"enabled": resellersEnabled()})
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	if !body.Enabled {
		count, err := countResellers(a.DB)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if count > 0 {
			writeJSONError(w, http.StatusConflict, "Cannot disable resellers while "+strconv.Itoa(count)+" reseller account(s) still exist. Delete them first.")
			return
		}
	}

	data := config.Load(config.AdminConfigPath)
	value := "no"
	if body.Enabled {
		value = "yes"
	}
	data.Set("RESELLERS", "enabled", value)
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to save setting: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "enabled": body.Enabled})
}
