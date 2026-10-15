// This file implements the domain suspended/default page + nginx/openresty/
// apache/varnish/caddy vhost template editor.
package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	"suspended_user":          "/etc/openpanel/caddy/templates/suspended_user.html",
	"suspended_website":       "/etc/openpanel/caddy/templates/suspended_domain.html",
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
		if msg, kind := saveSuspensionTemplatesForm(r); msg != "" {
			auth.AddFlash(w, r, d.Sessions, msg, kind)
		} else {
			auth.AddFlash(w, r, d.Sessions, "Templates updated successfully!", "success")
		}
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
		"Reasons":               listSuspensionTemplates(),
		"CSRFToken":             csrf.Token(r),
		"Flashes":               auth.PopFlashes(w, r, d.Sessions),
	}, r, "Domain Templates"))
}

// saveSuspensionTemplatesForm handles the reason rows, returns a flash only when something needs saying
func saveSuspensionTemplatesForm(r *http.Request) (string, string) {
	for _, t := range listSuspensionTemplates() {
		name, hasName := r.PostForm["reason_name__"+t.Slug]
		content, hasContent := r.PostForm["reason_content__"+t.Slug]
		if !hasName || !hasContent {
			continue
		}
		if cleanSuspensionName(name[0]) == "" {
			name[0] = t.Name
		}
		saveSuspensionTemplate(t.Slug, name[0], content[0])
	}

	if del := r.PostFormValue("delete_reason"); del != "" {
		if !suspensionTemplateExists(del) {
			return "Suspension reason not found.", "error"
		}
		if inUse := suspensionTemplateInUse(del); len(inUse) > 0 {
			return "Can't delete, these domains are suspended with it: " + strings.Join(inUse, ", "), "error"
		}
		os.Remove(filepath.Join(SuspensionTemplatesDir, del+".html"))
		return "Suspension reason deleted.", "success"
	}

	if name := cleanSuspensionName(r.PostFormValue("new_reason_name")); name != "" {
		slug := suspensionSlugFromName(name)
		if slug == "" {
			return "Reason name needs at least one letter or number.", "error"
		}
		if suspensionTemplateExists(slug) {
			return "A suspension reason with that name already exists.", "error"
		}
		content := r.PostFormValue("new_reason_content")
		if strings.TrimSpace(content) == "" {
			content, _ = readFileOrEmpty(domainTemplateFilePaths["suspended_website"])
		}
		if err := saveSuspensionTemplate(slug, name, content); err != nil {
			return "Failed to save suspension reason: " + err.Error(), "error"
		}
		return "Suspension reason '" + name + "' added.", "success"
	}
	return "", ""
}
