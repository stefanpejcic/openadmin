package handlers

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func stubRateLimitRun(t *testing.T, out string) *[][]string {
	t.Helper()
	var calls [][]string
	orig := rateLimitRun
	rateLimitRun = func(args ...string) (string, error) {
		calls = append(calls, args)
		return out, nil
	}
	t.Cleanup(func() { rateLimitRun = orig })
	return &calls
}

func TestRateLimitBulkCustomRunsOnce(t *testing.T) {
	calls := stubRateLimitRun(t, "Updated: a.com 600/min per IP, 20000/min per domain\n"+
		"ERROR: Domain config not found: /etc/openpanel/caddy/domains/b.com.conf\n"+
		"SKIPPED: c.com (no redirects import to place the limits after, suspended or custom vhost?)\n"+
		"SUCCESS: Caddy reloaded.\n")

	results, ok := rateLimitBulkBatch("custom", "600 / 20000", []string{"a.com", "b.com", "c.com", "bad domain"})
	if !ok {
		t.Fatal("expected custom to be batched")
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "a.com b.com c.com 600 20000" {
		t.Fatalf("expected one opencli call with the valid domains, got %v", *calls)
	}
	want := map[string]bool{"a.com": true, "b.com": false, "c.com": false, "bad domain": false}
	for _, res := range results {
		if res.OK != want[res.Item] {
			t.Errorf("%s: ok=%v (%s)", res.Item, res.OK, res.Message)
		}
	}
}

func TestRateLimitBulkDefaultAndOff(t *testing.T) {
	for _, mode := range []string{"default", "off"} {
		calls := stubRateLimitRun(t, "Updated: a.com uses the server default\nSUCCESS: Caddy reloaded.\n")
		if _, ok := rateLimitBulkBatch(mode, "", []string{"a.com"}); !ok {
			t.Fatalf("%s: expected it to be batched", mode)
		}
		if got := strings.Join((*calls)[0], " "); got != "a.com "+mode {
			t.Errorf("%s: opencli args %q", mode, got)
		}
	}
}

func TestRateLimitBulkCustomRejectsBadValue(t *testing.T) {
	calls := stubRateLimitRun(t, "")
	results, _ := rateLimitBulkBatch("custom", "abc/5", []string{"a.com"})
	if results[0].OK || len(*calls) != 0 {
		t.Fatalf("expected a bad value to run nothing, got %v and calls %v", results, *calls)
	}
}

func TestRateLimitRejectedConfigFailsAll(t *testing.T) {
	stubRateLimitRun(t, "Updated: a.com 5/min per IP, 0/min per domain\nERROR: Caddy rejected the new config, nothing was changed:\n")
	if res := rateLimitApply([]string{"a.com"}, "custom", 5, 0); res[0].OK {
		t.Fatal("expected a rolled back change to fail the domain")
	}
}

func TestRateLimitBulkValuePattern(t *testing.T) {
	act := RateLimitBulkActions()[1]
	for v, ok := range map[string]bool{"300/6000": true, "0 / 0": true, "300": false, "a/b": false, "-1/5": false} {
		if got := validateBulkValue(act.Input, v) == ""; got != ok {
			t.Errorf("%q: valid=%v, want %v", v, got, ok)
		}
	}
}

func TestReadRateLimitStatus(t *testing.T) {
	stubRateLimitRun(t, `{"default":{"per_ip":300,"per_site":6000},"domains":[{"domain":"a.com","mode":"default","per_ip":300,"per_site":6000},{"domain":"b.com","mode":"off","per_ip":0,"per_site":0}]}`+"\n")
	st, err := readRateLimitStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Default.PerIP != 300 || len(st.Domains) != 2 || st.Domains[1].Mode != "off" {
		t.Fatalf("unexpected status %+v", st)
	}
}

func TestDomainsBulkRateLimitBatched(t *testing.T) {
	calls := stubRateLimitRun(t, "Updated: a.com has no rate limits\nSUCCESS: Caddy reloaded.\n")
	results, ok := domainsBulkBatch("ratelimit", "off", []string{"a.com"})
	if !ok || !results[0].OK || strings.Join((*calls)[0], " ") != "a.com off" {
		t.Fatalf("got ok=%v results=%v calls=%v", ok, results, *calls)
	}
	if _, ok := domainsBulkBatch("ratelimit", "custom", []string{"a.com"}); ok {
		t.Fatal("expected an unknown value not to be batched")
	}
}

func TestRateLimitModeForDomain(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyDomainsConfDir
	CaddyDomainsConfDir = dir
	t.Cleanup(func() { CaddyDomainsConfDir = orig })
	files := map[string]string{
		"d.com": "route {\n    import ratelimit d.com\n}\n",
		"c.com": "route {\n    import ratelimit_ip 5 c.com\n}\n",
		"o.com": "route {\n    # ratelimit off\n}\n",
	}
	for d, body := range files {
		os.WriteFile(dir+"/"+d+".conf", []byte(body), 0644)
	}
	for d, want := range map[string]string{"d.com": "default", "c.com": "custom", "o.com": "off", "missing.com": "off"} {
		if got := rateLimitModeForDomain(d); got != want {
			t.Errorf("%s: %s, want %s", d, got, want)
		}
	}
}

func newRateLimitTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })
	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewManager("test-secret", false)
	h := &DomainsRateLimit{Sessions: sessions}
	origOwners := wafDomainOwners
	wafDomainOwners = func(*sql.DB) map[string]string { return map[string]string{"a.com": "john"} }
	t.Cleanup(func() { wafDomainOwners = origOwners })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/ratelimit", h.ServeRateLimit)
	mux.HandleFunc("POST /domains/ratelimit", h.ServeRateLimit)
	mux.HandleFunc("GET /domains/ratelimit/stats", h.ServeRateLimitStats)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})
	srv := httptest.NewServer(auth.WithUserLoader(sessions, db)(mux))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

func TestServeRateLimitRendersHTML(t *testing.T) {
	stubRateLimitRun(t, `{"default":{"per_ip":300,"per_site":6000},"domains":[{"domain":"a.com","mode":"custom","per_ip":600,"per_site":20000},{"domain":"b.com","mode":"off","per_ip":0,"per_site":0}]}`)
	srv, client := newRateLimitTestServer(t)
	resp, err := client.Get(srv.URL + "/domains/ratelimit")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, page)
	}
	for _, want := range []string{"Rate Limiting", `value="300"`, `value="6000"`, "a.com", "john", "b.com", "/domains/ratelimit/bulk", "Custom limits", "</html>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestServeRateLimitStatsCached(t *testing.T) {
	calls := stubRateLimitRun(t, `[{"domain":"a.com","hours":24,"requests":10,"blocked":2,"peak_minute":5,"peak_ip_minute":4,"top_blocked":[{"ip":"1.2.3.4","count":2}]}]`)
	rateLimitStatsCache.Lock()
	rateLimitStatsCache.stats = nil
	rateLimitStatsCache.Unlock()
	srv, client := newRateLimitTestServer(t)
	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL + "/domains/ratelimit/stats")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), `"a.com":{"domain":"a.com"`) || !strings.Contains(string(body), `"blocked":2`) {
			t.Fatalf("unexpected stats %s", body)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("expected the second load to come from the cache, opencli ran %d times", len(*calls))
	}
}

func TestServeRateLimitPostDefault(t *testing.T) {
	calls := stubRateLimitRun(t, "Server default set to 300/min per IP, 6000/min per domain (0 = off).\nSUCCESS: Caddy reloaded.\n")
	srv, client := newRateLimitTestServer(t)
	// no csrf middleware in this test server, the real route has it
	resp, err := client.PostForm(srv.URL+"/domains/ratelimit", url.Values{"action": {"default"}, "per_ip": {"300"}, "per_site": {"6000"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	found := false
	for _, c := range *calls {
		if strings.Join(c, " ") == "default 300 6000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected opencli default 300 6000, got %v", *calls)
	}
	for _, bad := range []url.Values{{"action": {"default"}, "per_ip": {"-5"}, "per_site": {"1"}}, {"action": {"domain"}, "domain": {"a.com"}, "mode": {"weird"}}} {
		*calls = nil
		resp, _ := client.PostForm(srv.URL+"/domains/ratelimit", bad)
		resp.Body.Close()
		for _, c := range *calls {
			if c[0] != "status" {
				t.Errorf("%v should not have changed anything, ran %v", bad, c)
			}
		}
	}
}
