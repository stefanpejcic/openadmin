package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/bootstrap"
	"openadmin/internal/config"
)

func newAPISecurityMux(s *APISecurity) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/security/basic-auth", s.ServeBasicAuth)
	mux.HandleFunc("POST /api/security/basic-auth", s.ServeBasicAuth)
	mux.HandleFunc("GET /api/security/blacklist-useragents", s.ServeBlacklistUseragents)
	mux.HandleFunc("POST /api/security/blacklist-useragents", s.ServeBlacklistUseragents)
	mux.HandleFunc("POST /api/security/disable-admin", s.HandleDisableAdmin)
	mux.HandleFunc("GET /api/security/firewall", s.ServeFirewall)
	mux.HandleFunc("POST /api/security/firewall", s.ServeFirewall)
	mux.HandleFunc("GET /api/security/waf", s.ServeWAFStatus)
	mux.HandleFunc("POST /api/security/waf", s.ServeWAFStatus)
	mux.HandleFunc("GET /api/security/waf/rules", s.ServeWAFRules)
	mux.HandleFunc("POST /api/security/waf/rules", s.ServeWAFRules)
	return mux
}

func withScratchAPIAdminIni(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = orig })
	return config.AdminConfigPath
}

// --- basic auth ---

func TestAPIServeBasicAuthGetReflectsConfig(t *testing.T) {
	path := withScratchAPIAdminIni(t)
	os.WriteFile(path, []byte("[SECURITY]\nbasic_auth=yes\nbasic_auth_username=admin\nbasic_auth_password=secret\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/basic-auth")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if got["basic_auth"] != "yes" || got["basic_auth_username"] != "admin" || got["basic_auth_password"] != "secret" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestAPIServeBasicAuthPostUpdatesConfigAndSetsRestartFlag(t *testing.T) {
	path := withScratchAPIAdminIni(t)
	os.WriteFile(path, []byte("[USERS]\nreseller=yes\n\n[SECURITY]\nbasic_auth=no\n"), 0644)

	dir := t.TempDir()
	origFlag := bootstrap.RestartFlagPath
	bootstrap.RestartFlagPath = filepath.Join(dir, "restart_needed")
	t.Cleanup(func() { bootstrap.RestartFlagPath = origFlag })

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/basic-auth",
		strings.NewReader(`{"basic_auth":"yes","basic_auth_username":"newadmin","basic_auth_password":"newsecret"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	written := config.Load(path)
	if written.Get("SECURITY", "basic_auth_username", "") != "newadmin" {
		t.Fatalf("expected updated username, got %+v", written)
	}
	if written.Get("USERS", "reseller", "") != "yes" {
		t.Fatalf("expected the unrelated USERS section to survive the rewrite, got %+v", written)
	}
	if _, err := os.Stat(bootstrap.RestartFlagPath); err != nil {
		t.Fatalf("expected the restart-needed flag to be written: %v", err)
	}
}

func TestAPIServeBasicAuthPostRejectsNonJSON(t *testing.T) {
	withScratchAPIAdminIni(t)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/basic-auth", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- blacklist useragents ---

func withScratchAPIBlacklist(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origFile := BlacklistUseragentsFilePath
	BlacklistUseragentsFilePath = filepath.Join(dir, "blacklist_useragents.txt")
	t.Cleanup(func() { BlacklistUseragentsFilePath = origFile })

	origFlag := OpenpanelRestartFlagPath
	OpenpanelRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	t.Cleanup(func() { OpenpanelRestartFlagPath = origFlag })
}

func TestAPIServeBlacklistUseragentsGet(t *testing.T) {
	withScratchAPIBlacklist(t)
	os.WriteFile(BlacklistUseragentsFilePath, []byte("BadBot\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/blacklist-useragents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if !strings.Contains(got["blacklist_useragents"], "BadBot") {
		t.Fatalf("expected file content in response, got %+v", got)
	}
}

func TestAPIServeBlacklistUseragentsPostSavesAndFlagsRestart(t *testing.T) {
	withScratchAPIBlacklist(t)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/blacklist-useragents",
		strings.NewReader(`{"blacklist_useragents":"BadBot/1.0\nEvilCrawler"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	written, err := os.ReadFile(BlacklistUseragentsFilePath)
	if err != nil {
		t.Fatalf("expected the blacklist file to be written: %v", err)
	}
	if !strings.Contains(string(written), "BadBot/1.0") {
		t.Fatalf("expected the new content written, got %q", written)
	}
	if _, err := os.Stat(OpenpanelRestartFlagPath); err != nil {
		t.Fatalf("expected the OpenPanel restart flag to be written: %v", err)
	}
}

func TestAPIServeBlacklistUseragentsPostNothingToUpdateIs400(t *testing.T) {
	withScratchAPIBlacklist(t)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/blacklist-useragents", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- disable admin ---

func TestAPIHandleDisableAdminRunsCommandAndReturnsMessage(t *testing.T) {
	orig := apiSecurityDisableAdminRun
	ran := false
	apiSecurityDisableAdminRun = func() { ran = true }
	t.Cleanup(func() { apiSecurityDisableAdminRun = orig })

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/security/disable-admin", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !ran {
		t.Fatal("expected the disable-admin command to be invoked")
	}
	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if got["success"] != true {
		t.Fatalf("expected success:true, got %+v", got)
	}
}

// --- firewall ---

func TestAPIServeFirewallGetAvailable(t *testing.T) {
	orig := firewallCommandAvailableRun
	firewallCommandAvailableRun = func(string) bool { return true }
	t.Cleanup(func() { firewallCommandAvailableRun = orig })

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/firewall")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]bool
	json.NewDecoder(resp.Body).Decode(&got)
	if !got["available"] {
		t.Fatalf("expected available:true, got %+v", got)
	}
}

func TestAPIServeFirewallPostRunsCSFAndReturnsOutput(t *testing.T) {
	orig := firewallCSFRun
	var gotFileContent string
	firewallCSFRun = func(tmpFile string) (string, error) {
		content, _ := os.ReadFile(tmpFile)
		gotFileContent = string(content)
		return "csf output here", nil
	}
	t.Cleanup(func() { firewallCSFRun = orig })

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/firewall", strings.NewReader(`{"action":"cf"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if got["output"] != "csf output here" {
		t.Fatalf("expected the csf output passed through, got %+v", got)
	}
	if gotFileContent != "action=cf" {
		t.Fatalf("expected the query string written to the temp file, got %q", gotFileContent)
	}
}

func TestAPIServeFirewallPostRejectsNonJSON(t *testing.T) {
	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/firewall", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- WAF status ---

func withScratchAPIWAF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	rulesDir := filepath.Join(dir, "rules") + string(os.PathSeparator)
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	origRulesDir := WAFRulesDir
	WAFRulesDir = rulesDir
	t.Cleanup(func() { WAFRulesDir = origRulesDir })

	origConfig := config.OpenpanelConfigPath
	config.OpenpanelConfigPath = filepath.Join(dir, "openpanel.config")
	t.Cleanup(func() { config.OpenpanelConfigPath = origConfig })

	return rulesDir
}

func TestAPIServeWAFStatusMissingConfigIs500(t *testing.T) {
	withScratchAPIWAF(t)
	// No config file written -- os.Open must fail.

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/waf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the config file can't be opened, got %d", resp.StatusCode)
	}
}

func TestAPIServeWAFStatusGetOnWhenConfigMentionsWaf(t *testing.T) {
	withScratchAPIWAF(t)
	os.WriteFile(config.OpenpanelConfigPath, []byte("some_setting=enable_waf_thing\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/waf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "on" {
		t.Fatalf("expected status on, got %+v", got)
	}
}

func TestAPIServeWAFStatusPostEnable(t *testing.T) {
	withScratchAPIWAF(t)
	os.WriteFile(config.OpenpanelConfigPath, []byte("nothing_here=1\n"), 0644)

	origRun := wafRunOpenCLIRun
	var capturedArgs []string
	wafRunOpenCLIRun = func(args ...string) error {
		capturedArgs = args
		return nil
	}
	t.Cleanup(func() { wafRunOpenCLIRun = origRun })

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf", strings.NewReader(`{"status":"yes"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "waf" || capturedArgs[1] != "enable" {
		t.Fatalf("expected opencli waf enable, got %v", capturedArgs)
	}
}

func TestAPIServeWAFStatusPostAlreadyEnabledIs400(t *testing.T) {
	withScratchAPIWAF(t)
	os.WriteFile(config.OpenpanelConfigPath, []byte("enable_waf=1\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf", strings.NewReader(`{"status":"yes"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for already-enabled, got %d", resp.StatusCode)
	}
}

func TestAPIServeWAFStatusPostInvalidStatusIs400(t *testing.T) {
	withScratchAPIWAF(t)
	os.WriteFile(config.OpenpanelConfigPath, []byte("nothing=1\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf", strings.NewReader(`{"status":"maybe"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid status value, got %d", resp.StatusCode)
	}
}

// --- WAF rules ---

func TestAPIServeWAFRulesGetListsAndCountsLines(t *testing.T) {
	rulesDir := withScratchAPIWAF(t)
	os.WriteFile(filepath.Join(rulesDir, "sqli.conf"), []byte("rule1\nrule2\n\nrule3\n"), 0644)
	os.WriteFile(filepath.Join(rulesDir, "xss.conf.disabled"), []byte("ruleA\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/waf/rules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"name":"sqli"`) || !strings.Contains(string(body), `"num_rules":3`) {
		t.Fatalf("expected sqli.conf with 3 non-empty lines, got %s", body)
	}
	if !strings.Contains(string(body), `"name":"xss"`) || !strings.Contains(string(body), `"status":"off"`) {
		t.Fatalf("expected xss disabled entry, got %s", body)
	}
}

func TestAPIServeWAFRulesPostTogglesOff(t *testing.T) {
	rulesDir := withScratchAPIWAF(t)
	os.WriteFile(filepath.Join(rulesDir, "sqli.conf"), []byte("rule1\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf/rules",
		strings.NewReader(`{"rule_name":"sqli","action":"off"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "sqli.conf.disabled")); err != nil {
		t.Fatal("expected sqli.conf renamed to sqli.conf.disabled")
	}
}

func TestAPIServeWAFRulesPostRuleNotFoundIs404(t *testing.T) {
	withScratchAPIWAF(t)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf/rules",
		strings.NewReader(`{"rule_name":"nonexistent","action":"off"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPIServeWAFRulesPostInvalidActionIs400(t *testing.T) {
	rulesDir := withScratchAPIWAF(t)
	os.WriteFile(filepath.Join(rulesDir, "sqli.conf"), []byte("rule1\n"), 0644)

	s := &APISecurity{}
	srv := httptest.NewServer(newAPISecurityMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/security/waf/rules",
		strings.NewReader(`{"rule_name":"sqli","action":"explode"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
