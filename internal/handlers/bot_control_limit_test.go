package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

const botConfFixture = `(ai_crawler_matcher) {
    @ai_crawlers {
        header_regexp ai_ua User-Agent (?i)(GPTBot|ClaudeBot)
    }
}

(botcontrol_ratelimit) {
    import ai_crawler_matcher
    handle @ai_crawlers {
        rate_limit {
            zone botcontrol_{args[0]} {
                key {http.request.header.User-Agent}
                events 10
                window 1m
            }
        }
    }
}
`

// botTestEnv points every path the bot control code touches at a temp dir and fakes caddy
func botTestEnv(t *testing.T) (reloads *int) {
	t.Helper()
	tmp := t.TempDir()
	origConfs, origReload, origRestart, origMod, origConf, origLogs := CaddyDomainsConfDir, botReloadCaddy, botRestartOpenPanel, ModulesConfigFilePath, BotControlConfPath, CaddyDomlogsDir
	t.Cleanup(func() {
		CaddyDomainsConfDir, botReloadCaddy, botRestartOpenPanel, ModulesConfigFilePath, BotControlConfPath, CaddyDomlogsDir = origConfs, origReload, origRestart, origMod, origConf, origLogs
	})
	CaddyDomainsConfDir = filepath.Join(tmp, "domains")
	ModulesConfigFilePath = filepath.Join(tmp, "openpanel.config")
	BotControlConfPath = filepath.Join(tmp, "botcontrol.conf")
	CaddyDomlogsDir = filepath.Join(tmp, "domlogs")
	n := 0
	botReloadCaddy = func(context.Context) error { n++; return nil }
	botRestartOpenPanel = func(context.Context) error { return nil }
	_ = os.MkdirAll(CaddyDomainsConfDir, 0o755)
	_ = os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="domains,bot_control"`+"\n"), 0o644)
	_ = os.WriteFile(BotControlConfPath, []byte(botConfFixture), 0o644)
	_ = os.WriteFile(filepath.Join(CaddyDomainsConfDir, "example.com.conf"), []byte("example.com {\n    # botcontrol: allow\n    reverse_proxy 127.0.0.1:8080\n}\n"), 0o644)
	return &n
}

func TestBotRateLimitReadsAndWritesTheSnippet(t *testing.T) {
	reloads := botTestEnv(t)
	if got := botRateLimit(); got != 10 {
		t.Fatalf("default limit %d, want 10", got)
	}
	if err := botSetRateLimit(context.Background(), 45); err != nil {
		t.Fatal(err)
	}
	if got := botRateLimit(); got != 45 || *reloads != 1 {
		t.Fatalf("limit %d after save with %d reloads, want 45 and 1", got, *reloads)
	}
	b, _ := os.ReadFile(BotControlConfPath)
	if !strings.Contains(string(b), "events 45") || !strings.Contains(string(b), "window 1m") {
		t.Fatalf("snippet not rewritten in place:\n%s", b)
	}
	for _, bad := range []int{0, -3, botMaxRateLimit + 1} {
		if err := botSetRateLimit(context.Background(), bad); err == nil {
			t.Fatalf("limit %d accepted", bad)
		}
	}

	botReloadCaddy = func(context.Context) error { return errors.New("bad config") }
	if err := botSetRateLimit(context.Background(), 99); err == nil {
		t.Fatal("caddy rejecting the reload must fail the save")
	}
	if got := botRateLimit(); got != 45 {
		t.Fatalf("rejected change left %d in the file, want 45 back", got)
	}

	_ = os.Remove(BotControlConfPath)
	if got := botRateLimit(); got != 10 {
		t.Fatalf("missing file gives %d, want the default 10", got)
	}
}

func TestBotAPIResellerScope(t *testing.T) {
	botTestEnv(t)
	bc := &BotControl{Sessions: auth.NewManager("test-secret", false)}
	reseller := &admindb.User{Username: "res1", Role: "reseller"}

	post := func(u *admindb.User, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/security/bot-control", strings.NewReader(body))
		if u != nil {
			r = withAPIUser(r, u)
		}
		w := httptest.NewRecorder()
		bc.ServeAPI(w, r)
		return w
	}

	for _, body := range []string{`{"action":"set_rate_limit","rate_limit":50}`, `{"action":"disable_module"}`, `{"action":"enable_module"}`} {
		if w := post(reseller, body); w.Code != http.StatusForbidden {
			t.Fatalf("reseller %s got %d, want 403", body, w.Code)
		}
	}
	if botRateLimit() != 10 {
		t.Fatal("reseller changed the rate limit")
	}
	// the reseller has no users here, so admin's example.com is outside their scope
	if w := post(reseller, `{"action":"set_policy","domain":"example.com","policy":"block"}`); w.Code != http.StatusNotFound {
		t.Fatalf("reseller changed a domain outside their scope: %d %s", w.Code, w.Body)
	}
	if botReadDomainPolicy("example.com") != botPolicyAllow {
		t.Fatal("policy changed by a reseller outside their scope")
	}

	r := withAPIUser(httptest.NewRequest(http.MethodGet, "/api/security/bot-control", nil), reseller)
	w := httptest.NewRecorder()
	bc.ServeAPI(w, r)
	var state struct {
		Domains   []BotDomainRow `json:"domains"`
		RateLimit int            `json:"rate_limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Domains) != 0 || state.RateLimit != 10 {
		t.Fatalf("reseller sees %d domains and limit %d, want 0 and 10", len(state.Domains), state.RateLimit)
	}

	if w := post(nil, `{"action":"set_rate_limit","rate_limit":25}`); w.Code != http.StatusOK || botRateLimit() != 25 {
		t.Fatalf("admin limit change: %d %s", w.Code, w.Body)
	}
	if w := post(nil, `{"action":"set_policy","domain":"example.com","policy":"ratelimit"}`); w.Code != http.StatusOK || botReadDomainPolicy("example.com") != botPolicyRateLimit {
		t.Fatalf("admin policy change: %d %s", w.Code, w.Body)
	}
	if w := post(nil, `{"action":"set_policy","domain":"example.com","policy":"nope"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy got %d", w.Code)
	}
	if w := post(nil, `{"action":"bogus","policy":"block"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Unknown action") {
		t.Fatalf("unknown action got %d %s", w.Code, w.Body)
	}
	if w := post(nil, `{"action":"set_rate_limit","rate_limit":"lots"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric limit got %d", w.Code)
	}
}

func TestBotPageResellerCantChangeLimit(t *testing.T) {
	botTestEnv(t)
	bc := &BotControl{Sessions: auth.NewManager("test-secret", false)}
	r := httptest.NewRequest(http.MethodPost, "/security/bot-control", strings.NewReader("action=set_rate_limit&rate_limit=70"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withAPIUser(r, &admindb.User{Username: "res1", Role: "reseller"})
	w := httptest.NewRecorder()
	bc.ServeBotControl(w, r)
	if w.Code != http.StatusSeeOther || botRateLimit() != 10 {
		t.Fatalf("reseller form post: code %d, limit %d", w.Code, botRateLimit())
	}
}
