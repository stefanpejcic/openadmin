// This file implements the JSON REST API's domain-management surface:
// GET /api/domains, POST /api/domains/new, POST /api/domains/{action}/{domain},
// and GET/POST /api/domains/docroot/{domain}.
package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"

	"openadmin/internal/paneldb"
)

// APIDomains bundles the /api/domains handlers.
type APIDomains struct {
	MySQL *sql.DB
}

// apiRunCapture runs an opencli invocation, capturing stdout and stderr
// separately and reporting the process's exit code -- mirroring
// subprocess.run(..., capture_output=True) rather than check_output: the
// caller always gets both streams back verbatim regardless of whether the
// command succeeded.
var apiRunCapture = func(args ...string) (stdout, stderr string, returncode int) {
	cmd := exec.Command(args[0], args[1:]...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		// The command itself never started (e.g. the binary is missing).
		// There's no real exit code in that case; -1 just signals "not a
		// normal exit" to callers, same as they'd treat any other failure.
		return outBuf.String(), errBuf.String(), -1
	}
	return outBuf.String(), errBuf.String(), 0
}

func writeAPIRunResult(w http.ResponseWriter, stdout, stderr string, returncode int) {
	writeJSON(w, map[string]interface{}{
		"stdout":     stdout,
		"stderr":     stderr,
		"returncode": returncode,
	})
}

// ServeDomains handles GET /api/domains.
func (a *APIDomains) ServeDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := paneldb.GetAllDomains(a.MySQL)
	if err != nil || domains == nil {
		domains = []paneldb.RowMap{}
	}
	writeJSON(w, map[string]interface{}{"domains": domains})
}

// HandleAddDomain handles POST /api/domains/new.
func (a *APIDomains) HandleAddDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Domain   string `json:"domain"`
		Docroot  string `json:"docroot"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSONError(w, http.StatusInternalServerError, "Invalid JSON body")
		return
	}

	if body.Docroot == "" {
		body.Docroot = "/var/www/html/" + body.Domain
	}

	if body.Username == "" || body.Domain == "" {
		writeJSONError(w, http.StatusBadRequest, "username and domain are required")
		return
	}
	if !strings.HasPrefix(body.Docroot, "/var/www/html/") {
		writeJSONError(w, http.StatusBadRequest, "docroot must start with /var/www/html/")
		return
	}

	stdout, stderr, returncode := apiRunCapture("opencli", "domains-add", body.Domain, body.Username, "--docroot", body.Docroot)
	writeAPIRunResult(w, stdout, stderr, returncode)
}

var apiDomainActionMap = map[string]string{
	"suspend":   "domains-suspend",
	"unsuspend": "domains-unsuspend",
	"delete":    "domains-delete",
}

// HandleDomainAction handles POST /api/domains/{action}/{domain}.
func (a *APIDomains) HandleDomainAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	domain := r.PathValue("domain")

	if domain == "" {
		writeJSONError(w, http.StatusBadRequest, "domain is required")
		return
	}

	cliCmd, ok := apiDomainActionMap[strings.ToLower(action)]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use suspend, unsuspend, or delete")
		return
	}

	stdout, stderr, returncode := apiRunCapture("opencli", cliCmd, domain)
	writeAPIRunResult(w, stdout, stderr, returncode)
}

// ServeDomainDocroot handles GET/POST /api/domains/docroot/{domain}.
func (a *APIDomains) ServeDomainDocroot(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		writeJSONError(w, http.StatusBadRequest, "domain is required")
		return
	}

	var stdout, stderr string
	var returncode int

	if r.Method == http.MethodPost {
		var body struct {
			Docroot string `json:"docroot"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeJSONError(w, http.StatusInternalServerError, "Invalid JSON body")
			return
		}
		if body.Docroot == "" {
			writeJSONError(w, http.StatusBadRequest, "docroot is required")
			return
		}
		if !strings.HasPrefix(body.Docroot, "/var/www/html/") {
			writeJSONError(w, http.StatusBadRequest, "docroot must start with /var/www/html/")
			return
		}
		stdout, stderr, returncode = apiRunCapture("opencli", "domains-docroot", domain, "update", body.Docroot)
	} else {
		stdout, stderr, returncode = apiRunCapture("opencli", "domains-docroot", domain)
	}

	writeAPIRunResult(w, stdout, stderr, returncode)
}
