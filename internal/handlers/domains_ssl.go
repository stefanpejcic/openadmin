// This file implements the per-domain SSL certificate management page at
// GET|POST /domains/ssl/<domain_name>. The certificate-details panel is
// parsed entirely client-side (an inline <script type="module"> using
// esm.sh-hosted asn1js/pkijs) -- the handler itself never touches
// crypto/x509 or any cert-parsing library, it just shells out to
// `opencli domains-ssl <domain> info` and hands the raw text through.
//
// IMPORTANT: this file deliberately reproduces two genuine, serious bugs
// rather than fixing them, since real users may already depend on (or have
// worked around) this exact behavior:
//
//  1. Every POST with action=custom or action=autossl ends by trying to
//     redirect to a route name that doesn't actually exist anywhere in this
//     app. Building that URL always fails, turning into a bare 500 response.
//     So today, clicking "Switch back to AutoSSL" or submitting the
//     custom-certificate form always 500s -- the underlying opencli command
//     still runs and its flash message is still queued before the crash,
//     but the user only sees it if they separately navigate to another page
//     afterward.
//  2. In the GET-rendering code, `keys` is only ever assigned once the SSL
//     status check succeeds; if the `opencli domains-ssl <domain> status`
//     call fails (nonzero exit, or the subprocess call itself errors),
//     `keys` is referenced before it's ever assigned, which is an unhandled
//     crash -> another bare 500. In other words, this page can normally
//     only render successfully when the domain's SSL status check
//     currently succeeds.
//
// Both are reproduced here via a deliberate panic(), letting the existing
// RecoverMiddleware (errors.go) render the same generic error page an
// unhandled exception would.
package handlers

import (
	"bytes"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// SSLPage bundles the /domains/ssl handlers.
type SSLPage struct {
	Sessions *auth.Manager
}

// sslCustomKeysHomeDir is the required containment directory for
// custom-SSL key/cert paths.
const sslCustomKeysHomeDir = "/var/www/html/"

// opencliSSLRun runs `opencli domains-ssl <args...>` and captures its
// stdout/stderr/exit code. Injectable so tests never shell out to a real
// opencli binary.
var opencliSSLRun = func(args ...string) (stdout, stderr string, exitCode int, err error) {
	cmdArgs := append([]string{"domains-ssl"}, args...)
	cmd := exec.Command("opencli", cmdArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), 0, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// resolveSSLKeyPath makes the path absolute (relative to the process's
// cwd) and normalizes "." / ".." segments. It does not additionally resolve
// symlinks for existing path components -- a minor simplification that
// doesn't weaken the traversal check below, since ".." normalization is
// what actually matters for the containment test.
func resolveSSLKeyPath(raw string) string {
	abs, err := filepath.Abs(raw)
	if err != nil {
		abs = raw
	}
	return filepath.Clean(abs)
}

// isRelativeToSSLHomeDir does a component-wise prefix check, not a naive
// string prefix (so "/var/www/html-evil/x" does not count as relative to
// "/var/www/html/").
func isRelativeToSSLHomeDir(resolved string) bool {
	return resolved == strings.TrimSuffix(sslCustomKeysHomeDir, "/") ||
		strings.HasPrefix(resolved+"/", sslCustomKeysHomeDir)
}

// panicDomainCustomSSLBuildError reproduces the URL-build crash described
// in the file-level comment.
func panicDomainCustomSSLBuildError() {
	panic("could not build url for endpoint 'domain_custom_ssl' (no such endpoint)")
}

// ServeSSL handles GET|POST /domains/ssl/{domain_name}.
func (h *SSLPage) ServeSSL(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")

	if !isDomain(domainName) {
		auth.AddFlash(w, r, h.Sessions, "Invalid domain name format.", "danger")
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		action := r.PostFormValue("action")

		switch action {
		case "custom":
			// Python's truthiness check here ("not public_path or not
			// private_path") can never fire: Path(...).resolve() always
			// returns a truthy Path, even for an empty input string
			// (resolves to the cwd). That branch is dead code in the
			// original and is not reproduced.
			publicPath := resolveSSLKeyPath(r.PostFormValue("public_path"))
			privatePath := resolveSSLKeyPath(r.PostFormValue("private_path"))

			if !isRelativeToSSLHomeDir(publicPath) {
				auth.AddFlash(w, r, h.Sessions, "Public key path must be inside '/var/www/html/' directory.", "error")
				panicDomainCustomSSLBuildError()
			}
			if !isRelativeToSSLHomeDir(privatePath) {
				auth.AddFlash(w, r, h.Sessions, "Private key path must be inside '/var/www/html/' directory.", "error")
				panicDomainCustomSSLBuildError()
			}

			stdout, stderr, exitCode, err := opencliSSLRun(domainName, "custom", publicPath, privatePath)
			if err == nil && exitCode == 0 {
				auth.AddFlash(w, r, h.Sessions, strings.TrimSpace(stdout), "success")
			} else if err == nil {
				auth.AddFlash(w, r, h.Sessions, strings.TrimSpace(stderr), "error")
			}
			panicDomainCustomSSLBuildError()

		case "autossl":
			stdout, stderr, exitCode, err := opencliSSLRun(domainName, "auto")
			if err == nil && exitCode == 0 {
				auth.AddFlash(w, r, h.Sessions, strings.TrimSpace(stdout), "success")
			} else if err == nil {
				auth.AddFlash(w, r, h.Sessions, strings.TrimSpace(stderr), "error")
			}
			panicDomainCustomSSLBuildError()

		case "logs":
			stdout, _, exitCode, err := opencliSSLRun(domainName, "logs", "1000")
			logs := ""
			if err == nil && exitCode == 0 {
				logs = strings.TrimSpace(stdout)
			}
			writeJSON(w, map[string]interface{}{"logs": logs})
			return

		default:
			auth.AddFlash(w, r, h.Sessions, "Invalid action! Only AutoSSL or Custom are available.", "error")
			// No return here: Python's else branch has no return either,
			// so control falls through to the status/info render below.
		}
	}

	var currentSetting, keys string
	var currentSettingSet, keysSet bool

	statusStdout, statusStderr, statusExit, statusErr := opencliSSLRun(domainName, "status")
	switch {
	case statusErr != nil:
		auth.AddFlash(w, r, h.Sessions, "An error occurred: "+statusErr.Error(), "error")
	case statusExit == 0:
		currentSetting = strings.ToLower(strings.TrimSpace(statusStdout))
		currentSettingSet = true

		infoStdout, _, infoExit, infoErr := opencliSSLRun(domainName, "info")
		if infoErr != nil {
			auth.AddFlash(w, r, h.Sessions, "An error occurred: "+infoErr.Error(), "error")
		} else {
			if infoExit == 0 {
				keys = strings.TrimSpace(infoStdout)
			} else {
				keys = ""
			}
			keysSet = true
		}
	default:
		currentSetting = ""
		currentSettingSet = true
		auth.AddFlash(w, r, h.Sessions, strings.TrimSpace(statusStderr), "error")
	}

	// See the file-level comment: this reproduces a genuine
	// referenced-before-assignment crash.
	if !currentSettingSet {
		panic("UnboundLocalError: cannot access local variable 'current_setting' where it is not associated with a value")
	}
	if !keysSet {
		panic("UnboundLocalError: cannot access local variable 'keys' where it is not associated with a value")
	}

	webtemplates.Render(w, "domains_ssl.html", mergeChrome(map[string]interface{}{
		"DomainName":     domainName,
		"CurrentSetting": currentSetting,
		"Keys":           keys,
		"CSRFToken":      csrf.Token(r),
		"Flashes":        auth.PopFlashes(w, r, h.Sessions),
	}, r, "SSL for "+domainName))
}
