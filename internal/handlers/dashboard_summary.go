package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
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
		Link   string `json:"link"`
	} `json:"attention"`
	Look []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Link   string `json:"link"`
	} `json:"look"`
	Databases []struct {
		Engine         string `json:"engine"`
		Feature        string `json:"feature"`
		Databases      int    `json:"databases"`
		Size           int64  `json:"size"`
		Queries        int64  `json:"queries"`
		Connections    int64  `json:"connections"`
		MaxConnections int64  `json:"max_connections"`
		Errors         int    `json:"errors"`
		Refused        int64  `json:"refused"`
	} `json:"databases"`
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
	// the website it's about, "" for account-wide ones like a failing cron or malware
	Domain string
}

// attentionGroup is one account in Needs attention: its account-wide problems, then each website's problems under it
type attentionGroup struct {
	Server, Link, Username string
	Count                  int
	// how bad the account's problems are together, the list is sorted by it
	Score   int
	General []summaryFinding
	Domains []attentionDomain
}

type attentionDomain struct {
	Name  string
	Items []summaryFinding
}

// Favicon is the website's icon, same source as the websites table
func (d attentionDomain) Favicon() string { return faviconBase() + d.Name }

var (
	attentionHolesRe = regexp.MustCompile(`(\d+) known security hole`)
	attentionTimesRe = regexp.MustCompile(`\((\d+) times?\)`)
	attentionFilesRe = regexp.MustCompile(`found (\d+) malicious`)
)

// findingScore weighs one problem: malware on top, then known security holes and failing tasks by how many, the rest after
func findingScore(f summaryFinding) int {
	num := func(re *regexp.Regexp) int {
		if m := re.FindStringSubmatch(f.Title); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
		return 0
	}
	switch {
	case strings.HasPrefix(f.Title, "ImunifyAV found"):
		return 1000 + num(attentionFilesRe)
	case attentionHolesRe.MatchString(f.Title):
		return 50 + num(attentionHolesRe)
	case strings.Contains(f.Title, "failed"):
		return 30 + min(num(attentionTimesRe), 50)
	}
	return 20
}

var (
	attentionPluginRe = regexp.MustCompile(`^They're in (.+?)\. `)
	attentionDigitsRe = regexp.MustCompile(`\d+`)
)

// mainAttentionProblem names what at least 40% of the accounts (3 or more) in Needs attention have in common
func mainAttentionProblem(groups []attentionGroup) string {
	if len(groups) < 3 {
		return ""
	}
	type hit struct {
		label  string
		plugin bool
		n      int
	}
	hits := map[string]*hit{}
	var order []string
	for _, g := range groups {
		seen := map[string]bool{}
		items := append([]summaryFinding{}, g.General...)
		for _, d := range g.Domains {
			items = append(items, d.Items...)
		}
		for _, f := range items {
			key, label, plugin := "", "", false
			if m := attentionPluginRe.FindStringSubmatch(f.Detail + " "); m != nil {
				key, label, plugin = "plugin:"+m[1], m[1], true
			} else {
				label = attentionDigitsRe.ReplaceAllString(f.Title, "#")
				key = "title:" + label
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if hits[key] == nil {
				hits[key] = &hit{label: label, plugin: plugin}
				order = append(order, key)
			}
			hits[key].n++
		}
	}
	var best *hit
	for _, k := range order {
		if best == nil || hits[k].n > best.n {
			best = hits[k]
		}
	}
	share := best.n * 100 / len(groups)
	if best.n < 3 || share < 40 {
		return ""
	}
	if best.plugin {
		return fmt.Sprintf("The main problem is %s, with known security holes on %d of the %d accounts that need attention (%d%%). Updating that clears most of the list.", best.label, best.n, len(groups), share)
	}
	return fmt.Sprintf("The main problem is \"%s\" on %d of the %d accounts that need attention (%d%%).", best.label, best.n, len(groups), share)
}

// groupAttention puts each account's findings together, worst accounts first, websites in the order their findings came
func groupAttention(list []summaryFinding) []attentionGroup {
	var out []attentionGroup
	index := map[string]int{}
	for _, f := range list {
		key := f.Server + "|" + f.Username
		gi, ok := index[key]
		if !ok {
			gi = len(out)
			index[key] = gi
			out = append(out, attentionGroup{Server: f.Server, Link: f.Link, Username: f.Username})
		}
		g := &out[gi]
		g.Count++
		g.Score += findingScore(f)
		if f.Domain == "" {
			g.General = append(g.General, f)
			continue
		}
		found := false
		for di := range g.Domains {
			if g.Domains[di].Name == f.Domain {
				g.Domains[di].Items = append(g.Domains[di].Items, f)
				found = true
				break
			}
		}
		if !found {
			g.Domains = append(g.Domains, attentionDomain{Name: f.Domain, Items: []summaryFinding{f}})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Username < out[b].Username
	})
	return out
}

// findingDomain is the website a finding is about: the link's domain, otherwise the longest of the account's sites named in it
func findingDomain(link, text string, sites []string) string {
	if _, q, ok := strings.Cut(link, "?"); ok {
		if v, err := url.ParseQuery(q); err == nil && v.Get("domain") != "" {
			return v.Get("domain")
		}
	}
	best := ""
	for _, d := range sites {
		if len(d) > len(best) && strings.Contains(text, d) {
			best = d
		}
	}
	return best
}

// trimDomainTitle drops the domain the group already shows, "x.com has 2 known security holes" reads "2 known security holes"
func trimDomainTitle(title, domain string) string {
	if domain == "" {
		return title
	}
	if rest, ok := strings.CutPrefix(title, domain+" has "); ok && rest != "" {
		return strings.ToUpper(rest[:1]) + rest[1:]
	}
	return title
}

// summaryDatabase is one account's database server on the busiest list
type summaryDatabase struct {
	Server, Link, Username, Engine string
	Queries, Size, Errors          string
	HasErrors                      bool
	Share                          int
}

// Simple Icons logos in the same color as the MySQL and MariaDB ones
var summaryEngineIcons = map[string]template.HTML{
	"postgresql": `<svg role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" class="size-6 shrink-0" aria-label="PostgreSQL"><title>PostgreSQL</title><path fill="#003545" d="M23.5594 14.7228a.5269.5269 0 0 0-.0563-.1191c-.139-.2632-.4768-.3418-1.0074-.2321-1.6533.3411-2.2935.1312-2.5256-.0191 1.342-2.0482 2.445-4.522 3.0411-6.8297.2714-1.0507.7982-3.5237.1222-4.7316a1.5641 1.5641 0 0 0-.1509-.235C21.6931.9086 19.8007.0248 17.5099.0005c-1.4947-.0158-2.7705.3461-3.1161.4794a9.449 9.449 0 0 0-.5159-.0816 8.044 8.044 0 0 0-1.3114-.1278c-1.1822-.0184-2.2038.2642-3.0498.8406-.8573-.3211-4.7888-1.645-7.2219.0788C.9359 2.1526.3086 3.8733.4302 6.3043c.0409.818.5069 3.334 1.2423 5.7436.4598 1.5065.9387 2.7019 1.4334 3.582.553.9942 1.1259 1.5933 1.7143 1.7895.4474.1491 1.1327.1441 1.8581-.7279.8012-.9635 1.5903-1.8258 1.9446-2.2069.4351.2355.9064.3625 1.39.3772a.0569.0569 0 0 0 .0004.0041 11.0312 11.0312 0 0 0-.2472.3054c-.3389.4302-.4094.5197-1.5002.7443-.3102.064-1.1344.2339-1.1464.8115-.0025.1224.0329.2309.0919.3268.2269.4231.9216.6097 1.015.6331 1.3345.3335 2.5044.092 3.3714-.6787-.017 2.231.0775 4.4174.3454 5.0874.2212.5529.7618 1.9045 2.4692 1.9043.2505 0 .5263-.0291.8296-.0941 1.7819-.3821 2.5557-1.1696 2.855-2.9059.1503-.8707.4016-2.8753.5388-4.1012.0169-.0703.0357-.1207.057-.1362.0007-.0005.0697-.0471.4272.0307a.3673.3673 0 0 0 .0443.0068l.2539.0223.0149.001c.8468.0384 1.9114-.1426 2.5312-.4308.6438-.2988 1.8057-1.0323 1.5951-1.6698zM2.371 11.8765c-.7435-2.4358-1.1779-4.8851-1.2123-5.5719-.1086-2.1714.4171-3.6829 1.5623-4.4927 1.8367-1.2986 4.8398-.5408 6.108-.13-.0032.0032-.0066.0061-.0098.0094-2.0238 2.044-1.9758 5.536-1.9708 5.7495-.0002.0823.0066.1989.0162.3593.0348.5873.0996 1.6804-.0735 2.9184-.1609 1.1504.1937 2.2764.9728 3.0892.0806.0841.1648.1631.2518.2374-.3468.3714-1.1004 1.1926-1.9025 2.1576-.5677.6825-.9597.5517-1.0886.5087-.3919-.1307-.813-.5871-1.2381-1.3223-.4796-.839-.9635-2.0317-1.4155-3.5126zm6.0072 5.0871c-.1711-.0428-.3271-.1132-.4322-.1772.0889-.0394.2374-.0902.4833-.1409 1.2833-.2641 1.4815-.4506 1.9143-1.0002.0992-.126.2116-.2687.3673-.4426a.3549.3549 0 0 0 .0737-.1298c.1708-.1513.2724-.1099.4369-.0417.156.0646.3078.26.3695.4752.0291.1016.0619.2945-.0452.4444-.9043 1.2658-2.2216 1.2494-3.1676 1.0128zm2.094-3.988-.0525.141c-.133.3566-.2567.6881-.3334 1.003-.6674-.0021-1.3168-.2872-1.8105-.8024-.6279-.6551-.9131-1.5664-.7825-2.5004.1828-1.3079.1153-2.4468.079-3.0586-.005-.0857-.0095-.1607-.0122-.2199.2957-.2621 1.6659-.9962 2.6429-.7724.4459.1022.7176.4057.8305.928.5846 2.7038.0774 3.8307-.3302 4.7363-.084.1866-.1633.3629-.2311.5454zm7.3637 4.5725c-.0169.1768-.0358.376-.0618.5959l-.146.4383a.3547.3547 0 0 0-.0182.1077c-.0059.4747-.054.6489-.115.8693-.0634.2292-.1353.4891-.1794 1.0575-.11 1.4143-.8782 2.2267-2.4172 2.5565-1.5155.3251-1.7843-.4968-2.0212-1.2217a6.5824 6.5824 0 0 0-.0769-.2266c-.2154-.5858-.1911-1.4119-.1574-2.5551.0165-.5612-.0249-1.9013-.3302-2.6462.0044-.2932.0106-.5909.019-.8918a.3529.3529 0 0 0-.0153-.1126 1.4927 1.4927 0 0 0-.0439-.208c-.1226-.4283-.4213-.7866-.7797-.9351-.1424-.059-.4038-.1672-.7178-.0869.067-.276.1831-.5875.309-.9249l.0529-.142c.0595-.16.134-.3257.213-.5012.4265-.9476 1.0106-2.2453.3766-5.1772-.2374-1.0981-1.0304-1.6343-2.2324-1.5098-.7207.0746-1.3799.3654-1.7088.5321a5.6716 5.6716 0 0 0-.1958.1041c.0918-1.1064.4386-3.1741 1.7357-4.4823a4.0306 4.0306 0 0 1 .3033-.276.3532.3532 0 0 0 .1447-.0644c.7524-.5706 1.6945-.8506 2.802-.8325.4091.0067.8017.0339 1.1742.081 1.939.3544 3.2439 1.4468 4.0359 2.3827.8143.9623 1.2552 1.9315 1.4312 2.4543-1.3232-.1346-2.2234.1268-2.6797.779-.9926 1.4189.543 4.1729 1.2811 5.4964.1353.2426.2522.4522.2889.5413.2403.5825.5515.9713.7787 1.2552.0696.087.1372.1714.1885.245-.4008.1155-1.1208.3825-1.0552 1.717-.0123.1563-.0423.4469-.0834.8148-.0461.2077-.0702.4603-.0994.7662zm.8905-1.6211c-.0405-.8316.2691-.9185.5967-1.0105a2.8566 2.8566 0 0 0 .135-.0406 1.202 1.202 0 0 0 .1342.103c.5703.3765 1.5823.4213 3.0068.1344-.2016.1769-.5189.3994-.9533.6011-.4098.1903-1.0957.333-1.7473.3636-.7197.0336-1.0859-.0807-1.1721-.151zm.5695-9.2712c-.0059.3508-.0542.6692-.1054 1.0017-.055.3576-.112.7274-.1264 1.1762-.0142.4368.0404.8909.0932 1.3301.1066.887.216 1.8003-.2075 2.7014a3.5272 3.5272 0 0 1-.1876-.3856c-.0527-.1276-.1669-.3326-.3251-.6162-.6156-1.1041-2.0574-3.6896-1.3193-4.7446.3795-.5427 1.3408-.5661 2.1781-.463zm.2284 7.0137a12.3762 12.3762 0 0 0-.0853-.1074l-.0355-.0444c.7262-1.1995.5842-2.3862.4578-3.4385-.0519-.4318-.1009-.8396-.0885-1.2226.0129-.4061.0666-.7543.1185-1.0911.0639-.415.1288-.8443.1109-1.3505.0134-.0531.0188-.1158.0118-.1902-.0457-.4855-.5999-1.938-1.7294-3.253-.6076-.7073-1.4896-1.4972-2.6889-2.0395.5251-.1066 1.2328-.2035 2.0244-.1859 2.0515.0456 3.6746.8135 4.8242 2.2824a.908.908 0 0 1 .0667.1002c.7231 1.3556-.2762 6.2751-2.9867 10.5405zm-8.8166-6.1162c-.025.1794-.3089.4225-.6211.4225a.5821.5821 0 0 1-.0809-.0056c-.1873-.026-.3765-.144-.5059-.3156-.0458-.0605-.1203-.178-.1055-.2844.0055-.0401.0261-.0985.0925-.1488.1182-.0894.3518-.1226.6096-.0867.3163.0441.6426.1938.6113.4186zm7.9305-.4114c.0111.0792-.049.201-.1531.3102-.0683.0717-.212.1961-.4079.2232a.5456.5456 0 0 1-.075.0052c-.2935 0-.5414-.2344-.5607-.3717-.024-.1765.2641-.3106.5611-.352.297-.0414.6111.0088.6356.1851z"/></svg>`,
	"mongodb":    `<svg role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" class="size-6 shrink-0" aria-label="MongoDB"><title>MongoDB</title><path fill="#003545" d="M17.193 9.555c-1.264-5.58-4.252-7.414-4.573-8.115-.28-.394-.53-.954-.735-1.44-.036.495-.055.685-.523 1.184-.723.566-4.438 3.682-4.74 10.02-.282 5.912 4.27 9.435 4.888 9.884l.07.05A73.49 73.49 0 0111.91 24h.481c.114-1.032.284-2.056.51-3.07.417-.296.604-.463.85-.693a11.342 11.342 0 003.639-8.464c.01-.814-.103-1.662-.197-2.218zm-5.336 8.195s0-8.291.275-8.29c.213 0 .49 10.695.49 10.695-.381-.045-.765-1.76-.765-2.405z"/></svg>`,
}

// EngineIcon is the logo of the database server, MySQL/MariaDB from the databases table, empty for engines without one
func (d summaryDatabase) EngineIcon() template.HTML {
	for _, o := range userCreateSQLOptions {
		if strings.EqualFold(o.Label, d.Engine) || o.Value == strings.ToLower(d.Engine) {
			return template.HTML(strings.Replace(string(o.Icon), `class="size-8"`, `class="size-6 shrink-0"`, 1))
		}
	}
	return summaryEngineIcons[strings.ToLower(d.Engine)]
}

// isDatabaseFinding is a look item from the account's databases, the admin sees those under the account like the urgent ones
func isDatabaseFinding(link string) bool {
	for _, p := range []string{"/mysql", "/postgresql", "/mongodb", "/containers/logs?container=mysql", "/containers/logs?container=mariadb", "/containers/logs?container=postgres", "/containers/logs?container=mongodb"} {
		if strings.HasPrefix(link, p) {
			return true
		}
	}
	return false
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

// Favicon is the site's icon from the same favicons url as the websites table
func (s summarySite) Favicon() string { return faviconBase() + s.Name }

// serverSummary is every account's daily summary for one day added up
type serverSummary struct {
	Date     string
	DayLabel string
	Headline string
	// the traffic sentence on its own, the page shows it under the attention box
	Overview  string
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
	// Attention per account and website, what the page lists
	AttentionGroups []attentionGroup
	// one problem most accounts share, e.g. the same plugin everywhere, "" when nothing stands out
	MainProblem string
	Sites       []summarySite
	// database servers with the most queries that day, each account's MySQL, PostgreSQL or MongoDB on its own row
	Databases []summaryDatabase
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
	type dbRow struct {
		server, link, user, engine string
		queries, size              int64
		errors                     int
	}
	var dbs []dbRow
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
		var siteNames []string
		for _, w := range s.Websites {
			siteNames = append(siteNames, w.Name)
		}
		for _, it := range s.Attention {
			// plugin names come from WordPress already entity encoded, the template escapes them again
			title, detail := html.UnescapeString(it.Title), html.UnescapeString(it.Detail)
			domain := findingDomain(it.Link, title+" "+detail, siteNames)
			out.Attention = append(out.Attention, summaryFinding{Server: s.Server, Link: s.Link, Username: s.Username, Title: trimDomainTitle(title, domain), Detail: detail, Domain: domain})
		}
		looks := 0
		for _, it := range s.Look {
			if isDatabaseFinding(it.Link) {
				out.Attention = append(out.Attention, summaryFinding{Server: s.Server, Link: s.Link, Username: s.Username, Title: html.UnescapeString(it.Title), Detail: html.UnescapeString(it.Detail)})
				continue
			}
			looks++
		}
		if looks > 0 {
			out.LookCount += looks
			lookAccounts[s.Username] = true
		}
		for _, d := range s.Databases {
			if d.Queries > 0 || d.Size > 0 || d.Errors > 0 {
				dbs = append(dbs, dbRow{s.Server, s.Link, s.Username, d.Engine, d.Queries, d.Size, d.Errors})
			}
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

	sort.Slice(dbs, func(i, j int) bool {
		if dbs[i].queries != dbs[j].queries {
			return dbs[i].queries > dbs[j].queries
		}
		return dbs[i].size > dbs[j].size
	})
	if len(dbs) > 5 {
		dbs = dbs[:5]
	}
	for _, d := range dbs {
		row := summaryDatabase{Server: d.server, Link: d.link, Username: d.user, Engine: d.engine, Size: humanSize(d.size), HasErrors: d.errors > 0}
		if d.queries >= 0 {
			row.Queries = commaNum(int(d.queries))
		}
		if d.errors > 0 {
			row.Errors = countWord(d.errors, "error", "errors")
		}
		if dbs[0].queries > 0 {
			row.Share = max(int(max(d.queries, 0)*100/dbs[0].queries), 1)
		}
		out.Databases = append(out.Databases, row)
	}

	// the page shows the first few and the rest behind "show all", the widget only the count
	out.AttentionTotal = len(out.Attention)
	out.MoreAttn = max(0, len(out.Attention)-8)
	out.AttentionGroups = groupAttention(out.Attention)
	out.MainProblem = mainAttentionProblem(out.AttentionGroups)

	accounts := countWord(len(found), "account", "accounts")
	if requests == 0 {
		out.Headline = "Websites on " + accounts + " had no visitors."
	} else {
		out.Headline = fmt.Sprintf("Websites on %s handled %s requests from %s.", accounts, commaNum(requests), countWord(visitors, "visitor", "visitors"))
	}
	out.Overview = out.Headline
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

// summaryNav is the day picker above the summary: the shown day, its neighbours and every day with a saved summary
type summaryNav struct {
	Date, Label, Prev, Next string
	Dates                   []string
	// a day the admin picked, not the newest one
	Picked bool
}

var summaryDayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ClusterSummariesDay is this server's part of a master's summary for one past day
func ClusterSummariesDay(db *sql.DB) func(day string) any {
	return func(day string) any {
		users, err := paneldb.ListUsernames(db, "")
		if err != nil || !summaryDayRe.MatchString(day) {
			return clusterSummaries{}
		}
		return clusterSummaries{Day: day, Found: summariesForDay(users, day)}
	}
}

func summariesForDay(users []string, day string) []userSummary {
	var found []userSummary
	for _, u := range users {
		if s, ok := loadUserSummary(u, day); ok {
			if s.Username == "" {
				s.Username = u
			}
			found = append(found, s)
		}
	}
	return found
}

// summaryDates lists every day any of these accounts has a summary for, oldest first
func summaryDates(users []string) []string {
	seen := map[string]bool{}
	for _, u := range users {
		entries, err := os.ReadDir(filepath.Join(userSummariesDir, u, "summaries"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if day, ok := strings.CutSuffix(e.Name(), ".json"); ok && summaryDayRe.MatchString(day) {
				seen[day] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func summaryCacheFor(key string) *ttlCache[*serverSummary] {
	serverSummaryMu.Lock()
	defer serverSummaryMu.Unlock()
	c, ok := serverSummaryCache[key]
	if !ok {
		c = &ttlCache[*serverSummary]{ttl: 5 * time.Minute}
		serverSummaryCache[key] = c
	}
	return c
}

// cachedServerSummaryDay is cachedServerSummary for a picked day
func cachedServerSummaryDay(db *sql.DB, owner, day string) *serverSummary {
	return summaryCacheFor(owner + "@" + day).get(func() *serverSummary {
		users, err := paneldb.ListUsernames(db, owner)
		if err != nil {
			return nil
		}
		return summarizeDay(day, summariesForDay(users, day))
	})
}

// cachedClusterSummaryDay is cachedClusterSummary for a picked day, every member is asked for that same day
func cachedClusterSummaryDay(db *sql.DB, userID int64, day string) *serverSummary {
	return summaryCacheFor(fmt.Sprintf("cluster:%d@%s", userID, day)).get(func() *serverSummary {
		var found []userSummary
		servers := 0
		if users, err := paneldb.ListUsernames(db, ""); err == nil {
			local := summariesForDay(users, day)
			for i := range local {
				local[i].Server = ClusterManager.Hostname
			}
			if len(local) > 0 {
				found, servers = append(found, local...), 1
			}
		}
		var mu sync.Mutex
		ClusterManager.CallAllData("summaries_day", day, func(id, name string, raw json.RawMessage) {
			var p clusterSummaries
			if json.Unmarshal(raw, &p) != nil || p.Day != day || len(p.Found) == 0 {
				return
			}
			for i := range p.Found {
				p.Found[i].Server = name
				p.Found[i].Link = ClusterManager.OpenLink(userID, id, "/users/"+p.Found[i].Username)
			}
			mu.Lock()
			found, servers = append(found, p.Found...), servers+1
			mu.Unlock()
		})
		out := summarizeDay(day, found)
		if out != nil {
			out.Servers = servers
		}
		return out
	})
}

// buildSummaryNav finds the days around the shown one, picked is "" for the newest day
func buildSummaryNav(dates []string, shown, picked string) *summaryNav {
	day := picked
	if day == "" {
		day = shown
	}
	if day == "" {
		return nil
	}
	nav := &summaryNav{Date: day, Dates: dates, Picked: picked != ""}
	if t, err := time.Parse("2006-01-02", day); err == nil {
		nav.Label = t.Format("Monday, 2 January 2006")
	}
	for _, d := range dates {
		if d < day {
			nav.Prev = d
		} else if d > day && nav.Next == "" {
			nav.Next = d
		}
	}
	return nav
}
