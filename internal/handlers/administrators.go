package handlers

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/license"
	"openadmin/internal/webtemplates"
)

const adminCommandError = "Error occurred running opencli admin command - consult documentation: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openadmin"

var (
	adminUsernameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	adminPasswordRe = regexp.MustCompile(`^\S{6,30}$`)
)

// Administrators bundles the /administrators handlers.
type Administrators struct {
	DB             *admindb.DB
	Sessions       *auth.Manager
	LicenseChecker *license.Checker // nil on Community
}

type administratorRow struct {
	Username        string
	IsActive        bool
	Role            string
	LastIP          string
	LastLogin       string
	TOTPEnabled     bool
	PasskeysEnabled bool
}

type administratorsPageData struct {
	webtemplates.Chrome
	Users     []administratorRow
	Admin     bool // current user's role == "admin" (Super Administrator)
	Self      string
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeAdministrators handles GET/POST /administrators.
func (a *Administrators) ServeAdministrators(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)

	if r.Method == http.MethodPost {
		a.handlePost(w, r, currentUser)
		return
	}

	a.render(w, r, currentUser)
}

func (a *Administrators) render(w http.ResponseWriter, r *http.Request, currentUser *admindb.User) {
	users, err := a.DB.AllUsers()
	if err != nil {
		http.Error(w, "Error loading administrators: "+err.Error(), http.StatusInternalServerError)
		return
	}

	lastLogin := parseLoginLog()

	var rows []administratorRow
	for _, u := range users {
		if strings.EqualFold(u.Role, "reseller") {
			continue
		}
		info := lastLogin[u.Username]
		credCount, _ := a.DB.CredentialCountByUserID(u.ID)
		rows = append(rows, administratorRow{
			Username:        u.Username,
			IsActive:        u.IsActive,
			Role:            u.Role,
			LastIP:          orNA(info.ip),
			LastLogin:       orNA(info.login),
			TOTPEnabled:     u.TOTPEnabled,
			PasskeysEnabled: credCount > 0,
		})
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, rows)
		return
	}

	webtemplates.Render(w, "administrators.html", administratorsPageData{
		Chrome:    buildChrome(r, "Administrators"),
		Users:     rows,
		Admin:     currentUser.Role == "admin",
		Self:      currentUser.Username,
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, a.Sessions),
	})
}

func (a *Administrators) handlePost(w http.ResponseWriter, r *http.Request, currentUser *admindb.User) {
	action := r.FormValue("action")
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username != "" && !adminUsernameRe.MatchString(username) {
		auth.AddFlash(w, r, a.Sessions, "Error: Username can only contain letters, numbers, and underscores.", "error")
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
		return
	}
	if password != "" && !adminPasswordRe.MatchString(password) {
		auth.AddFlash(w, r, a.Sessions, "Error: Password must be 6-30 characters and contain no spaces.", "error")
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
		return
	}

	if action == "" || username == "" {
		auth.AddFlash(w, r, a.Sessions, "Error: Missing required fields.", "error")
		http.Redirect(w, r, "/administrators", http.StatusSeeOther)
		return
	}

	success, message := a.runAction(action, username, password, r, currentUser)

	if success {
		auth.AddFlash(w, r, a.Sessions, "Success: "+message, "success")
	} else {
		auth.AddFlash(w, r, a.Sessions, "Error: "+message, "error")
	}
	http.Redirect(w, r, "/administrators", http.StatusSeeOther)
}

func (a *Administrators) runAction(action, username, password string, r *http.Request, currentUser *admindb.User) (success bool, message string) {
	switch action {
	case "reset_password":
		newPassword := r.FormValue("new_password")
		if a.updatePasswordForUser(username, newPassword, currentUser) {
			return true, "Password changed for admin user: " + username
		}
		return false, "Failed changing password for admin user: " + username

	case "disable_2fa":
		if a.disable2FAForUser(username, currentUser) {
			return true, "Two-factor authentication disabled for admin user: " + username
		}
		return false, "Failed disabling two-factor authentication for admin user: " + username + ". Only the Super Administrator can do this."

	case "disable_passkeys":
		if a.disablePasskeysForUser(username, currentUser) {
			return true, "Passkeys disabled for admin user: " + username
		}
		return false, "Failed disabling passkeys for admin user: " + username + ". Only the Super Administrator can do this."

	case "create":
		if a.LicenseChecker == nil || !a.LicenseChecker.Valid() {
			return false, "Community edition supports only one Administrator account."
		}
		hash, err := auth.GeneratePasswordHash(password)
		if err != nil {
			return false, "Failed creating a new admin user: " + username
		}
		if err := a.DB.CreateUser(username, hash, "user"); err != nil {
			return false, "Failed creating a new admin user: " + username
		}
		notifySentinel("admin_create", "Administrator created", "Administrator account '"+username+"' has been created.")
		return true, "Successfully created a new admin user: " + username

	case "rename_user":
		return runOpenCLI(adminCommandError, "opencli", "admin", "rename", username, r.FormValue("new_username"))

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

// updatePasswordForUser enforces role-based permission checks: an admin may
// reset anyone's password; a plain "user" may not touch the Super
// Administrator's password; a reseller may not touch any OpenAdmin account
// (this handler is already gated to admins, so resellers never reach here
// in practice, but the check is kept anyway as defense in depth).
func (a *Administrators) updatePasswordForUser(username, newPassword string, currentUser *admindb.User) bool {
	target, err := a.DB.UserByUsername(username)
	if err != nil {
		return false
	}

	switch currentUser.Role {
	case "admin":
		// allowed
	case "user":
		if target.Role == "admin" {
			return false
		}
	case "reseller":
		// A reseller may change only their own password here -- rejecting
		// role=='reseller' unconditionally would mean a reseller's
		// self-service password change on /account could never succeed,
		// no matter what.
		if username != currentUser.Username {
			return false
		}
	default:
		return false
	}

	hash, err := auth.GeneratePasswordHash(newPassword)
	if err != nil {
		return false
	}
	if err := a.DB.UpdatePasswordHash(username, hash); err != nil {
		return false
	}
	notifySentinel("admin_password", "Administrator password changed", "Administrator account '"+username+"' has password changed.")
	return true
}

func (a *Administrators) disable2FAForUser(username string, currentUser *admindb.User) bool {
	if currentUser.Role != "admin" || username == currentUser.Username {
		return false
	}
	if _, err := a.DB.UserByUsername(username); err != nil {
		return false
	}
	return a.DB.SetTOTP(username, "", false) == nil
}

func (a *Administrators) disablePasskeysForUser(username string, currentUser *admindb.User) bool {
	if currentUser.Role != "admin" || username == currentUser.Username {
		return false
	}
	target, err := a.DB.UserByUsername(username)
	if err != nil {
		return false
	}
	return a.DB.DeleteCredentialsByUserID(target.ID) == nil
}

// --- edit forms (rename / password) ---

type editAdminPageData struct {
	webtemplates.Chrome
	Action    string
	Username  string
	IsActive  bool
	Role      string
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeEditForm handles GET /administrators/{action}/{username}.
func (a *Administrators) ServeEditForm(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)

	if action != "rename" && action != "password" {
		auth.AddFlash(w, r, a.Sessions, "Error: Administrator accounts can only be renamed or password changed from this page. For suspending use the table.", "error")
		http.Redirect(w, r, "/administrators", http.StatusSeeOther)
		return
	}

	target, err := a.DB.UserByUsername(username)
	if err != nil {
		auth.AddFlash(w, r, a.Sessions, "Error: Administrator "+username+" does not exist!", "error")
		http.Redirect(w, r, "/administrators", http.StatusSeeOther)
		return
	}

	if target.Role != "user" && currentUser.Role != "admin" {
		auth.AddFlash(w, r, a.Sessions, "Error: Super Administrator and Reseller users can not be edited!", "error")
		http.Redirect(w, r, "/administrators", http.StatusSeeOther)
		return
	}

	title := "Change Password for " + target.Username
	if action == "rename" {
		title = "Rename Administrator " + target.Username
	}
	webtemplates.Render(w, "edit_admin.html", editAdminPageData{
		Chrome:    buildChrome(r, title),
		Action:    action,
		Username:  target.Username,
		IsActive:  target.IsActive,
		Role:      target.Role,
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, a.Sessions),
	})
}

// --- shared helpers ---

func runOpenCLI(fallbackError string, args ...string) (bool, string) {
	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fallbackError
		}
		return false, firstLine(msg)
	}
	return true, firstLine(strings.TrimSpace(stdout.String()))
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		return s[:idx]
	}
	return s
}

func notifySentinel(action, title, message string) {
	cmd := exec.Command("opencli", "sentinel", "--action="+action, "--title="+title, "--message="+message)
	cmd.Start() // fire-and-forget: we don't wait for or care about the child process's exit
}

type loginInfo struct{ ip, login string }

func parseLoginLog() map[string]loginInfo {
	out := map[string]loginInfo{}
	f, err := os.Open(LoginLogPath)
	if err != nil {
		return out
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 4 {
			out[parts[2]] = loginInfo{ip: parts[3], login: parts[0] + " " + parts[1]}
		}
	}
	return out
}

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}
