package handlers

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/webtemplates"
)

func TestLoadSummaryDay(t *testing.T) {
	dir := t.TempDir()
	origSnap, origNotif, origReports := ResourceUsageSnapshotFile, NotificationsLogPath, EmailsReportsDataDir
	t.Cleanup(func() {
		ResourceUsageSnapshotFile, NotificationsLogPath, EmailsReportsDataDir = origSnap, origNotif, origReports
	})
	ResourceUsageSnapshotFile = filepath.Join(dir, "snap.jsonl")
	NotificationsLogPath = filepath.Join(dir, "notifications.log")
	EmailsReportsDataDir = dir

	os.WriteFile(ResourceUsageSnapshotFile, []byte(
		`{"ts":"2026-11-10 23:55:00","load":{"5m":"99.0"},"cpu_pct":99,"mem":{"pct":99},"disk_pct":10,"fail":1}`+"\n"+
			`{"ts":"2026-11-11 03:00:00","load":{"5m":"4.20"},"cpu_pct":30,"mem":{"pct":40},"disk_pct":60,"fail":0}`+"\n"+
			`{"ts":"2026-11-11 14:05:00","load":{"5m":"12.50"},"cpu_pct":85,"mem":{"pct":35},"disk_pct":61,"fail":2}`+"\n"), 0644)
	os.WriteFile(NotificationsLogPath, []byte(
		`{"time":"2026-11-11 14:06:00","last_seen":"2026-11-11 15:00:00","count":3,"status":"resolved","severity":"warning","title":"High System Load!"}`+"\n"+
			`{"time":"2026-11-11 09:00:00","count":1,"status":"unread","severity":"critical","title":"MySQL is down"}`+"\n"+
			`{"time":"2026-11-11 10:00:00","count":1,"status":"unread","severity":"info","title":"User created"}`+"\n"+
			`{"time":"2026-11-09 10:00:00","last_seen":"2026-11-11 08:00:00","count":2,"status":"unread","severity":"warning","title":"Large mail queue!"}`+"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "2026-Nov-12.html"), []byte(`<table id="PerDayTrafficSummaryTable"><tbody>
<tr><td>Nov 10 2026</td><td>1</td><td>1</td><td>1</td><td>1</td><td>1</td></tr>
<tr><td>Nov 11 2026</td><td>110</td><td>182</td><td>15</td><td>54</td><td>21</td></tr>
</tbody></table>`), 0644)

	d := loadSummaryDay(nil, "2026-11-11")
	if d == nil || d.Health == nil || d.Mail == nil {
		t.Fatalf("expected health and mail: %+v", d)
	}
	h := d.Health
	if h.PeakLoad != "12.50" || h.PeakLoadAt != "14:05" || h.PeakCPU != 85 || h.PeakRAM != 40 || h.Disk != 61 || h.Runs != 2 || h.FailedRuns != 1 {
		t.Fatalf("health wrong (the day before must not count): %+v", h)
	}
	if len(d.Problems) != 3 || d.Problems[0].Title != "MySQL is down" || d.Problems[1].Title != "High System Load!" || d.Problems[1].Count != 3 || !d.Problems[1].Resolved {
		t.Fatalf("problems wrong, critical first then by count, info left out, last_seen counts: %+v", d.Problems)
	}
	m := d.Mail
	if m.Received != 110 || m.Delivered != 182 || m.Bounced != 54 || m.BouncePct != 22 || m.Link != "/emails/data/2026-Nov-12.html" {
		t.Fatalf("mail wrong: %+v", m)
	}
	if loadSummaryDay(nil, "2026-01-01") != nil {
		t.Fatalf("a day with nothing should be nil")
	}
}

func TestSummaryPageRendersServerHealthAndMail(t *testing.T) {
	one := &summaryDay{Health: &summaryHealth{PeakLoad: "12.50", PeakLoadAt: "14:05", Runs: 2, Cores: 4, LoadPct: 100, PeakCPU: 75}, Problems: []summaryProblem{{Title: "MySQL is down", Severity: "critical", Count: 1}},
		Mail: &summaryMail{Received: 110, Bounced: 54, BouncePct: 22, Link: "/emails/data/2026-Nov-12.html"}}
	two := &summaryDay{Mail: &summaryMail{Received: 7, Link: "/emails/data/2026-Nov-12.html"}}
	data := dashboardPageData{Summary: &serverSummary{Date: "2026-11-11", DayLabel: "Wednesday", Accounts: 1},
		SummaryDays: []summaryServerDay{{Name: "s1.example.com", Color: "bg-blue-500", Day: one, MailLink: one.Mail.Link}, {Name: "s2.example.com", Color: "bg-violet-500", Day: two, MailLink: "/_cluster/open?node=n2"}}}
	out, err := webtemplates.RenderToString("dashboard_summary.html", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Server health", "12.50", "MySQL is down", ">Mail<", `href="/emails/data/2026-Nov-12.html"`, `href="/_cluster/open?node=n2"`, "s2.example.com", "stroke-red-500", "stroke-yellow-500", "every server in the cluster"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary page is missing %q", want)
		}
	}
}

func TestSummaryDayNavigation(t *testing.T) {
	dir := t.TempDir()
	orig := userSummariesDir
	userSummariesDir = dir + "/"
	t.Cleanup(func() { userSummariesDir = orig })
	for u, days := range map[string][]string{"amy": {"2026-11-08", "2026-11-10"}, "bob": {"2026-11-10", "2026-11-11"}} {
		os.MkdirAll(filepath.Join(dir, u, "summaries"), 0o755)
		for _, d := range days {
			os.WriteFile(filepath.Join(dir, u, "summaries", d+".json"), []byte(`{"username":"`+u+`","date":"`+d+`","websites":[{"name":"`+u+`.com","requests":5}]}`), 0o644)
		}
		os.WriteFile(filepath.Join(dir, u, "summaries", "notes.txt"), nil, 0o644)
	}
	dates := summaryDates([]string{"amy", "bob", "nobody"})
	if strings.Join(dates, ",") != "2026-11-08,2026-11-10,2026-11-11" {
		t.Fatalf("dates: %v", dates)
	}
	nav := buildSummaryNav(dates, "2026-11-11", "2026-11-10")
	if nav.Date != "2026-11-10" || nav.Prev != "2026-11-08" || nav.Next != "2026-11-11" || !nav.Picked || nav.Label != "Tuesday, 10 November 2026" {
		t.Fatalf("nav: %+v", nav)
	}
	if nav = buildSummaryNav(dates, "2026-11-11", ""); nav.Prev != "2026-11-10" || nav.Next != "" || nav.Picked {
		t.Fatalf("latest day nav: %+v", nav)
	}
	if got := summariesForDay([]string{"amy", "bob"}, "2026-11-10"); len(got) != 2 {
		t.Fatalf("both accounts have the 10th: %d", len(got))
	}
	s := summarizeDay("2026-11-08", summariesForDay([]string{"amy", "bob"}, "2026-11-08"))
	if s == nil || s.Accounts != 1 {
		t.Fatalf("picked day summary: %+v", s)
	}
	picker, err := webtemplates.RenderToString("summary_day_picker", buildSummaryNav(dates, "2026-11-11", "2026-11-08"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"summaryDayPicker(", "Sunday, 8 November 2026", `href="/dashboard/summary?date=2026-11-10"`, `href="/dashboard/summary"`} {
		if !strings.Contains(picker, want) {
			t.Fatalf("day picker missing %q", want)
		}
	}
	page := dashboardPageData{Summary: s}
	page.TabExtra = template.HTML(picker)
	page.PageTabs = []webtemplates.NavLink{{Label: "Daily summary", Href: "/dashboard/summary", Active: true}}
	if out, err := webtemplates.RenderToString("dashboard_summary.html", page); err != nil || !strings.Contains(out, "summaryDayPicker(") {
		t.Fatalf("the picker should be in the tab bar: %v", err)
	}
	empty, err := webtemplates.RenderToString("dashboard_summary.html", dashboardPageData{Nav: buildSummaryNav(dates, "", "2026-11-09")})
	if err != nil || !strings.Contains(empty, "No account has a summary for Monday, 9 November 2026") {
		t.Fatalf("picked day without data: %v", err)
	}
}

func TestSummaryMalwareFindings(t *testing.T) {
	orig := NotificationsLogPath
	NotificationsLogPath = filepath.Join(t.TempDir(), "n.log")
	t.Cleanup(func() { NotificationsLogPath = orig })
	os.WriteFile(NotificationsLogPath, []byte(
		`{"time":"2026-11-12 21:16:00","source":"imunify_scan","severity":"info","title":"ImunifyAV scan running: koraciznanja","details":{"user":"koraciznanja","malicious":0}}`+"\n"+
			`{"time":"2026-11-12 21:29:59","source":"imunify_scan","severity":"warning","title":"ImunifyAV found 20 malicious files: koraciznanja","details":{"user":"koraciznanja","malicious":20,"malicious_files":["/home/koraciznanja/docker-data/volumes/koraciznanja_html_data/_data/a.php","/home/koraciznanja/docker-data/volumes/koraciznanja_html_data/_data/b.php","/x/c.php","/x/d.php"]}}`+"\n"+
			`{"time":"2026-11-11 05:00:00","source":"imunify_scan","severity":"warning","title":"ImunifyAV found 1 malicious file: other","details":{"user":"other","malicious":1,"malicious_files":["/x"]}}`+"\n"), 0o644)
	got := summaryMalwareFor(nil, "2026-11-12")
	if len(got) != 1 || got[0].Username != "koraciznanja" || got[0].Title != "ImunifyAV found 20 malicious files" ||
		got[0].Detail != "/var/www/html/a.php, /var/www/html/b.php, /x/c.php and 17 more" || got[0].Link != "/security/imunify/koraciznanja" {
		t.Fatalf("malware findings: %+v", got)
	}
}

func TestDailyReportEmailRendersSummary(t *testing.T) {
	s := &serverSummary{Date: "2026-11-11", DayLabel: "Wednesday, 11 November", Accounts: 73, Servers: 2, Requests: "603,330", Visitors: "56,266", Sent: "14.0 GB", Errors: "1,412",
		Attention:      []summaryFinding{{Username: "koraciznanja", Server: "s1", Title: "ImunifyAV found 20 malicious files"}, {Username: "a", Title: "t2"}, {Username: "b", Title: "t3"}, {Username: "c", Title: "t4"}, {Username: "d", Title: "t5"}, {Username: "e", Title: "sixth"}},
		AttentionTotal: 6, MoreAttn: 1, Sites: []summarySite{{Name: "elit.rs", Username: "zlatko888", Requests: "255,542"}}}
	days := []summaryServerDay{{Name: "s1.maxhost.rs", Day: &summaryDay{Health: &summaryHealth{PeakLoad: "8.64", LoadPct: 48, Cores: 18, PeakCPU: 78, Disk: 66, Runs: 258, FailedRuns: 1}, Mail: &summaryMail{Received: 143, Bounced: 58, BouncePct: 18}}},
		{Name: "s2.maxhost.rs", Day: &summaryDay{Health: &summaryHealth{PeakLoad: "14.91", LoadPct: 82, Cores: 18}}}}
	out, err := webtemplates.RenderToString("email_daily_system_report.html", map[string]interface{}{
		"Title": "Daily Usage Report", "Hostname": "s1.maxhost.rs", "AdminURL": "https://s1.maxhost.rs:2087/", "Summary": s, "Days": days, "Multi": true, "TotalUsers": 46,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Wednesday, 11 November", "603,330", "koraciznanja", "ImunifyAV found 20 malicious files", "and 1 more on the daily summary", "elit.rs", "s2.maxhost.rs", "14.91", "58 (18%)", "dashboard/summary?date=2026-11-11", "emails/reports", ">46<"} {
		if !strings.Contains(out, want) {
			t.Fatalf("daily report email is missing %q", want)
		}
	}
	if strings.Contains(out, "sixth") {
		t.Fatalf("only the first 5 attention items go in the email")
	}
}

func TestAttentionGroupedByAccountAndWebsite(t *testing.T) {
	var u userSummary
	if err := json.Unmarshal([]byte(`{"username":"apeiron","date":"2026-11-11","websites":[{"name":"apeiron.rs","requests":5},{"name":"lager.apeiron.rs","requests":3}],"attention":[
		{"title":"apeiron.rs has 1 known security hole","detail":"They're in WP Fastest Cache &#8211; Plugin.","link":"/website?domain=apeiron.rs"},
		{"title":"lager.apeiron.rs has 18 known security holes","detail":"They're in 7.1.3.","link":"/website?domain=lager.apeiron.rs"},
		{"title":"Your scheduled task \"wp-cron-lager.apeiron.rs\" failed every time it ran (96 times)","detail":"No such container","link":"/cronjobs"},
		{"title":"Disk almost full","detail":"95% used","link":"/disk-usage"}]}`), &u); err != nil {
		t.Fatal(err)
	}
	s := summarizeDay("2026-11-11", []userSummary{u, {Username: "bob", Attention: []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Link   string `json:"link"`
	}{{Title: "x"}}}})
	g := s.AttentionGroups
	if len(g) != 2 || g[0].Username != "apeiron" || g[0].Count != 4 || len(g[0].General) != 1 || g[0].General[0].Title != "Disk almost full" {
		t.Fatalf("groups: %+v", g)
	}
	if len(g[0].Domains) != 2 || g[0].Domains[0].Name != "apeiron.rs" || g[0].Domains[0].Items[0].Title != "1 known security hole" || g[0].Domains[0].Items[0].Detail != "They're in WP Fastest Cache – Plugin." {
		t.Fatalf("apeiron.rs: %+v", g[0].Domains)
	}
	lager := g[0].Domains[1]
	if lager.Name != "lager.apeiron.rs" || len(lager.Items) != 2 || lager.Items[0].Title != "18 known security holes" || !strings.Contains(lager.Items[1].Title, "wp-cron-lager") {
		t.Fatalf("the cron mentioning lager.apeiron.rs belongs under it: %+v", lager)
	}
	out, err := webtemplates.RenderToString("dashboard_summary.html", dashboardPageData{Summary: s})
	if err != nil || !strings.Contains(out, "&#8627;</span>lager.apeiron.rs") || !strings.Contains(out, "4 issues") {
		t.Fatalf("grouped list not rendered: %v", err)
	}
}

func TestAttentionWorstAccountsFirst(t *testing.T) {
	g := groupAttention([]summaryFinding{
		{Username: "aaa", Title: "2 known security holes", Domain: "a.com"},
		{Username: "bbb", Title: "Your scheduled task \"x\" failed every time it ran (96 times)"},
		{Username: "ccc", Title: "18 known security holes", Domain: "c.com"},
		{Username: "ddd", Title: "ImunifyAV found 3 malicious files"},
		{Username: "aaa", Title: "1 known security hole", Domain: "b.com"},
		{Username: "eee", Title: "Something minor"},
	})
	var order []string
	for _, x := range g {
		order = append(order, x.Username)
	}
	// malware first, then aaa (two sites with holes: 52+51) over ccc (68), bbb's failing cron (80) sits between
	if got := strings.Join(order, ","); got != "ddd,aaa,bbb,ccc,eee" {
		t.Fatalf("order: %s (scores %v)", got, g)
	}
}
