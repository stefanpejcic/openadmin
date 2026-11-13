package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openadmin/internal/auth"
)

func TestBotRewritePolicyDualBlock(t *testing.T) {
	base := `http://example.com, http://www.example.com {
  route {
    import ratelimit example.com
    coraza_waf {
      SecRuleEngine On
    }
    reverse_proxy http://127.0.0.1:8080 {
    }
  }
}

https://example.com, https://www.example.com {
  route {
    import ratelimit example.com
    coraza_waf {
      SecRuleEngine On
    }
    reverse_proxy https://127.0.0.1:8443 {
    }
  }
}`

	if got := botPolicyFromConfig(base); got != botPolicyAllow {
		t.Fatalf("initial policy = %q, want allow", got)
	}

	blk := botMustRewrite(t, base, botPolicyBlock)
	if got := botPolicyFromConfig(blk); got != botPolicyBlock {
		t.Fatalf("after block = %q, want block", got)
	}
	if strings.Count(blk, "import botcontrol_block") != 2 {
		t.Fatalf("expected 2 botcontrol_block directives, got:\n%s", blk)
	}

	rl := botMustRewrite(t, blk, botPolicyRateLimit)
	if got := botPolicyFromConfig(rl); got != botPolicyRateLimit {
		t.Fatalf("after ratelimit = %q, want ratelimit", got)
	}
	if strings.Count(rl, "import botcontrol_ratelimit example.com") != 2 {
		t.Fatalf("expected 2 botcontrol_ratelimit directives, got:\n%s", rl)
	}

	allow := botMustRewrite(t, rl, botPolicyAllow)
	if got := botPolicyFromConfig(allow); got != botPolicyAllow {
		t.Fatalf("after allow = %q, want allow", got)
	}
}

func TestBotAnalyzeDomains(t *testing.T) {
	tmp := t.TempDir()
	origDomlogs := CaddyDomlogsDir
	origASN := BotASNPrefixesPath
	CaddyDomlogsDir = tmp
	BotASNPrefixesPath = filepath.Join(tmp, "asn.tsv")
	t.Cleanup(func() {
		CaddyDomlogsDir = origDomlogs
		BotASNPrefixesPath = origASN
	})

	if err := os.WriteFile(BotASNPrefixesPath, []byte("203.0.113.0/24\tAS12345\tOpenAI\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_800_000_000, 0)
	if err := os.MkdirAll(filepath.Join(tmp, "site1.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "site2.com"), 0o755); err != nil {
		t.Fatal(err)
	}

	log1 := strings.Join([]string{
		`{"ts":1799999900,"status":403,"request":{"client_ip":"203.0.113.10","uri":"/?s=ai","headers":{"User-Agent":["GPTBot/1.2"]}}}`,
		`{"ts":1799999910,"status":200,"request":{"client_ip":"198.51.100.5","uri":"/","headers":{"User-Agent":["Googlebot/2.1"]}}}`,
	}, "\n")
	log2 := `{"ts":1799999920,"status":429,"request":{"client_ip":"203.0.113.20","uri":"/shop","headers":{"User-Agent":["ClaudeBot/1.0"]}}}` + "\n"

	if err := os.WriteFile(filepath.Join(tmp, "site1.com", "access.log"), []byte(log1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "site2.com", "access.log"), []byte(log2), 0o644); err != nil {
		t.Fatal(err)
	}

	report, counts := botAnalyzeDomains([]string{"site1.com", "site2.com"}, "", "24h", now)
	if report.TotalRequests != 3 || report.AIRequests != 2 || report.BlockedCount != 2 {
		t.Fatalf("unexpected totals: %+v", report)
	}
	if counts["site1.com"] != [3]int{2, 1, 1} {
		t.Fatalf("unexpected site1 counts: %+v", counts["site1.com"])
	}
	if len(report.TopNetworks) == 0 || report.TopNetworks[0].ASN != "AS12345 (OpenAI)" || report.TopNetworks[0].UniqueIPs != 2 {
		t.Fatalf("unexpected TopNetworks: %+v", report.TopNetworks)
	}

	filtered, _ := botAnalyzeDomains([]string{"site1.com", "site2.com"}, "site2.com", "24h", now)
	if filtered.TotalRequests != 1 || len(filtered.TopURLs) != 1 || filtered.TopURLs[0].Target != "/shop" {
		t.Fatalf("unexpected filtered report: %+v", filtered)
	}
}

func TestServeBotControlPOST(t *testing.T) {
	tmp := t.TempDir()
	origConfs := CaddyDomainsConfDir
	origDomlogs := CaddyDomlogsDir
	origReload := botReloadCaddy
	origRestart := botRestartOpenPanel
	origModCfg := ModulesConfigFilePath
	origRestartFlag := ModulesOpenpanelRestartFlagPath
	CaddyDomainsConfDir = filepath.Join(tmp, "domains")
	CaddyDomlogsDir = filepath.Join(tmp, "domlogs")
	ModulesConfigFilePath = filepath.Join(tmp, "openpanel.config")
	ModulesOpenpanelRestartFlagPath = filepath.Join(tmp, "openpanel_restart_needed")
	reloaded := false
	restarted := 0
	botReloadCaddy = func(context.Context) error {
		reloaded = true
		return nil
	}
	botRestartOpenPanel = func(context.Context) error {
		restarted++
		return nil
	}
	t.Cleanup(func() {
		CaddyDomainsConfDir = origConfs
		CaddyDomlogsDir = origDomlogs
		botReloadCaddy = origReload
		botRestartOpenPanel = origRestart
		ModulesConfigFilePath = origModCfg
		ModulesOpenpanelRestartFlagPath = origRestartFlag
	})

	if err := os.MkdirAll(CaddyDomainsConfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="domains,waf"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(CaddyDomainsConfDir, "example.com.conf")
	if err := os.WriteFile(confPath, []byte("http://example.com {\n  route {\n    coraza_waf {\n    }\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bc := &BotControl{Sessions: auth.NewManager("test-secret", false)}

	// Disabled module rejects policy edits until enabled from OpenAdmin.
	formDisabled := url.Values{"domain": {"example.com"}, "policy": {"block"}}
	reqDisabled := httptest.NewRequest(http.MethodPost, "/security/bot-control", strings.NewReader(formDisabled.Encode()))
	reqDisabled.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recDisabled := httptest.NewRecorder()
	bc.ServeBotControl(recDisabled, reqDisabled)
	if botReadDomainPolicy("example.com") != botPolicyAllow {
		t.Fatalf("expected policy change to be refused while module is disabled")
	}

	// Enable module from OpenAdmin.
	reqEnable := httptest.NewRequest(http.MethodPost, "/security/bot-control", strings.NewReader(url.Values{"action": {"enable_module"}}.Encode()))
	reqEnable.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recEnable := httptest.NewRecorder()
	bc.ServeBotControl(recEnable, reqEnable)
	if recEnable.Code != http.StatusSeeOther || !botModuleEnabled() || restarted != 1 {
		t.Fatalf("enable failed: code=%d enabled=%v restarted=%d", recEnable.Code, botModuleEnabled(), restarted)
	}

	// Update per-site policy once enabled.
	form := url.Values{"domain": {"example.com"}, "policy": {"block"}}
	req := httptest.NewRequest(http.MethodPost, "/security/bot-control", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	bc.ServeBotControl(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if !reloaded {
		t.Fatalf("expected Caddy reload to be called")
	}
	if got := botReadDomainPolicy("example.com"); got != botPolicyBlock {
		t.Fatalf("saved policy = %q, want block", got)
	}

	getEnabledReq := httptest.NewRequest(http.MethodGet, "/security/bot-control?output=json", nil)
	getEnabledRec := httptest.NewRecorder()
	bc.ServeBotControl(getEnabledRec, getEnabledReq)
	if getEnabledRec.Code != http.StatusOK || !strings.Contains(getEnabledRec.Body.String(), `"policy":"block"`) || !strings.Contains(getEnabledRec.Body.String(), `"module_enabled":true`) {
		t.Fatalf("GET enabled json = %d %s", getEnabledRec.Code, getEnabledRec.Body.String())
	}

	// Disable module resets domains back to Allow and removes bot_control.
	reqDisable := httptest.NewRequest(http.MethodPost, "/security/bot-control", strings.NewReader(url.Values{"action": {"disable_module"}}.Encode()))
	reqDisable.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recDisable := httptest.NewRecorder()
	bc.ServeBotControl(recDisable, reqDisable)
	if recDisable.Code != http.StatusSeeOther || botModuleEnabled() || restarted != 2 {
		t.Fatalf("disable failed: code=%d enabled=%v restarted=%d", recDisable.Code, botModuleEnabled(), restarted)
	}
	if got := botReadDomainPolicy("example.com"); got != botPolicyAllow {
		t.Fatalf("expected disable_module to reset policy to allow, got %q", got)
	}
	if area := moduleAreaOf("/server/bot-control"); area != "security" {
		t.Fatalf("moduleAreaOf(/server/bot-control) = %q, want security", area)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/security/bot-control?output=json", nil)
	getRec := httptest.NewRecorder()
	bc.ServeBotControl(getRec, getReq)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `"policy":"allow"`) || !strings.Contains(getRec.Body.String(), `"module_enabled":false`) {
		t.Fatalf("GET disabled json = %d %s", getRec.Code, getRec.Body.String())
	}
}

func botMustRewrite(t *testing.T, content string, p botPolicy) string {
	t.Helper()
	out, err := botRewritePolicy(content, "example.com", p)
	if err != nil {
		t.Fatalf("botRewritePolicy(%s): %v", p, err)
	}
	return out
}

func TestBotRewritePolicyNoAnchor(t *testing.T) {
	content := "example.com {\n  respond \"hi\"\n}\n"
	if _, err := botRewritePolicy(content, "example.com", botPolicyBlock); !errors.Is(err, errBotNoAnchor) {
		t.Fatalf("err = %v, want errBotNoAnchor", err)
	}
}

// caddy rejecting the change has to leave every file as it was, or caddy won't start next time
func TestBotApplyPolicyRollsBack(t *testing.T) {
	tmp := t.TempDir()
	origConfs, origReload := CaddyDomainsConfDir, botReloadCaddy
	t.Cleanup(func() { CaddyDomainsConfDir, botReloadCaddy = origConfs, origReload })
	CaddyDomainsConfDir = tmp

	conf := "https://%s {\n  route {\n    # botcontrol: allow\n    reverse_proxy http://127.0.0.1:8080\n  }\n}\n"
	for _, d := range []string{"a.com", "b.com"} {
		if err := os.WriteFile(filepath.Join(tmp, d+".conf"), []byte(fmt.Sprintf(conf, d)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "bare.com.conf"), []byte("bare.com {\n  respond \"hi\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	botReloadCaddy = func(context.Context) error { return errors.New("unrecognized directive: rate_limit") }
	if _, _, err := botApplyPolicy(context.Background(), []string{"a.com", "b.com"}, botPolicyRateLimit); err == nil || !strings.Contains(err.Error(), "rate_limit") {
		t.Fatalf("err = %v, want caddy's message", err)
	}
	for _, d := range []string{"a.com", "b.com"} {
		if got, _ := os.ReadFile(filepath.Join(tmp, d+".conf")); string(got) != fmt.Sprintf(conf, d) {
			t.Fatalf("%s not restored:\n%s", d, got)
		}
	}

	reloads := 0
	botReloadCaddy = func(context.Context) error { reloads++; return nil }
	changed, skipped, err := botApplyPolicy(context.Background(), []string{"a.com", "b.com", "bare.com", "missing.com"}, botPolicyBlock)
	if err != nil || changed != 2 || reloads != 1 || len(skipped) != 2 {
		t.Fatalf("changed=%d skipped=%v reloads=%d err=%v", changed, skipped, reloads, err)
	}
	if botReadDomainPolicy("a.com") != botPolicyBlock || botReadDomainPolicy("b.com") != botPolicyBlock {
		t.Fatal("block not saved")
	}
}

func TestBotCaddyErrorLine(t *testing.T) {
	jsonOut := `{"level":"info","ts":1794591082.760779,"msg":"using config from file","file":"/etc/caddy/Caddyfile"}` + "\n" + `{"level":"error","ts":1794591082.7608736,"msg":"adapting config using caddyfile: File to import not found: /etc/openpanel/caddy/botcontrol.conf, at /etc/caddy/Caddyfile:79"}` + "\n"
	if got := botCaddyErrorLine(jsonOut); got != "adapting config using caddyfile: File to import not found: /etc/openpanel/caddy/botcontrol.conf, at /etc/caddy/Caddyfile:79" {
		t.Fatalf("json: got %q", got)
	}
	if got := botCaddyErrorLine("Error: adapting config using caddyfile: boom\n"); got != "adapting config using caddyfile: boom" {
		t.Fatalf("plain: got %q", got)
	}
}

// a suspended domain's config and an empty one got the marker after the site block, block there broke every reload
func TestBotRewritePolicyDropsTopLevelLines(t *testing.T) {
	suspended := "old.example.org {\n  route {\n    file_server\n  }\n}\n    # botcontrol: allow\n"
	if _, err := botRewritePolicy(suspended, "old.example.org", botPolicyBlock); !errors.Is(err, errBotNoAnchor) {
		t.Fatalf("suspended: err = %v, want errBotNoAnchor", err)
	}
	if out, err := botRewritePolicy(suspended, "old.example.org", botPolicyAllow); err != nil || strings.Contains(out, "botcontrol") {
		t.Fatalf("allow should drop the stray marker, got %q, %v", out, err)
	}
	if _, err := botRewritePolicy("\n    # botcontrol: allow\n", "x.org", botPolicyBlock); !errors.Is(err, errBotNoAnchor) {
		t.Fatalf("empty: err = %v, want errBotNoAnchor", err)
	}
	waf := "https://a.com {\n  route {\n    coraza_waf {\n      directives `\n        SecRuleEngine Off\n      `\n    }\n    reverse_proxy x\n  }\n}\n# botcontrol: block\n"
	out, err := botRewritePolicy(waf, "a.com", botPolicyBlock)
	if err != nil || strings.Count(out, "import botcontrol_block") != 1 || strings.Contains(out, "# botcontrol") {
		t.Fatalf("waf + stray: %q, %v", out, err)
	}
}
