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
}

type resellerData struct {
	MaxAccounts       int   `json:"max_accounts"`
	CurrentAccounts   int   `json:"current_accounts"`
	AllowedPlans      []int `json:"allowed_plans"`
	CurrentDiskBlocks int   `json:"current_disk_blocks"`
	MaxDiskBlocks     int   `json:"max_disk_blocks"`
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
		action = "reset_password"
		username = currentUser.Username
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
		// restricted by license tier.
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
		})
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, rows)
		return
	}

	webtemplates.Render(w, "users_resellers.html", mergeChrome(map[string]interface{}{
		"Users":   rows,
		"Flashes": auth.PopFlashes(w, r, rs.Sessions),
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

	webtemplates.Render(w, "users_password_reseller.html", mergeChrome(map[string]interface{}{
		"Username":    "",
		"SelfService": true,
		"Flashes":     auth.PopFlashes(w, r, rs.Sessions),
	}, r, "Change Password"))
}
