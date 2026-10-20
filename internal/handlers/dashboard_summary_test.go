package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeUserSummary(t *testing.T, dir, user, date, body string) {
	t.Helper()
	p := filepath.Join(dir, user, "summaries")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, date+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildServerSummary(t *testing.T) {
	dir := t.TempDir()
	orig := userSummariesDir
	userSummariesDir = dir
	t.Cleanup(func() { userSummariesDir = orig })

	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	writeUserSummary(t, dir, "alice", "2026-10-19", `{"username":"alice","prev_requests":100,
		"attention":[{"title":"SSL expired","detail":"Yoast SEO &#8211; Q&amp;A"}],"look":[{"title":"disk"}],
		"websites":[{"name":"a.com","requests":1500,"unique_ips":40,"bytes":2048,"server_errors":3,"waf_blocked":2}]}`)
	writeUserSummary(t, dir, "bob", "2026-10-19", `{"username":"bob","prev_requests":100,
		"websites":[{"name":"b.com","requests":500,"unique_ips":10,"bytes":1024}]}`)
	// another reseller's account, not in the list
	writeUserSummary(t, dir, "carol", "2026-10-19", `{"username":"carol","websites":[{"name":"c.com","requests":9000}]}`)

	s := buildServerSummary([]string{"alice", "bob", "dave"}, now)
	if s == nil {
		t.Fatal("expected a summary")
	}
	if s.Date != "2026-10-19" || s.Accounts != 2 {
		t.Fatalf("date %s accounts %d", s.Date, s.Accounts)
	}
	if s.Requests != "2,000" || s.Visitors != "50" || s.Errors != "3" || s.Blocked != "2" || s.Sent != "3.0 KB" {
		t.Fatalf("totals %+v", s)
	}
	if s.Delta != "+900%" || !s.DeltaUp {
		t.Fatalf("delta %s", s.Delta)
	}
	if len(s.Attention) != 1 || s.Attention[0].Username != "alice" || s.LookCount != 1 || s.LookAccounts != 1 {
		t.Fatalf("findings %+v", s)
	}
	if s.Attention[0].Detail != "Yoast SEO – Q&A" {
		t.Fatalf("detail not unescaped %q", s.Attention[0].Detail)
	}
	if len(s.Sites) != 2 || s.Sites[0].Name != "a.com" || s.Sites[0].Share != 100 || s.Sites[1].Share != 33 {
		t.Fatalf("sites %+v", s.Sites)
	}
	if !strings.Contains(s.Headline, "2 accounts") || !strings.Contains(s.Headline, "1 thing needs attention") {
		t.Fatalf("headline %q", s.Headline)
	}
	for _, f := range s.Attention {
		if f.Username == "carol" {
			t.Fatal("reseller scope leaked another account")
		}
	}
}

func TestBuildServerSummaryNone(t *testing.T) {
	orig := userSummariesDir
	userSummariesDir = t.TempDir()
	t.Cleanup(func() { userSummariesDir = orig })
	if s := buildServerSummary([]string{"alice"}, time.Now()); s != nil {
		t.Fatalf("expected nil, got %+v", s)
	}
}
