// This file implements listing domains (with SSL/status/WAF/HSTS
// detection), adding a domain, and the feature-toggle actions
// (waf/hsts/dns/suspend/unsuspend/delete). Deliberately out of scope for
// this pass (see the migration backlog): the DNS zone editor, Caddyfile
// editor, VirtualHosts editor, SSL certificate management page, access log
// viewer, and GoAccess stats viewer -- each of those is its own substantial
// file-editing page.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Domains bundles the /domains handlers.
type Domains struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// CaddyDomainsConfDir is the directory holding each domain's per-domain
// Caddy config snippet.
var CaddyDomainsConfDir = "/etc/openpanel/caddy/domains"

// readCaddyFileForDomain does a substring-sniffing read of a domain's
// Caddy config, not a real parse.
func readCaddyFileForDomain(domainURL string) (ssl, status, waf, hsts string) {
	ssl, status, waf, hsts = "none", "suspended", "none", "off"

	content, err := os.ReadFile(CaddyDomainsConfDir + "/" + domainURL + ".conf")
	if err != nil {
		return ssl, status, waf, hsts
	}
	s := string(content)

	switch {
	case strings.Contains(s, "on_demand"):
		ssl = "automatic"
	case strings.Contains(s, "fullchain.pem"):
		ssl = "custom"
	}

	switch {
	case strings.Contains(s, "reverse_proxy"):
		status = "active"
	case strings.Contains(s, "file_server"):
		status = "suspended"
	}

	switch {
	case strings.Contains(s, "SecRuleEngine On"):
		waf = "on"
	case strings.Contains(s, "SecRuleEngine Off"):
		waf = "off"
	}

	if strings.Contains(s, "Strict-Transport-Security") {
		hsts = "on"
	}

	return ssl, status, waf, hsts
}

type domainsListPageData struct {
	webtemplates.Chrome
	Domains       []paneldb.RowMap
	PHPVersions   map[string]interface{}
	MySQLIsDown   bool
	SortCol       string
	SortDirection string
	CSRFToken     string
	Flashes       []auth.Flash
}

// ServeList handles GET /domains, /domains/.
func (d *Domains) ServeList(w http.ResponseWriter, r *http.Request) {
	domains, err := paneldb.GetAllDomains(d.MySQL)
	mysqlIsDown := err != nil
	if mysqlIsDown {
		domains = nil
	}

	for _, dom := range domains {
		domainURL, _ := dom["domain_url"].(string)
		ssl, status, waf, hsts := readCaddyFileForDomain(domainURL)
		dom["ssl"] = ssl
		dom["status"] = status
		dom["waf"] = waf
		dom["hsts"] = hsts
	}

	columnKeyMap := map[string]string{
		"id": "domain_id", "name": "domain_url", "docroot": "docroot",
		"status": "status", "php": "php_version", "waf": "waf",
		"ssl": "ssl", "hsts": "hsts", "owner": "username",
	}
	if sortCol := r.URL.Query().Get("sort"); sortCol != "" {
		if key, ok := columnKeyMap[sortCol]; ok {
			desc := strings.EqualFold(r.URL.Query().Get("direction"), "desc")
			sortRowMaps(domains, key, desc)
		}
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{"domains": domains})
		return
	}

	webtemplates.Render(w, "domains_list.html", domainsListPageData{
		Chrome:        buildChrome(r, "Domains"),
		Domains:       domains,
		PHPVersions:   phpVersionsEOL(),
		MySQLIsDown:   mysqlIsDown,
		SortCol:       r.URL.Query().Get("sort"),
		SortDirection: r.URL.Query().Get("direction"),
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, d.Sessions),
	})
}

// HandleAdd handles POST /domains/add.
func (d *Domains) HandleAdd(w http.ResponseWriter, r *http.Request) {
	domain := r.FormValue("domain")
	username := r.FormValue("username")

	if domain == "" || username == "" {
		auth.AddFlash(w, r, d.Sessions, "Domain and username are required.", "error")
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}

	success, output := runOpenCLI("", "opencli", "domains-add", domain, username)
	if success {
		auth.AddFlash(w, r, d.Sessions, "Domain \""+domain+"\" added for user \""+username+"\".", "info")
	} else {
		auth.AddFlash(w, r, d.Sessions, "Failed to add domain: "+output, "error")
	}
	http.Redirect(w, r, "/domains", http.StatusSeeOther)
}

var dnsAllowedActions = map[string]bool{
	"check": true, "reload": true, "create": true, "delete": true, "default": true,
}

// HandleToggleFeature handles POST /domains/{feature}/toggle, mirroring
// toggle_feature().
func (d *Domains) HandleToggleFeature(w http.ResponseWriter, r *http.Request) {
	feature := strings.ToLower(r.PathValue("feature"))
	domainName := r.FormValue("domain_name")
	domain := strings.SplitN(domainName, "/", 2)[0]

	var cliArgs []string
	var action string

	switch feature {
	case "waf":
		action = "disable"
		if r.FormValue("modsec_action") == "On" {
			action = "enable"
		}
		cliArgs = []string{"opencli", "waf", "domain", domain, action}

	case "hsts":
		action = "disable"
		if r.FormValue("hsts_action") == "On" {
			action = "enable"
		}
		cliArgs = []string{"opencli", "domains-hsts", domain, action}

	case "dns":
		newStatus := r.FormValue("dns_action")
		if !dnsAllowedActions[newStatus] {
			auth.AddFlash(w, r, d.Sessions, "Error: '"+newStatus+"' is not a valid DNS action", "error")
			http.Redirect(w, r, "/domains", http.StatusSeeOther)
			return
		}
		cliArgs = []string{"opencli", "domains-dns", newStatus, domain}
		switch {
		case strings.HasSuffix(newStatus, "e"):
			action = newStatus + "d"
		case newStatus == "check":
			action = "checked"
		default:
			action = newStatus + "ed"
		}

	case "suspend":
		cliArgs = []string{"opencli", "domains-suspend", domain}
		action = "executed"
	case "unsuspend":
		cliArgs = []string{"opencli", "domains-unsuspend", domain}
		action = "executed"
	case "delete":
		cliArgs = []string{"opencli", "domains-delete", domain}
		action = "executed"

	default:
		auth.AddFlash(w, r, d.Sessions, "Unknown feature: "+feature, "error")
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}

	success, output := runOpenCLI("", cliArgs...)
	if success {
		auth.AddFlash(w, r, d.Sessions, "Successfully "+action+" "+strings.ToUpper(feature)+" for domain "+domain, "info")
	} else {
		auth.AddFlash(w, r, d.Sessions, "Failed to "+action+" "+strings.ToUpper(feature)+" for domain: "+output, "error")
	}
	http.Redirect(w, r, "/domains", http.StatusSeeOther)
}

// --- PHP version EOL info (cached, matches @lru_cache(maxsize=1)) ---

var (
	phpVersionsOnce sync.Once
	phpVersionsData map[string]interface{}
)

// PHPVersionsEOLURL is the upstream endpoint for PHP version end-of-life data.
var PHPVersionsEOLURL = "https://api.openpanel.com/php-versions/"

// phpVersionsEOLFetch is a var (not a direct call) so tests can stub out
// the real network call -- consistent with the other external-command vars
// in this package (dropCacheRun, timedatectlRun, etc.).
var phpVersionsEOLFetch = fetchPHPVersionsEOL

// phpVersionsEOL fetches once per process lifetime and never expires within
// that lifetime, keyed by PHP version name.
func phpVersionsEOL() map[string]interface{} {
	phpVersionsOnce.Do(func() {
		phpVersionsData = phpVersionsEOLFetch()
	})
	return phpVersionsData
}

func fetchPHPVersionsEOL() map[string]interface{} {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(PHPVersionsEOLURL)
	if err != nil {
		return map[string]interface{}{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return map[string]interface{}{}
	}

	var parsed struct {
		Data map[string]struct {
			Name            string `json:"name"`
			StatusLabel     string `json:"statusLabel"`
			IsEOLVersion    bool   `json:"isEOLVersion"`
			IsSecureVersion bool   `json:"isSecureVersion"`
			IsLatestVersion bool   `json:"isLatestVersion"`
			IsFutureVersion bool   `json:"isFutureVersion"`
			IsNextVersion   bool   `json:"isNextVersion"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&parsed) != nil {
		return map[string]interface{}{}
	}

	out := make(map[string]interface{}, len(parsed.Data))
	for _, v := range parsed.Data {
		out[v.Name] = map[string]interface{}{
			"statusLabel":     v.StatusLabel,
			"isEOLVersion":    v.IsEOLVersion,
			"isSecureVersion": v.IsSecureVersion,
			"isLatestVersion": v.IsLatestVersion,
			"isFutureVersion": v.IsFutureVersion,
			"isNextVersion":   v.IsNextVersion,
		}
	}
	return out
}
