// This file implements the inline-edit account settings on the Users >
// <username> Overview tab: Locale, Web server, Database type, Varnish
// caching, and 2FA (disable only). Webserver/database-type switching
// mirrors the native Go logic OpenPanel's own app uses (internal/modules/
// docker/switch.go) -- stop old container, drop its data volume, start the
// new one, persist the choice to the user's .env -- rather than an opencli
// wrapper, since OpenPanel itself has no opencli command for either.
// Varnish and 2FA-disable DO have opencli commands (user-varnish,
// user-2fa) and just shell out to those.
package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
)

// userWebserverOptions is the fixed set of webservers a user can switch
// to, matching OpenPanel's own hardcoded list (internal/modules/docker/
// switch.go) -- not derived from which podman images happen to be
// downloaded.
var userWebserverOptions = []string{"apache", "nginx", "openresty", "openlitespeed", "litespeed"}

func userEnvPath(context string) string {
	return "/home/" + context + "/.env"
}

// getUserEnvValue reads one KEY="value" entry from a user's .env, "" if
// missing/unreadable.
func getUserEnvValue(context, key string) string {
	return quickStartParseEnv(userEnvPath(context))[key]
}

// setUserEnvValue rewrites one existing KEY="value" line in a user's .env
// in place. Like updateContainerRAMOrCPU's env edit, this only replaces an
// already-present key -- WEB_SERVER/MYSQL_TYPE are always set by account
// creation, so there's nothing to append for the fields this file edits.
func setUserEnvValue(context, key, value string) error {
	path := userEnvPath(context)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := splitFileLinesPreserving(string(raw))
	for i, line := range lines {
		if strings.HasPrefix(line, key+"=") {
			lines[i] = key + `="` + value + "\"\n"
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "")), 0644)
}

// userDomainCount returns how many domains the given (already-owner-
// checked) username has.
func userDomainCount(mysqlDB *sql.DB, username string) (int, error) {
	var count int
	err := mysqlDB.QueryRow(`
		SELECT COUNT(*) FROM domains d
		JOIN users u ON d.user_id = u.id
		WHERE u.username = ?
	`, username).Scan(&count)
	return count, err
}

// containerIsRunningRun is injectable so tests never shell out to a real
// podman binary. Unlike containerExistsRun (podman.go's plain "inspect
// succeeds"), this specifically checks the container's running state --
// an existing-but-stopped container must not block a database-type
// switch.
var containerIsRunningRun = func(context, containerName string) bool {
	cmd, err := podman.Command(context, "inspect", "--format", "{{.State.Running}}", containerName)
	if err != nil {
		return false
	}
	out, runErr := cmd.Output()
	return runErr == nil && strings.TrimSpace(string(out)) == "true"
}

// deletePodmanVolumeRun is injectable so tests never shell out to a real
// podman binary. Errors are deliberately ignored by callers -- a volume
// that's already gone (or was never created) isn't a failure, matching
// OpenPanel's own deleteDockerVolume which also swallows this.
var deletePodmanVolumeRun = func(context, volumeName string) error {
	cmd, err := podman.Command(context, "volume", "rm", "-f", volumeName)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// ServeUserAccountSetting handles POST
// /users/{username}/account-setting/{field}: locale, webserver,
// database_type, varnish, or twofa. Ownership-checked the same way every
// other per-user action in this package is. A plain form POST + full-page
// redirect + flash, matching the CPU/RAM/PIDs inline editors already on
// this page (user_detail.html's #services tab) rather than a JSON/fetch
// round trip -- webserver/database-type switches can block for as long as
// a podman-compose pull takes, same as those editors' podman update call.
func (u *Users) ServeUserAccountSetting(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	field := r.PathValue("field")
	currentUser := auth.CurrentUser(r)

	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.ParseForm()
	value := r.PostFormValue("value")

	context, err := queryContextByUsername(u.MySQL, username)
	if err != nil || context == "" {
		auth.AddFlash(w, r, u.Sessions, "Error: User "+username+" not found", "error")
		http.Redirect(w, r, "/users/"+username+"#overview", http.StatusSeeOther)
		return
	}

	var message string
	switch field {
	case "locale":
		message, err = u.setUserLocale(context, value)
	case "webserver":
		message, err = u.setUserWebserver(username, context, value)
	case "database_type":
		message, err = u.setUserDatabaseType(context, value)
	case "varnish":
		message, err = u.setUserVarnish(username, value)
	case "twofa":
		message, err = u.disableUserTwoFA(username)
	default:
		auth.AddFlash(w, r, u.Sessions, "Error: Unknown field: "+field, "error")
		http.Redirect(w, r, "/users/"+username+"#overview", http.StatusSeeOther)
		return
	}

	if err != nil {
		auth.AddFlash(w, r, u.Sessions, "Error: "+err.Error(), "error")
		http.Redirect(w, r, "/users/"+username+"#overview", http.StatusSeeOther)
		return
	}

	logUserAction(username, clientIP(r), "Administrator "+currentUser.Username+" "+message)
	auth.AddFlash(w, r, u.Sessions, strings.ToUpper(message[:1])+message[1:], "success")
	http.Redirect(w, r, "/users/"+username+"#overview", http.StatusSeeOther)
}

// installedLocaleCodes lists the 2-letter locale codes with a translation
// directory actually present on this server -- the same set userLocale()
// itself would recognize (see its 2-lowercase-letter validation), so a
// value picked here always reads back correctly afterward.
func installedLocaleCodes() []string {
	entries, err := os.ReadDir(TranslationsDir)
	if err != nil {
		return []string{"en"}
	}
	codes := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if len(name) == 2 {
			codes = append(codes, name)
		}
	}
	if len(codes) == 0 {
		return []string{"en"}
	}
	return codes
}

func (u *Users) setUserLocale(context, locale string) (string, error) {
	locale = strings.ToLower(strings.TrimSpace(locale))
	valid := false
	for _, c := range installedLocaleCodes() {
		if c == locale {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("unknown or not-installed locale: %s", locale)
	}
	path := "/home/" + context + "/locale"
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(locale), 0644); err != nil {
		return "", err
	}
	return "changed locale to " + locale + " for user in context " + context, nil
}

func (u *Users) setUserWebserver(username, context, newWebserver string) (string, error) {
	valid := false
	for _, ws := range userWebserverOptions {
		if ws == newWebserver {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("invalid webserver: %s", newWebserver)
	}

	current := getUserEnvValue(context, "WEB_SERVER")
	if current == newWebserver {
		return "", fmt.Errorf("%s is already the active webserver", newWebserver)
	}

	count, err := userDomainCount(u.MySQL, username)
	if err != nil {
		return "", fmt.Errorf("failed to check existing domains: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("existing domains (%d) must first be removed in order to change webserver", count)
	}

	if current != "" {
		if res := startOrStopContainer(context, current, "deactivate", false); !res.Success {
			return "", fmt.Errorf("failed to stop %s: %s", current, res.Message)
		}
	}
	_ = deletePodmanVolumeRun(context, context+"_webserver_data")

	if res := startOrStopContainer(context, newWebserver, "activate", true); !res.Success {
		return "", fmt.Errorf("failed to start %s: %s", newWebserver, res.Message)
	}
	if err := setUserEnvValue(context, "WEB_SERVER", newWebserver); err != nil {
		return "", fmt.Errorf("webserver started, but failed to persist the change to .env: %w", err)
	}

	return fmt.Sprintf("changed webserver from %s to %s for user %s", current, newWebserver, username), nil
}

func (u *Users) setUserDatabaseType(context, newType string) (string, error) {
	if newType != "mysql" && newType != "mariadb" {
		return "", fmt.Errorf("invalid database type: %s", newType)
	}

	current := getUserEnvValue(context, "MYSQL_TYPE")
	if current == newType {
		return "", fmt.Errorf("%s is already the active database type", newType)
	}
	if current != "" && containerIsRunningRun(context, current) {
		return "", fmt.Errorf("existing %s container must first be stopped (delete its databases first) in order to change database type", current)
	}

	_ = deletePodmanVolumeRun(context, context+"_mysql_data")
	_ = startOrStopContainer(context, "phpmyadmin", "deactivate", false)

	if res := startOrStopContainer(context, newType, "activate", true); !res.Success {
		return "", fmt.Errorf("failed to start %s: %s", newType, res.Message)
	}
	if err := setUserEnvValue(context, "MYSQL_TYPE", newType); err != nil {
		return "", fmt.Errorf("%s started, but failed to persist the change to .env: %w", newType, err)
	}

	return fmt.Sprintf("changed database type from %s to %s", current, newType), nil
}

func (u *Users) setUserVarnish(username, value string) (string, error) {
	action := strings.ToLower(strings.TrimSpace(value))
	if action != "enable" && action != "disable" {
		return "", fmt.Errorf("value must be 'enable' or 'disable'")
	}
	ok, out := runOpenCLI("Error occurred running opencli user-varnish command", "opencli", "user-varnish", username, action)
	if !ok {
		return "", fmt.Errorf("%s", opencliResultMessage(ok, out))
	}
	return action + "d Varnish caching for user " + username, nil
}

func (u *Users) disableUserTwoFA(username string) (string, error) {
	ok, out := runOpenCLI("Error occurred running opencli user-2fa command", "opencli", "user-2fa", username, "disable")
	if !ok {
		return "", fmt.Errorf("%s", opencliResultMessage(ok, out))
	}
	return "disabled 2FA for user " + username, nil
}
