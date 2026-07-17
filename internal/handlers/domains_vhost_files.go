// This file implements the per-domain VirtualHost file editor at
// /domains/vhost and /domains/vhost/<username>/<domain_name>. Structurally
// similar to the DNS zone editor and Caddy file editor (domains_dns_zones.go,
// domains_caddy_files.go), whose fileExists/podmanFireAndForgetRun helpers
// this file reuses -- but notably simpler: there is no validate-then-revert-
// on-failure step or backup/restore-from-tmp-file banner for this editor,
// so neither is implemented here.
package handlers

import (
	"bufio"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// VHostFileEditor bundles the /domains/vhost handlers.
type VHostFileEditor struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// VHostHomeDir is the home directory prefix used to locate both a
// VirtualHost file's bind path and its context's .env file.
var VHostHomeDir = "/home"

// getWebserverFor reads WEB_SERVER= out of /home/<context>/.env to learn
// which webserver container to restart after saving a VirtualHost file.
// Note the quirk preserved here rather than fixed: on a missing .env file
// it returns the literal string ".env file not found." as the "webserver"
// value (not an error/empty result) -- that string then flows straight
// into the podman restart call and the success-flash message below.
func getWebserverFor(context string) string {
	envPath := filepath.Join(VHostHomeDir, context, ".env")
	f, err := os.Open(envPath)
	if err != nil {
		return ".env file not found."
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.Index(trimmed, "=")
		if idx < 0 {
			// A line without '=' is skipped rather than treated as an
			// error: a real .env file always has '=' on every
			// non-comment line, so this would only matter for a
			// hand-corrupted file, and introducing a panic path for
			// that isn't worth it.
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		if key == "WEB_SERVER" {
			value := strings.TrimSpace(trimmed[idx+1:])
			value = strings.Trim(value, `'"`)
			return value
		}
	}
	return ""
}

// ServeEditVHostFile handles GET /domains/vhost, GET
// /domains/vhost/{username}/{domain_name} and POST
// /domains/vhost/{username}/{domain_name}.
func (h *VHostFileEditor) ServeEditVHostFile(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	domainName := r.PathValue("domain_name")

	if domainName == "" || username == "" {
		domains, err := paneldb.GetAllDomains(h.MySQL)
		mysqlIsDown := err != nil
		if mysqlIsDown {
			domains = nil
		}
		webtemplates.Render(w, "domains_vhost_editor.html", mergeChrome(map[string]interface{}{
			"Domains":     domains,
			"MySQLIsDown": mysqlIsDown,
			"DomainName":  "",
			"CSRFToken":   csrf.Token(r),
			"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		}, r, "Edit Domain VirtualHosts file"))
		return
	}

	if !isDomain(domainName) {
		auth.AddFlash(w, r, h.Sessions, "Invalid domain name format.", "danger")
		http.Redirect(w, r, "/domains/vhost", http.StatusSeeOther)
		return
	}

	// queryContextByUsername returns an empty string when MySQL is down or
	// there's no such user, which produces a nonexistent path
	// ("/home//docker-data/..."), so the observable outcome (file not
	// found -> error flash below) is the same as if it had returned
	// something more descriptive.
	context, _ := queryContextByUsername(h.MySQL, username)
	bindFilePath := filepath.Join(VHostHomeDir, context, "docker-data", "volumes", context+"_webserver_data", "_data", domainName+".conf")

	if r.Method == http.MethodPost {
		r.ParseForm()
		bindContentForm := r.PostFormValue("bind_content")

		procErr := func() error {
			if err := os.WriteFile(bindFilePath, []byte(bindContentForm), 0644); err != nil {
				return err
			}
			webserver := getWebserverFor(context)
			if err := podmanFireAndForgetRun(context, "restart", webserver); err != nil {
				return err
			}
			auth.AddFlash(w, r, h.Sessions, "VirtualHosts file for "+domainName+" saved successfully and "+webserver+" restarted.", "success")
			return nil
		}()

		if procErr != nil {
			auth.AddFlash(w, r, h.Sessions, "Error saving VirtualHosts file for "+domainName+".", "error")
		}
	}

	var bindContent string
	if fileExists(bindFilePath) {
		content, err := os.ReadFile(bindFilePath)
		if err == nil {
			bindContent = string(content)
		}
	} else {
		auth.AddFlash(w, r, h.Sessions, "Error reading VirtualHosts file for domain "+domainName+".", "error")
		bindContent = ""
	}

	webtemplates.Render(w, "domains_vhost_editor.html", mergeChrome(map[string]interface{}{
		"Domains":     nil,
		"DomainName":  domainName,
		"BindContent": bindContent,
		"CSRFToken":   csrf.Token(r),
		"Flashes":     auth.PopFlashes(w, r, h.Sessions),
	}, r, "Edit Domain VirtualHosts file"))
}
