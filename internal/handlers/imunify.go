// This file implements the ImunifyAV admin GUI: a reverse proxy fronting a
// local PHP service on 127.0.0.1:9000.
package handlers

import (
	"bytes"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Imunify bundles the /security/imunify and /imav handlers.
type Imunify struct {
	Sessions *auth.Manager
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

// imunifyGetTokenRun returns ("", false) for both a nonzero exit and a
// failure to even start the binary.
var imunifyGetTokenRun = func() (string, bool) {
	out, err := exec.Command("imunify360-agent", "login", "get", "--username", "root").Output()
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
		webtemplates.Render(w, "security_imunify_not_installed.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Not Installed)"))
		return
	}

	if !imunifyIsPortOpenRun("127.0.0.1", 9000) {
		imunifyStartDetachedRun()
		if !imunifyIsPortOpenRun("127.0.0.1", 9000) {
			webtemplates.Render(w, "security_imunify_not_running.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Not Running)"))
			return
		}
	}

	token, ok := imunifyGetTokenRun()
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
		if !imunifyIsPortOpenRun("127.0.0.1", 9000) {
			webtemplates.Render(w, "security_imunify_not_running.html", mergeChrome(map[string]interface{}{}, r, "ImunifyAV (Not Running)"))
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
