// This file implements the IPv4/IPv6 BIND zone template editor.
package handlers

import (
	"net/http"
	"os"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// DNSTemplates bundles the /domains/zone-templates handler.
type DNSTemplates struct {
	Sessions *auth.Manager
}

// DNSZoneTemplateIPv4Path / DNSZoneTemplateIPv6Path are the on-disk BIND
// zone template files this handler edits.
var (
	DNSZoneTemplateIPv4Path = "/etc/openpanel/bind9/zone_template.txt"
	DNSZoneTemplateIPv6Path = "/etc/openpanel/bind9/zone_template_ipv6.txt"
)

// ServeDNSZoneTemplates handles GET/POST /domains/zone-templates.
func (d *DNSTemplates) ServeDNSZoneTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		if formHasKey(r, "zone_template_ipv4") {
			os.WriteFile(DNSZoneTemplateIPv4Path, []byte(r.PostFormValue("zone_template_ipv4")), 0644)
		}
		if formHasKey(r, "zone_template_ipv6") {
			os.WriteFile(DNSZoneTemplateIPv6Path, []byte(r.PostFormValue("zone_template_ipv6")), 0644)
		}
		auth.AddFlash(w, r, d.Sessions, "Template updated successfully!", "success")
	}

	ipv4, _ := readFileOrEmpty(DNSZoneTemplateIPv4Path)
	ipv6, _ := readFileOrEmpty(DNSZoneTemplateIPv6Path)

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]string{
			"zone_template_ipv4": ipv4,
			"zone_template_ipv6": ipv6,
		})
		return
	}

	webtemplates.Render(w, "domains_dns_templates.html", mergeChrome(map[string]interface{}{
		"ZoneTemplateIPv4": ipv4,
		"ZoneTemplateIPv6": ipv6,
		"CSRFToken":        csrf.Token(r),
		"Flashes":          auth.PopFlashes(w, r, d.Sessions),
	}, r, "DNS Zone Templates"))
}
