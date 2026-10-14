package handlers

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/activity"
	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// Websites bundles the /websites handlers, a server-wide view of every user's Site Manager entries
type Websites struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// where opencli websites-pagespeed and websites-vulnerability leave their reports
var (
	WebsitesPageSpeedDir     = "/etc/openpanel/openpanel/websites"
	WebsitesVulnerabilityDir = "/etc/openpanel/wordpress/vulnerability"
)

// websitesStart is a var so tests don't run opencli, scans and pagespeed checks take minutes so they run in the background
var websitesStart = func(args ...string) error {
	cmd := exec.Command("opencli", args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

type websiteRow struct {
	ID          int    `json:"id"`
	SiteName    string `json:"site_name"`
	Type        string `json:"type"`
	Version     string `json:"version"`
	AdminEmail  string `json:"admin_email"`
	Created     string `json:"created_date"`
	CreatedAgo  string `json:"created_ago"`
	Domain      string `json:"domain"`
	Docroot     string `json:"docroot"`
	Owner       string `json:"owner"`
	Context     string `json:"-"`
	HostDocroot string `json:"host_docroot"` // where the docroot is on the server, /var/www/html in the container is /home/<context>/files

	Speed         *pageSpeed   `json:"pagespeed"`                // nil when there's no report yet
	LatestVersion string       `json:"latest_version,omitempty"` // set on wordpress sites that are behind it
	Vulns         *vulnReport  `json:"vulnerabilities"`
	Imunify       *imunifyUser `json:"imunify,omitempty"`
	Git           []gitRepo    `json:"git"` // OpenPanel repositories deploying into this site // the owner's last ImunifyAV scan, nil when imunify isn't installed or never scanned them          // nil when there's no report, opencli only writes one when something was found
}

// hostDocroot maps a container docroot to the server path, unchanged when it's outside /var/www/html or the context is unknown
func hostDocroot(userContext, docroot string) string {
	rest, ok := strings.CutPrefix(docroot, "/var/www/html")
	if !ok || userContext == "" || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return docroot
	}
	return "/home/" + userContext + "/files" + rest
}

// Menu is what the page's shared actions menu needs to know about the row
func (s websiteRow) Menu() map[string]any {
	// same as the stripSuspendedPrefix template func
	login := s.Owner
	if i := strings.LastIndex(login, "_"); strings.Contains(login, "SUSPENDED_") && i >= 0 {
		login = login[i+1:]
	}
	return map[string]any{"id": s.ID, "name": s.SiteName, "owner": s.Owner, "login": login, "domain": s.Domain, "wp": s.IsWordPress(), "scan": s.Imunify != nil,
		"git": len(s.Git), "gitPull": s.GitRemote(), "gitDeploy": s.GitDeployable()}
}

func (s websiteRow) IsWordPress() bool { return strings.EqualFold(s.Type, "wordpress") }

// reportFile is the file name opencli uses for a site's report, slashes in subfolder sites become underscores
func reportFile(dir, siteName string) string {
	name := strings.ReplaceAll(strings.ReplaceAll(siteName, "://", "_"), "/", "_")
	return filepath.Join(dir, name+".json")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return strconv.Itoa(n) + " " + unit + "s ago"
}

// parsePanelTime reads a sites.created_date, the panel db connection uses parseTime so it arrives as RFC3339 with the server's local wall clock labeled UTC
func parsePanelTime(v string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local), true
		}
	}
	return time.Time{}, false
}

// timeSince turns a created date into "3 hours ago"
func timeSince(created string, now time.Time) string {
	t, ok := parsePanelTime(created)
	if !ok {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 730*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month")
	}
	return plural(int(d.Hours()/24/365), "year")
}

type speedResult struct {
	Score       int    `json:"score"`
	FCP         string `json:"first_contentful_paint"`
	SpeedIndex  string `json:"speed_index"`
	Interactive string `json:"interactive"`
}

type pageSpeed struct {
	Checked string      `json:"checked"`
	Desktop speedResult `json:"desktop"`
	Mobile  speedResult `json:"mobile"`
}

// RingOffset is the stroke-dashoffset of the desktop score ring, the circle is 78.54 around
func (p *pageSpeed) RingOffset() string {
	return strconv.FormatFloat(78.54-78.54*float64(p.Desktop.Score)/100, 'f', 2, 64)
}

// RingColor uses the same thresholds as the user panel's /sites
func (p *pageSpeed) RingColor() string {
	switch d := p.Desktop.Score; {
	case d < 60:
		return "stroke-red-500"
	case d < 80:
		// the user panel uses yellow, output.css only has orange
		return "stroke-orange-500"
	case d < 90:
		return "stroke-blue-500"
	}
	return "stroke-green-500"
}

// readPageSpeed reads the opencli websites-pagespeed report, nil when it's missing or the check failed
func readPageSpeed(siteName string) *pageSpeed {
	b, err := os.ReadFile(reportFile(WebsitesPageSpeedDir, siteName))
	if err != nil {
		return nil
	}
	type strategy struct {
		Score       *float64 `json:"performance_score"`
		FCP         string   `json:"first_contentful_paint"`
		SpeedIndex  string   `json:"speed_index"`
		Interactive string   `json:"interactive"`
	}
	var data struct {
		Timestamp string   `json:"timestamp"`
		Desktop   strategy `json:"desktop_speed"`
		Mobile    strategy `json:"mobile_speed"`
	}
	if json.Unmarshal(b, &data) != nil || data.Desktop.Score == nil || data.Mobile.Score == nil {
		return nil
	}
	conv := func(s strategy) speedResult {
		return speedResult{Score: int(*s.Score*100 + 0.5), FCP: s.FCP, SpeedIndex: s.SpeedIndex, Interactive: s.Interactive}
	}
	return &pageSpeed{Checked: data.Timestamp, Desktop: conv(data.Desktop), Mobile: conv(data.Mobile)}
}

func (p *pageSpeed) score() int {
	if p == nil {
		return -1
	}
	return p.Desktop.Score
}

type vulnSource struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

type vulnIssue struct {
	Name    string       `json:"name"`
	FixedIn string       `json:"fixed_in"`
	Sources []vulnSource `json:"sources"`
}

type vulnComponent struct {
	Kind   string      `json:"kind"` // core, plugin, theme or mu-plugin
	Name   string      `json:"name"`
	Link   string      `json:"link"`
	Issues []vulnIssue `json:"issues"`
}

type vulnReport struct {
	Checked    string          `json:"checked"`
	Total      int             `json:"total"`
	Components []vulnComponent `json:"components"`
}

type cachedVulnReport struct {
	mod    time.Time
	size   int64
	report *vulnReport
}

var (
	vulnCacheMu sync.Mutex
	vulnCache   = map[string]cachedVulnReport{}
)

// readVulnerabilities returns the parsed report, reparsing only when a new scan changed the file
func readVulnerabilities(siteName string) *vulnReport {
	path := reportFile(WebsitesVulnerabilityDir, siteName)
	info, err := os.Stat(path)
	if err != nil {
		vulnCacheMu.Lock()
		delete(vulnCache, path)
		vulnCacheMu.Unlock()
		return nil
	}
	vulnCacheMu.Lock()
	c, ok := vulnCache[path]
	vulnCacheMu.Unlock()
	if ok && c.mod.Equal(info.ModTime()) && c.size == info.Size() {
		return c.report
	}
	report := parseVulnerabilities(path)
	vulnCacheMu.Lock()
	vulnCache[path] = cachedVulnReport{mod: info.ModTime(), size: info.Size(), report: report}
	vulnCacheMu.Unlock()
	return report
}

// parseVulnerabilities reads the opencli websites-vulnerability report, keys are core_<version>, plugin_<slug>, theme_<slug> or mu-plugin_<slug>
func parseVulnerabilities(path string) *vulnReport {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	report := &vulnReport{}
	_ = json.Unmarshal(raw["timestamp"], &report.Checked)
	delete(raw, "timestamp")

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var item struct {
			Data *struct {
				Name          string `json:"name"`
				Link          string `json:"link"`
				Vulnerability []struct {
					Name     string `json:"name"`
					Operator struct {
						MaxVersion  string `json:"max_version"`
						MaxOperator string `json:"max_operator"`
					} `json:"operator"`
					Source []vulnSource `json:"source"`
				} `json:"vulnerability"`
			} `json:"data"`
		}
		if json.Unmarshal(raw[key], &item) != nil || item.Data == nil || len(item.Data.Vulnerability) == 0 {
			continue
		}
		kind, slug, _ := strings.Cut(key, "_")
		// wordpress.org names come html-encoded, like "Ajax Search Lite &#8211; Live Search"
		c := vulnComponent{Kind: kind, Name: html.UnescapeString(item.Data.Name), Link: item.Data.Link}
		if c.Name == "" {
			c.Name = slug
		}
		if kind == "core" {
			c.Name = "WordPress " + strings.TrimPrefix(slug, "version_")
		}
		for _, v := range item.Data.Vulnerability {
			issue := vulnIssue{Name: html.UnescapeString(v.Name), Sources: v.Source}
			// "lt" means the max version is the first fixed one
			if v.Operator.MaxOperator == "lt" {
				issue.FixedIn = v.Operator.MaxVersion
			}
			c.Issues = append(c.Issues, issue)
		}
		report.Total += len(c.Issues)
		report.Components = append(report.Components, c)
	}
	if len(report.Components) == 0 {
		return nil
	}
	return report
}

func (r *vulnReport) Count() int {
	if r == nil {
		return 0
	}
	return r.Total
}

func (ws *Websites) loadSites() ([]websiteRow, error) {
	rows, err := ws.MySQL.Query(`
		SELECT s.id, s.site_name, s.type, s.version, s.admin_email, s.created_date,
		       d.domain_url, d.docroot, u.username, u.server
		FROM sites s
		LEFT JOIN domains d ON d.domain_id = s.domain_id
		LEFT JOIN users u ON u.id = d.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []websiteRow
	gitByContext := map[string][]gitRepoFile{}
	now := time.Now()
	for rows.Next() {
		var s websiteRow
		var name, typ, version, email, created, domain, docroot, owner, userContext sql.NullString
		if err := rows.Scan(&s.ID, &name, &typ, &version, &email, &created, &domain, &docroot, &owner, &userContext); err != nil {
			return nil, err
		}
		s.SiteName, s.Type, s.Version, s.AdminEmail, s.Created = name.String, typ.String, version.String, email.String, created.String
		s.Domain, s.Docroot, s.Owner = domain.String, docroot.String, owner.String
		s.CreatedAgo = timeSince(s.Created, time.Now())
		if t, ok := parsePanelTime(s.Created); ok {
			s.Created = t.Format("2006-01-02 15:04:05")
		}
		// subfolder installs live under the domain's docroot
		if _, folder, ok := strings.Cut(s.SiteName, "/"); ok && folder != "" && s.Docroot != "" {
			s.Docroot = strings.TrimRight(s.Docroot, "/") + "/" + folder
		}
		s.Context = userContext.String
		s.HostDocroot = hostDocroot(userContext.String, s.Docroot)
		if _, seen := gitByContext[s.Context]; !seen {
			gitByContext[s.Context] = readGitRepos(s.Context)
		}
		s.Git = gitReposForSite(gitByContext[s.Context], s.Docroot, now)
		s.Speed = readPageSpeed(s.SiteName)
		if s.IsWordPress() {
			s.Vulns = readVulnerabilities(s.SiteName)
		}
		sites = append(sites, s)
	}
	return sites, rows.Err()
}

func (ws *Websites) siteByID(idStr string) (websiteRow, bool) {
	id, err := strconv.Atoi(idStr)
	if err != nil || ws.MySQL == nil {
		return websiteRow{}, false
	}
	var s websiteRow
	var typ sql.NullString
	if ws.MySQL.QueryRow("SELECT id, site_name, type FROM sites WHERE id = ?", id).Scan(&s.ID, &s.SiteName, &typ) != nil {
		return websiteRow{}, false
	}
	s.Type = typ.String
	return s, true
}

func sortWebsites(sites []websiteRow, col string, desc bool) {
	key := map[string]func(s websiteRow) string{
		"name":    func(s websiteRow) string { return strings.ToLower(s.SiteName) },
		"type":    func(s websiteRow) string { return strings.ToLower(s.Type) },
		"owner":   func(s websiteRow) string { return strings.ToLower(s.Owner) },
		"version": func(s websiteRow) string { return s.Version },
		"created": func(s websiteRow) string { return s.Created },
		"email":   func(s websiteRow) string { return strings.ToLower(s.AdminEmail) },
	}
	if col == "vulns" {
		sort.SliceStable(sites, func(i, j int) bool {
			if desc {
				return sites[i].Vulns.Count() > sites[j].Vulns.Count()
			}
			return sites[i].Vulns.Count() < sites[j].Vulns.Count()
		})
		return
	}
	if col == "imunify" {
		infected := func(s websiteRow) int {
			if s.Imunify == nil {
				return -1
			}
			return s.Imunify.Total()
		}
		sort.SliceStable(sites, func(i, j int) bool {
			if desc {
				return infected(sites[i]) > infected(sites[j])
			}
			return infected(sites[i]) < infected(sites[j])
		})
		return
	}
	if col == "speed" {
		sort.SliceStable(sites, func(i, j int) bool {
			if desc {
				return sites[i].Speed.score() > sites[j].Speed.score()
			}
			return sites[i].Speed.score() < sites[j].Speed.score()
		})
		return
	}
	f, ok := key[col]
	if !ok {
		return
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if desc {
			return f(sites[i]) > f(sites[j])
		}
		return f(sites[i]) < f(sites[j])
	})
}

type websiteTypeCount struct {
	Type  string
	Count int
}

type websitesPageData struct {
	webtemplates.Chrome
	FaviconBase   string
	Imunify       bool // imunify is installed, shows the ImunifyAV column
	GitEnabled    bool // the OpenPanel git module is on, shows the Git column and actions
	Sites         []websiteRow
	Types         []websiteTypeCount
	TypeNames     []string
	MySQLIsDown   bool
	SortCol       string
	SortDirection string
	CSRFToken     string
	Flashes       []auth.Flash
}

// faviconBase is the favicons url from openpanel.config that the site name gets appended to, same as the user panel's /sites
func faviconBase() string {
	base := strings.TrimSpace(config.Load(config.OpenpanelConfigPath).Get("PANEL", "favicons", ""))
	if base == "" || base == "local" {
		return "https://www.google.com/s2/favicons?domain="
	}
	return base
}

// WordPressStableCheckURL is the same wordpress.org api the user panel's /sites update badges use
var WordPressStableCheckURL = "https://api.wordpress.org/core/stable-check/1.0/"

var (
	wpLatestMu      sync.Mutex
	wpLatestVersion string
	wpLatestUntil   time.Time
)

// wpLatestFetch is a var so tests don't hit wordpress.org
var wpLatestFetch = func() (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(WordPressStableCheckURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var statuses map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return "", err
	}
	latest := ""
	for version, status := range statuses {
		if status == "latest" && (latest == "" || compareVersionTags(version, latest) > 0) {
			latest = version
		}
	}
	return latest, nil
}

// latestWordPress caches the answer for 6 hours like the user panel does, only the very first lookup waits on wordpress.org, later refreshes run in the background
func latestWordPress() string {
	wpLatestMu.Lock()
	defer wpLatestMu.Unlock()
	if time.Now().Before(wpLatestUntil) {
		return wpLatestVersion
	}
	// a failed or in-flight check is retried after 10 minutes
	wpLatestUntil = time.Now().Add(10 * time.Minute)
	if wpLatestVersion == "" {
		refreshLatestWordPress()
		return wpLatestVersion
	}
	go func() {
		wpLatestMu.Lock()
		defer wpLatestMu.Unlock()
		refreshLatestWordPress()
	}()
	return wpLatestVersion
}

// refreshLatestWordPress expects wpLatestMu to be held
func refreshLatestWordPress() {
	if v, err := wpLatestFetch(); err == nil && v != "" {
		wpLatestVersion, wpLatestUntil = v, time.Now().Add(6*time.Hour)
	}
}

// renderGzip renders a template gzipped when the browser accepts it, openadmin doesn't compress responses and this page is mostly repeated markup
func renderGzip(w http.ResponseWriter, r *http.Request, name string, data any) {
	out, err := webtemplates.RenderToString(name, data)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		_, _ = w.Write([]byte(out))
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	gz := gzip.NewWriter(w)
	_, _ = gz.Write([]byte(out))
	_ = gz.Close()
}

// ServeList handles GET /websites
func (ws *Websites) ServeList(w http.ResponseWriter, r *http.Request) {
	sites, err := ws.loadSites()
	mysqlIsDown := err != nil

	latestWP := ""
	for _, s := range sites {
		if s.IsWordPress() {
			latestWP = latestWordPress()
			break
		}
	}
	for i := range sites {
		if latestWP != "" && sites[i].IsWordPress() && sites[i].Version != "" && compareVersionTags(sites[i].Version, latestWP) < 0 {
			sites[i].LatestVersion = latestWP
		}
	}

	imunify := imunifyInstalled()
	if imunify {
		users := cachedImunifyUsers()
		for i := range sites {
			if u, ok := users[sites[i].Context]; ok {
				sites[i].Imunify = &u
			}
		}
	}

	counts := map[string]int{}
	var types []websiteTypeCount
	for _, s := range sites {
		t := strings.ToLower(s.Type)
		if _, seen := counts[t]; !seen {
			types = append(types, websiteTypeCount{Type: t})
		}
		counts[t]++
	}
	for i := range types {
		types[i].Count = counts[types[i].Type]
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Count > types[j].Count })
	typeNames := make([]string, len(types))
	for i, t := range types {
		typeNames[i] = t.Type
	}

	sortCol, sortDir := r.URL.Query().Get("sort"), r.URL.Query().Get("direction")
	if sortCol != "" {
		sortWebsites(sites, sortCol, strings.EqualFold(sortDir, "desc"))
	}

	if r.URL.Query().Get("output") == "json" {
		if sites == nil {
			sites = []websiteRow{}
		}
		writeJSON(w, map[string]interface{}{"websites": sites})
		return
	}

	chrome := buildChrome(r, "Websites")
	gitEnabled := gitModuleEnabled()
	chrome.BulkActions = WebsitesBulkActions(imunify, gitEnabled)
	renderGzip(w, r, "websites.html", websitesPageData{
		Chrome:        chrome,
		FaviconBase:   faviconBase(),
		Imunify:       imunify,
		GitEnabled:    gitEnabled,
		Sites:         sites,
		Types:         types,
		TypeNames:     typeNames,
		MySQLIsDown:   mysqlIsDown,
		SortCol:       sortCol,
		SortDirection: sortDir,
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, ws.Sessions),
	})
}

// ServeVulnerabilities handles GET /websites/vulnerabilities/{id}, the popup body for one site's report
func (ws *Websites) ServeVulnerabilities(w http.ResponseWriter, r *http.Request) {
	site, ok := ws.siteByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	var report *vulnReport
	if site.IsWordPress() {
		report = readVulnerabilities(site.SiteName)
	}
	renderGzip(w, r, "websites_vulns", struct{ Vulns *vulnReport }{report})
}

// HandleScan handles POST /websites/scan, scans one user's files or every user's for sites missing from Site Manager
func (ws *Websites) HandleScan(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.FormValue("username"))
	if target == "" {
		target = "-all"
	} else if !userExists(ws.MySQL, target) {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "User \""+target+"\" does not exist.", "error")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	if err := websitesStart("websites-scan", target); err != nil {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "Failed to start the scan: "+err.Error(), "error")
	} else if target == "-all" {
		auth.AddFlash(w, r, ws.Sessions, "Scanning all users for websites, new sites show up here once it finishes.", "info")
	} else {
		auth.AddFlash(w, r, ws.Sessions, "Scanning "+target+"'s files for websites, new sites show up here once it finishes.", "info")
	}
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// HandleDetach handles POST /websites/detach, removes the Site Manager entry and keeps files and databases
func (ws *Websites) HandleDetach(w http.ResponseWriter, r *http.Request) {
	site, ok := ws.siteByID(r.FormValue("id"))
	if !ok {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "Website not found.", "error")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	if _, err := ws.MySQL.Exec("DELETE FROM sites WHERE id = ?", site.ID); err != nil {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "Failed to detach "+site.SiteName+": "+err.Error(), "error")
	} else {
		activity.Describe(r.Context(), "Detached website "+site.SiteName)
		auth.AddFlash(w, r, ws.Sessions, site.SiteName+" detached from Site Manager, its files and database are kept.", "success")
	}
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// HandleCheck handles POST /websites/{check}, starts a PageSpeed or WordPress vulnerability check for one site
func (ws *Websites) HandleCheck(w http.ResponseWriter, r *http.Request) {
	check := r.PathValue("check")
	site, ok := ws.siteByID(r.FormValue("id"))
	if !ok {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "Website not found.", "error")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}

	var cmd, label string
	switch check {
	case "pagespeed":
		cmd, label = "websites-pagespeed", "PageSpeed check"
	case "vulnerability":
		if !site.IsWordPress() {
			activity.Fail(r.Context())
			auth.AddFlash(w, r, ws.Sessions, "Vulnerability scans are only available for WordPress sites.", "error")
			http.Redirect(w, r, "/websites", http.StatusSeeOther)
			return
		}
		cmd, label = "websites-vulnerability", "Vulnerability scan"
	default:
		http.NotFound(w, r)
		return
	}

	if err := websitesStart(cmd, site.SiteName); err != nil {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, "Failed to start the "+label+" for "+site.SiteName+": "+err.Error(), "error")
	} else {
		activity.Describe(r.Context(), label+" started for "+site.SiteName)
		auth.AddFlash(w, r, ws.Sessions, label+" started for "+site.SiteName+", refresh in a minute to see the results.", "info")
	}
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func WebsitesBulkActions(imunify, git bool) []webtemplates.BulkAction {
	actions := []webtemplates.BulkAction{
		{Key: "pagespeed", Icon: "refresh-cw", Label: "Check PageSpeed", Confirm: "Run a Google PageSpeed check for the selected websites?"},
		{Key: "vulnerability", Icon: "shield-check", Label: "Scan for vulnerabilities", Confirm: "Scan the selected WordPress sites for vulnerable plugins and themes?"},
	}
	if imunify {
		actions = append(actions, webtemplates.BulkAction{Key: "imunify", Icon: "scan-search", Label: "Scan with ImunifyAV", Confirm: "Queue an ImunifyAV malware scan for the owners of the selected websites? Each owner is scanned once, even with several sites selected."})
	}
	if git {
		actions = append(actions,
			webtemplates.BulkAction{Key: "git_pull", Icon: "download", Label: "Git: pull", Confirm: "Pull updates for the remote Git repositories of the selected websites? Repositories in automatic mode are deployed too."},
			webtemplates.BulkAction{Key: "git_deploy", Icon: "rocket", Label: "Git: deploy", Confirm: "Deploy the Git repositories of the selected websites now?"},
		)
	}
	return append(actions, webtemplates.BulkAction{Key: "detach", Icon: "file-x", Label: "Detach", Confirm: "Detach the selected websites from Site Manager? Files and databases are kept.", Danger: true})
}

// BulkRoute items are site ids, each one goes through the single-site route
func (ws *Websites) BulkRoute(action, _, item string) (*BulkCall, BulkResult) {
	site, ok := ws.siteByID(item)
	if !ok {
		return BulkSkip("Website not found.")
	}
	form := url.Values{"id": {item}}
	switch action {
	case "pagespeed", "vulnerability":
		if action == "vulnerability" && !site.IsWordPress() {
			return BulkSkip("Not a WordPress site.")
		}
		return BulkDo(BulkCall{Label: site.SiteName, Method: http.MethodPost, Path: "/websites/" + action, Form: form})
	case "imunify":
		if site.Owner == "" {
			return BulkSkip("Website has no owner.")
		}
		// a scan covers the owner's whole home, so a second site of the same owner is skipped
		if u, ok := cachedImunifyUsers()[site.Context]; ok && u.Scanning() {
			return BulkSkip("ImunifyAV scan already queued for " + site.Owner + ".")
		}
		return BulkDo(BulkCall{Label: site.SiteName, Method: http.MethodPost, Path: "/security/imunify/" + url.PathEscape(site.Owner)})
	case "detach":
		return BulkDo(BulkCall{Label: site.SiteName, Method: http.MethodPost, Path: "/websites/detach", Form: form})
	case "git_pull", "git_deploy":
		withGit, _ := ws.siteGit(item)
		if action == "git_pull" && !withGit.GitRemote() {
			return BulkSkip("No remote Git repository.")
		}
		if action == "git_deploy" && !withGit.GitDeployable() {
			return BulkSkip("No Git repository to deploy.")
		}
		return gitBulkRoute(withGit, action, item)
	}
	return BulkSkip("Unknown bulk action.")
}
