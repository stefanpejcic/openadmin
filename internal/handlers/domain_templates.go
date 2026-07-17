// This file implements the domain suspended/default page + nginx/openresty/
// apache/varnish/caddy vhost template editor.
package handlers

import (
	"net/http"
	"os"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// DomainTemplates bundles the /domains/file-templates handler.
type DomainTemplates struct {
	Sessions *auth.Manager
}

// domainTemplateFilePaths maps each template field name to the file on disk
// that holds its content.
var domainTemplateFilePaths = map[string]string{
	"default_page":            "/etc/openpanel/nginx/default_page.html",
	"suspended_user":          "/etc/openpanel/nginx/suspended_user.html",
	"suspended_website":       "/etc/openpanel/nginx/suspended_website.html",
	"docker_nginx_domain":     "/etc/openpanel/nginx/vhosts/1.1/docker_nginx_domain.conf",
	"docker_openresty_domain": "/etc/openpanel/nginx/vhosts/1.1/docker_openresty_domain.conf",
	"docker_apache_domain":    "/etc/openpanel/nginx/vhosts/1.1/docker_apache_domain.conf",
	"docker_varnish":          "/etc/openpanel/varnish/default.vcl",
	"docker_caddy":            "/etc/openpanel/caddy/templates/domain.conf",
}

// domainTemplateFieldOrder fixes the field order since Go maps don't
// preserve one -- used for deterministic JSON output.
var domainTemplateFieldOrder = []string{
	"default_page", "suspended_user", "suspended_website",
	"docker_nginx_domain", "docker_openresty_domain", "docker_apache_domain",
	"docker_varnish", "docker_caddy",
}

// ServeDomainTemplates handles GET/POST /domains/file-templates: it saves
// posted template contents to disk and renders the current contents of each
// template file.
func (d *DomainTemplates) ServeDomainTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		for _, key := range domainTemplateFieldOrder {
			if formHasKey(r, key) {
				os.WriteFile(domainTemplateFilePaths[key], []byte(r.PostFormValue(key)), 0644)
			}
		}
		auth.AddFlash(w, r, d.Sessions, "Templates updated successfully!", "success")
	}

	fileContents := make(map[string]string, len(domainTemplateFieldOrder))
	for _, key := range domainTemplateFieldOrder {
		content, _ := readFileOrEmpty(domainTemplateFilePaths[key])
		fileContents[key] = content
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, fileContents)
		return
	}

	webtemplates.Render(w, "domains_templates.html", mergeChrome(map[string]interface{}{
		"DefaultPage":           fileContents["default_page"],
		"SuspendedUser":         fileContents["suspended_user"],
		"SuspendedWebsite":      fileContents["suspended_website"],
		"DockerNginxDomain":     fileContents["docker_nginx_domain"],
		"DockerOpenrestyDomain": fileContents["docker_openresty_domain"],
		"DockerApacheDomain":    fileContents["docker_apache_domain"],
		"DockerVarnish":         fileContents["docker_varnish"],
		"DockerCaddy":           fileContents["docker_caddy"],
		"CSRFToken":             csrf.Token(r),
		"Flashes":               auth.PopFlashes(w, r, d.Sessions),
	}, r, "Domain Templates"))
}
