package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/webtemplates"
)

func TestLoadSummaryDay(t *testing.T) {
	dir := t.TempDir()
	origSnap, origNotif, origReports := ResourceUsageSnapshotFile, NotificationsLogPath, EmailsReportsDataDir
	t.Cleanup(func() { ResourceUsageSnapshotFile, NotificationsLogPath, EmailsReportsDataDir = origSnap, origNotif, origReports })
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

	d := loadSummaryDay("2026-11-11")
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
	if loadSummaryDay("2026-01-01") != nil {
		t.Fatalf("a day with nothing should be nil")
	}
}

func TestSummaryPageRendersServerHealthAndMail(t *testing.T) {
	data := dashboardPageData{Summary: &serverSummary{Date: "2026-11-11", DayLabel: "Wednesday", Accounts: 1},
		SummaryDay: &summaryDay{Health: &summaryHealth{PeakLoad: "12.50", PeakLoadAt: "14:05", Runs: 2}, Problems: []summaryProblem{{Title: "MySQL is down", Severity: "critical", Count: 1}},
			Mail: &summaryMail{Received: 110, Bounced: 54, BouncePct: 22, Link: "/emails/data/2026-Nov-12.html"}}}
	out, err := webtemplates.RenderToString("dashboard_summary.html", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Server health", "12.50", "MySQL is down", ">Mail<", "/emails/data/2026-Nov-12.html"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary page is missing %q", want)
		}
	}
}
