// This file implements the JSON REST API's raw-domain-file routes: viewing/
// replacing a domain's per-domain Caddy config snippet (validated and
// reloaded on save, like the DNS zone editor's flow in api_dns.go), viewing/
// replacing a user's raw webserver vhost file (restarted on save), and
// viewing/updating the default nginx/apache/varnish/caddy domain file
// templates used for new domains. Each reuses the same on-disk paths and
// podman plumbing as its HTML admin-page equivalent (domains_caddy_files.go,
// domains_vhost_files.go, domain_templates.go) -- only the response shape
// differs.
package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// APIDomainFiles bundles the /api/domains/{domain}/caddy,
// /api/domains/{domain}/vhost/{username} and /api/domains/file-templates
// handlers.
type APIDomainFiles struct {
	MySQL *sql.DB
}

// apiVHostRestartRun is injectable so tests never shell out to a real
// podman binary.
var apiVHostRestartRun = func(context, webserver string) error {
	return podmanFireAndForgetRun(context, "restart", webserver)
}

// ServeDomainCaddyConfig handles GET/POST /api/domains/{domain_name}/caddy.
func (a *APIDomainFiles) ServeDomainCaddyConfig(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	if !isDomain(domainName) {
		writeJSONError(w, http.StatusBadRequest, "Invalid domain name.")
		return
	}
	bindFilePath := filepath.Join(CaddyDomainsConfDir, domainName+".conf")

	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		contentVal, hasContent := body["content"]
		if !hasContent || contentVal == nil {
			writeJSONError(w, http.StatusBadRequest, "content is required")
			return
		}
		bindContent, isString := contentVal.(string)

		timestamp := caddyBackupTimestampRun()
		backupFilePath := filepath.Join(CaddyFileBackupDir, domainName+".conf.backup_"+timestamp)

		// Any failure in the backup/write/validate sequence below, including
		// a failed revert after a failed validation, is reported the same
		// way: a 500 with the failure text, never a bare crash. crashErr/
		// validationFailed/stderrOut model that block's three possible
		// outcomes.
		var stderrOut string
		var crashErr error
		validationFailed := false

		func() {
			if !isString {
				crashErr = fmt.Errorf("write() argument must be str")
				return
			}
			if fileExists(bindFilePath) {
				if err := copyFileWithMode(bindFilePath, backupFilePath); err != nil {
					crashErr = err
					return
				}
			}
			if err := os.WriteFile(bindFilePath, []byte(bindContent), 0644); err != nil {
				crashErr = err
				return
			}

			_, stderr, exitCode, verr := caddyValidateRun()
			if verr != nil {
				crashErr = verr
				return
			}
			if exitCode == 0 {
				// Reload result is intentionally not checked -- a failed
				// reload still counts as a successful save here.
				caddyReloadRun()
				if fileExists(backupFilePath) {
					os.Remove(backupFilePath)
				}
				return
			}

			stderrOut = stderr
			validationFailed = true
			if fileExists(backupFilePath) {
				if err := copyFileWithMode(backupFilePath, bindFilePath); err != nil {
					crashErr = err
				}
			}
		}()

		if crashErr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": crashErr.Error()})
			return
		}
		if validationFailed {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Caddyfile validation failed. Changes were reverted. Error: %s", stderrOut),
			})
			return
		}
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Caddyfile for domain %s saved successfully and reloaded.", domainName),
		})
		return
	}

	if !fileExists(bindFilePath) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Caddy file for domain %s not found", domainName))
		return
	}
	content, err := os.ReadFile(bindFilePath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"domain": domainName, "content": string(content)})
}

// ServeDomainVHostConfig handles GET/POST
// /api/domains/{domain_name}/vhost/{username}.
func (a *APIDomainFiles) ServeDomainVHostConfig(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	username := r.PathValue("username")

	context, err := queryContextByUsername(a.MySQL, username)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "No context found for user")
		return
	}
	// Any other lookup failure (e.g. MySQL unreachable) falls through with
	// an empty context: the resulting path just doesn't exist on disk,
	// producing the same "file not found" outcome below.

	bindFilePath := filepath.Join(VHostHomeDir, context, "docker-data", "volumes", context+"_webserver_data", "_data", domainName+".conf")

	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		contentVal, hasContent := body["content"]
		if !hasContent || contentVal == nil {
			writeJSONError(w, http.StatusBadRequest, "content is required")
			return
		}
		bindContent, isString := contentVal.(string)

		var crashErr error
		var webserver string
		func() {
			if !isString {
				crashErr = fmt.Errorf("write() argument must be str")
				return
			}
			if err := os.WriteFile(bindFilePath, []byte(bindContent), 0644); err != nil {
				crashErr = err
				return
			}
			webserver = getWebserverFor(context)
			// The restart result is intentionally not checked -- a failed
			// restart still counts as a successful save here.
			apiVHostRestartRun(context, webserver)
		}()

		if crashErr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": crashErr.Error()})
			return
		}
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("VirtualHosts file for %s saved successfully and %s restarted.", domainName, webserver),
		})
		return
	}

	if !fileExists(bindFilePath) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("VirtualHosts file for domain %s not found", domainName))
		return
	}
	content, err := os.ReadFile(bindFilePath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"domain": domainName, "content": string(content)})
}

// ServeDomainFileTemplates handles GET/POST /api/domains/file-templates.
func (a *APIDomainFiles) ServeDomainFileTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		for _, key := range domainTemplateFieldOrder {
			v, present := body[key]
			if !present || v == nil {
				continue
			}
			s, isString := v.(string)
			if !isString {
				// write_file()'s f.write(value) requires a str; a non-string
				// value would raise a TypeError here that nothing in this
				// route catches, so this crashes out the same way.
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			os.WriteFile(domainTemplateFilePaths[key], []byte(s), 0644)
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Templates updated successfully!"})
		return
	}

	fileContents := make(map[string]string, len(domainTemplateFieldOrder))
	for _, key := range domainTemplateFieldOrder {
		content, err := readFileOrEmpty(domainTemplateFilePaths[key])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
	}
	writeJSON(w, fileContents)
}
