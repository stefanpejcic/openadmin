// This file implements the ImunifyAV admin GUI: a reverse proxy fronting a
// local PHP service on 127.0.0.1:9000.
package handlers

import (
	"bytes"
	"database/sql"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Imunify bundles the /security/imunify and /imav handlers.
type Imunify struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

var (
	imunifyPHPRoot   = "/etc/sysconfig/imunify360/"
	imunifyStaticDir = "/etc/sysconfig/imunify360/imav/assets/static"
)

var imunifyCommandAvailableRun = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// imunifyIsPortOpenRun does a plain TCP dial with a 1s timeout, no
// protocol-level handshake.
var imunifyIsPortOpenRun = func(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// imunifyStartDetachedRun, despite the name, blocks the request until
// "opencli imunify start" actually exits -- it is not detached/
// non-blocking at all. That (mis-named but real) blocking behavior is
// preserved here rather than "fixed" into an actually-async start.
var imunifyStartDetachedRun = func() {
	cmd := exec.Command("opencli", "imunify", "start")
	_ = cmd.Run()
}

// imunifyWaitForPortRun polls imunifyIsPortOpenRun for a few seconds after
// a fresh start instead of relying on a single immediate re-check.
// "opencli imunify start" returning (or the port accepting a bare TCP
// connection) doesn't mean the app behind it has finished initializing --
// a single dial right after start can race a still-warming-up backend,
// which is why the GUI previously failed to load on the first open after a
// cold start but worked on the very next one (by then the backend had
// caught up).
var imunifyWaitForPortRun = func(host string, port int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if imunifyIsPortOpenRun(host, port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

var (
	// live output of the last install started from the gui
	ImunifyInstallLogPath = "/tmp/openpanel_imunify_install.log"
	ImunifyInstallPIDFile = "/tmp/openpanel_imunify_install.pid"
)

var imunifyInstallRun = func() error {
	pid, err := startDetached("openpanel-imunify-install", ImunifyInstallLogPath, []string{"timeout", "1800s", "opencli", "imunify", "install"})
	if err != nil {
		return err
	}
	return os.WriteFile(ImunifyInstallPIDFile, []byte(strconv.Itoa(pid)), 0644)
}

func imunifyInstallRunning() bool {
	raw, err := os.ReadFile(ImunifyInstallPIDFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return err == nil && processAlive(pid)
}

// imunifyGetTokenRun returns ("", false) for both a nonzero exit and a
// failure to even start the binary.
var imunifyGetTokenRun = func(username string) (string, bool) {
	out, err := exec.Command("imunify360-agent", "login", "get", "--username", username).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// ServeImunifyStatic handles GET /security/imunify/assets/static/{filename...}.
func (im *Imunify) ServeImunifyStatic(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	filePath, ok := safeJoinOr400(imunifyStaticDir, filename)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	mimeType := mime.TypeByExtension(filepath.Ext(filePath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	http.ServeFile(w, r, filePath)
}

// ServeImunifyGUI handles GET /security/imunify/.
func (im *Imunify) ServeImunifyGUI(w http.ResponseWriter, r *http.Request) {
	if !imunifyCommandAvailableRun("imunify360-agent") {
		webtemplates.Render(w, "security_imunify_not_installed.html", mergeChrome(map[string]interface{}{
			"Installing": imunifyInstallRunning(),
		}, r, "ImunifyAV (Not Installed)"))
		return
	}

	if !imunifyIsPortOpenRun("127.0.0.1", 9000) {
		imunifyStartDetachedRun()
		if !imunifyWaitForPortRun("127.0.0.1", 9000) {
			webtemplates.Render(w, "security_imunify_not_running.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Starting)"))
			return
		}
	}

	if scope := scopeFor(r, im.MySQL); scope != nil {
		im.serveResellerImunify(w, r, scope)
		return
	}
	token, ok := imunifyGetTokenRun("root")
	if !ok {
		auth.AddFlash(w, r, im.Sessions, "Failed to generate token for auto-login to ImunifyAV. Please use SSH user and password to login.", "warning")
		webtemplates.Render(w, "security_imunify.html", mergeChrome(map[string]interface{}{
			"Token":   "",
			"Flashes": auth.PopFlashes(w, r, im.Sessions),
		}, r, "ImunifyAV"))
		return
	}

	webtemplates.Render(w, "security_imunify.html", mergeChrome(map[string]interface{}{
		"Token":   token,
		"Flashes": auth.PopFlashes(w, r, im.Sessions),
	}, r, "ImunifyAV"))
}

// serveResellerImunify never hands a reseller the root token, they pick one of their users and get a token for that user only
func (im *Imunify) serveResellerImunify(w http.ResponseWriter, r *http.Request, scope *resellerScope) {
	username := r.URL.Query().Get("user")
	if username == "" {
		scans := cachedImunifyUsers()
		type row struct {
			Username string
			Scan     imunifyUser
			HasScan  bool
		}
		users := scope.Users()
		sort.Strings(users)
		rows := []row{}
		contexts := paneldb.UserContexts(im.MySQL)
		for _, u := range users {
			sc, ok := scans[contexts[u]]
			rows = append(rows, row{Username: u, Scan: sc, HasScan: ok})
		}
		webtemplates.Render(w, "security_imunify_reseller.html", mergeChrome(map[string]interface{}{
			"Users":   rows,
			"Flashes": auth.PopFlashes(w, r, im.Sessions),
		}, r, "ImunifyAV"))
		return
	}
	userContext, err := queryContextByUsername(im.MySQL, username)
	if !scope.AllowsUser(username) || err != nil || !imunifyUserRE.MatchString(userContext) {
		auth.AddFlash(w, r, im.Sessions, "User \""+username+"\" is not one of your users.", "error")
		http.Redirect(w, r, "/security/imunify/", http.StatusSeeOther)
		return
	}
	token, ok := imunifyGetTokenRun(userContext)
	if !ok {
		auth.AddFlash(w, r, im.Sessions, "Failed to open ImunifyAV for "+username+".", "error")
		http.Redirect(w, r, "/security/imunify/", http.StatusSeeOther)
		return
	}
	webtemplates.Render(w, "security_imunify.html", mergeChrome(map[string]interface{}{
		"Token":   token,
		"ForUser": username,
		"Flashes": auth.PopFlashes(w, r, im.Sessions),
	}, r, "ImunifyAV: "+username))
}

// ImunifyGUILogPath is where opencli imunify start sends the php server output
var ImunifyGUILogPath = "/var/log/imunify-php-server.log"

// ServeImunifyGUIStatus handles GET /security/imunify/gui/status, start=1 starts the gui again if it went down
func (im *Imunify) ServeImunifyGUIStatus(w http.ResponseWriter, r *http.Request) {
	running := imunifyIsPortOpenRun("127.0.0.1", 9000)
	if !running && r.URL.Query().Get("start") == "1" {
		imunifyStartDetachedRun()
		running = imunifyIsPortOpenRun("127.0.0.1", 9000)
	}
	log := ""
	// a few spare lines covers trailing blank ones that TrimSpace drops
	if raw, err := tailFileLines(ImunifyGUILogPath, 40); err == nil {
		lines := strings.Split(strings.TrimSpace(raw), "\n")
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		log = strings.Join(lines, "\n")
	}
	writeJSON(w, map[string]interface{}{"running": running, "log": log})
}

// ServeImunifyInstall handles POST /security/imunify/install.
func (im *Imunify) ServeImunifyInstall(w http.ResponseWriter, r *http.Request) {
	if imunifyInstallRunning() {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "ImunifyAV installation is already running."})
		return
	}
	if imunifyCommandAvailableRun("imunify360-agent") {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "ImunifyAV is already installed."})
		return
	}
	if err := imunifyInstallRun(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Failed to start the installation. Details: " + err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"success": true})
}

// ServeImunifyInstallStatus handles GET /security/imunify/install/status.
func (im *Imunify) ServeImunifyInstallStatus(w http.ResponseWriter, r *http.Request) {
	output := ""
	if raw, err := os.ReadFile(ImunifyInstallLogPath); err == nil {
		output = string(raw)
	}
	status := "idle"
	if imunifyInstallRunning() {
		status = "running"
	} else if _, err := os.Stat(ImunifyInstallPIDFile); err == nil {
		status = "finished"
	}
	writeJSON(w, map[string]interface{}{"status": status, "output": output, "installed": imunifyCommandAvailableRun("imunify360-agent")})
}

// imunifyProxyRun performs the actual reverse-proxy call to the local PHP
// service. Injectable so tests don't need a real PHP service listening on
// :9000.
var imunifyProxyRun = func(r *http.Request) (status int, header http.Header, body []byte, err error) {
	targetURL := "http://127.0.0.1:9000" + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		return 0, nil, nil, err
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, nil, err
	}
	for k, vv := range r.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

// ServeImunifyPHP handles GET/POST /imav/ and /imav/{path...}.
func (im *Imunify) ServeImunifyPHP(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		path = "index.php"
	}

	if !imunifyCommandAvailableRun("imunify360-agent") {
		webtemplates.Render(w, "security_imunify_not_installed.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Not Installed)"))
		return
	}
	if !imunifyIsPortOpenRun("127.0.0.1", 9000) {
		imunifyStartDetachedRun()
		if !imunifyWaitForPortRun("127.0.0.1", 9000) {
			webtemplates.Render(w, "security_imunify_not_running.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Starting)"))
			return
		}
	}

	scriptPath := filepath.Clean(filepath.Join(imunifyPHPRoot, path))
	if !strings.HasPrefix(scriptPath, strings.TrimSuffix(imunifyPHPRoot, "/")) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	status, header, body, err := imunifyProxyRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Copies every upstream header through verbatim, including
	// Content-Encoding/Transfer-Encoding, even though `body` here is
	// already the fully-read (and possibly auto-decompressed) response
	// bytes. This can produce a mismatched Content-Encoding header if the
	// upstream gzips its response; preserved as-is since it's the real
	// (if questionable) upstream behavior being proxied.
	for k, vv := range header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	w.Write(body)
}
