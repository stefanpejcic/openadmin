// This file implements the custom CSS/JS/header/footer/dashboard-section/
// how-to-guides editor (Enterprise only) plus the Community-tier
// forbidden-usernames/domain-restriction/WordPress-sets/PageSpeed-key
// files.
package handlers

import (
	"net/http"
	"os"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/license"
	"openadmin/internal/webtemplates"
)

// CustomCode bundles the /settings/custom-code handler.
type CustomCode struct {
	Sessions       *auth.Manager
	LicenseChecker *license.Checker // nil on Community
}

// customCodeFilePaths maps each custom-code field to the file it is
// persisted to.
var customCodeFilePaths = map[string]string{
	"custom_css":          "/etc/openpanel/openpanel/custom_code/custom.css",
	"custom_js":           "/etc/openpanel/openpanel/custom_code/custom.js",
	"in_header":           "/etc/openpanel/openpanel/custom_code/in_header.html",
	"in_footer":           "/etc/openpanel/openpanel/custom_code/in_footer.html",
	"post_update":         "/root/openpanel_run_after_update",
	"pre_startup":         "/root/openpanel_run_on_startup",
	"custom_section":      "/etc/openpanel/openpanel/conf/custom_dashboard_section.json",
	"forbidden_usernames": "/etc/openpanel/openadmin/config/forbidden_usernames.txt",
	"restricted_domains":  "/etc/openpanel/openpanel/conf/domain_restriction.txt",
	"howto_guides":        "/etc/openpanel/openpanel/conf/knowledge_base_articles.json",
	"wp_themes":           "/etc/openpanel/wordpress/sets/themes.txt",
	"wp_plugins":          "/etc/openpanel/wordpress/sets/plugins.txt",
	"pagespeed_api_key":   "/etc/openpanel/openpanel/service/pagespeed.api",
}

// customCodeFieldOrder defines a stable field order, since Go maps don't
// preserve one -- used for deterministic template/JSON output.
var customCodeFieldOrder = []string{
	"custom_css", "custom_js", "in_header", "in_footer", "post_update",
	"pre_startup", "custom_section", "forbidden_usernames", "restricted_domains",
	"howto_guides", "wp_themes", "wp_plugins", "pagespeed_api_key",
}

// customCodeEnterpriseFields lists the fields that require an active
// Enterprise license and a non-reseller role to edit.
var customCodeEnterpriseFields = map[string]bool{
	"custom_css":     true,
	"custom_js":      true,
	"in_header":      true,
	"in_footer":      true,
	"custom_section": true,
	"howto_guides":   true,
}

// CustomCodeRestartFlagPath is the restart-flag file written
// unconditionally on every POST, regardless of which (if any) fields were
// actually submitted.
var CustomCodeRestartFlagPath = "/root/openpanel_restart_needed"

// hasEnterpriseAccess reports whether the current request has an active
// Enterprise license and the user is not a reseller.
func (c *CustomCode) hasEnterpriseAccess(r *http.Request) bool {
	if c.LicenseChecker == nil || !c.LicenseChecker.Valid() {
		return false
	}
	user := auth.CurrentUser(r)
	return user != nil && user.Role != "reseller"
}

// ServeCustomCode handles GET/POST /settings/custom-code: it renders the
// custom code editor and, on POST, persists any submitted fields to their
// backing files.
//
// SECURITY NOTE: only the Enterprise-only fields (custom_css, custom_js,
// in_header, in_footer, custom_section, howto_guides) require an active
// Enterprise license and non-reseller role before being written; the
// Community-tier fields are unrestricted.
func (c *CustomCode) ServeCustomCode(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		enterpriseOK := c.hasEnterpriseAccess(r)
		for _, key := range customCodeFieldOrder {
			if !formHasKey(r, key) {
				continue
			}
			if customCodeEnterpriseFields[key] && !enterpriseOK {
				continue
			}
			if err := os.WriteFile(customCodeFilePaths[key], []byte(r.PostFormValue(key)), 0644); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}

		if err := os.WriteFile(CustomCodeRestartFlagPath, []byte("Restart needed"), 0644); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		auth.AddFlash(w, r, c.Sessions, "Files updated successfully!", "success")
	}

	fileContents := make(map[string]string, len(customCodeFieldOrder))
	for _, key := range customCodeFieldOrder {
		content, err := readFileOrEmpty(customCodeFilePaths[key])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		fileContents[key] = content
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, fileContents)
		return
	}

	data := map[string]interface{}{
		"CSRFToken":         csrf.Token(r),
		"Flashes":           auth.PopFlashes(w, r, c.Sessions),
		"HasEnterprise":     c.hasEnterpriseAccess(r),
		"CustomCSS":         fileContents["custom_css"],
		"CustomJS":          fileContents["custom_js"],
		"InHeader":          fileContents["in_header"],
		"InFooter":          fileContents["in_footer"],
		"CustomSection":     fileContents["custom_section"],
		"HowtoGuides":       fileContents["howto_guides"],
		"PostUpdate":        fileContents["post_update"],
		"PreStartup":        fileContents["pre_startup"],
		"ForbiddenUsers":    fileContents["forbidden_usernames"],
		"RestrictedDomains": fileContents["restricted_domains"],
		"WPThemes":          fileContents["wp_themes"],
		"WPPlugins":         fileContents["wp_plugins"],
		"PagespeedAPIKey":   fileContents["pagespeed_api_key"],
	}
	webtemplates.Render(w, "settings_custom_code.html", mergeChrome(data, r, "Custom Code"))
}
