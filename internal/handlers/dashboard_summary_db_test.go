package handlers

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSummarizeDayDatabases(t *testing.T) {
	var a, b userSummary
	_ = json.Unmarshal([]byte(`{"username":"ana","look":[{"title":"MariaDB logged 2 errors","link":"/containers/logs?container=mariadb&lines=100&ctx=service"},{"title":"Your mailbox is almost full","link":"/emails"}],
		"databases":[{"engine":"MariaDB","feature":"mysql","size":1048576,"queries":500,"errors":2}]}`), &a)
	_ = json.Unmarshal([]byte(`{"username":"bob","databases":[{"engine":"PostgreSQL","feature":"postgresql","size":2048,"queries":2000},{"engine":"MongoDB","feature":"mongodb","queries":-1}]}`), &b)
	out := summarizeDay("2026-11-12", []userSummary{a, b})
	if len(out.Databases) != 2 || out.Databases[0].Username != "bob" || out.Databases[1].Errors != "2 errors" || out.Databases[1].Share != 25 {
		t.Fatalf("databases: %+v", out.Databases)
	}
	// the database problem moves up under the account, the mailbox stays a plain look item
	if len(out.Attention) != 1 || out.Attention[0].Username != "ana" || out.LookCount != 1 {
		t.Fatalf("attention %+v look %d", out.Attention, out.LookCount)
	}
}

func TestSummaryProblemsMergeIPs(t *testing.T) {
	dir := t.TempDir()
	old := NotificationsLogPath
	NotificationsLogPath = dir + "/notifications.log"
	defer func() { NotificationsLogPath = old }()
	lines := `{"time":"2026-11-12 17:15:02","status":"unread","severity":"warning","title":"Admin stefan accessed from new IP: 31.3.155.127","count":1}
{"time":"2026-11-12 17:14:10","status":"unread","severity":"warning","title":"Admin stefan accessed from new IP: 185.7.32.61","count":1}
{"time":"2026-11-12 18:00:00","status":"unread","severity":"warning","title":"Admin ana accessed from new IP: 10.0.0.1","count":1}
`
	if err := os.WriteFile(NotificationsLogPath, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	list, _ := summaryProblemsFor("2026-11-12", 8)
	if len(list) != 2 {
		t.Fatalf("got %+v", list)
	}
	byTitle := map[string]summaryProblem{}
	for _, p := range list {
		byTitle[p.Title] = p
	}
	merged := byTitle["Admin stefan accessed from new IPs"]
	if merged.Count != 2 || merged.First != "17:14" || len(merged.IPs) != 2 {
		t.Fatalf("merged: %+v", merged)
	}
	if _, ok := byTitle["Admin ana accessed from new IP: 10.0.0.1"]; !ok {
		t.Fatalf("single IP should keep the original title: %+v", list)
	}
}
