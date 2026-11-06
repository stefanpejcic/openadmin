package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"openadmin/internal/cluster"
	"openadmin/internal/paneldb"
)

// where OpenPanel's daily-summary saves each account's day
var userSummariesDir = "/etc/openpanel/openpanel/core/users/"

// only look this far back for a day that has summaries
const summaryLookbackDays = 7

// userSummary is the part of OpenPanel's saved summary JSON the dashboard needs
type userSummary struct {
	// where this account lives when a master adds up several servers, not part of the saved file
	Server string `json:"-"`
	Link   string `json:"-"`

	Username     string `json:"username"`
	Date         string `json:"date"`
	PrevRequests int    `json:"prev_requests"`
	Attention    []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"attention"`
	Look []struct {
		Title string `json:"title"`
	} `json:"look"`
	Websites []struct {
		Name         string `json:"name"`
		Requests     int    `json:"requests"`
		UniqueIPs    int    `json:"unique_ips"`
		Bytes        int64  `json:"bytes"`
		ServerErrors int    `json:"server_errors"`
		WAFBlocked   int    `json:"waf_blocked"`
	} `json:"websites"`
}

// summaryFinding is one account's problem on the widget
type summaryFinding struct {
	Server   string
	Link     string
	Username string
	Title    string
	Detail   string
}

// summarySite is one website on the busiest list
type summarySite struct {
	Server   string
	Link     string
	Name     string
	Username string
	Requests string
	Share    int
}

// serverSummary is every account's daily summary for one day added up
type serverSummary struct {
	Date      string
	DayLabel  string
	Headline  string
	Accounts  int
	Requests  string
	Visitors  string
	Sent      string
	Errors    string
	Blocked   string
	Delta     string
	DeltaUp   bool
	Attention []summaryFinding
	// more than one when a master adds up its members
	Servers int
	// accounts with something worth a look, only counted
	LookCount    int
	LookAccounts int
	MoreAttn     int
	Sites        []summarySite
	// findings before the list is cut to 8, the collapsed widget shows this count
	AttentionTotal int
}

// summaryEnabled is the summary module being on in OpenPanel, the widget only shows then
func summaryEnabled() bool {
	return slices.Contains(modulesEnabledList(ModulesConfigFilePath), "summary")
}

// the files only change once a day, so a few minutes of caching per scope is plenty
var (
	serverSummaryMu    sync.Mutex
	serverSummaryCache = map[string]*ttlCache[*serverSummary]{}
)

// ClusterManager is set on every server, a master uses it to add up its members
var ClusterManager *cluster.Manager

type clusterSummaries struct {
	Day   string        `json:"day"`
	Found []userSummary `json:"found"`
}

// ClusterSummaries is this server's part of a master's combined summary
func ClusterSummaries(db *sql.DB) func() any {
	return func() any {
		users, err := paneldb.ListUsernames(db, "")
		if err != nil {
			return clusterSummaries{}
		}
		day, found := collectUserSummaries(users, time.Now())
		return clusterSummaries{Day: day, Found: found}
	}
}

// cachedClusterSummary adds up every member's newest day, a member that hasn't reported that day yet stays out rather than mixing days
func cachedClusterSummary(db *sql.DB, userID int64) *serverSummary {
	key := fmt.Sprintf("cluster:%d", userID)
	serverSummaryMu.Lock()
	c, ok := serverSummaryCache[key]
	if !ok {
		c = &ttlCache[*serverSummary]{ttl: 5 * time.Minute}
		serverSummaryCache[key] = c
	}
	serverSummaryMu.Unlock()
	return c.get(func() *serverSummary {
		parts := []clusterSummaries{}
		if users, err := paneldb.ListUsernames(db, ""); err == nil {
			day, found := collectUserSummaries(users, time.Now())
			for i := range found {
				found[i].Server = ClusterManager.Hostname
			}
			parts = append(parts, clusterSummaries{Day: day, Found: found})
		}
		ClusterManager.CallAll("summaries", func(id, name string, raw json.RawMessage) {
			var p clusterSummaries
			if json.Unmarshal(raw, &p) != nil {
				return
			}
			for i := range p.Found {
				p.Found[i].Server = name
				p.Found[i].Link = ClusterManager.OpenLink(userID, id, "/users/"+p.Found[i].Username)
			}
			parts = append(parts, p)
		})
		newest := ""
		for _, p := range parts {
			if p.Day > newest && len(p.Found) > 0 {
				newest = p.Day
			}
		}
		var found []userSummary
		servers := 0
		for _, p := range parts {
			if p.Day == newest && len(p.Found) > 0 {
				found = append(found, p.Found...)
				servers++
			}
		}
		out := summarizeDay(newest, found)
		if out != nil {
			out.Servers = servers
		}
		return out
	})
}

// cachedServerSummary is the summary for everyone, or only owner's accounts when owner is a reseller
func cachedServerSummary(db *sql.DB, owner string) *serverSummary {
	serverSummaryMu.Lock()
	c, ok := serverSummaryCache[owner]
	if !ok {
		c = &ttlCache[*serverSummary]{ttl: 5 * time.Minute}
		serverSummaryCache[owner] = c
	}
	serverSummaryMu.Unlock()
	return c.get(func() *serverSummary {
		users, err := paneldb.ListUsernames(db, owner)
		if err != nil {
			return nil
		}
		return buildServerSummary(users, time.Now())
	})
}

func loadUserSummary(username, date string) (userSummary, bool) {
	var s userSummary
	raw, err := os.ReadFile(filepath.Join(userSummariesDir, username, "summaries", date+".json"))
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return s, false
	}
	return s, true
}

// buildServerSummary adds up the newest day any of these accounts has a summary for, nil when none do
func buildServerSummary(users []string, now time.Time) *serverSummary {
	day, found := collectUserSummaries(users, now)
	return summarizeDay(day, found)
}

func collectUserSummaries(users []string, now time.Time) (string, []userSummary) {
	var day string
	var found []userSummary
	for i := 1; i <= summaryLookbackDays && len(found) == 0; i++ {
		day = now.AddDate(0, 0, -i).Format("2006-01-02")
		for _, u := range users {
			if s, ok := loadUserSummary(u, day); ok {
				if s.Username == "" {
					s.Username = u
				}
				found = append(found, s)
			}
		}
	}
	return day, found
}

func summarizeDay(day string, found []userSummary) *serverSummary {
	if len(found) == 0 {
		return nil
	}

	out := &serverSummary{Date: day, Accounts: len(found)}
	if t, err := time.Parse("2006-01-02", day); err == nil {
		out.DayLabel = t.Format("Monday, 2 January")
	}
	var requests, visitors, errors, blocked, prev int
	var sent int64
	prevKnown := true
	type site struct {
		name, user string
		requests   int
		server     string
		link       string
	}
	var sites []site
	lookAccounts := map[string]bool{}
	for _, s := range found {
		for _, w := range s.Websites {
			requests += w.Requests
			visitors += w.UniqueIPs
			sent += w.Bytes
			errors += w.ServerErrors
			blocked += w.WAFBlocked
			if w.Requests > 0 {
				sites = append(sites, site{w.Name, s.Username, w.Requests, s.Server, s.Link})
			}
		}
		if s.PrevRequests < 0 {
			prevKnown = false
		} else {
			prev += s.PrevRequests
		}
		for _, it := range s.Attention {
			// plugin names come from WordPress already entity encoded, the template escapes them again
			out.Attention = append(out.Attention, summaryFinding{Server: s.Server, Link: s.Link, Username: s.Username, Title: html.UnescapeString(it.Title), Detail: html.UnescapeString(it.Detail)})
		}
		if len(s.Look) > 0 {
			out.LookCount += len(s.Look)
			lookAccounts[s.Username] = true
		}
	}
	out.LookAccounts = len(lookAccounts)
	total := len(out.Attention)

	out.Requests, out.Visitors, out.Errors, out.Blocked = commaNum(requests), commaNum(visitors), commaNum(errors), commaNum(blocked)
	out.Sent = humanSize(sent)
	if prevKnown && prev > 0 {
		pct := (requests - prev) * 100 / prev
		out.DeltaUp = pct >= 0
		out.Delta = fmt.Sprintf("%+d%%", pct)
	}

	sort.Slice(sites, func(i, j int) bool {
		if sites[i].requests != sites[j].requests {
			return sites[i].requests > sites[j].requests
		}
		return sites[i].name < sites[j].name
	})
	if len(sites) > 5 {
		sites = sites[:5]
	}
	for _, s := range sites {
		out.Sites = append(out.Sites, summarySite{Server: s.server, Link: s.link, Name: s.name, Username: s.user, Requests: commaNum(s.requests), Share: max(s.requests*100/sites[0].requests, 1)})
	}

	// the widget only lists the first few, the rest are a count
	out.AttentionTotal = len(out.Attention)
	if len(out.Attention) > 8 {
		out.MoreAttn = len(out.Attention) - 8
		out.Attention = out.Attention[:8]
	}

	accounts := countWord(len(found), "account", "accounts")
	if requests == 0 {
		out.Headline = "Websites on " + accounts + " had no visitors."
	} else {
		out.Headline = fmt.Sprintf("Websites on %s handled %s requests from %s.", accounts, commaNum(requests), countWord(visitors, "visitor", "visitors"))
	}
	switch {
	case total > 0:
		out.Headline += " " + countWord(total, "thing needs", "things need") + " attention."
	case out.LookCount > 0:
		out.Headline += " Nothing urgent, but " + countWord(out.LookCount, "thing is", "things are") + " worth a look."
	default:
		out.Headline += " Nothing needs attention."
	}
	return out
}

func countWord(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commaNum(n) + " " + many
}

// commaNum writes 4812 as 4,812
func commaNum(n int) string {
	if n < 0 {
		return "-" + commaNum(-n)
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
