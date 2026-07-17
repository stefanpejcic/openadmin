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
	"openadmin/internal/config"
)

func newWAFTestServer(t *testing.T, wf *WAF) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	rulesDir := filepath.Join(dir, "rules") + string(os.PathSeparator)
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	origRulesDir := WAFRulesDir
	WAFRulesDir = rulesDir
	t.Cleanup(func() { WAFRulesDir = origRulesDir })

	origOpenpanelConfig := config.OpenpanelConfigPath
	config.OpenpanelConfigPath = filepath.Join(dir, "openpanel.config")
	t.Cleanup(func() { config.OpenpanelConfigPath = origOpenpanelConfig })

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
	wf.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /security/waf", wf.ServeWAFStatus)
	mux.HandleFunc("POST /security/waf", wf.ServeWAFStatus)
	mux.HandleFunc("GET /security/waf/rules", wf.ServeWAFRules)
	mux.HandleFunc("POST /security/waf/rules", wf.ServeWAFRules)
	mux.HandleFunc("GET /security/waf/view-rules", wf.ServeWAFViewRules)
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

func TestWafResolveRuleFile(t *testing.T) {
	rulesDir := "/etc/openpanel/caddy/coreruleset/rules/"

	// Absolute filename already inside rulesDir (the real "View" link
	// shape) must resolve to itself, not get double-prefixed.
	got, ok := wafResolveRuleFile(rulesDir, rulesDir+"foo.conf")
	if !ok || got != rulesDir+"foo.conf" {
		t.Fatalf("expected the absolute in-dir path preserved, got %q ok=%v", got, ok)
	}

	// Absolute filename outside rulesDir must be rejected.
	_, ok = wafResolveRuleFile(rulesDir, "/etc/passwd")
	if ok {
		t.Fatal("expected an absolute out-of-dir path to be rejected")
	}

	// Relative traversal must be rejected.
	_, ok = wafResolveRuleFile(rulesDir, "../../../etc/passwd")
	if ok {
		t.Fatal("expected a relative traversal path to be rejected")
	}

	// Plain relative filename resolves inside rulesDir.
	got, ok = wafResolveRuleFile(rulesDir, "foo.conf")
	if !ok || got != rulesDir+"foo.conf" {
		t.Fatalf("expected foo.conf resolved inside rulesDir, got %q ok=%v", got, ok)
	}
}

func TestServeWAFRulesListsAndCountsLines(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	os.WriteFile(filepath.Join(WAFRulesDir, "sqli.conf"), []byte("rule1\nrule2\n\nrule3\n"), 0644)
	os.WriteFile(filepath.Join(WAFRulesDir, "xss.conf.disabled"), []byte("ruleA\n"), 0644)

	resp, err := client.Get(srv.URL + "/security/waf/rules?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"name":"sqli"`) || !strings.Contains(string(body), `"num_rules":3`) {
		t.Fatalf("expected sqli.conf with 3 non-empty lines, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), `"name":"xss"`) || !strings.Contains(string(body), `"status":"off"`) {
		t.Fatalf("expected xss disabled entry, got %s", truncate(string(body)))
	}
}

func TestServeWAFRulesRendersHTML(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	os.WriteFile(filepath.Join(WAFRulesDir, "sqli.conf"), []byte("rule1\nrule2\n\nrule3\n"), 0644)
	os.WriteFile(filepath.Join(WAFRulesDir, "xss.conf.disabled"), []byte("ruleA\n"), 0644)

	resp, err := client.Get(srv.URL + "/security/waf/rules")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"sqli", "xss", "Waf Rules", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeWAFRulesPOSTTogglesOff(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	os.WriteFile(filepath.Join(WAFRulesDir, "sqli.conf"), []byte("rule1\n"), 0644)

	resp, err := client.PostForm(srv.URL+"/security/waf/rules", url.Values{
		"rule_name": {"sqli"},
		"action":    {"off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "disabled. Restart Caddy") {
		t.Fatalf("expected disabled flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(filepath.Join(WAFRulesDir, "sqli.conf.disabled")); err != nil {
		t.Fatal("expected sqli.conf renamed to sqli.conf.disabled")
	}
}

func TestServeWAFRulesPOSTTogglesOn(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	os.WriteFile(filepath.Join(WAFRulesDir, "sqli.conf.disabled"), []byte("rule1\n"), 0644)

	resp, err := client.PostForm(srv.URL+"/security/waf/rules", url.Values{
		"rule_name": {"sqli"},
		"action":    {"on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "enabled. Restart Caddy") {
		t.Fatalf("expected enabled flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(filepath.Join(WAFRulesDir, "sqli.conf")); err != nil {
		t.Fatal("expected sqli.conf.disabled renamed back to sqli.conf")
	}
}

func TestServeWAFRulesPOSTMissingRuleFlashesError(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/security/waf/rules", url.Values{
		"rule_name": {"nonexistent"},
		"action":    {"off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "rule_file is missing") {
		t.Fatalf("expected missing-rule-file flash, got %s", truncate(string(body)))
	}
}

func TestServeWAFViewRulesReturnsEscapedContent(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	rulePath := filepath.Join(WAFRulesDir, "sqli.conf")
	os.WriteFile(rulePath, []byte("<script>alert(1)</script>"), 0644)

	resp, err := client.Get(srv.URL + "/security/waf/view-rules?edit=" + url.QueryEscape(rulePath))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "&lt;script&gt;") {
		t.Fatalf("expected HTML-escaped file content, got %s", truncate(string(body)))
	}
}

func TestServeWAFViewRulesRejectsBadExtension(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	badPath := filepath.Join(WAFRulesDir, "sneaky.txt")
	os.WriteFile(badPath, []byte("data"), 0644)

	resp, err := client.Get(srv.URL + "/security/waf/view-rules?edit=" + url.QueryEscape(badPath))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid file extension") {
		t.Fatalf("expected invalid-extension flash, got %s", truncate(string(body)))
	}
}

func TestServeWAFViewRulesRejectsPathOutsideRulesDir(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.Get(srv.URL + "/security/waf/view-rules?edit=" + url.QueryEscape("/etc/passwd.conf"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid file path") {
		t.Fatalf("expected invalid-file-path flash, got %s", truncate(string(body)))
	}
}

func TestServeWAFStatusOffByDefault(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	resp, err := client.Get(srv.URL + "/security/waf?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"off"`) {
		t.Fatalf("expected off status with no config file, got %s", truncate(string(body)))
	}
}

func TestServeWAFStatusRendersHTML(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	resp, err := client.Get(srv.URL + "/security/waf")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"CorazaWAF", "Manage Rules", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeWAFStatusOnWhenConfigMentionsWaf(t *testing.T) {
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	os.WriteFile(config.OpenpanelConfigPath, []byte("some_setting=enable_waf_thing\n"), 0644)

	resp, err := client.Get(srv.URL + "/security/waf?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"on"`) {
		t.Fatalf("expected on status due to broad substring match, got %s", truncate(string(body)))
	}
}

func TestServeWAFStatusPOSTEnable(t *testing.T) {
	origRun := wafRunOpenCLIRun
	var capturedArgs []string
	wafRunOpenCLIRun = func(args ...string) error {
		capturedArgs = args
		return nil
	}
	t.Cleanup(func() { wafRunOpenCLIRun = origRun })

	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	resp, err := client.PostForm(srv.URL+"/security/waf", url.Values{"status": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "waf" || capturedArgs[1] != "enable" {
		t.Fatalf("expected opencli waf enable, got %v", capturedArgs)
	}
}

func TestServeWAFStatusPOSTFailureFlashesError(t *testing.T) {
	origRun := wafRunOpenCLIRun
	wafRunOpenCLIRun = func(args ...string) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { wafRunOpenCLIRun = origRun })

	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	resp, err := client.PostForm(srv.URL+"/security/waf", url.Values{"status": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Failed to change WAF status") {
		t.Fatalf("expected the failure flash, got %s", truncate(string(body)))
	}
}
