package handlers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"openadmin/internal/paneldb"
)

// --- Website traffic ---

// trafficPeaks is the busiest moment in sentinel's snapshots, PerIP only exists in snapshots written after web_conn_ip was added
type trafficPeaks struct {
	Total, PerIP         int
	TotalAt, PerIPAt     time.Time
	TotalSpan, PerIPSpan time.Duration
}

const snapshotTimeLayout = "2006-01-02 15:04:05"

func readTrafficPeaks(path string) trafficPeaks {
	var p trafficPeaks
	f, err := os.Open(path)
	if err != nil {
		return p
	}
	defer f.Close()

	var first, firstIP, last time.Time
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var snap struct {
			TS        string `json:"ts"`
			WebConn   *int   `json:"web_conn"`
			WebConnIP *int   `json:"web_conn_ip"`
		}
		if json.Unmarshal(scanner.Bytes(), &snap) != nil || snap.WebConn == nil {
			continue
		}
		ts, err := time.Parse(snapshotTimeLayout, snap.TS)
		if err != nil {
			continue
		}
		if first.IsZero() {
			first = ts
		}
		last = ts
		if *snap.WebConn >= p.Total {
			p.Total, p.TotalAt = *snap.WebConn, ts
		}
		if snap.WebConnIP != nil {
			if firstIP.IsZero() {
				firstIP = ts
			}
			if *snap.WebConnIP >= p.PerIP {
				p.PerIP, p.PerIPAt = *snap.WebConnIP, ts
			}
		}
	}
	if !first.IsZero() {
		p.TotalSpan = last.Sub(first)
	}
	if !firstIP.IsZero() {
		p.PerIPSpan = last.Sub(firstIP)
	}
	return p
}

// niceCeil rounds up to a number that reads well in a form field
func niceCeil(n int) int {
	step := 10
	switch {
	case n >= 1000:
		step = 100
	case n >= 100:
		step = 50
	}
	return int(math.Ceil(float64(n)/float64(step))) * step
}

// connAdvice is the recommendation shown under one of the Website traffic fields
type connAdvice struct {
	Hint        string
	Recommended int
	// alert would have fired at or below this value, 0 means don't warn
	Peak     int
	WarnText string
}

const minTrafficSpan = 24 * time.Hour

func trafficAdvice(peak int, at time.Time, span time.Duration, floor, domains int, what string) connAdvice {
	if domains == 0 {
		return connAdvice{Hint: "No domains on the server yet, so there's no traffic to base a recommendation on."}
	}
	if span < minTrafficSpan {
		return connAdvice{Hint: fmt.Sprintf("A recommendation shows once the system has 24h of traffic data (%dh so far).", int(span.Hours()))}
	}
	rec := niceCeil(max(peak*2, floor))
	days := int(math.Round(span.Hours() / 24))
	a := connAdvice{
		Hint:        fmt.Sprintf("Highest %s in the last %s: %d on %s. Recommended: %d.", what, pluralDays(days), peak, at.Format("Jan 2 15:04")+" UTC", rec),
		Recommended: rec,
	}
	if peak > 0 {
		a.Peak = peak
		a.WarnText = fmt.Sprintf("At this value you'd have been alerted on %s, when it reached %d.", at.Format("Jan 2"), peak)
	}
	return a
}

func pluralDays(n int) string {
	if n == 1 {
		return "day"
	}
	return fmt.Sprintf("%d days", n)
}

// notifDomainCountRun is swapped out in tests, -1 means the panel DB couldn't be reached
var notifDomainCountRun = func(db *sql.DB) int {
	if db == nil {
		return -1
	}
	c, err := paneldb.GetCounts(db)
	if err != nil {
		return -1
	}
	return c.DomainCount
}

// --- Services ---

var (
	csfBinaryPath = "/usr/sbin/csf"
	coreServices  = map[string]bool{"panel": true, "admin": true, "caddy": true, "mysql": true, "podman": true}
)

// serviceAdvice returns what to show when the service is off and when it's on, empty means nothing to say
func serviceAdvice(key string, modules []string) (whenOff, whenOn string) {
	switch {
	case coreServices[key]:
		return "Recommended: on, OpenPanel depends on this service.", ""
	case key == "named":
		if hasModule(modules, "dns") {
			return "Recommended: on, the DNS module is enabled so BIND9 serves your domains.", ""
		}
		return "", "The DNS module is disabled, so BIND9 is probably not in use."
	case key == "csf":
		if _, err := os.Stat(csfBinaryPath); err == nil {
			return "Recommended: on, Sentinel Firewall (CSF) is installed on this server.", ""
		}
		return "", "CSF isn't installed, so this check has nothing to monitor."
	}
	return "", ""
}

func hasModule(modules []string, name string) bool {
	for _, m := range modules {
		if strings.TrimSpace(m) == name {
			return true
		}
	}
	return false
}

// --- User actions ---

type notifActionGroup struct {
	Name    string
	Actions []notifToggleView
}

func groupUserActions(actions []notifToggleView) []notifActionGroup {
	var groups []notifActionGroup
	for _, a := range actions {
		name := userActionGroupName(a.Key)
		if len(groups) == 0 || groups[len(groups)-1].Name != name {
			groups = append(groups, notifActionGroup{Name: name})
		}
		groups[len(groups)-1].Actions = append(groups[len(groups)-1].Actions, a)
	}
	return groups
}

func userActionGroupName(key string) string {
	switch {
	case strings.HasPrefix(key, "admin_"), key == "reseller_create":
		return "OpenAdmin"
	case strings.HasPrefix(key, "waf_"):
		return "Web Firewall"
	case strings.HasPrefix(key, "user_"):
		return "Users"
	case strings.HasPrefix(key, "ftp_"):
		return "FTP"
	case strings.HasPrefix(key, "domains_"):
		return "Domains"
	case strings.HasPrefix(key, "imunify_"):
		return "ImunifyAV"
	case key == "docker_backup":
		return "Backups"
	}
	return "Other"
}

// --- Login whitelist ---

type whitelistSuggestion struct {
	IP, Note string
}

const maxWhitelistSuggestions = 10

// whitelistSuggestions offers the admin's current IP and IPs that logged into OpenAdmin before, skipping ones already covered
func whitelistSuggestions(currentIP, loginLogPath string, whitelist []string) []whitelistSuggestion {
	covered := func(ip string) bool {
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.IsLoopback() {
			return true
		}
		for _, entry := range whitelist {
			if _, n, err := net.ParseCIDR(withCIDRSuffix(entry)); err == nil && n.Contains(parsed) {
				return true
			}
		}
		return false
	}

	var out []whitelistSuggestion
	seen := map[string]bool{}
	if !covered(currentIP) {
		out = append(out, whitelistSuggestion{IP: currentIP, Note: "your current IP"})
		seen[currentIP] = true
	}

	type past struct {
		users map[string]bool
		last  string
	}
	byIP := map[string]*past{}
	if f, err := os.Open(loginLogPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			// "2026-10-09 16:01:02 stefan 203.0.113.4", sso logins add " (sso)"
			parts := strings.Fields(scanner.Text())
			if len(parts) < 4 {
				continue
			}
			ip := parts[3]
			if seen[ip] || covered(ip) {
				continue
			}
			p := byIP[ip]
			if p == nil {
				p = &past{users: map[string]bool{}}
				byIP[ip] = p
			}
			p.users[parts[2]] = true
			p.last = parts[0]
		}
		f.Close()
	}

	ips := make([]string, 0, len(byIP))
	for ip := range byIP {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return byIP[ips[i]].last > byIP[ips[j]].last })
	for _, ip := range ips {
		if len(out) >= maxWhitelistSuggestions {
			break
		}
		users := make([]string, 0, len(byIP[ip].users))
		for u := range byIP[ip].users {
			users = append(users, u)
		}
		sort.Strings(users)
		out = append(out, whitelistSuggestion{IP: ip, Note: strings.Join(users, ", ") + ", last login " + byIP[ip].last})
	}
	return out
}
