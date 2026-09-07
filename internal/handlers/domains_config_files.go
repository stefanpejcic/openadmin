// This file implements the per-user main webserver configuration file
// editor at /domains/config and /domains/config/<username>. Structurally
// mirrors the VHost file editor (domains_vhost_files.go): it reads
// WEB_SERVER out of the user's .env to know which file to edit and which
// container to restart, writes the file on POST, then restarts that
// user's webserver container. Like the VHost editor (and unlike the Caddy
// editor), there is no validate-then-revert-on-failure step.
package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// ConfigFileEditor bundles the /domains/config handlers.
type ConfigFileEditor struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// webserverMainConfFilenames maps a WEB_SERVER value to its main
// configuration file, which lives directly under /home/<context>/ (a
// sibling of docker-compose.yml and .env) per OpenPanel's own structure
// docs -- distinct from the per-domain VirtualHost files under
// /home/<context>/docker-data/volumes/<context>_webserver_data/_data/
// handled by the VHost editor. Hardcoded rather than discovered because
// nothing on disk otherwise identifies these as "the main conf file".
var webserverMainConfFilenames = map[string]string{
	"apache":        "httpd.conf",
	"nginx":         "nginx.conf",
	"openresty":     "openresty.conf",
	"openlitespeed": "openlitespeed.conf",
	"litespeed":     "openlitespeed.conf",
}

// mainConfPathFor returns the absolute path to context's main webserver
// config file, or "" if webserver isn't a recognized value.
func mainConfPathFor(context, webserver string) string {
	filename, ok := webserverMainConfFilenames[webserver]
	if !ok {
		return ""
	}
	return filepath.Join("/home", context, filename)
}

// ServeEditConfigFile handles GET /domains/config, GET
// /domains/config/{username} and POST /domains/config/{username}.
func (h *ConfigFileEditor) ServeEditConfigFile(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")

	if username == "" {
		domains, err := paneldb.GetAllDomains(h.MySQL)
		mysqlIsDown := err != nil
		if mysqlIsDown {
			domains = nil
		}
		webtemplates.Render(w, "domains_config_editor.html", mergeChrome(map[string]interface{}{
			"Domains":     domains,
			"MySQLIsDown": mysqlIsDown,
			"Username":    "",
			"CSRFToken":   csrf.Token(r),
			"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		}, r, "Edit Main Webserver Configuration"))
		return
	}

	// queryContextByUsername returns an empty string when MySQL is down or
	// there's no such user, which produces a nonexistent path
	// ("/home//..."), so the observable outcome (file not found -> error
	// flash below) is the same as if it had returned something more
	// descriptive.
	context, _ := queryContextByUsername(h.MySQL, username)
	webserver := readEnvFile(context)["WEB_SERVER"]
	confPath := mainConfPathFor(context, webserver)

	if r.Method == http.MethodPost {
		r.ParseForm()
		confContentForm := r.PostFormValue("conf_content")

		procErr := func() error {
			if confPath == "" {
				return fmt.Errorf("unrecognized webserver %q for user %q", webserver, username)
			}
			if err := os.WriteFile(confPath, []byte(confContentForm), 0644); err != nil {
				return err
			}
			if err := podmanFireAndForgetRun(context, "restart", webserver); err != nil {
				return err
			}
			auth.AddFlash(w, r, h.Sessions, "Main configuration file for "+username+" saved successfully and "+webserver+" restarted.", "success")
			return nil
		}()

		if procErr != nil {
			auth.AddFlash(w, r, h.Sessions, "Error saving main configuration file for "+username+".", "error")
		}
	}

	var confContent string
	if confPath != "" && fileExists(confPath) {
		content, err := os.ReadFile(confPath)
		if err == nil {
			confContent = string(content)
		}
	} else {
		auth.AddFlash(w, r, h.Sessions, "Error reading main configuration file for user "+username+".", "error")
	}

	webtemplates.Render(w, "domains_config_editor.html", mergeChrome(map[string]interface{}{
		"Domains":     nil,
		"Username":    username,
		"Webserver":   webserver,
		"ConfContent": confContent,
		"CSRFToken":   csrf.Token(r),
		"Flashes":     auth.PopFlashes(w, r, h.Sessions),
	}, r, "Edit Main Webserver Configuration"))
}
