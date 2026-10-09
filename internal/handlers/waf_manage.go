// This file implements the admin side of CorazaWAF: app profiles, CRS updates, server/user/domain rule exclusions and the audit log viewer.
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// WAFLogDir holds the per-domain Coraza audit logs written by each domain's SecAuditLog
var WAFLogDir = "/var/log/caddy/coraza_waf/"

// WAFPluginsDir is where `opencli waf plugins install` clones the CRS rule exclusion plugins
var WAFPluginsDir = "/etc/openpanel/caddy/coreruleset-plugins/"

// WAFCRSDir is the OWASP CRS git checkout
var WAFCRSDir = "/etc/openpanel/caddy/coreruleset/"

// CRS loads this file last from rules/*.conf and keeps it out of git, so server-wide exclusions survive `opencli waf update`
const wafGlobalExclusionsName = "RESPONSE-999-EXCLUSION-RULES-AFTER-CRS.conf"

func wafGlobalExclusionsPath() string {
	return filepath.Join(WAFRulesDir, wafGlobalExclusionsName)
}

// wafOpenCLIOutput is injectable so tests never shell out to a real opencli
var wafOpenCLIOutput = func(args ...string) (string, error) {
	out, err := exec.Command("opencli", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wafGitLogRun returns `git log --oneline` for a checkout, injectable for tests
var wafGitLogRun = func(dir string, n string) string {
	out, err := exec.Command("git", "-C", dir, "log", "-n", n, "--format=%h|%cd|%s", "--date=short").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// wafGitRun runs git in a checkout with a timeout, injectable so tests never touch real repos or the network
var wafGitRun = func(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// wafFetchMaxAge is how old the last fetch can get before the overview refreshes it in the background
const wafFetchMaxAge = 6 * time.Hour

var wafFetching atomic.Bool

// wafBackgroundFetch refreshes stale checkouts without holding up the page, one run at a time, injectable for tests
var wafBackgroundFetch = func(dirs []string) {
	if !wafFetching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer wafFetching.Store(false)
		wafFetchAll(dirs)
	}()
}

// wafRepoDirs is the CRS checkout plus every cloned app profile
func wafRepoDirs() []string {
	dirs := []string{WAFCRSDir}
	for _, p := range wafProfileCatalog {
		if _, err := os.Stat(filepath.Join(wafPluginDir(p.Key), ".git")); err == nil {
			dirs = append(dirs, wafPluginDir(p.Key))
		}
	}
	return dirs
}

func wafLastFetch(dir string) time.Time {
	info, err := os.Stat(filepath.Join(dir, ".git", "FETCH_HEAD"))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func wafFetchAll(dirs []string) {
	for _, d := range dirs {
		wafGitRun(d, "fetch", "--quiet")
	}
}

// wafPendingUpdates counts upstream commits not pulled yet, force fetches now, otherwise stale checkouts get fetched in the background for the next page load
func wafPendingUpdates(force bool) (behind int, checked time.Time) {
	if _, err := os.Stat(filepath.Join(WAFCRSDir, ".git")); err != nil {
		return 0, time.Time{}
	}
	dirs := wafRepoDirs()
	if force {
		wafFetchAll(dirs)
	} else {
		var stale []string
		for _, d := range dirs {
			if time.Since(wafLastFetch(d)) > wafFetchMaxAge {
				stale = append(stale, d)
			}
		}
		if len(stale) > 0 {
			wafBackgroundFetch(stale)
		}
	}
	for _, d := range dirs {
		if out, err := wafGitRun(d, "rev-list", "--count", "HEAD..@{u}"); err == nil {
			n, _ := strconv.Atoi(out)
			behind += n
		}
		if t := wafLastFetch(d); checked.IsZero() || (!t.IsZero() && t.Before(checked)) {
			checked = t
		}
	}
	return behind, checked
}

// OpenPanel always writes these first in SecRuleRemoveById/SecRuleRemoveByTag and hides them in the UI
const (
	wafSentinelRuleID = "007"
	wafSentinelTag    = "example"
)

var (
	wafRuleIDRE   = regexp.MustCompile(`^\d+$`)
	wafTagRE      = regexp.MustCompile(`^[A-Za-z0-9_./:-]+$`)
	wafDomainRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	wafUsernameRE = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	wafEngineRE   = regexp.MustCompile(`(?m)^(\s*SecRuleEngine\s+)(On|Off|DetectionOnly)\b`)
	wafPluginRE   = regexp.MustCompile(`/([a-z0-9]+)-rule-exclusions-plugin/plugins/`)
	wafParanoiaRE = regexp.MustCompile(`blocking_paranoia_level=(\d+)`)
	wafInboundRE  = regexp.MustCompile(`inbound_anomaly_score_threshold=(\d+)`)
)

// wafProfile is one app profile, backed by coreruleset/<Key>-rule-exclusions-plugin
type wafProfile struct {
	Key  string
	Name string
}

var wafProfileCatalog = []wafProfile{
	{"wordpress", "WordPress"}, {"drupal", "Drupal"}, {"nextcloud", "Nextcloud"}, {"dokuwiki", "DokuWiki"},
	{"phpbb", "phpBB"}, {"xenforo", "XenForo"}, {"phpmyadmin", "phpMyAdmin"},
}

func wafPluginDir(key string) string {
	return filepath.Join(WAFPluginsDir, key+"-rule-exclusions-plugin")
}

func wafPluginInstalled(key string) bool {
	_, err := os.Stat(filepath.Join(wafPluginDir(key), "plugins", key+"-rule-exclusions-before.conf"))
	return err == nil
}

// 949/959 block on the total anomaly score and 980 reports it, removing them turns blocking off entirely
func wafIsScoringRule(id string) bool {
	return strings.HasPrefix(id, "949") || strings.HasPrefix(id, "959") || strings.HasPrefix(id, "980")
}

var (
	errWAFRuleID      = errors.New("Rule IDs must be numbers.")
	errWAFTag         = errors.New("Tags can only contain letters, numbers and _ . / : -")
	errWAFScoringRule = errors.New("Rules 949xxx, 959xxx and 980xxx only add up the scores of other rules, disabling them turns off blocking.")
	errWAFNoConf      = errors.New("WAF config not found for this domain.")
)

// wafCleanRules validates rule IDs, drops the sentinel and duplicates
func wafCleanRules(ids []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id == wafSentinelRuleID || seen[id] {
			continue
		}
		if !wafRuleIDRE.MatchString(id) {
			return nil, errWAFRuleID
		}
		if wafIsScoringRule(id) {
			return nil, errWAFScoringRule
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func wafCleanTags(tags []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		if strings.EqualFold(t, wafSentinelTag) || seen[t] {
			continue
		}
		if !wafTagRE.MatchString(t) {
			return nil, errWAFTag
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}

// wafDomainState is what a domain's caddy conf says about its WAF
type wafDomainState struct {
	Domain       string
	Owner        string
	Engine       string
	Level        string
	Profiles     []string
	RemovedRules []string
	RemovedTags  []string
}

func wafDomainConfPath(domain string) string {
	return filepath.Join(CaddyDomainsConfDir, domain+".conf")
}

func wafReadDomainState(domain string) (wafDomainState, bool) {
	content, err := os.ReadFile(wafDomainConfPath(domain))
	if err != nil {
		return wafDomainState{Domain: domain, Engine: "Unknown"}, false
	}
	s := string(content)
	st := wafDomainState{Domain: domain, Engine: "Unknown", Level: wafParseLevel(s), Profiles: wafParseProfiles(s)}
	if m := wafEngineRE.FindStringSubmatch(s); m != nil {
		st.Engine = m[2]
	}
	st.RemovedRules, st.RemovedTags = wafParseRemovals(s)
	return st, true
}

// wafParseRemovals reads the first SecRuleRemoveById line and the SecRuleRemoveByTag lines that follow it, same as OpenPanel's waf module
func wafParseRemovals(content string) (rules, tags []string) {
	foundRule, tagStarted, tagEnded := false, false, false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case !foundRule && strings.HasPrefix(line, "SecRuleRemoveById"):
			for _, id := range strings.Fields(line)[1:] {
				if id != wafSentinelRuleID {
					rules = append(rules, id)
				}
			}
			foundRule = true
		case !tagEnded && strings.HasPrefix(line, "SecRuleRemoveByTag"):
			tagStarted = true
			for _, tok := range strings.Fields(strings.TrimPrefix(line, "SecRuleRemoveByTag")) {
				if tag := strings.Trim(tok, `"`); tag != "" && !strings.EqualFold(tag, wafSentinelTag) {
					tags = append(tags, tag)
				}
			}
		case tagStarted && !tagEnded:
			tagEnded = true
		}
		if foundRule && tagEnded {
			break
		}
	}
	return rules, tags
}

// wafRewriteRemovals replaces the SecRuleRemoveById/ByTag lines in every directives block, sentinels go first like OpenPanel writes them
func wafRewriteRemovals(content string, rules, tags []string) string {
	rules = append([]string{wafSentinelRuleID}, rules...)
	tags = append([]string{wafSentinelTag}, tags...)
	var out, buffer []string
	inside := false
	for _, line := range strings.SplitAfter(content, "\n") {
		if line == "" {
			continue
		}
		stripped := strings.TrimSpace(line)
		switch {
		case !inside && strings.HasPrefix(stripped, "directives `"):
			inside = true
			buffer = []string{line}
		case inside && stripped == "`":
			for _, l := range buffer {
				s := strings.TrimSpace(l)
				if !strings.HasPrefix(s, "SecRuleRemoveById") && !strings.HasPrefix(s, "SecRuleRemoveByTag") {
					out = append(out, l)
				}
			}
			out = append(out, "            SecRuleRemoveById "+strings.Join(rules, " ")+"\n")
			for _, t := range tags {
				out = append(out, "            SecRuleRemoveByTag \""+t+"\"\n")
			}
			out = append(out, line)
			buffer, inside = nil, false
		case inside:
			buffer = append(buffer, line)
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

// wafParseLevel maps the per-domain level SecAction (id 10100) to OpenPanel's preset names
func wafParseLevel(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "SecAction") || !strings.Contains(s, "id:10100,") {
			continue
		}
		pm, im := wafParanoiaRE.FindStringSubmatch(s), wafInboundRE.FindStringSubmatch(s)
		if pm == nil || im == nil {
			return "custom"
		}
		switch pm[1] + "/" + im[1] {
		case "1/10":
			return "compatibility"
		case "1/5":
			return "standard"
		case "2/5":
			return "strict"
		}
		return "custom"
	}
	return "standard"
}

func wafParseProfiles(content string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Include") {
			continue
		}
		if m := wafPluginRE.FindStringSubmatch(line); m != nil {
			seen[m[1]] = true
		}
	}
	var out []string
	for _, p := range wafProfileCatalog {
		if seen[p.Key] {
			out = append(out, p.Key)
		}
	}
	return out
}

// wafSaveDomain writes engine (empty keeps it) and the exclusion lists to a domain conf and reloads caddy
func wafSaveDomain(domain, engine string, rules, tags []string) error {
	path := wafDomainConfPath(domain)
	content, err := os.ReadFile(path)
	if err != nil {
		return errWAFNoConf
	}
	s := string(content)
	if engine != "" {
		s = wafEngineRE.ReplaceAllString(s, "${1}"+engine)
	}
	s = wafRewriteRemovals(s, rules, tags)
	if s == string(content) {
		return nil
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return err
	}
	caddyReloadRun()
	return nil
}

// wafReadGlobalExclusions parses the server-wide exclusions file, a missing file means none
func wafReadGlobalExclusions() (rules, tags []string) {
	content, err := os.ReadFile(wafGlobalExclusionsPath())
	if err != nil {
		return nil, nil
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SecRuleRemoveById"):
			rules = append(rules, strings.Fields(line)[1:]...)
		case strings.HasPrefix(line, "SecRuleRemoveByTag"):
			for _, tok := range strings.Fields(strings.TrimPrefix(line, "SecRuleRemoveByTag")) {
				if t := strings.Trim(tok, `"`); t != "" {
					tags = append(tags, t)
				}
			}
		}
	}
	return rules, tags
}

func wafWriteGlobalExclusions(rules, tags []string) error {
	path := wafGlobalExclusionsPath()
	if len(rules) == 0 && len(tags) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		caddyReloadRun()
		return nil
	}
	var b strings.Builder
	b.WriteString("# Server-wide WAF rule exclusions, managed from OpenAdmin > Security > Web Firewall\n")
	for _, id := range rules {
		b.WriteString("SecRuleRemoveById " + id + "\n")
	}
	for _, t := range tags {
		b.WriteString("SecRuleRemoveByTag \"" + t + "\"\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	caddyReloadRun()
	return nil
}

func wafContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func wafWithout(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// wafDomainOwners maps domain to username from the panel DB, injectable for tests
var wafDomainOwners = func(db *sql.DB) map[string]string {
	owners := map[string]string{}
	if db == nil {
		return owners
	}
	if rows, err := paneldb.GetAllDomains(db); err == nil {
		for _, row := range rows {
			d, _ := row["domain_url"].(string)
			u, _ := row["username"].(string)
			owners[d] = u
		}
	}
	return owners
}

// wafAllDomains lists every domain that has a caddy conf, owners come from the panel DB when it's reachable
func (wf *WAF) wafAllDomains() (domains []string, owners map[string]string) {
	owners = wafDomainOwners(wf.MySQL)
	entries, _ := os.ReadDir(CaddyDomainsConfDir)
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".conf") && !e.IsDir() {
			domains = append(domains, strings.TrimSuffix(name, ".conf"))
		}
	}
	sort.Strings(domains)
	return domains, owners
}

// wafResolveTarget treats anything with a dot as a domain and the rest as a username
func (wf *WAF) wafResolveTarget(target string) (kind string, domains []string, owners map[string]string, ok bool) {
	all, owners := wf.wafAllDomains()
	if strings.Contains(target, ".") {
		if !wafDomainRE.MatchString(target) || !wafContains(all, target) {
			return "", nil, owners, false
		}
		return "domain", []string{target}, owners, true
	}
	if !wafUsernameRE.MatchString(target) {
		return "", nil, owners, false
	}
	for _, d := range all {
		if owners[d] == target {
			domains = append(domains, d)
		}
	}
	return "user", domains, owners, len(domains) > 0
}

// wafLogRule is one rule that matched a request
type wafLogRule struct {
	ID       string `json:"id"`
	Msg      string `json:"msg"`
	Category string `json:"category"`
	Data     string `json:"data,omitempty"`
}

// wafLogEvent is one request the firewall flagged
type wafLogEvent struct {
	Time   string       `json:"time"`
	Domain string       `json:"domain"`
	IP     string       `json:"ip"`
	Method string       `json:"method"`
	URI    string       `json:"uri"`
	Status int          `json:"status"`
	Result string       `json:"result"`
	Rules  []wafLogRule `json:"rules"`
}

type wafRuleGroup struct {
	wafLogRule
	Count    int      `json:"count"`
	Blocked  int      `json:"blocked"`
	Domains  []string `json:"domains"`
	Paths    []string `json:"paths"`
	LastSeen string   `json:"last_seen"`
	Disabled bool     `json:"disabled_server_wide"`
	// set on the domain logs page when the domain itself already excludes the rule
	DisabledHere bool `json:"disabled_for_domain,omitempty"`
}

type wafDomainHits struct {
	Domain     string `json:"domain"`
	Owner      string `json:"owner"`
	Blocked    int    `json:"blocked"`
	WouldBlock int    `json:"would_block"`
	Flagged    int    `json:"flagged"`
	Total      int    `json:"total"`
	LastSeen   string `json:"last_seen"`
}

type wafIPHits struct {
	IP      string `json:"ip"`
	Count   int    `json:"count"`
	Blocked int    `json:"blocked"`
}

// wafLogReport is the aggregate behind the logs pages and the overview's top domains
type wafLogReport struct {
	Events     []wafLogEvent   `json:"events"`
	Rules      []wafRuleGroup  `json:"rules"`
	Domains    []wafDomainHits `json:"domains"`
	IPs        []wafIPHits     `json:"ips"`
	Blocked    int             `json:"blocked"`
	WouldBlock int             `json:"would_block"`
	Flagged    int             `json:"flagged"`
}

// wafLogTailLines is how many of the newest entries per domain log get summarized
const wafLogTailLines = 500

var (
	wafLogIDRE   = regexp.MustCompile(`\[id "(\d+)"\]`)
	wafLogMsgRE  = regexp.MustCompile(`\[msg "([^"]*)"\]`)
	wafLogDataRE = regexp.MustCompile(`\[data "([^"]*)"\]`)
	wafLogTagRE  = regexp.MustCompile(`\[tag "([^"]+)"\]`)
)

var wafAttackCategories = map[string]string{
	"attack-sqli": "SQL injection", "attack-xss": "Cross-site scripting (XSS)", "attack-rce": "Remote command execution",
	"attack-lfi": "Access to restricted files", "attack-rfi": "Remote file inclusion", "attack-php": "PHP code injection",
	"attack-java": "Java code injection", "attack-injection-php": "PHP code injection", "attack-injection-generic": "Code injection",
	"attack-generic": "Code injection", "attack-protocol": "Malformed request", "attack-reputation-scanner": "Vulnerability scanner",
	"attack-reputation-scripting": "Automated script", "attack-fixation": "Session hijacking", "attack-disclosure": "Information leak",
	"attack-multipart-header": "Malformed upload", "attack-ssrf": "Server-side request forgery", "attack-deserialization": "Unsafe data deserialization",
}

func wafRuleCategory(tags []string) string {
	for _, t := range tags {
		if c, ok := wafAttackCategories[t]; ok {
			return c
		}
	}
	for _, t := range tags {
		if strings.HasPrefix(t, "attack-") {
			return strings.ReplaceAll(strings.TrimPrefix(t, "attack-"), "-", " ")
		}
	}
	return "Suspicious request"
}

type wafRawLogEntry struct {
	Transaction struct {
		Timestamp string `json:"timestamp"`
		ClientIP  string `json:"client_ip"`
		Request   struct {
			Method string `json:"method"`
			URI    string `json:"uri"`
		} `json:"request"`
		Response struct {
			Status int `json:"status"`
		} `json:"response"`
		IsInterrupted bool `json:"is_interrupted"`
	} `json:"transaction"`
	Messages []struct {
		ErrorMessage string `json:"error_message"`
	} `json:"messages"`
}

// wafReadLogTail returns up to max of the newest lines of a JSON log, newest first, reading backwards
func wafReadLogTail(path string, max int) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	const blockSize = 64 * 1024
	var out [][]byte
	var rest []byte
	for pos := info.Size(); pos > 0 && len(out) < max; {
		size := int64(blockSize)
		if size > pos {
			size = pos
		}
		pos -= size
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, pos); err != nil {
			return out
		}
		lines := bytes.Split(append(buf, rest...), []byte("\n"))
		// first piece may be cut in half, keep it for the next block
		rest = lines[0]
		if pos == 0 {
			rest = nil
		} else {
			lines = lines[1:]
		}
		for i := len(lines) - 1; i >= 0 && len(out) < max; i-- {
			if l := bytes.TrimSpace(lines[i]); len(l) > 0 {
				out = append(out, append([]byte(nil), l...))
			}
		}
	}
	return out
}

// wafParseEvent turns a raw log line into an event, ok is false for logged requests no rule matched (plain 404s)
func wafParseEvent(domain string, line []byte) (wafLogEvent, bool) {
	var e wafRawLogEntry
	if json.Unmarshal(line, &e) != nil {
		return wafLogEvent{}, false
	}
	ev := wafLogEvent{
		Time: e.Transaction.Timestamp, Domain: domain, IP: e.Transaction.ClientIP, Method: e.Transaction.Request.Method,
		URI: e.Transaction.Request.URI, Status: e.Transaction.Response.Status,
	}
	scored := false
	for _, m := range e.Messages {
		id := firstSubmatchOr(wafLogIDRE, m.ErrorMessage)
		if id == "" {
			continue
		}
		if wafIsScoringRule(id) {
			scored = scored || !strings.HasPrefix(id, "980")
			continue
		}
		dup := false
		for _, r := range ev.Rules {
			dup = dup || r.ID == id
		}
		if dup {
			continue
		}
		var tags []string
		for _, t := range wafLogTagRE.FindAllStringSubmatch(m.ErrorMessage, -1) {
			tags = append(tags, t[1])
		}
		ev.Rules = append(ev.Rules, wafLogRule{
			ID: id, Msg: firstSubmatchOr(wafLogMsgRE, m.ErrorMessage), Category: wafRuleCategory(tags),
			Data: strings.TrimPrefix(firstSubmatchOr(wafLogDataRE, m.ErrorMessage), "Matched Data: "),
		})
	}
	if len(ev.Rules) == 0 && !e.Transaction.IsInterrupted {
		return ev, false
	}
	switch {
	case e.Transaction.IsInterrupted:
		ev.Result = "blocked"
	case scored:
		ev.Result = "would_block"
	default:
		ev.Result = "flagged"
	}
	return ev, true
}

func firstSubmatchOr(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// wafBuildReport summarizes the newest log entries of each domain, maxEvents caps the latest-requests list
func wafBuildReport(domains []string, owners map[string]string, maxEvents int) wafLogReport {
	var rep wafLogReport
	groups := map[string]*wafRuleGroup{}
	ips := map[string]*wafIPHits{}
	globalRules, _ := wafReadGlobalExclusions()

	for _, domain := range domains {
		hits := wafDomainHits{Domain: domain, Owner: owners[domain]}
		for _, line := range wafReadLogTail(filepath.Join(WAFLogDir, domain+".log"), wafLogTailLines) {
			ev, ok := wafParseEvent(domain, line)
			if !ok {
				continue
			}
			hits.Total++
			if hits.LastSeen == "" || ev.Time > hits.LastSeen {
				hits.LastSeen = ev.Time
			}
			switch ev.Result {
			case "blocked":
				hits.Blocked++
				rep.Blocked++
			case "would_block":
				hits.WouldBlock++
				rep.WouldBlock++
			default:
				hits.Flagged++
				rep.Flagged++
			}
			rep.Events = append(rep.Events, ev)

			ip := ips[ev.IP]
			if ip == nil {
				ip = &wafIPHits{IP: ev.IP}
				ips[ev.IP] = ip
			}
			ip.Count++
			if ev.Result == "blocked" {
				ip.Blocked++
			}

			for _, r := range ev.Rules {
				g := groups[r.ID]
				if g == nil {
					g = &wafRuleGroup{wafLogRule: r, Disabled: wafContains(globalRules, r.ID)}
					groups[r.ID] = g
				}
				g.Count++
				if ev.Result != "flagged" {
					g.Blocked++
				}
				if ev.Time > g.LastSeen {
					g.LastSeen = ev.Time
				}
				if !wafContains(g.Domains, domain) {
					g.Domains = append(g.Domains, domain)
				}
				path := ev.URI
				if i := strings.IndexByte(path, '?'); i >= 0 {
					path = path[:i]
				}
				if len(g.Paths) < 3 && !wafContains(g.Paths, path) {
					g.Paths = append(g.Paths, path)
				}
			}
		}
		if hits.Total > 0 {
			rep.Domains = append(rep.Domains, hits)
		}
	}

	// log timestamps are YYYY/MM/DD HH:MM:SS so string order is time order
	sort.SliceStable(rep.Events, func(i, j int) bool { return rep.Events[i].Time > rep.Events[j].Time })
	if len(rep.Events) > maxEvents {
		rep.Events = rep.Events[:maxEvents]
	}
	for _, g := range groups {
		rep.Rules = append(rep.Rules, *g)
	}
	sort.SliceStable(rep.Rules, func(i, j int) bool {
		if rep.Rules[i].Blocked != rep.Rules[j].Blocked {
			return rep.Rules[i].Blocked > rep.Rules[j].Blocked
		}
		if rep.Rules[i].Count != rep.Rules[j].Count {
			return rep.Rules[i].Count > rep.Rules[j].Count
		}
		return rep.Rules[i].ID < rep.Rules[j].ID
	})
	sort.SliceStable(rep.Domains, func(i, j int) bool {
		if rep.Domains[i].Blocked != rep.Domains[j].Blocked {
			return rep.Domains[i].Blocked > rep.Domains[j].Blocked
		}
		return rep.Domains[i].Total > rep.Domains[j].Total
	})
	for _, ip := range ips {
		rep.IPs = append(rep.IPs, *ip)
	}
	sort.SliceStable(rep.IPs, func(i, j int) bool {
		if rep.IPs[i].Count != rep.IPs[j].Count {
			return rep.IPs[i].Count > rep.IPs[j].Count
		}
		return rep.IPs[i].IP < rep.IPs[j].IP
	})
	if len(rep.IPs) > 20 {
		rep.IPs = rep.IPs[:20]
	}
	if rep.Events == nil {
		rep.Events = []wafLogEvent{}
	}
	return rep
}

// wafProfileRow is one app profile on the overview page
type wafProfileRow struct {
	Key       string
	Name      string
	Installed bool
	Domains   int
}

func (wf *WAF) wafProfileRows(domains []string) []wafProfileRow {
	used := map[string]int{}
	for _, d := range domains {
		if content, err := os.ReadFile(wafDomainConfPath(d)); err == nil {
			for _, k := range wafParseProfiles(string(content)) {
				used[k]++
			}
		}
	}
	var rows []wafProfileRow
	for _, p := range wafProfileCatalog {
		rows = append(rows, wafProfileRow{Key: p.Key, Name: p.Name, Installed: wafPluginInstalled(p.Key), Domains: used[p.Key]})
	}
	return rows
}

// wafCommit is one line of the CRS git history
type wafCommit struct {
	Hash, Date, Subject string
}

func wafCRSHistory(n string) []wafCommit {
	var out []wafCommit
	for _, line := range strings.Split(wafGitLogRun(WAFCRSDir, n), "\n") {
		if parts := strings.SplitN(line, "|", 3); len(parts) == 3 {
			out = append(out, wafCommit{parts[0], parts[1], parts[2]})
		}
	}
	return out
}

// wafCRSPending lists CRS commits fetched but not pulled yet, newest first
func wafCRSPending(n string) []wafCommit {
	out, err := wafGitRun(WAFCRSDir, "log", "-n", n, "--format=%h|%cd|%s", "--date=short", "HEAD..@{u}")
	if err != nil {
		return nil
	}
	var commits []wafCommit
	for _, line := range strings.Split(out, "\n") {
		if parts := strings.SplitN(line, "|", 3); len(parts) == 3 {
			commits = append(commits, wafCommit{parts[0], parts[1], parts[2]})
		}
	}
	return commits
}

// wafFirstLines keeps flash messages short when opencli prints a lot
func wafFirstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, " ")
}

// wafRedownloadProfile moves the plugin aside, reinstalls it and puts the old copy back if the download fails, domains include it so it can't go missing
func wafRedownloadProfile(key string) (string, error) {
	dir := wafPluginDir(key)
	backup := dir + ".bak"
	os.RemoveAll(backup)
	hadOld := os.Rename(dir, backup) == nil
	out, err := wafOpenCLIOutput("waf", "plugins", "install")
	if !wafPluginInstalled(key) {
		if hadOld {
			os.RemoveAll(dir)
			os.Rename(backup, dir)
		}
		if err == nil {
			err = errors.New("plugin files missing after download")
		}
		return out, err
	}
	os.RemoveAll(backup)
	return out, nil
}

// wafSafeBack only follows redirects back into the WAF pages
func wafSafeBack(r *http.Request, fallback string) string {
	back := r.PostFormValue("back")
	if strings.HasPrefix(back, "/security/waf") && !strings.Contains(back, "//") {
		return back
	}
	return fallback
}

func wafSplitFields(s string) []string {
	return strings.Fields(strings.NewReplacer(",", " ", "\"", " ").Replace(s))
}

// handleGlobalRulesPOST covers the server-wide exclusion actions on /security/waf/rules, handled reports whether action was one of them
func (wf *WAF) handleGlobalRulesPOST(w http.ResponseWriter, r *http.Request, action string) (handled bool) {
	rules, tags := wafReadGlobalExclusions()
	var err error
	var msg string
	switch action {
	case "global_exclusions":
		if rules, err = wafCleanRules(wafSplitFields(r.PostFormValue("removed_rules"))); err == nil {
			tags, err = wafCleanTags(wafSplitFields(r.PostFormValue("removed_tags")))
		}
		msg = "Server-wide rule exclusions saved."
	case "disable_rule":
		id := strings.TrimSpace(r.PostFormValue("rule_id"))
		var clean []string
		if clean, err = wafCleanRules([]string{id}); err == nil && len(clean) == 1 && !wafContains(rules, id) {
			rules = append(rules, id)
		}
		msg = "Rule " + id + " disabled on all domains."
	case "enable_rule":
		id := strings.TrimSpace(r.PostFormValue("rule_id"))
		rules = wafWithout(rules, id)
		msg = "Rule " + id + " enabled again on all domains."
	default:
		return false
	}
	if err == nil {
		err = wafWriteGlobalExclusions(rules, tags)
	}
	if err != nil {
		auth.AddFlash(w, r, wf.Sessions, "Error: "+err.Error(), "error")
	} else {
		auth.AddFlash(w, r, wf.Sessions, msg, "success")
	}
	http.Redirect(w, r, wafSafeBack(r, "/security/waf/rules"), http.StatusSeeOther)
	return true
}

// wafDomainRow is one domain in the rules overview tables
type wafDomainRow struct {
	wafDomainState
	Disabled int
	// LevelDots fills one dot per level of strength like OpenPanel's WAF list, custom gets none
	LevelDots []bool
}

func wafDomainRows(domains []string, owners map[string]string) []wafDomainRow {
	rows := make([]wafDomainRow, 0, len(domains))
	for _, d := range domains {
		st, _ := wafReadDomainState(d)
		st.Owner = owners[d]
		dots := make([]bool, 3)
		for i := range dots {
			dots[i] = i < wafLevelRank[st.Level] && st.Level != "custom"
		}
		rows = append(rows, wafDomainRow{wafDomainState: st, Disabled: len(st.RemovedRules) + len(st.RemovedTags), LevelDots: dots})
	}
	return rows
}

func wafKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func wafUsersOf(domains []string, owners map[string]string) []string {
	seen := map[string]bool{}
	var users []string
	for _, d := range domains {
		if u := owners[d]; u != "" && !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	sort.Strings(users)
	return users
}

// ServeWAFDomains handles GET /security/waf/domains, every domain with its WAF mode and links to manage it per domain or per user
func (wf *WAF) ServeWAFDomains(w http.ResponseWriter, r *http.Request) {
	domains, owners := wf.wafAllDomains()
	rows := wafDomainRows(domains, owners)
	sortCol, sortDirection := readSort(r, wafDomainSortKeys)
	sortWAFDomains(rows, sortCol, sortDirection)
	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows)
		return
	}
	webtemplates.Render(w, "security_coraza_domains.html", mergeChrome(map[string]interface{}{
		"DomainRows":    rows,
		"SortCol":       sortCol,
		"SortDirection": sortDirection,
		"Users":         wafUsersOf(domains, owners),
		"Flashes":       auth.PopFlashes(w, r, wf.Sessions),
		"WAFTab":        "domains",
	}, r, "Web Firewall Domains"))
}

// ServeWAFTargetRules handles GET/POST /security/waf/rules/{target}, target is a domain or a username
func (wf *WAF) ServeWAFTargetRules(w http.ResponseWriter, r *http.Request) {
	target := r.PathValue("target")
	kind, domains, owners, ok := wf.wafResolveTarget(target)
	if !ok {
		auth.AddFlash(w, r, wf.Sessions, "Error: no domain or user with WAF config found for "+target, "error")
		http.Redirect(w, r, "/security/waf/domains", http.StatusSeeOther)
		return
	}
	self := "/security/waf/rules/" + target

	if r.Method == http.MethodPost {
		r.ParseForm()
		wf.handleTargetRulesPOST(w, r, kind, domains)
		http.Redirect(w, r, wafSafeBack(r, self), http.StatusSeeOther)
		return
	}

	rows := wafDomainRows(domains, owners)
	globalRules, globalTags := wafReadGlobalExclusions()
	data := map[string]interface{}{
		"Kind":        kind,
		"Target":      target,
		"Owner":       owners[domains[0]],
		"Rows":        rows,
		"GlobalRules": globalRules,
		"GlobalTags":  globalTags,
		"CSRFToken":   csrf.Token(r),
		"Flashes":     auth.PopFlashes(w, r, wf.Sessions),
		"WAFTab":      "domains",
	}
	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"kind": kind, "target": target, "domains": rows})
		return
	}
	webtemplates.Render(w, "security_coraza_target.html", mergeChrome(data, r, "Web Firewall Rules: "+target))
}

func (wf *WAF) handleTargetRulesPOST(w http.ResponseWriter, r *http.Request, kind string, domains []string) {
	action := r.PostFormValue("action")
	engine := r.PostFormValue("engine")
	if engine != "" && engine != "On" && engine != "Off" && engine != "DetectionOnly" {
		auth.AddFlash(w, r, wf.Sessions, "Error: invalid WAF mode.", "error")
		return
	}
	id := strings.TrimSpace(r.PostFormValue("rule_id"))
	if action == "disable_rule" || action == "enable_rule" {
		if _, err := wafCleanRules([]string{id}); err != nil || id == "" {
			if err == nil {
				err = errWAFRuleID
			}
			auth.AddFlash(w, r, wf.Sessions, "Error: "+err.Error(), "error")
			return
		}
	}

	var failed []string
	for _, d := range domains {
		st, found := wafReadDomainState(d)
		if !found {
			failed = append(failed, d)
			continue
		}
		rules, tags := st.RemovedRules, st.RemovedTags
		var err error
		switch action {
		case "save":
			if kind != "domain" {
				auth.AddFlash(w, r, wf.Sessions, "Error: invalid action.", "error")
				return
			}
			if rules, err = wafCleanRules(wafSplitFields(r.PostFormValue("removed_rules"))); err == nil {
				tags, err = wafCleanTags(wafSplitFields(r.PostFormValue("removed_tags")))
			}
			if err != nil {
				auth.AddFlash(w, r, wf.Sessions, "Error: "+err.Error(), "error")
				return
			}
		case "engine":
			if engine == "" {
				auth.AddFlash(w, r, wf.Sessions, "Error: invalid WAF mode.", "error")
				return
			}
		case "disable_rule":
			if !wafContains(rules, id) {
				rules = append(rules, id)
			}
		case "enable_rule":
			rules = wafWithout(rules, id)
		default:
			auth.AddFlash(w, r, wf.Sessions, "Error: invalid action.", "error")
			return
		}
		if err := wafSaveDomain(d, engine, rules, tags); err != nil {
			failed = append(failed, d)
		}
	}

	if len(failed) > 0 {
		auth.AddFlash(w, r, wf.Sessions, "Error: failed to update WAF for "+strings.Join(failed, ", "), "error")
		return
	}
	scope := domains[0]
	if kind == "user" {
		scope = "all domains of this user"
	}
	switch action {
	case "save":
		auth.AddFlash(w, r, wf.Sessions, "WAF settings saved for "+scope+".", "success")
	case "engine":
		auth.AddFlash(w, r, wf.Sessions, "WAF mode set to "+wafEngineLabel(engine)+" for "+scope+".", "success")
	case "disable_rule":
		auth.AddFlash(w, r, wf.Sessions, "Rule "+id+" disabled for "+scope+".", "success")
	case "enable_rule":
		auth.AddFlash(w, r, wf.Sessions, "Rule "+id+" enabled again for "+scope+".", "success")
	}
}

func wafEngineLabel(engine string) string {
	switch engine {
	case "On":
		return "Protect"
	case "DetectionOnly":
		return "Monitor only"
	case "Off":
		return "Off"
	}
	return engine
}

// ServeWAFLogs handles GET /security/waf/logs and GET/POST /security/waf/logs/{target}
func (wf *WAF) ServeWAFLogs(w http.ResponseWriter, r *http.Request) {
	target := r.PathValue("target")
	kind := "server"
	var domains []string
	var owners map[string]string
	if target == "" {
		domains, owners = wf.wafAllDomains()
	} else {
		var ok bool
		kind, domains, owners, ok = wf.wafResolveTarget(target)
		if !ok {
			auth.AddFlash(w, r, wf.Sessions, "Error: no domain or user with WAF logs found for "+target, "error")
			http.Redirect(w, r, "/security/waf/logs", http.StatusSeeOther)
			return
		}
	}
	self := strings.TrimSuffix("/security/waf/logs/"+target, "/")

	if r.Method == http.MethodPost {
		r.ParseForm()
		if target == "" || r.PostFormValue("action") != "purge" {
			auth.AddFlash(w, r, wf.Sessions, "Error: invalid action.", "error")
		} else {
			var failed []string
			for _, d := range domains {
				if err := os.Truncate(filepath.Join(WAFLogDir, d+".log"), 0); err != nil && !os.IsNotExist(err) {
					failed = append(failed, d)
				}
			}
			if len(failed) > 0 {
				auth.AddFlash(w, r, wf.Sessions, "Error: failed to clear WAF logs for "+strings.Join(failed, ", "), "error")
			} else {
				auth.AddFlash(w, r, wf.Sessions, "WAF logs cleared for "+target+".", "success")
			}
		}
		http.Redirect(w, r, self, http.StatusSeeOther)
		return
	}

	maxEvents := 50
	if kind == "domain" {
		maxEvents = 200
	}
	rep := wafBuildReport(domains, owners, maxEvents)
	if kind == "domain" {
		st, _ := wafReadDomainState(domains[0])
		for i := range rep.Rules {
			rep.Rules[i].DisabledHere = wafContains(st.RemovedRules, rep.Rules[i].ID)
		}
	}
	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rep)
		return
	}

	// each table on the logs page sorts on its own params so the links don't clash
	sorts := map[string][2]string{}
	for prefix, keys := range map[string]map[string]bool{"d": wafLogDomainSortKeys, "r": wafLogRuleSortKeys, "i": wafLogIPSortKeys, "e": wafLogEventSortKeys} {
		col, dir := readSortParam(r, prefix+"_sort", prefix+"_direction", keys)
		sorts[prefix] = [2]string{col, dir}
	}
	sortWAFLogDomains(rep.Domains, sorts["d"][0], sorts["d"][1])
	sortWAFLogRules(rep.Rules, sorts["r"][0], sorts["r"][1])
	sortWAFLogIPs(rep.IPs, sorts["i"][0], sorts["i"][1])
	sortWAFLogEvents(rep.Events, sorts["e"][0], sorts["e"][1])
	owner := ""
	if kind != "server" {
		owner = owners[domains[0]]
	}
	title := "Web Firewall Logs"
	if target != "" {
		title += ": " + target
	}
	webtemplates.Render(w, "security_coraza_logs.html", mergeChrome(map[string]interface{}{
		"Kind":      kind,
		"Target":    target,
		"Owner":     owner,
		"Self":      self,
		"Report":    rep,
		"Sorts":     sorts,
		"TailLines": wafLogTailLines,
		"Users":     wafUsersOf(wafKeys(owners), owners),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, wf.Sessions),
		"WAFTab":    "logs",
	}, r, title))
}
