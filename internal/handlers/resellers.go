// This file implements reseller account management (mostly a
// reseller-scoped counterpart to administrators.go, reusing several of
// its helpers directly) plus the reseller's own self-service password
// page.
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Resellers bundles the /resellers*, /account handlers.
type Resellers struct {
	DB       *admindb.DB
	MySQL    *sql.DB
	Sessions *auth.Manager
}

var resellerValidActions = map[string]bool{
	"create": true, "reset_password": true, "rename_user": true, "update": true,
	"suspend": true, "unsuspend": true, "delete": true, "disable_2fa": true, "disable_passkeys": true,
	"update_branding": true,
}

// resellersEnabled reports whether reseller functionality is turned on
// (admin.ini's [RESELLERS] enabled=yes). Off by default -- an admin has to
// explicitly turn it on (from this page) before any reseller account can
// be created; see handleToggleResellers for the reverse direction, which
// refuses to turn it back off while any reseller account still exists.
func resellersEnabled() bool {
	return config.Load(config.AdminConfigPath).Get("RESELLERS", "enabled", "no") == "yes"
}

// countResellers returns how many admindb accounts currently have the
// "reseller" role.
func countResellers(db *admindb.DB) (int, error) {
	users, err := db.AllUsers()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, u := range users {
		if strings.EqualFold(u.Role, "reseller") {
			count++
		}
	}
	return count, nil
}

type resellerRow struct {
	Username          string `json:"username"`
	IsActive          bool   `json:"is_active"`
	Role              string `json:"role"`
	LastIP            string `json:"last_ip"`
	LastLogin         string `json:"last_login"`
	TOTPEnabled       bool   `json:"totp_enabled"`
	PasskeysEnabled   bool   `json:"passkeys_enabled"`
	MaxAccounts       int    `json:"max_accounts"`
	CurrentAccounts   int    `json:"current_accounts"`
	CurrentDiskBlocks int    `json:"current_disk_blocks"`
	MaxDiskBlocks     int    `json:"max_disk_blocks"`
	AllowedPlans      []int  `json:"allowed_plans"`
	LogoURL           string `json:"logo_url"`
}

type resellerData struct {
	MaxAccounts       int    `json:"max_accounts"`
	CurrentAccounts   int    `json:"current_accounts"`
	AllowedPlans      []int  `json:"allowed_plans"`
	CurrentDiskBlocks int    `json:"current_disk_blocks"`
	MaxDiskBlocks     int    `json:"max_disk_blocks"`
	LogoURL           string `json:"logo_url"`
}

func defaultResellerData() resellerData {
	return resellerData{AllowedPlans: []int{}}
}

// readResellerData reads a reseller's JSON data file, falling back to
// defaults if it's missing or invalid.
func readResellerData(username string) resellerData {
	raw, err := os.ReadFile(filepath.Join(paneldb.ResellerConfigDir, username+".json"))
	if err != nil {
		return defaultResellerData()
	}
	var parsed resellerData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return defaultResellerData()
	}
	if parsed.AllowedPlans == nil {
		parsed.AllowedPlans = []int{}
	}
	return parsed
}

// ServeResellers handles GET/POST /resellers.
func (rs *Resellers) ServeResellers(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)

	if r.Method == http.MethodPost {
		rs.handlePost(w, r, currentUser)
	}

	// Unlike administrators.go (which always redirects after a POST),
	// this handler does NOT redirect after processing a POST for a
	// non-reseller actor -- it just falls through and re-renders the
	// same page in this same request/response, flash included. A
	// reseller actor (self-service password reset) DOES get redirected
	// to /account, but that redirect is unconditional on role, not on
	// the HTTP method.
	if currentUser.Role == "reseller" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}

	rs.render(w, r)
}

func (rs *Resellers) handlePost(w http.ResponseWriter, r *http.Request, currentUser *admindb.User) {
	r.ParseForm()
	action := r.PostFormValue("action")
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	if currentUser.Role == "reseller" {
		if action == "update_branding" {
			// The one other self-service action a reseller can take
			// besides resetting their own password: setting their own
			// logo URL. Still forced to their own username, same as the
			// reset_password override below.
			username = currentUser.Username
		} else {
			action = "reset_password"
			username = currentUser.Username
		}
	}

	// The master on/off toggle has no username -- handled separately,
	// before the "username required" check below applies to everything
	// else.
	if action == "enable_resellers" || action == "disable_resellers" {
		rs.handleToggleResellers(w, r, action)
		return
	}

	if !resellerValidActions[action] || username == "" {
		auth.AddFlash(w, r, rs.Sessions, "Error: Missing required fields.", "error")
		return
	}

	success, message := rs.runAction(action, username, password, r, currentUser)
	if success {
		// Note: unlike administrators.go (which prefixes both outcomes
		// with "Success: "/"Error: "), only the failure case is prefixed
		// here -- the success message is flashed as-is.
		auth.AddFlash(w, r, rs.Sessions, message, "success")
	} else {
		auth.AddFlash(w, r, rs.Sessions, "Error: "+message, "error")
	}
}

// handleToggleResellers handles the "enable_resellers"/"disable_resellers"
// actions: the master switch gating whether reseller accounts can be
// created at all. Turning it off is refused while any reseller account
// still exists -- they have to be deleted first.
func (rs *Resellers) handleToggleResellers(w http.ResponseWriter, r *http.Request, action string) {
	if action == "disable_resellers" {
		count, err := countResellers(rs.DB)
		if err != nil {
			auth.AddFlash(w, r, rs.Sessions, "Error: "+err.Error(), "error")
			return
		}
		if count > 0 {
			auth.AddFlash(w, r, rs.Sessions, fmt.Sprintf("Error: Cannot disable resellers while %d reseller account(s) still exist. Delete them first.", count), "error")
			return
		}
	}

	data := config.Load(config.AdminConfigPath)
	value := "no"
	message := "Resellers disabled."
	if action == "enable_resellers" {
		value = "yes"
		message = "Resellers enabled. You can now create reseller accounts."
	}
	data.Set("RESELLERS", "enabled", value)
	if err := config.Save(config.AdminConfigPath, data); err != nil {
		auth.AddFlash(w, r, rs.Sessions, "Error: Failed to save setting: "+err.Error(), "error")
		return
	}
	auth.AddFlash(w, r, rs.Sessions, message, "success")
}

func (rs *Resellers) runAction(action, username, password string, r *http.Request, currentUser *admindb.User) (success bool, message string) {
	// Reuses Administrators' role-gated helpers directly rather than
	// redefining them.
	admins := &Administrators{DB: rs.DB}

	switch action {
	case "reset_password":
		newPassword := r.FormValue("new_password")
		if admins.updatePasswordForUser(username, newPassword, currentUser) {
			return true, "Password changed for reseller user: " + username
		}
		return false, "Failed changing password for reseller user: " + username

	case "disable_2fa":
		if admins.disable2FAForUser(username, currentUser) {
			return true, "Two-factor authentication disabled for reseller: " + username
		}
		return false, "Failed disabling two-factor authentication for reseller: " + username + ". Only the Super Administrator can do this."

	case "disable_passkeys":
		if admins.disablePasskeysForUser(username, currentUser) {
			return true, "Passkeys disabled for reseller: " + username
		}
		return false, "Failed disabling passkeys for reseller: " + username + ". Only the Super Administrator can do this."

	case "create":
		// Unlike administrators.go's own "create" case, there's no
		// Enterprise-license gate here -- reseller account creation isn't
		// restricted by license tier. There IS the master on/off switch
		// though -- checked here too (not just hiding the form) since a
		// direct POST could otherwise bypass it.
		if !resellersEnabled() {
			return false, "Resellers are disabled. Enable them on this page first."
		}
		hash, err := auth.GeneratePasswordHash(password)
		if err != nil {
			return false, "Failed creating a new reseller user: " + username
		}
		if err := rs.DB.CreateUser(username, hash, "reseller"); err != nil {
			return false, "Failed creating a new reseller user: " + username
		}
		return true, "Successfully created a new reseller user: " + username

	case "rename_user":
		return runOpenCLI(adminCommandError, "opencli", "admin", "rename", username, r.FormValue("new_username"))

	case "update":
		args := []string{"opencli", "admin", "update", username}
		if v := r.FormValue("allowed_plans"); v != "" {
			args = append(args, "--allowed_plans="+v)
		}
		if v := r.FormValue("max_accounts"); v != "" {
			args = append(args, "--max_accounts="+v)
		}
		if v := r.FormValue("max_disk_blocks"); v != "" {
			args = append(args, "--max_disk_blocks="+v)
		}
		return runOpenCLI(adminCommandError, args...)

	case "update_branding":
		// Reseller self-service (or admin, from the Resellers list page):
		// sets only the logo URL, leaving plans/limits untouched -- see
		// update_reseller_account in opencli's admin.sh, which only
		// touches fields whose flag is actually passed.
		logoURL := r.FormValue("logo_url")
		ok, out := runOpenCLI(adminCommandError, "opencli", "admin", "update", username, "--logo_url="+logoURL)
		if !ok {
			return false, opencliResultMessage(ok, out)
		}
		return true, "Branding updated for " + username + "."

	case "suspend":
		return runOpenCLI(adminCommandError, "opencli", "admin", "suspend", username)
	case "unsuspend":
		return runOpenCLI(adminCommandError, "opencli", "admin", "unsuspend", username)
	case "delete":
		return runOpenCLI(adminCommandError, "opencli", "admin", "delete", username)

	default:
		return false, "Unknown action."
	}
}

func (rs *Resellers) render(w http.ResponseWriter, r *http.Request) {
	users, err := rs.DB.AllUsers()
	if err != nil {
		http.Error(w, "Error loading resellers: "+err.Error(), http.StatusInternalServerError)
		return
	}

	lastLogin := parseLoginLog()

	var rows []resellerRow
	for _, u := range users {
		// Case-insensitive here -- a looser check than ServeEditForm's
		// exact-match one below. This inconsistency between the two
		// checks is left as-is rather than unified.
		if !strings.EqualFold(u.Role, "reseller") {
			continue
		}
		info := lastLogin[u.Username]
		credCount, _ := rs.DB.CredentialCountByUserID(u.ID)
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

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, rows)
		return
	}

	webtemplates.Render(w, "users_resellers.html", mergeChrome(map[string]interface{}{
		"Users":            rows,
		"ResellersEnabled": resellersEnabled(),
		"ResellerCount":    len(rows),
		"Flashes":          auth.PopFlashes(w, r, rs.Sessions),
	}, r, "Resellers"))
}

// ServeEditForm handles GET /resellers/{action}/{username}.
func (rs *Resellers) ServeEditForm(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	username := r.PathValue("username")

	if action != "rename" && action != "update" && action != "password" {
		auth.AddFlash(w, r, rs.Sessions, "Error: invalid action, only 'rename', 'update' and 'password' are permitted.", "error")
		http.Redirect(w, r, "/resellers", http.StatusSeeOther)
		return
	}

	user, err := rs.DB.UserByUsername(username)
	// Exact-match here (not EqualFold) -- distinct from render()'s looser
	// case-insensitive filter above.
	if err != nil || user.Role != "reseller" {
		message := "Error: Administrator users cannot be edited!"
		if err != nil {
			message = fmt.Sprintf("Error: Reseller %s does not exist!", username)
		}
		auth.AddFlash(w, r, rs.Sessions, message, "error")
		http.Redirect(w, r, "/resellers", http.StatusSeeOther)
		return
	}

	switch action {
	case "rename":
		webtemplates.Render(w, "users_rename_reseller.html", mergeChrome(map[string]interface{}{
			"Username": user.Username,
			"Flashes":  auth.PopFlashes(w, r, rs.Sessions),
		}, r, "Rename Reseller: "+user.Username))
	case "password":
		webtemplates.Render(w, "users_password_reseller.html", mergeChrome(map[string]interface{}{
			"Username":    user.Username,
			"SelfService": false,
			"Flashes":     auth.PopFlashes(w, r, rs.Sessions),
		}, r, "Change Password for Reseller: "+user.Username))
	case "update":
		rd := readResellerData(username)
		var plans []paneldb.RowMap
		if rs.MySQL != nil {
			plans, _ = paneldb.GetAllPlans(rs.MySQL, nil)
		}
		webtemplates.Render(w, "users_update_reseller.html", mergeChrome(map[string]interface{}{
			"Username":     user.Username,
			"ResellerData": rd,
			"Plans":        plans,
			"Flashes":      auth.PopFlashes(w, r, rs.Sessions),
		}, r, "Edit Reseller: "+user.Username))
	}
}

// ServeAccount handles GET /account.
func (rs *Resellers) ServeAccount(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	if currentUser.Role != "reseller" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	rd := readResellerData(currentUser.Username)
	webtemplates.Render(w, "users_password_reseller.html", mergeChrome(map[string]interface{}{
		"Username":     "",
		"SelfService":  true,
		"ResellerData": rd,
		"Flashes":      auth.PopFlashes(w, r, rs.Sessions),
	}, r, "Change Password"))
}
