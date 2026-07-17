// This file implements the JSON REST API's account-management surface:
// GET/POST /api/users, GET/POST/DELETE/PATCH/PUT /api/users/{username}, and
// POST /api/users/{username}/autologin (see the comment on ServeAutologin
// for why this exists as a distinct route instead of reusing an HTTP verb).
package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// APIUsers bundles the /api/users handlers.
type APIUsers struct {
	MySQL    *sql.DB
	PublicIP string
}

// ForbiddenUsernamesPath lists (one per line) usernames account creation
// must refuse, case-insensitively.
var ForbiddenUsernamesPath = "/etc/openpanel/openadmin/config/forbidden_usernames.txt"

// isJSONRequest reports whether the request declares a JSON body via its
// Content-Type header. This only checks the declared media type, not
// whether the body actually parses -- a request with this header set but
// an empty or malformed body still counts, matching how every handler here
// gates on "is this even meant to be JSON" before trying to decode it.
func isJSONRequest(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// apiCheckOutputRun runs an opencli invocation and captures only its
// stdout, leaving stderr to go wherever the process's own stderr goes.
// This intentionally does NOT capture stderr: on failure, the caller only
// ever has whatever partial stdout was produced before the process exited
// non-zero, which is often empty since opencli reports errors on stderr --
// callers surface that empty string rather than substituting anything more
// helpful, matching the underlying command's own output contract.
var apiCheckOutputRun = func(args ...string) (output string, runErr error) {
	cmd := exec.Command(args[0], args[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return out.String(), err
}

// scanRowsAsOrderedMaps converts *sql.Rows into a slice of column-name ->
// value maps, decoding []byte to string. When a query selects two columns
// under the same alias, the later one wins -- map assignment in column
// order naturally does this, mirroring how a dict-building cursor would
// overwrite the same key twice.
func scanRowsAsOrderedMaps(rows *sql.Rows) ([]map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for rows.Next() {
		raw := make([]interface{}, len(columns))
		ptrs := make([]interface{}, len(columns))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(columns))
		for i, col := range columns {
			if b, ok := raw[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = raw[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// fetchUserPlanRow joins a user with its plan, matching either the exact
// username or a "SUSPENDED_<id>_<username>" variant. The unescaped "_" in
// the LIKE pattern matches any single character, same as elsewhere in this
// package.
func (a *APIUsers) fetchUserPlanRow(username string) (map[string]interface{}, bool) {
	suspendedPattern := "SUSPENDED_%_" + username
	rows, err := a.MySQL.Query(`
		SELECT
			u.id AS user_id,
			u.username,
			u.email,
			u.owner,
			u.user_domains,
			u.twofa_enabled,
			u.registered_date,
			u.server,
			u.plan_id,
			p.id AS plan_id,
			p.name AS plan_name,
			p.description AS plan_description,
			p.domains_limit,
			p.websites_limit,
			p.email_limit,
			p.ftp_limit,
			p.disk_limit,
			p.inodes_limit,
			p.db_limit,
			p.cpu,
			p.ram,
			p.bandwidth,
			p.feature_set
		FROM users u
		LEFT JOIN plans p ON u.plan_id = p.id
		WHERE u.username = ? OR u.username LIKE ?
		LIMIT 1
	`, username, suspendedPattern)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	maps, err := scanRowsAsOrderedMaps(rows)
	if err != nil || len(maps) == 0 {
		return nil, false
	}
	return maps[0], true
}

// fetchDomainsAndSites returns every domain owned by userID (deduplicated,
// in first-seen order) plus every site attached to any of those domains.
func (a *APIUsers) fetchDomainsAndSites(userID interface{}) ([]map[string]interface{}, []map[string]interface{}) {
	domains := []map[string]interface{}{}
	sites := []map[string]interface{}{}

	rows, err := a.MySQL.Query(`
		SELECT
			d.domain_id,
			d.docroot,
			d.domain_url,
			d.php_version,
			s.id AS site_id,
			s.site_name,
			s.admin_email,
			s.version,
			s.created_date AS site_created,
			s.type,
			s.ports,
			s.path,
			s.container
		FROM domains d
		LEFT JOIN sites s ON d.domain_id = s.domain_id
		WHERE d.user_id = ?
	`, userID)
	if err != nil {
		return domains, sites
	}
	defer rows.Close()

	all, err := scanRowsAsOrderedMaps(rows)
	if err != nil {
		return domains, sites
	}

	seen := map[interface{}]bool{}
	for _, row := range all {
		domainID := row["domain_id"]
		if !seen[domainID] {
			seen[domainID] = true
			domains = append(domains, map[string]interface{}{
				"domain_id":   domainID,
				"docroot":     row["docroot"],
				"domain_url":  row["domain_url"],
				"php_version": row["php_version"],
			})
		}
		if row["site_id"] != nil {
			sites = append(sites, map[string]interface{}{
				"id":           row["site_id"],
				"site_name":    row["site_name"],
				"domain_id":    domainID,
				"admin_email":  row["admin_email"],
				"version":      row["version"],
				"created_date": row["site_created"],
				"type":         row["type"],
				"ports":        row["ports"],
				"path":         row["path"],
				"container":    row["container"],
			})
		}
	}
	return domains, sites
}

// apiDiskUsageForUsername reads QuotaReportPath (users.go) looking for an
// exact username match -- unlike the HTML users pages, this never strips a
// "SUSPENDED_" prefix first, matching the raw username the caller asked
// about.
func apiDiskUsageForUsername(username string) map[string]interface{} {
	raw, err := os.ReadFile(QuotaReportPath)
	if err != nil {
		return map[string]interface{}{}
	}
	var parsed struct {
		Users []map[string]interface{} `json:"users"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return map[string]interface{}{}
	}
	for _, u := range parsed.Users {
		name, _ := u["username"].(string)
		if name != username {
			continue
		}
		homePath, _ := u["home_path"].(string)
		if homePath == "" {
			homePath = "/var/www/html/"
		}
		return map[string]interface{}{
			"disk_used":   u["disk_used"],
			"disk_soft":   u["disk_soft"],
			"disk_hard":   u["disk_hard"],
			"inodes_used": u["inodes_used"],
			"inodes_soft": u["inodes_soft"],
			"inodes_hard": u["inodes_hard"],
			"home_path":   homePath,
		}
	}
	return map[string]interface{}{}
}

// userDataForAPI builds the nested user/plan/domains/sites/disk_usage
// payload for GET /api/users/{username}.
func (a *APIUsers) userDataForAPI(username string) (map[string]interface{}, bool) {
	row, ok := a.fetchUserPlanRow(username)
	if !ok {
		return nil, false
	}

	userInfo := map[string]interface{}{
		"id":              row["user_id"],
		"username":        row["username"],
		"email":           row["email"],
		"owner":           row["owner"],
		"twofa_enabled":   row["twofa_enabled"],
		"registered_date": row["registered_date"],
		"server":          row["server"],
	}
	planInfo := map[string]interface{}{
		"id":             row["plan_id"],
		"name":           row["plan_name"],
		"description":    row["plan_description"],
		"domains_limit":  row["domains_limit"],
		"websites_limit": row["websites_limit"],
		"email_limit":    row["email_limit"],
		"ftp_limit":      row["ftp_limit"],
		"disk_limit":     row["disk_limit"],
		"inodes_limit":   row["inodes_limit"],
		"db_limit":       row["db_limit"],
		"cpu":            row["cpu"],
		"ram":            row["ram"],
		"bandwidth":      row["bandwidth"],
		"feature_set":    row["feature_set"],
	}

	domains, sites := a.fetchDomainsAndSites(row["user_id"])

	return map[string]interface{}{
		"user":       userInfo,
		"plan":       planInfo,
		"domains":    domains,
		"sites":      sites,
		"disk_usage": apiDiskUsageForUsername(username),
	}, true
}

// ServeUsers handles GET/POST /api/users and
// GET/POST/DELETE/PATCH/PUT /api/users/{username}. Wrap with
// (*APIAuth).RequireAPIOwnerOrAdmin("username", ...).
func (a *APIUsers) ServeUsers(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")

	switch r.Method {
	case http.MethodGet:
		a.handleGet(w, r, username)
	case http.MethodPost:
		// A POST to /api/users/{username} behaves identically to a POST to
		// /api/users: the path segment is never consulted here, only the
		// JSON body's own "username" field is used to create the account.
		a.handleCreate(w, r)
	case http.MethodPut:
		a.handleChangePlan(w, r, username)
	case http.MethodPatch:
		a.handlePatch(w, r, username)
	case http.MethodDelete:
		a.handleDelete(w, r, username)
	}
}

func (a *APIUsers) handleGet(w http.ResponseWriter, r *http.Request, username string) {
	if username != "" {
		data, ok := a.userDataForAPI(username)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}
		writeJSON(w, map[string]interface{}{"user": data})
		return
	}

	// The account list relies on a page-scoped notion of "current user"
	// that a bearer-token request never populates, so the underlying query
	// never runs for this API and the list always comes back empty here,
	// regardless of the caller's role.
	writeJSON(w, map[string]interface{}{"users": []interface{}{}})
}

func (a *APIUsers) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	var body struct {
		Email     string `json:"email"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		PlanName  string `json:"plan_name"`
		Webserver string `json:"webserver"`
		SQLType   string `json:"sql_type"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	if body.Email == "" || body.Username == "" || body.Password == "" || body.PlanName == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	if forbidden, err := os.ReadFile(ForbiddenUsernamesPath); err == nil {
		lower := strings.ToLower(body.Username)
		for _, line := range strings.Split(string(forbidden), "\n") {
			if strings.TrimSpace(line) == lower {
				writeJSONError(w, http.StatusBadRequest, "Username is not allowed")
				return
			}
		}
	}

	args := []string{"opencli", "user-add", body.Username, body.Password, body.Email, body.PlanName}
	if body.SQLType == "mysql" || body.SQLType == "mariadb" {
		args = append(args, "--sql="+body.SQLType)
	}
	switch body.Webserver {
	case "nginx", "apache", "openresty", "varnish+apache", "varnish+nginx", "varnish+openresty":
		args = append(args, "--webserver="+body.Webserver)
	}

	output, runErr := apiCheckOutputRun(args...)
	if runErr == nil {
		writeJSONStatus(w, http.StatusCreated, map[string]interface{}{
			"success":  true,
			"response": map[string]string{"message": strings.TrimSpace(output)},
		})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   strings.TrimSpace(output),
	})
}

func (a *APIUsers) handleChangePlan(w http.ResponseWriter, r *http.Request, username string) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var body struct {
		PlanName string `json:"plan_name"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if username == "" || body.PlanName == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing username or plan name.")
		return
	}

	output, runErr := apiCheckOutputRun("opencli", "user-change_plan", username, body.PlanName)
	if runErr == nil {
		writeJSONStatus(w, http.StatusCreated, map[string]interface{}{
			"success":  true,
			"response": map[string]string{"message": strings.TrimSpace(output)},
		})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   "Error changing plan for user: " + runErr.Error(),
	})
}

func (a *APIUsers) handlePatch(w http.ResponseWriter, r *http.Request, username string) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	var body struct {
		Password string `json:"password"`
		Action   string `json:"action"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	switch {
	case body.Password != "":
		output, runErr := apiCheckOutputRun("opencli", "user-password", username, body.Password)
		if runErr == nil {
			writeJSONStatus(w, http.StatusCreated, map[string]interface{}{
				"success":  true,
				"response": map[string]string{"message": strings.TrimSpace(output)},
			})
			return
		}
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "Error executing command: " + runErr.Error(),
		})

	case body.Action != "":
		if body.Action != "suspend" && body.Action != "unsuspend" {
			writeJSONError(w, http.StatusBadRequest, "Invalid action, only suspend and unsuspend are allowed.")
			return
		}

		var output string
		var runErr error
		if body.Action == "suspend" {
			output, runErr = apiCheckOutputRun("opencli", "user-suspend", username, "-y")
		} else {
			output, runErr = apiCheckOutputRun("opencli", "user-unsuspend", username)
		}

		if runErr == nil {
			writeJSONStatus(w, http.StatusCreated, map[string]interface{}{
				"success":  true,
				"response": map[string]string{"message": strings.TrimSpace(output)},
			})
			return
		}
		errPrefix := "Error suspending user: "
		if body.Action == "unsuspend" {
			errPrefix = "Error unsuspending user: "
		}
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   errPrefix + runErr.Error(),
		})

	default:
		writeJSONError(w, http.StatusBadRequest, "Something went wrong..")
	}
}

func (a *APIUsers) handleDelete(w http.ResponseWriter, r *http.Request, username string) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	if username == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing username")
		return
	}

	output, runErr := apiCheckOutputRun("opencli", "user-delete", username, "-y")
	if runErr == nil {
		writeJSONStatus(w, http.StatusCreated, map[string]interface{}{
			"success":  true,
			"response": map[string]string{"message": strings.TrimSpace(output)},
		})
		return
	}
	writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
		"success": false,
		"error":   "Error executing command: " + runErr.Error(),
	})
}

// ServeAutologin handles POST /api/users/{username}/autologin: generates a
// one-time admin_token file the user-panel's /login_autologin route reads
// to sign the caller in as username. This lives on its own POST route
// rather than being dispatched by HTTP method on /api/users/{username}:
// CONNECT requests use an authority-form target (host:port), not a URL
// path, so no ordinary HTTP client library can address a path-based
// resource with that method -- a dedicated route is the only reliable way
// to expose this action.
func (a *APIUsers) ServeAutologin(w http.ResponseWriter, r *http.Request) {
	if !isJSONRequest(r) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	username := r.PathValue("username")
	if username == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing username")
		return
	}

	tokenDir := filepath.Join(AutologinTokenBaseDir, username)
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	token := generateRandomToken(30)
	if err := os.WriteFile(filepath.Join(tokenDir, "logintoken.txt"), []byte(token), 0644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	forceDomain := autologinOpenpanelDomainRun()
	port := autologinOpenpanelPortRun()

	var hostname, scheme string
	if forceDomain != "" {
		hostname = strings.TrimSpace(forceDomain)
		scheme = "https"
	} else {
		hostname = a.PublicIP
		scheme = "http"
	}

	link := fmt.Sprintf("%s://%s:%s/login_autologin?username=%s&admin_token=%s", scheme, hostname, port, username, token)
	writeJSON(w, map[string]string{"link": link})
}
