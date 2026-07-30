// This file implements the ConfigServer Firewall (CSF) iframe wrapper,
// which shells out to csf.pl's own CGI-style web UI script and reinjects a
// CSRF token into its raw HTML output.
package handlers

import (
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Firewall bundles the /configservercsf/iframe/ and /security/firewall
// handlers.
type Firewall struct {
	Sessions *auth.Manager
}

// CSFScriptPath is the hardcoded path to the csf.pl script.
var CSFScriptPath = "/usr/local/admin/modules/security/csf.pl"

// csfImagesDir holds csf.pl's own UI icons. DisplayUI.pm hardcodes the
// image src it emits as "/static/configservercsf/...", so that URL is
// fixed regardless of how it's served on this end -- this serves the
// directory directly instead of the old approach of symlinking it into
// the static/ dir at startup (see internal/bootstrap/housekeeping.go),
// which required static/ to stay a real directory rather than an
// embedded FS.
var csfImagesDir = "/etc/csf/ui/images"

// ServeCSFImages handles GET /static/configservercsf/{filename...}.
func (f *Firewall) ServeCSFImages(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	filePath, ok := safeJoinOr400(csfImagesDir, filename)
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

// firewallCommandAvailableRun / firewallCSFRun are injectable so tests
// never shell out to a real binary.
var firewallCommandAvailableRun = func(command string) bool {
	return exec.Command(command, "--version").Run() == nil
}

// firewallCSFRun invokes csf.pl. A non-zero exit is NOT treated as an
// error -- stdout is used regardless of exit code. Only a failure to even
// start the process (e.g. csf.pl missing) is treated as an error here.
var firewallCSFRun = func(tmpFile string) (string, error) {
	cmd := exec.Command(CSFScriptPath, tmpFile)
	out, err := cmd.Output()
	if _, ok := err.(*exec.ExitError); ok {
		return string(out), nil
	}
	return string(out), err
}

// ServeCSFIframe handles GET/POST /configservercsf/iframe/.
func (f *Firewall) ServeCSFIframe(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	var pairs []string
	if r.Method == http.MethodGet {
		for k, v := range r.URL.Query() {
			if k == "csrf_token" || len(v) == 0 {
				continue
			}
			pairs = append(pairs, k+"="+v[0])
		}
	} else {
		for k, v := range r.PostForm {
			if k == "csrf_token" || len(v) == 0 {
				continue
			}
			pairs = append(pairs, k+"="+v[0])
		}
	}
	qs := strings.Join(pairs, "&")

	var output string
	tmp, err := os.CreateTemp("", "csf-ui-")
	if err != nil {
		output = "Unable to create csf UI temp file: " + err.Error()
	} else {
		tmpName := tmp.Name()
		_, writeErr := tmp.WriteString(qs)
		tmp.Close()
		defer func() { _ = os.Remove(tmpName) }()

		if writeErr != nil {
			output = "Unable to create csf UI temp file: " + writeErr.Error()
		} else {
			out, runErr := firewallCSFRun(tmpName)
			if runErr != nil {
				// Quirk preserved here: a genuine failure to run csf.pl
				// (e.g. the script missing entirely) falls through to the
				// SAME generic "Unable to create csf UI temp file" message
				// used for actual temp-file errors, mislabeling the real
				// cause.
				output = "Unable to create csf UI temp file: " + runErr.Error()
			} else {
				output = out
			}
		}
	}

	// csf.pl's own UI (Sentinel::DisplayUI) renders many independent
	// <form> elements on a single page -- one per action button/row, not
	// just one for the whole page -- so the token must be injected into
	// every one of them, not just the first.
	token := `<input type="hidden" name="csrf_token" value="` + csrf.Token(r) + `">`
	var b strings.Builder
	rest := output
	for {
		idx := strings.Index(rest, "<form")
		if idx == -1 {
			b.WriteString(rest)
			break
		}
		end := strings.Index(rest[idx:], ">")
		if end == -1 {
			b.WriteString(rest)
			break
		}
		insertAt := idx + end + 1
		b.WriteString(rest[:insertAt])
		b.WriteString(token)
		rest = rest[insertAt:]
	}
	output = b.String()

	w.Write([]byte(output))
}

// ServeFirewallSettings handles GET /security/firewall.
func (f *Firewall) ServeFirewallSettings(w http.ResponseWriter, r *http.Request) {
	if firewallCommandAvailableRun("csf") {
		webtemplates.Render(w, "security_csf.html", mergeChrome(map[string]interface{}{
			"CSRFToken": csrf.Token(r),
		}, r, "ConfigServer Firewall"))
		return
	}
	// Rendered through the same chrome-wrapped error page used for
	// 404s/panics elsewhere, rather than a bare 404 string with no
	// template.
	renderErrorPage(w, r, "ConfigServer Firewall (CSF) is not available on this system.", http.StatusNotFound)
}
