// This file implements the per-domain Caddy config editor at
// /domains/caddy and /domains/caddy/<domain_name>. Nearly identical in
// shape to the DNS zone editor in domains_dns_zones.go (which also holds
// the podman-exec helpers shared by both).
package handlers

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// CaddyFileEditor bundles the /domains/caddy handlers.
type CaddyFileEditor struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// CaddyFileBackupDir is the /tmp prefix used for the Caddyfile's
// timestamped backups and their glob pattern. CaddyDomainsConfDir
// (domains.go) already holds the per-domain conf directory this editor
// reads and writes.
var CaddyFileBackupDir = "/tmp"

// caddyValidateRun is injectable so tests never shell out to a real
// podman/caddy binary. Note this intentionally validates the container's
// *whole* Caddyfile (/etc/openpanel/caddy/Caddyfile), not just the
// per-domain conf snippet that was just written -- that's the intended
// behavior, not a bug to "fix" here.
var caddyValidateRun = func() (stdout, stderr string, exitCode int, err error) {
	return podmanExecCapture("default", "exec", "caddy", "caddy", "validate", "--config", "/etc/openpanel/caddy/Caddyfile", "--adapter", "caddyfile")
}

// caddyReloadRun is injectable; issues a fire-and-forget podman exec into
// the caddy container to reload it. Its return value is intentionally not
// checked by callers.
var caddyReloadRun = func() error {
	return podmanFireAndForgetRun("default", "exec", "caddy", "caddy", "reload")
}

// caddyBackupTimestampRun is injectable so tests can assert on a fixed
// backup filename.
var caddyBackupTimestampRun = defaultBackupTimestamp

// ServeEditCaddyFile handles GET /domains/caddy, GET
// /domains/caddy/{domain_name} and POST /domains/caddy/{domain_name}.
func (h *CaddyFileEditor) ServeEditCaddyFile(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")

	if domainName == "" {
		domains, err := paneldb.GetAllDomains(h.MySQL)
		mysqlIsDown := err != nil
		if mysqlIsDown {
			domains = nil
		}
		webtemplates.Render(w, "domains_caddy_editor.html", mergeChrome(map[string]interface{}{
			"Domains":     domains,
			"MySQLIsDown": mysqlIsDown,
			"DomainName":  "",
			"CSRFToken":   csrf.Token(r),
			"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		}, r, "Edit Domain Caddyfile"))
		return
	}

	if !isDomain(domainName) {
		auth.AddFlash(w, r, h.Sessions, "Invalid domain name format.", "danger")
		http.Redirect(w, r, "/domains/caddy", http.StatusSeeOther)
		return
	}

	bindFilePath := filepath.Join(CaddyDomainsConfDir, domainName+".conf")
	backupGlobPattern := filepath.Join(CaddyFileBackupDir, domainName+".conf.backup_*")

	var tmpFileContent string
	var hasTmpFileContent bool
	if latest, ok := latestBackupFile(backupGlobPattern); ok {
		if content, err := os.ReadFile(latest); err == nil {
			tmpFileContent = string(content)
			hasTmpFileContent = true
		}
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		bindContentForm := r.PostFormValue("bind_content")
		timestamp := caddyBackupTimestampRun()
		backupFilePath := filepath.Join(CaddyFileBackupDir, domainName+".conf.backup_"+timestamp)

		procErr := func() error {
			if fileExists(bindFilePath) {
				if err := copyFileWithMode(bindFilePath, backupFilePath); err != nil {
					return err
				}
			}
			if err := os.WriteFile(bindFilePath, []byte(bindContentForm), 0644); err != nil {
				return err
			}

			_, stderr, exitCode, verr := caddyValidateRun()
			if verr != nil {
				return verr
			}
			if exitCode == 0 {
				if rerr := caddyReloadRun(); rerr != nil {
					return rerr
				}
				auth.AddFlash(w, r, h.Sessions, "Caddyfile for domain "+domainName+" saved successfully and reloaded.", "success")
				if fileExists(backupFilePath) {
					if err := os.Remove(backupFilePath); err != nil {
						return err
					}
				}
				return nil
			}

			// Validation failed: revert.
			if fileExists(backupFilePath) {
				if err := copyFileWithMode(backupFilePath, bindFilePath); err != nil {
					return err
				}
			}
			auth.AddFlash(w, r, h.Sessions, "Caddyfile validation failed. Changes were reverted. Error: "+stderr, "error")
			return nil
		}()

		if procErr != nil {
			// Unlike the DNS zone editor, this failure path is a single
			// flash with no backup-restore attempt -- kept deliberately
			// simple here rather than matching the DNS editor's richer
			// recovery.
			auth.AddFlash(w, r, h.Sessions, "Error saving Caddyfile for "+domainName+".", "error")
		}
	}

	var bindContent string
	if fileExists(bindFilePath) {
		content, err := os.ReadFile(bindFilePath)
		if err == nil {
			bindContent = string(content)
		}
	} else {
		auth.AddFlash(w, r, h.Sessions, "Error reading Caddy file for domain "+domainName+".", "error")
		bindContent = ""
	}

	webtemplates.Render(w, "domains_caddy_editor.html", mergeChrome(map[string]interface{}{
		"Domains":           nil,
		"DomainName":        domainName,
		"BindContent":       bindContent,
		"TmpFileContent":    tmpFileContent,
		"HasTmpFileContent": hasTmpFileContent,
		"CSRFToken":         csrf.Token(r),
		"Flashes":           auth.PopFlashes(w, r, h.Sessions),
	}, r, "Edit Domain Caddyfile"))
}
