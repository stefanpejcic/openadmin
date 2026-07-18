// This file implements the Edit tab's multi-field account form: username,
// email, password, dedicated IP, plan, and reseller/owner changes, each
// mapped to its own opencli command (or, for reseller, a direct owner-column
// update -- see updateUserReseller) so a failure partway through reports
// exactly which field failed rather than rejecting the whole submission.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

// opencliChangeSucceeded treats an opencli invocation as successful only
// when it both exits zero and its (first line of) output mentions
// "success" -- every user-* mutation script used below prints some form of
// "successfully"/"Success:" on its happy path, so this catches scripts that
// exit 0 but bailed out early with an error message too.
func opencliChangeSucceeded(success bool, output string) bool {
	return success && strings.Contains(strings.ToLower(output), "success")
}

// handleEditUser handles the "edit" action of HandleManage: the Edit tab's
// username/email/password/IP/plan/reseller form. Fields are applied in an
// order that matches the underlying opencli scripts' assumptions -- IP,
// reseller, email, password, and plan changes all address the account by
// its *current* username, so the rename (which changes that address) is
// applied last.
func (u *Users) handleEditUser(w http.ResponseWriter, r *http.Request, username string, currentUser *admindb.User) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	userData, err := paneldb.GetUserDataByUsername(u.MySQL, username)
	if err != nil {
		auth.AddFlash(w, r, u.Sessions, "Error: User "+username+" not found", "error")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}

	newUsername := strings.TrimSpace(r.FormValue("new_username"))
	newEmail := strings.TrimSpace(r.FormValue("new_email"))
	newPlanID := r.FormValue("plan_id")
	newPassword := r.FormValue("new_password")
	newIP := strings.TrimSpace(r.FormValue("new_ip"))
	newReseller := strings.TrimSpace(r.FormValue("reseller"))

	oldEmail := userData.Email
	oldIP := firstIPFor(username)
	oldReseller := userData.Owner.String

	oldPlanName := ""
	if row, err := paneldb.GetPlanByID(u.MySQL, strconv.FormatInt(userData.PlanID, 10)); err == nil {
		oldPlanName, _ = row["name"].(string)
	}
	newPlanName := oldPlanName
	if newPlanID != "" {
		if row, err := paneldb.GetPlanByID(u.MySQL, newPlanID); err == nil {
			if n, ok := row["name"].(string); ok && n != "" {
				newPlanName = n
			}
		}
	}

	fail := func(action, output string) {
		auth.AddFlash(w, r, u.Sessions, "Error "+action+" for user "+username+": "+output, "error")
		http.Redirect(w, r, "/users/"+username+"#edit", http.StatusSeeOther)
	}

	var changes []string

	if newIP != "" && newIP != oldIP {
		success, output := runOpenCLI("", "opencli", "user-ip", username, newIP, "-y")
		if !opencliChangeSucceeded(success, output) {
			fail("changing IP address", output)
			return
		}
		logUserAction(username, "Administrator "+currentUser.Username+" changed IP address to "+newIP+" for user "+username)
		changes = append(changes, "IP address changed to "+newIP)
	}

	if newReseller != oldReseller {
		if ok, errMsg := u.updateUserReseller(username, newReseller); !ok {
			auth.AddFlash(w, r, u.Sessions, "Error: "+errMsg, "error")
			http.Redirect(w, r, "/users/"+username+"#edit", http.StatusSeeOther)
			return
		}
		if newReseller == "" || newReseller == "root" {
			logUserAction(username, "Administrator "+currentUser.Username+" removed reseller for user "+username)
			changes = append(changes, "Reseller removed")
		} else {
			logUserAction(username, "Administrator "+currentUser.Username+" changed reseller to "+newReseller+" for user "+username)
			changes = append(changes, "Reseller changed to "+newReseller)
		}
	}

	if newEmail != "" && newEmail != oldEmail {
		success, output := runOpenCLI("", "opencli", "user-email", username, newEmail)
		if !opencliChangeSucceeded(success, output) {
			fail("changing email", output)
			return
		}
		logUserAction(username, "Administrator "+currentUser.Username+" changed the email address to "+newEmail+" for user "+username)
		changes = append(changes, "Email changed")
	}

	if newPassword != "" {
		success, output := runOpenCLI("", "opencli", "user-password", username, newPassword)
		if !opencliChangeSucceeded(success, output) {
			fail("changing password", output)
			return
		}
		logUserAction(username, "Administrator "+currentUser.Username+" changed password for user "+username)
		changes = append(changes, "Password changed")
	}

	if newPlanName != "" && newPlanName != oldPlanName {
		success, output := runOpenCLI("", "opencli", "user-change_plan", username, newPlanName)
		if !opencliChangeSucceeded(success, output) {
			fail("changing plan", output)
			return
		}
		logUserAction(username, "Administrator "+currentUser.Username+" changed plan from "+oldPlanName+" to "+newPlanName+" for user "+username)
		changes = append(changes, "Plan changed from "+oldPlanName+" to "+newPlanName)
	}

	if newUsername != "" && newUsername != username {
		success, output := runOpenCLI("", "opencli", "user-rename", username, newUsername)
		if !opencliChangeSucceeded(success, output) {
			fail("renaming user", output)
			return
		}
		logUserAction(username, "Administrator "+currentUser.Username+" changed username from "+username+" to "+newUsername)
		changes = append(changes, "Username changed from "+username+" to "+newUsername)
		username = newUsername
	}

	if len(changes) == 0 {
		auth.AddFlash(w, r, u.Sessions, "No data provided to change", "info")
	} else {
		auth.AddFlash(w, r, u.Sessions, "User '"+username+"' updated successfully: "+strings.Join(changes, ", "), "success")
	}
	http.Redirect(w, r, "/users/"+username+"#edit", http.StatusSeeOther)
}

// updateUserReseller reassigns username's owner directly in MySQL (there is
// no opencli command for this -- reseller ownership is an OpenAdmin-only
// concept, not something opencli's user scripts know about). newReseller
// "" or "root" clears ownership. Enforces the same reseller-must-exist and
// account-limit checks as the target reseller's plan-assignment limits
// file, and keeps that file's cached current_accounts in sync.
func (u *Users) updateUserReseller(username, newReseller string) (ok bool, errMsg string) {
	hasReseller := newReseller != "" && newReseller != "root"

	if hasReseller {
		acct, err := u.AdminDB.UserByUsername(newReseller)
		if err != nil || acct.Role != "reseller" {
			return false, "User '" + newReseller + "' is not a reseller or is not allowed to manage users."
		}

		if limits, limitsPath, ok := readResellerLimits(newReseller); ok {
			var currentAccounts int
			u.MySQL.QueryRow(`SELECT COUNT(*) FROM users WHERE owner = ?`, newReseller).Scan(&currentAccounts)
			if maxAccounts, capped := resellerMaxAccounts(limits); capped && currentAccounts >= maxAccounts {
				return false, fmt.Sprintf("Reseller '%s' has reached the maximum account limit (%d).", newReseller, maxAccounts)
			}
			limits["current_accounts"] = currentAccounts
			writeResellerLimits(limitsPath, limits)
		}
	}

	var err error
	if hasReseller {
		_, err = u.MySQL.Exec(`UPDATE users SET owner = ? WHERE username = ?`, newReseller, username)
	} else {
		_, err = u.MySQL.Exec(`UPDATE users SET owner = NULL WHERE username = ?`, username)
	}
	if err != nil {
		return false, err.Error()
	}

	if hasReseller {
		if limits, limitsPath, ok := readResellerLimits(newReseller); ok {
			var currentAccounts int
			u.MySQL.QueryRow(`SELECT COUNT(*) FROM users WHERE owner = ?`, newReseller).Scan(&currentAccounts)
			limits["current_accounts"] = currentAccounts
			writeResellerLimits(limitsPath, limits)
		}
	}
	return true, ""
}

func readResellerLimits(reseller string) (limits map[string]interface{}, path string, ok bool) {
	path = paneldb.ResellerConfigDir + "/" + reseller + ".json"
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, path, false
	}
	if json.Unmarshal(raw, &limits) != nil {
		return nil, path, false
	}
	return limits, path, true
}

func writeResellerLimits(path string, limits map[string]interface{}) {
	if out, err := json.MarshalIndent(limits, "", "  "); err == nil {
		os.WriteFile(path, out, 0644)
	}
}

// resellerMaxAccounts reads a limits file's "max_accounts" value, which may
// be the string "unlimited"/"0" (meaning uncapped) or a number in either
// JSON or string form.
func resellerMaxAccounts(limits map[string]interface{}) (max int, capped bool) {
	switch v := limits["max_accounts"].(type) {
	case string:
		if v == "" || v == "unlimited" || v == "0" {
			return 0, false
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	case float64:
		if v <= 0 {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}
