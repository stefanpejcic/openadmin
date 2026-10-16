// This file implements /domains/ratelimit: the server default and per-domain caddy rate limits, plus usage read from the access logs, all through opencli domains-ratelimit.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// DomainsRateLimit bundles the /domains/ratelimit handlers.
type DomainsRateLimit struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

type rateLimitDefault struct {
	PerIP   int `json:"per_ip"`
	PerSite int `json:"per_site"`
}

// rateLimitRow is one domain, Mode is default, custom or off and the limits are the ones that apply, default already resolved
type rateLimitRow struct {
	Domain  string `json:"domain"`
	Owner   string `json:"owner"`
	Mode    string `json:"mode"`
	PerIP   int    `json:"per_ip"`
	PerSite int    `json:"per_site"`
}

type rateLimitStatus struct {
	Default rateLimitDefault `json:"default"`
	Domains []rateLimitRow   `json:"domains"`
}

type rateLimitBlockedIP struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
}

// rateLimitStats is one domain's usage over the last Hours
type rateLimitStats struct {
	Domain       string               `json:"domain"`
	Hours        int                  `json:"hours"`
	Requests     int                  `json:"requests"`
	Blocked      int                  `json:"blocked"`
	PeakMinute   int                  `json:"peak_minute"`
	PeakIPMinute int                  `json:"peak_ip_minute"`
	TopBlocked   []rateLimitBlockedIP `json:"top_blocked"`
}

// rateLimitRun is a var so tests can skip the real opencli call
var rateLimitRun = func(args ...string) (string, error) {
	out, err := exec.Command("opencli", append([]string{"domains-ratelimit"}, args...)...).CombinedOutput()
	return string(out), err
}

func readRateLimitStatus() (rateLimitStatus, error) {
	var st rateLimitStatus
	out, err := rateLimitRun("status", "--json")
	if err != nil {
		return st, err
	}
	err = json.Unmarshal([]byte(strings.TrimSpace(out)), &st)
	return st, err
}

// log parsing takes a while on busy servers, so usage is cached for a few minutes
const rateLimitStatsTTL = 5 * time.Minute

var rateLimitStatsCache struct {
	sync.Mutex
	at    time.Time
	stats []rateLimitStats
}

func readRateLimitStats(refresh bool) ([]rateLimitStats, time.Time, error) {
	c := &rateLimitStatsCache
	c.Lock()
	defer c.Unlock()
	if !refresh && c.stats != nil && time.Since(c.at) < rateLimitStatsTTL {
		return c.stats, c.at, nil
	}
	out, err := rateLimitRun("stats", "--all", "--json")
	if err != nil {
		return nil, time.Time{}, err
	}
	var stats []rateLimitStats
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &stats); err != nil {
		return nil, time.Time{}, err
	}
	c.stats, c.at = stats, time.Now()
	return stats, c.at, nil
}

var rateLimitOK = regexp.MustCompile(`^Updated: (\S+) `)

// rateLimitApply runs one opencli change for all domains at once and reports per domain from its Updated:/ERROR lines
func rateLimitApply(domains []string, mode string, perIP, perSite int) []BulkResult {
	results := make([]BulkResult, len(domains))
	var valid []string
	for i, d := range domains {
		results[i].Item = d
		if isDomain(d) {
			valid = append(valid, d)
		} else {
			results[i].Message = "Invalid domain."
		}
	}
	if len(valid) == 0 {
		return results
	}

	args := append([]string{}, valid...)
	if mode == "custom" {
		args = append(args, strconv.Itoa(perIP), strconv.Itoa(perSite))
	} else {
		args = append(args, mode)
	}
	out, _ := rateLimitRun(args...)

	updated := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if m := rateLimitOK.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			updated[m[1]] = true
		}
	}
	// a rejected config is rolled back whole, so it fails every domain
	failure := ""
	switch {
	case strings.Contains(out, "Caddy rejected the new config"):
		failure = "Caddy rejected the new config, nothing was changed."
	case strings.Contains(out, "no rate limit module"):
		failure = "Caddy has no rate limit module, check 'podman logs caddy'."
	case strings.Contains(out, "Failed to reload Caddy"):
		failure = "Saved, but reloading Caddy failed."
	}
	for i := range results {
		d := results[i].Item
		switch {
		case results[i].Message != "":
		case failure != "":
			results[i].Message = failure
		case updated[d]:
			results[i].OK, results[i].Message = true, "Rate limit updated."
		case strings.Contains(out, "SKIPPED: "+d):
			results[i].Message = "Skipped, the domain is suspended or has a custom vhost."
		default:
			results[i].Message = "Domain config not found."
		}
	}
	return results
}

// rateLimitModeForDomain sniffs the domain's caddy config like readCaddyFileForDomain does, default, custom or off
func rateLimitModeForDomain(domain string) string {
	content, err := os.ReadFile(CaddyDomainsConfDir + "/" + domain + ".conf")
	if err != nil {
		return "off"
	}
	for _, line := range strings.Split(string(content), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "import" {
			continue
		}
		switch f[1] {
		case "ratelimit":
			return "default"
		case "ratelimit_ip", "ratelimit_site":
			return "custom"
		}
	}
	return "off"
}

// parseLimit takes a non-negative whole number, the form sends empty for 0
func parseLimit(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && n <= 1000000
}

// ServeRateLimit handles GET/POST /domains/ratelimit: the server default form and the per-domain table
func (h *DomainsRateLimit) ServeRateLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		h.handlePOST(w, r)
		http.Redirect(w, r, "/domains/ratelimit", http.StatusSeeOther)
		return
	}

	st, err := readRateLimitStatus()
	scope := scopeFor(r, h.MySQL)
	st.Domains = filterRows(scope, st.Domains, func(d rateLimitRow) bool { return scope.AllowsDomain(d.Domain) })
	owners := wafDomainOwners(h.MySQL)
	users := []string{}
	seen := map[string]bool{}
	for i := range st.Domains {
		o := owners[st.Domains[i].Domain]
		st.Domains[i].Owner = o
		if o != "" && !seen[o] {
			seen[o] = true
			users = append(users, o)
		}
	}
	sort.Strings(users)

	if r.URL.Query().Get("output") == "json" {
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "opencli domains-ratelimit failed: " + err.Error()})
			return
		}
		writeJSON(w, st)
		return
	}

	data := mergeChrome(map[string]interface{}{
		"Status":      st,
		"StatusError": err != nil,
		"Users":       users,
		"CSRFToken":   csrf.Token(r),
		"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		"BulkActions": RateLimitBulkActions(),
	}, r, "Rate Limiting")
	webtemplates.Render(w, "domains_ratelimit.html", data)
}

func (h *DomainsRateLimit) handlePOST(w http.ResponseWriter, r *http.Request) {
	switch r.PostFormValue("action") {
	case "default":
		// the server default applies to every domain, so it's admin only
		if IsReseller(r) {
			auth.AddFlash(w, r, h.Sessions, "Only administrators can change the server default.", "error")
			return
		}
		perIP, ok1 := parseLimit(r.PostFormValue("per_ip"))
		perSite, ok2 := parseLimit(r.PostFormValue("per_site"))
		if !ok1 || !ok2 {
			auth.AddFlash(w, r, h.Sessions, "Limits must be whole numbers, 0 turns a limit off.", "error")
			return
		}
		out, err := rateLimitRun("default", strconv.Itoa(perIP), strconv.Itoa(perSite))
		if err != nil || !strings.Contains(out, "Server default set") || strings.Contains(out, "Caddy rejected") {
			auth.AddFlash(w, r, h.Sessions, "Failed to save the server default: "+firstLine(strings.TrimSpace(out)), "error")
			return
		}
		if perIP == 0 && perSite == 0 {
			auth.AddFlash(w, r, h.Sessions, "Server default rate limit turned off.", "success")
		} else {
			auth.AddFlash(w, r, h.Sessions, "Server default set to "+strconv.Itoa(perIP)+"/min per IP and "+strconv.Itoa(perSite)+"/min per domain.", "success")
		}

	case "domain":
		domain := r.PostFormValue("domain")
		if !scopeFor(r, h.MySQL).AllowsDomain(domain) {
			auth.AddFlash(w, r, h.Sessions, "Domain "+domain+" is not owned by one of your users.", "error")
			return
		}
		mode := r.PostFormValue("mode")
		perIP, ok1 := parseLimit(r.PostFormValue("per_ip"))
		perSite, ok2 := parseLimit(r.PostFormValue("per_site"))
		if mode != "default" && mode != "custom" && mode != "off" {
			auth.AddFlash(w, r, h.Sessions, "Pick default, custom or off.", "error")
			return
		}
		if mode == "custom" && (!ok1 || !ok2) {
			auth.AddFlash(w, r, h.Sessions, "Limits must be whole numbers, 0 turns a limit off.", "error")
			return
		}
		res := rateLimitApply([]string{domain}, mode, perIP, perSite)[0]
		if res.OK {
			auth.AddFlash(w, r, h.Sessions, "Rate limit for "+domain+" updated.", "success")
		} else {
			auth.AddFlash(w, r, h.Sessions, "Rate limit for "+domain+" not changed: "+res.Message, "error")
		}

	default:
		auth.AddFlash(w, r, h.Sessions, "Unknown action.", "error")
	}
}

// ServeRateLimitStats handles GET /domains/ratelimit/stats, the page loads usage after rendering so it never waits on the logs
func (h *DomainsRateLimit) ServeRateLimitStats(w http.ResponseWriter, r *http.Request) {
	stats, at, err := readRateLimitStats(r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "Reading usage failed: " + err.Error()})
		return
	}
	scope := scopeFor(r, h.MySQL)
	byDomain := make(map[string]rateLimitStats, len(stats))
	for _, s := range stats {
		if scope.AllowsDomain(s.Domain) {
			byDomain[s.Domain] = s
		}
	}
	writeJSON(w, map[string]interface{}{"updated": at.Unix(), "stats": byDomain})
}

// RateLimitBulkActions are the /domains/ratelimit bulk actions, custom takes "per_ip/per_site"
func RateLimitBulkActions() []webtemplates.BulkAction {
	return []webtemplates.BulkAction{
		{Key: "default", Icon: "rotate-cw", Label: "Use server default", Confirm: "Use the server default rate limit for the selected domains?"},
		{Key: "custom", Icon: "pencil", Label: "Custom limits", Confirm: "Requests per minute for the selected domains, per IP / per domain:",
			Input: &webtemplates.BulkInput{Type: "text", Placeholder: "300/6000", Pattern: `[0-9]{1,7}\s*/\s*[0-9]{1,7}`, Hint: "per IP / per domain, 0 = off"}},
		{Key: "off", Icon: "infinity", Label: "No limits", Confirm: "Remove all rate limits from the selected domains?"},
	}
}

// rateLimitBulkBatch changes the whole selection in one opencli call so caddy reloads once
func rateLimitBulkBatch(action, value string, items []string) ([]BulkResult, bool) {
	switch action {
	case "default", "off":
		return rateLimitApply(items, action, 0, 0), true
	case "custom":
		ip, site, _ := strings.Cut(value, "/")
		perIP, ok1 := parseLimit(ip)
		perSite, ok2 := parseLimit(site)
		if !ok1 || !ok2 {
			results := make([]BulkResult, len(items))
			for i, d := range items {
				results[i] = BulkResult{Item: d, Message: "Limits must be whole numbers."}
			}
			return results, true
		}
		return rateLimitApply(items, "custom", perIP, perSite), true
	}
	return nil, false
}

func rateLimitBulkRoute(action, value, domain string) (*BulkCall, BulkResult) {
	return BulkSkip("Unknown bulk action.")
}
