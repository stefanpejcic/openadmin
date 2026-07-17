package handlers

import (
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

func newDomainLimitsTestServer(t *testing.T, e *Emails) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origPostfwd := PostfwdConfigPath
	PostfwdConfigPath = filepath.Join(dir, "postfwd.cf")
	t.Cleanup(func() { PostfwdConfigPath = origPostfwd })

	origHup := hupPostfwdRun
	hupPostfwdRun = func() {}
	t.Cleanup(func() { hupPostfwdRun = origHup })

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
	e.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /emails/domain-limits", e.ServeDomainLimits)
	mux.HandleFunc("POST /emails/domain-limits/save-raw", e.ServeDomainLimitsSaveRaw)
	mux.HandleFunc("GET /emails/domain-limits/hits", e.ServeDomainLimitsHits)
	mux.HandleFunc("POST /emails/domain-limits/api", e.ServeDomainLimitsAPI)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

const testPostfwdRules = `id=limit_stefan_stefan_rs ; sender=~.+@stefan.rs ; protocol_state==RCPT
                action=rate(stefan_ratelimit/100/3600/450 4.7.1 sorry, OpenPanel account reached limit of 100 emails per hour)

id=limit_alice_example_com ; sender=~.+@example.com ; protocol_state==RCPT
                action=rate(alice_ratelimit/50/3600/450 4.7.1 sorry, OpenPanel account reached limit of 50 emails per hour)
`

func TestParsePostfwdRules(t *testing.T) {
	rules := parsePostfwdRules(testPostfwdRules)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].Username != "stefan" || rules[0].Domain != "stefan.rs" || rules[0].Limit != 100 {
		t.Fatalf("unexpected first rule: %+v", rules[0])
	}
	if rules[1].Username != "alice" || rules[1].Domain != "example.com" || rules[1].Limit != 50 {
		t.Fatalf("unexpected second rule: %+v", rules[1])
	}
}

func TestParsePostfwdRulesEmptyContent(t *testing.T) {
	if got := parsePostfwdRules(""); got != nil {
		t.Fatalf("expected nil for empty content, got %+v", got)
	}
	if got := parsePostfwdRules("   \n  "); got != nil {
		t.Fatalf("expected nil for whitespace-only content, got %+v", got)
	}
}

func TestBuildAndWriteDomainRule(t *testing.T) {
	dir := t.TempDir()
	origPostfwd := PostfwdConfigPath
	PostfwdConfigPath = filepath.Join(dir, "postfwd.cf")
	t.Cleanup(func() { PostfwdConfigPath = origPostfwd })

	origHup := hupPostfwdRun
	hupCalled := false
	hupPostfwdRun = func() { hupCalled = true }
	t.Cleanup(func() { hupPostfwdRun = origHup })

	ok, msg := writeDomainRule("bob", 25, "bob.com")
	if !ok {
		t.Fatalf("expected success, got %q", msg)
	}
	if !hupCalled {
		t.Fatal("expected postfwd HUP to be triggered")
	}

	content := readPostfwdRaw()
	if !strings.Contains(content, "id=limit_bob_bob_com") || !strings.Contains(content, "bob_ratelimit/25/3600") {
		t.Fatalf("expected the new rule written, got:\n%s", content)
	}

	// Writing again with a different limit must replace the old rule for
	// the same id, not append a duplicate.
	hupCalled = false
	ok, _ = writeDomainRule("bob", 75, "bob.com")
	if !ok {
		t.Fatal("expected the second write to succeed")
	}
	content = readPostfwdRaw()
	if strings.Count(content, "id=limit_bob_bob_com") != 1 {
		t.Fatalf("expected exactly one rule for bob.com after re-writing, got:\n%s", content)
	}
	if !strings.Contains(content, "bob_ratelimit/75/3600") {
		t.Fatalf("expected the updated limit, got:\n%s", content)
	}
}

func TestServeDomainLimitsGUIMode(t *testing.T) {
	origCounters := getPostfwdCountersRun
	getPostfwdCountersRun = func() map[string]int { return map[string]int{"stefan": 42} }
	t.Cleanup(func() { getPostfwdCountersRun = origCounters })

	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)
	os.WriteFile(PostfwdConfigPath, []byte(testPostfwdRules), 0644)

	resp, err := client.Get(srv.URL + "/emails/domain-limits")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "stefan") || !strings.Contains(string(body), "42") {
		t.Fatalf("expected the current counter joined into the rule row, got %s", truncate(string(body)))
	}
	for _, want := range []string{"Email Rate Limits", "postfwd", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeDomainLimitsRawModeRendersHTML(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)
	os.WriteFile(PostfwdConfigPath, []byte(testPostfwdRules), 0644)

	resp, err := client.Get(srv.URL + "/emails/domain-limits?mode=raw")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Editing file:", PostfwdConfigPath, "id=limit_stefan_stefan_rs", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeDomainLimitsSaveRaw(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/emails/domain-limits/save-raw", url.Values{
		"raw_content": {"id=limit_x_y ; sender=~.+@y ; protocol_state==RCPT\n                action=rate(x_ratelimit/10/3600/450 4.7.1 msg)\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "File saved and postfwd reloaded") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	content := readPostfwdRaw()
	if !strings.Contains(content, "id=limit_x_y") {
		t.Fatalf("expected raw content persisted, got:\n%s", content)
	}
}

func TestServeDomainLimitsHitsRequiresDomain(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Get(srv.URL + "/emails/domain-limits/hits")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeDomainLimitsHitsReturnsLines(t *testing.T) {
	origHits := getLimitHitsRun
	getLimitHitsRun = func(domain string, n int) []string { return []string{"line1 reached limit of 10", "line2 4.7.1"} }
	t.Cleanup(func() { getLimitHitsRun = origHits })

	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Get(srv.URL + "/emails/domain-limits/hits?domain=example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "line1") || !strings.Contains(string(body), `"domain":"example.com"`) {
		t.Fatalf("expected hits with domain echoed, got %s", truncate(string(body)))
	}
}

func TestServeDomainLimitsAPIUpdateDomain(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/domain-limits/api", "application/json",
		strings.NewReader(`{"action":"update-domain","domain":"acme.com","username":"acme","limit":30}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("expected ok:true, got %s", truncate(string(body)))
	}
	content := readPostfwdRaw()
	if !strings.Contains(content, "id=limit_acme_acme_com") {
		t.Fatalf("expected the rule written, got:\n%s", content)
	}
}

func TestServeDomainLimitsAPIUpdateDomainInvalidLimit(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/domain-limits/api", "application/json",
		strings.NewReader(`{"action":"update-domain","domain":"acme.com","username":"acme","limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-positive limit, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeDomainLimitsAPIResetAll(t *testing.T) {
	origScript := runRatelimitScriptRun
	var capturedArgs []string
	runRatelimitScriptRun = func(skipReload bool, args ...string) (bool, string) {
		capturedArgs = args
		return true, "reset"
	}
	t.Cleanup(func() { runRatelimitScriptRun = origScript })

	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/domain-limits/api", "application/json",
		strings.NewReader(`{"action":"reset-all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("expected ok:true, got %s", truncate(string(body)))
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "--all-users" {
		t.Fatalf("expected --all-users passed to the ratelimit script, got %v", capturedArgs)
	}
}

func TestServeDomainLimitsAPIDeleteAll(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)
	os.WriteFile(PostfwdConfigPath, []byte(testPostfwdRules), 0644)

	resp, err := client.Post(srv.URL+"/emails/domain-limits/api", "application/json",
		strings.NewReader(`{"action":"delete-all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("expected ok:true, got %s", truncate(string(body)))
	}
	if readPostfwdRaw() != "" {
		t.Fatal("expected postfwd.cf truncated to empty")
	}
}

func TestServeDomainLimitsAPIMissingDomainForResetDomain(t *testing.T) {
	e := &Emails{}
	srv, client := newDomainLimitsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/domain-limits/api", "application/json",
		strings.NewReader(`{"action":"reset-domain"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}
