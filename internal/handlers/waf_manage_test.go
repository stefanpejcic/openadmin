package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wafTestDomainConf = `example.com {
    coraza_waf {
        directives ` + "`" + `
            Include /etc/openpanel/caddy/coraza_rules.conf
            Include /etc/openpanel/caddy/coreruleset/rules/*.conf
            SecRuleEngine On
            SecRuleRemoveById 007 920350
            SecRuleRemoveByTag "example"
            SecRuleRemoveByTag "attack-xss"
            SecAuditLog /var/log/caddy/coraza_waf/example.com.log
        ` + "`" + `
    }
}
`

// withWAFScratch points every WAF path at temp dirs, gives example.com and shop.example.org to alice and stubs caddy reload
func withWAFScratch(t *testing.T) (domainsDir, logDir string, reloads *int) {
	t.Helper()
	dir := t.TempDir()
	domainsDir = filepath.Join(dir, "domains")
	logDir = filepath.Join(dir, "logs") + string(os.PathSeparator)
	os.MkdirAll(domainsDir, 0755)
	os.MkdirAll(logDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "example.com.conf"), []byte(wafTestDomainConf), 0644)
	os.WriteFile(filepath.Join(domainsDir, "shop.example.org.conf"), []byte(strings.ReplaceAll(wafTestDomainConf, "example.com", "shop.example.org")), 0644)

	origDomains, origLogs, origPlugins, origCRS := CaddyDomainsConfDir, WAFLogDir, WAFPluginsDir, WAFCRSDir
	CaddyDomainsConfDir, WAFLogDir, WAFPluginsDir, WAFCRSDir = domainsDir, logDir, filepath.Join(dir, "plugins"), filepath.Join(dir, "crs")
	origOwners, origReload, origGit, origGitRun, origBg := wafDomainOwners, caddyReloadRun, wafGitLogRun, wafGitRun, wafBackgroundFetch
	wafDomainOwners = func(*sql.DB) map[string]string {
		return map[string]string{"example.com": "alice", "shop.example.org": "alice"}
	}
	n := 0
	reloads = &n
	caddyReloadRun = func() error { n++; return nil }
	wafGitLogRun = func(string, string) string { return "abc123|2026-10-08|fix(921120): standardize lowercase" }
	wafGitRun = func(string, ...string) (string, error) { return "0", nil }
	wafBackgroundFetch = func([]string) {}
	t.Cleanup(func() {
		CaddyDomainsConfDir, WAFLogDir, WAFPluginsDir, WAFCRSDir = origDomains, origLogs, origPlugins, origCRS
		wafDomainOwners, caddyReloadRun, wafGitLogRun, wafGitRun, wafBackgroundFetch = origOwners, origReload, origGit, origGitRun, origBg
	})
	return domainsDir, logDir, reloads
}

func wafGetBody(t *testing.T, client *http.Client, u string) (int, string) {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func wafPost(t *testing.T, client *http.Client, u string, form url.Values) string {
	t.Helper()
	resp, err := client.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestWAFParseAndRewriteRemovals(t *testing.T) {
	rules, tags := wafParseRemovals(wafTestDomainConf)
	if strings.Join(rules, ",") != "920350" || strings.Join(tags, ",") != "attack-xss" {
		t.Fatalf("expected sentinels stripped, got rules=%v tags=%v", rules, tags)
	}
	out := wafRewriteRemovals(wafTestDomainConf, []string{"920350", "942100"}, nil)
	if !strings.Contains(out, "SecRuleRemoveById 007 920350 942100\n") {
		t.Fatalf("expected sentinel then ids, got %s", out)
	}
	if strings.Contains(out, "attack-xss") || !strings.Contains(out, `SecRuleRemoveByTag "example"`) {
		t.Fatalf("expected only the sentinel tag left, got %s", out)
	}
	if !strings.Contains(out, "SecAuditLog /var/log/caddy/coraza_waf/example.com.log") {
		t.Fatalf("expected other directives kept, got %s", out)
	}
}

func TestWAFCleanRulesRefusesScoringAndJunk(t *testing.T) {
	if _, err := wafCleanRules([]string{"949110"}); err != errWAFScoringRule {
		t.Fatalf("expected scoring rule refused, got %v", err)
	}
	if _, err := wafCleanRules([]string{"12a"}); err != errWAFRuleID {
		t.Fatalf("expected non-numeric refused, got %v", err)
	}
	if _, err := wafCleanTags([]string{`x" SecRuleEngine Off`}); err != errWAFTag {
		t.Fatalf("expected tag with quote refused, got %v", err)
	}
}

func TestServeWAFRulesGlobalExclusionsSaveAndHidden(t *testing.T) {
	withWAFScratch(t)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf/rules", url.Values{
		"action": {"global_exclusions"}, "removed_rules": {"942100 920350"}, "removed_tags": {"attack-xss"},
	})
	if !strings.Contains(body, "Server-wide rule exclusions saved.") {
		t.Fatalf("expected success flash, got %s", truncate(body))
	}
	content, err := os.ReadFile(wafGlobalExclusionsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SecRuleRemoveById 942100\n", "SecRuleRemoveById 920350\n", "SecRuleRemoveByTag \"attack-xss\"\n"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("expected %q in exclusions file, got %s", want, content)
		}
	}
	// the exclusions file must not show up as a toggleable rule set
	_, list := wafGetBody(t, client, srv.URL+"/security/waf/rules?output=json")
	if strings.Contains(list, "EXCLUSION-RULES-AFTER-CRS") {
		t.Fatalf("expected exclusions file hidden from rule sets, got %s", list)
	}
	_, page := wafGetBody(t, client, srv.URL+"/security/waf/rules")
	if !strings.Contains(page, "942100 920350") || strings.Contains(page, "shop.example.org") {
		t.Fatalf("expected saved ids and no domain rows on the rules page, got %s", truncate(page))
	}
	_, domainsPage := wafGetBody(t, client, srv.URL+"/security/waf/domains")
	for _, want := range []string{"example.com", "shop.example.org", "/security/waf/rules/alice", "</html>"} {
		if !strings.Contains(domainsPage, want) {
			t.Fatalf("expected domains page to contain %q, got %s", want, truncate(domainsPage))
		}
	}
}

func TestServeWAFRulesGlobalDisableRefusesScoringRule(t *testing.T) {
	withWAFScratch(t)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf/rules", url.Values{"action": {"disable_rule"}, "rule_id": {"949110"}})
	if !strings.Contains(body, "only add up the scores") {
		t.Fatalf("expected scoring rule error, got %s", truncate(body))
	}
	if _, err := os.Stat(wafGlobalExclusionsPath()); err == nil {
		t.Fatal("expected no exclusions file written")
	}
}

func TestServeWAFTargetRulesDomainSave(t *testing.T) {
	domainsDir, _, reloads := withWAFScratch(t)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf/rules/example.com", url.Values{
		"action": {"save"}, "engine": {"DetectionOnly"}, "removed_rules": {"942100"}, "removed_tags": {""},
	})
	if !strings.Contains(body, "WAF settings saved for example.com.") {
		t.Fatalf("expected success flash, got %s", truncate(body))
	}
	content, _ := os.ReadFile(filepath.Join(domainsDir, "example.com.conf"))
	if !strings.Contains(string(content), "SecRuleEngine DetectionOnly") || !strings.Contains(string(content), "SecRuleRemoveById 007 942100\n") {
		t.Fatalf("expected engine and rules written, got %s", content)
	}
	if *reloads != 1 {
		t.Fatalf("expected one caddy reload, got %d", *reloads)
	}
	other, _ := os.ReadFile(filepath.Join(domainsDir, "shop.example.org.conf"))
	if !strings.Contains(string(other), "SecRuleEngine On") {
		t.Fatalf("expected the other domain untouched, got %s", other)
	}
}

func TestServeWAFTargetRulesUserDisableRuleOnAllDomains(t *testing.T) {
	domainsDir, _, _ := withWAFScratch(t)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf/rules/alice", url.Values{"action": {"disable_rule"}, "rule_id": {"930120"}})
	if !strings.Contains(body, "Rule 930120 disabled for all domains of this user.") {
		t.Fatalf("expected success flash, got %s", truncate(body))
	}
	for _, d := range []string{"example.com", "shop.example.org"} {
		content, _ := os.ReadFile(filepath.Join(domainsDir, d+".conf"))
		if !strings.Contains(string(content), "SecRuleRemoveById 007 920350 930120\n") {
			t.Fatalf("expected rule added for %s keeping existing ones, got %s", d, content)
		}
		if !strings.Contains(string(content), `SecRuleRemoveByTag "attack-xss"`) {
			t.Fatalf("expected existing tags kept for %s, got %s", d, content)
		}
	}
}

func TestServeWAFTargetRulesUnknownTargetRedirects(t *testing.T) {
	withWAFScratch(t)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	for _, target := range []string{"nope.example", "bob", "a$b"} {
		_, body := wafGetBody(t, client, srv.URL+"/security/waf/rules/"+url.PathEscape(target))
		if !strings.Contains(body, "no domain or user with WAF config found") {
			t.Fatalf("expected not-found flash for %q, got %s", target, truncate(body))
		}
	}
}

const wafTestLogBlocked = `{"transaction":{"timestamp":"2026/10/09 11:00:00","client_ip":"1.2.3.4","request":{"method":"GET","uri":"/?id=1' or 1=1"},"response":{"status":403},"is_interrupted":true},"messages":[{"error_message":"[id \"942100\"] [msg \"SQL Injection Attack Detected via libinjection\"] [data \"Matched Data: s&1c found\"] [tag \"attack-sqli\"]"},{"error_message":"[id \"949110\"] [msg \"Inbound Anomaly Score Exceeded\"]"}]}`
const wafTestLogFlagged = `{"transaction":{"timestamp":"2026/10/09 12:00:00","client_ip":"5.6.7.8","request":{"method":"GET","uri":"/wp-login.php"},"response":{"status":200},"is_interrupted":false},"messages":[{"error_message":"[id \"913100\"] [msg \"Found User-Agent associated with security scanner\"] [tag \"attack-reputation-scanner\"]"}]}`
const wafTestLog404 = `{"transaction":{"timestamp":"2026/10/09 12:30:00","client_ip":"9.9.9.9","request":{"method":"GET","uri":"/favicon.ico"},"response":{"status":404},"is_interrupted":false}}`

func TestServeWAFLogsAggregatesAcrossDomains(t *testing.T) {
	_, logDir, _ := withWAFScratch(t)
	os.WriteFile(filepath.Join(logDir, "example.com.log"), []byte(wafTestLogBlocked+"\n"+wafTestLogFlagged+"\n"+wafTestLog404+"\n"), 0644)
	os.WriteFile(filepath.Join(logDir, "shop.example.org.log"), []byte(wafTestLogBlocked+"\n"), 0644)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	_, body := wafGetBody(t, client, srv.URL+"/security/waf/logs?output=json")
	var rep wafLogReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("bad json: %v: %s", err, body)
	}
	if rep.Blocked != 2 || rep.Flagged != 1 || len(rep.Events) != 3 {
		t.Fatalf("expected 2 blocked, 1 flagged, 3 events (404 skipped), got %+v", rep)
	}
	if rep.Events[0].Time != "2026/10/09 12:00:00" {
		t.Fatalf("expected newest event first, got %s", rep.Events[0].Time)
	}
	if len(rep.Rules) == 0 || rep.Rules[0].ID != "942100" || rep.Rules[0].Blocked != 2 || len(rep.Rules[0].Domains) != 2 {
		t.Fatalf("expected 942100 on top with 2 blocks across 2 domains, got %+v", rep.Rules)
	}
	for _, r := range rep.Rules {
		if r.ID == "949110" {
			t.Fatal("expected scoring rule left out of the reasons")
		}
	}
	if len(rep.Domains) != 2 || rep.Domains[0].Owner != "alice" {
		t.Fatalf("expected both domains with owner, got %+v", rep.Domains)
	}

	_, html := wafGetBody(t, client, srv.URL+"/security/waf/logs/example.com")
	for _, want := range []string{"SQL injection", "/wp-login.php", "Disable for domain", "Clear logs", "</html>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected domain logs page to contain %q, got %s", want, truncate(html))
		}
	}
	_, userPage := wafGetBody(t, client, srv.URL+"/security/waf/logs/alice")
	if !strings.Contains(userPage, `<option value="alice" selected>alice</option>`) || !strings.Contains(userPage, "</html>") {
		t.Fatalf("expected user logs page, got %s", truncate(userPage))
	}
}

func TestServeWAFLogsPurgeDomain(t *testing.T) {
	_, logDir, _ := withWAFScratch(t)
	logPath := filepath.Join(logDir, "example.com.log")
	os.WriteFile(logPath, []byte(wafTestLogBlocked+"\n"), 0644)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf/logs/example.com", url.Values{"action": {"purge"}})
	if !strings.Contains(body, "WAF logs cleared for example.com.") {
		t.Fatalf("expected success flash, got %s", truncate(body))
	}
	if info, err := os.Stat(logPath); err != nil || info.Size() != 0 {
		t.Fatalf("expected log truncated in place, got %v %v", info, err)
	}
}

func TestServeWAFStatusOverviewShowsProfilesAndHistory(t *testing.T) {
	_, logDir, _ := withWAFScratch(t)
	os.WriteFile(filepath.Join(logDir, "example.com.log"), []byte(wafTestLogBlocked+"\n"), 0644)
	pluginFile := filepath.Join(wafPluginDir("wordpress"), "plugins", "wordpress-rule-exclusions-before.conf")
	os.MkdirAll(filepath.Dir(pluginFile), 0755)
	os.WriteFile(pluginFile, nil, 0644)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	_, body := wafGetBody(t, client, srv.URL+"/security/waf")
	for _, want := range []string{"Installed version: <code class=\"text-xs\">abc123</code>", "WordPress", "Re-download", "Download", "example.com", "Check for updates", "</html>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected overview to contain %q, got %s", want, truncate(body))
		}
	}
	if strings.Contains(body, "standardize lowercase") {
		t.Fatal("expected no commit list when up to date")
	}
}

func TestServeWAFStatusUpdateRunsOpenCLI(t *testing.T) {
	withWAFScratch(t)
	orig := wafOpenCLIOutput
	var got []string
	wafOpenCLIOutput = func(args ...string) (string, error) { got = args; return "Update successful.", nil }
	t.Cleanup(func() { wafOpenCLIOutput = orig })
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	body := wafPost(t, client, srv.URL+"/security/waf", url.Values{"action": {"update"}})
	if strings.Join(got, " ") != "waf update" || !strings.Contains(body, "OWASP CRS and app profiles updated.") {
		t.Fatalf("expected opencli waf update and success flash, got %v %s", got, truncate(body))
	}
}

func TestWAFRedownloadProfileRestoresOnFailure(t *testing.T) {
	withWAFScratch(t)
	pluginFile := filepath.Join(wafPluginDir("wordpress"), "plugins", "wordpress-rule-exclusions-before.conf")
	os.MkdirAll(filepath.Dir(pluginFile), 0755)
	os.WriteFile(pluginFile, []byte("old"), 0644)
	orig := wafOpenCLIOutput
	wafOpenCLIOutput = func(args ...string) (string, error) {
		return "- Failed to download plugin wordpress-rule-exclusions", os.ErrDeadlineExceeded
	}
	t.Cleanup(func() { wafOpenCLIOutput = orig })

	if _, err := wafRedownloadProfile("wordpress"); err == nil {
		t.Fatal("expected an error")
	}
	if content, err := os.ReadFile(pluginFile); err != nil || string(content) != "old" {
		t.Fatalf("expected the old plugin put back, got %q %v", content, err)
	}
}

func TestServeWAFStatusShowsUpdateOnlyWhenBehind(t *testing.T) {
	withWAFScratch(t)
	os.MkdirAll(filepath.Join(WAFCRSDir, ".git"), 0755)
	var fetched []string
	wafGitRun = func(dir string, args ...string) (string, error) {
		if args[0] == "fetch" {
			fetched = append(fetched, dir)
			return "", nil
		}
		if args[0] == "log" {
			return "def456|2026-10-09|feat: new rule for CVE-2026-1234", nil
		}
		return "3", nil
	}
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	_, body := wafGetBody(t, client, srv.URL+"/security/waf")
	if !strings.Contains(body, "3 new changes available") || !strings.Contains(body, "Update rules") || strings.Contains(body, "Check for updates") || !strings.Contains(body, "new rule for CVE-2026-1234") {
		t.Fatalf("expected update button when behind, got %s", truncate(body))
	}

	wafGitRun = func(dir string, args ...string) (string, error) {
		if args[0] == "fetch" {
			fetched = append(fetched, dir)
		}
		return "0", nil
	}
	fetched = nil
	resp := wafPost(t, client, srv.URL+"/security/waf", url.Values{"action": {"check_updates"}})
	if len(fetched) == 0 || !strings.Contains(resp, "WAF rules are up to date.") {
		t.Fatalf("expected a forced fetch and up-to-date flash, fetched=%v got %s", fetched, truncate(resp))
	}
	if strings.Contains(resp, ">Update rules<") || !strings.Contains(resp, "Check for updates") || strings.Contains(resp, "Coming in this update") {
		t.Fatalf("expected no update button when up to date, got %s", truncate(resp))
	}
}

func TestServeWAFDomainsAndLogsSort(t *testing.T) {
	_, logDir, _ := withWAFScratch(t)
	os.WriteFile(filepath.Join(logDir, "example.com.log"), []byte(wafTestLogBlocked+"\n"+wafTestLogFlagged+"\n"), 0644)
	os.WriteFile(filepath.Join(logDir, "shop.example.org.log"), []byte(wafTestLogBlocked+"\n"), 0644)
	wf := &WAF{}
	srv, client := newWAFTestServer(t, wf)

	_, page := wafGetBody(t, client, srv.URL+"/security/waf/domains?sort=domain&direction=desc")
	// both test domains have no level line, so standard: 2 of 3 dots filled each
	if n := strings.Count(page, "size-1.5 rounded-full bg-indigo-600"); n != 4 {
		t.Fatalf("expected 4 filled level dots, got %d", n)
	}
	if strings.Index(page, ">shop.example.org<") > strings.Index(page, ">example.com<") {
		t.Fatalf("expected shop.example.org first when sorted desc, got %s", truncate(page))
	}

	_, logs := wafGetBody(t, client, srv.URL+"/security/waf/logs?r_sort=hits&r_direction=asc&d_sort=domain&d_direction=desc")
	rules := logs[strings.Index(logs, `id="waf-rules"`):]
	if strings.Index(rules, ">913100<") > strings.Index(rules, ">942100<") {
		t.Fatal("expected the rule with fewer hits first when sorted by hits asc")
	}
	domains := logs[strings.Index(logs, `id="waf-domains"`):strings.Index(logs, `id="waf-rules"`)]
	if strings.Index(domains, ">shop.example.org<") > strings.Index(domains, ">example.com<") {
		t.Fatal("expected the domains table sorted by its own params")
	}
	if !strings.Contains(logs, "/security/waf/logs?r_sort=hits&r_direction=desc#waf-rules") {
		t.Fatalf("expected sort links to keep their table params and anchor, got %s", truncate(rules))
	}
}
