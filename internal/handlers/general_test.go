package handlers

import (
	"errors"
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

func withScratchGeneralGetters(t *testing.T, port, adminPort, domain, proxy string) {
	t.Helper()
	origPort, origAdminPort, origDomain, origProxy, origHostname := generalOpenpanelPortRun, generalOpenadminPortRun, generalAdminDomainRun, generalOpenpanelProxyRun, generalHostnameRun
	generalOpenpanelPortRun = func() (string, error) { return port, nil }
	generalOpenadminPortRun = func() (string, error) { return adminPort, nil }
	generalAdminDomainRun = func() (string, error) { return domain, nil }
	generalOpenpanelProxyRun = func() (string, error) { return proxy, nil }
	generalHostnameRun = func() (string, error) { return "fallback-host", nil }
	t.Cleanup(func() {
		generalOpenpanelPortRun, generalOpenadminPortRun, generalAdminDomainRun, generalOpenpanelProxyRun, generalHostnameRun = origPort, origAdminPort, origDomain, origProxy, origHostname
	})
}

type generalSetterCalls struct {
	port, domain, devMode, adminPort, proxy []string
}

func withScratchGeneralSetters(t *testing.T) *generalSetterCalls {
	t.Helper()
	calls := &generalSetterCalls{}
	origPort, origDomain, origDevMode, origAdminPort, origProxy := generalSetOpenpanelPortRun, generalSetDomainRun, generalSetDevModeRun, generalSetAdminPortRun, generalSetProxyRun
	generalSetOpenpanelPortRun = func(v string) { calls.port = append(calls.port, v) }
	generalSetDomainRun = func(v string) { calls.domain = append(calls.domain, v) }
	generalSetDevModeRun = func(v string) { calls.devMode = append(calls.devMode, v) }
	generalSetAdminPortRun = func(v string) { calls.adminPort = append(calls.adminPort, v) }
	generalSetProxyRun = func(v string) { calls.proxy = append(calls.proxy, v) }
	t.Cleanup(func() {
		generalSetOpenpanelPortRun, generalSetDomainRun, generalSetDevModeRun, generalSetAdminPortRun, generalSetProxyRun = origPort, origDomain, origDevMode, origAdminPort, origProxy
	})
	return calls
}

func withScratchGeneralRestartFlags(t *testing.T) (openpanelFlag, openadminFlag string) {
	t.Helper()
	dir := t.TempDir()
	origOpenpanel, origOpenadmin := GeneralOpenpanelRestartFlagPath, GeneralOpenadminRestartFlagPath
	GeneralOpenpanelRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	GeneralOpenadminRestartFlagPath = filepath.Join(dir, "openadmin_restart_needed")
	t.Cleanup(func() {
		GeneralOpenpanelRestartFlagPath = origOpenpanel
		GeneralOpenadminRestartFlagPath = origOpenadmin
	})
	return GeneralOpenpanelRestartFlagPath, GeneralOpenadminRestartFlagPath
}

func newGeneralTestServer(t *testing.T, g *General) (*httptest.Server, *http.Client) {
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
	g.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/general", g.ServeGeneral)
	mux.HandleFunc("POST /settings/general", g.ServeGeneral)
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

func TestGeneralValidatedPort(t *testing.T) {
	if got := generalValidatedPort("8080", nil, "2083"); got != "8080" {
		t.Fatalf("expected valid port passed through, got %q", got)
	}
	if got := generalValidatedPort("", errors.New("boom"), "2083"); got != "2083" {
		t.Fatalf("expected fallback on error, got %q", got)
	}
	if got := generalValidatedPort("not-a-port", nil, "2083"); got != "2083" {
		t.Fatalf("expected fallback on non-numeric, got %q", got)
	}
	if got := generalValidatedPort("99999999", nil, "2083"); got != "2083" {
		t.Fatalf("expected fallback on out-of-range (regex-rejected) value, got %q", got)
	}
}

func TestServeGeneralGetRendersCurrentValues(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "srv.example.net", "openpanel")

	g := &General{}
	srv, client := newGeneralTestServer(t, g)

	resp, err := client.Get(srv.URL + "/settings/general")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, "srv.example.net") {
		t.Fatalf("expected domain rendered, got %s", truncate(got))
	}
	if !strings.Contains(got, `value="2087"`) || !strings.Contains(got, `value="2083"`) {
		t.Fatalf("expected both ports rendered, got %s", truncate(got))
	}
}

func TestServeGeneralGetJSON(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "srv.example.net", "openpanel")

	g := &General{DevMode: true}
	srv, client := newGeneralTestServer(t, g)

	resp, err := client.Get(srv.URL + "/settings/general?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"DevMode":"on"`) {
		t.Fatalf("expected dev_mode 'on' string (not boolean) in JSON, got %s", body)
	}
}

func TestServeGeneralPostNoChanges(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "srv.example.net", "openpanel")
	calls := withScratchGeneralSetters(t)
	openpanelFlag, openadminFlag := withScratchGeneralRestartFlags(t)

	g := &General{DevMode: false}
	srv, client := newGeneralTestServer(t, g)

	resp, err := client.PostForm(srv.URL+"/settings/general", url.Values{
		"force_domain": {"srv.example.net"}, "2087_port": {"2087"}, "2083_port": {"2083"},
		"openpanel_proxy": {"openpanel"}, "dev_mode": {"off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "No changes made.") {
		t.Fatalf("expected 'No changes made.' flash, got %s", truncate(string(body)))
	}
	if len(calls.port) != 0 || len(calls.domain) != 0 || len(calls.devMode) != 0 || len(calls.adminPort) != 0 {
		t.Fatalf("expected no mutating opencli calls, got %+v", calls)
	}
	// Fixed from the original: the proxy branch now only runs when the
	// value actually changed, so submitting the already-current "openpanel"
	// value makes no call at all.
	if len(calls.proxy) != 0 {
		t.Fatalf("expected no proxy-set call when the value didn't change, got %+v", calls.proxy)
	}
	if _, err := os.Stat(openpanelFlag); !os.IsNotExist(err) {
		t.Fatalf("expected no openpanel restart flag when nothing panel-related changed, err=%v", err)
	}
	// Fixed from the original (which unconditionally forced this flag on
	// every POST): with nothing actually changed, no restart is needed.
	if _, err := os.Stat(openadminFlag); !os.IsNotExist(err) {
		t.Fatalf("expected no openadmin restart flag when nothing changed, err=%v", err)
	}
}

func TestServeGeneralPostChangesEverything(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "old.example.net", "openpanel")
	calls := withScratchGeneralSetters(t)
	openpanelFlag, openadminFlag := withScratchGeneralRestartFlags(t)

	g := &General{DevMode: false}
	srv, client := newGeneralTestServer(t, g)

	resp, err := client.PostForm(srv.URL+"/settings/general", url.Values{
		"force_domain": {"new.example.net"}, "2087_port": {"9087"}, "2083_port": {"9083"},
		"openpanel_proxy": {"myproxy"}, "dev_mode": {"on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"user-panel port", "domain", "dev_mode", "admin-panel port", "proxy"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in the changes summary, got %s", want, truncate(got))
		}
	}

	if len(calls.port) != 1 || calls.port[0] != "9083" {
		t.Fatalf("expected openpanel port set to 9083, got %+v", calls.port)
	}
	if len(calls.domain) != 1 || calls.domain[0] != "new.example.net" {
		t.Fatalf("expected domain set, got %+v", calls.domain)
	}
	if len(calls.devMode) != 1 || calls.devMode[0] != "on" {
		t.Fatalf("expected dev_mode set to on, got %+v", calls.devMode)
	}
	if len(calls.adminPort) != 1 || calls.adminPort[0] != "9087" {
		t.Fatalf("expected admin port set to 9087, got %+v", calls.adminPort)
	}
	if len(calls.proxy) != 1 || calls.proxy[0] != "myproxy" {
		t.Fatalf("expected proxy set to myproxy, got %+v", calls.proxy)
	}

	if _, err := os.Stat(openpanelFlag); err != nil {
		t.Fatalf("expected openpanel restart flag written, err=%v", err)
	}
	if _, err := os.Stat(openadminFlag); err != nil {
		t.Fatalf("expected openadmin restart flag written, err=%v", err)
	}
}

func TestServeGeneralPostProxyResetToDefaultIsDetectedAndReported(t *testing.T) {
	// Fixed from the original: resetting a custom proxy back to the
	// default is a real change and must now both run the command AND be
	// reported in the changes summary (previously it ran the command
	// unconditionally but never mentioned it).
	withScratchGeneralGetters(t, "2083", "2087", "srv.example.net", "custom-proxy")
	calls := withScratchGeneralSetters(t)
	openpanelFlag, openadminFlag := withScratchGeneralRestartFlags(t)
	_ = openpanelFlag

	g := &General{}
	srv, client := newGeneralTestServer(t, g)

	resp, err := client.PostForm(srv.URL+"/settings/general", url.Values{
		"force_domain": {"srv.example.net"}, "2087_port": {"2087"}, "2083_port": {"2083"},
		"openpanel_proxy": {"openpanel"}, "dev_mode": {"off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Settings updated: proxy") {
		t.Fatalf("expected 'proxy' to be reported in the changes summary, got %s", truncate(string(body)))
	}
	if len(calls.proxy) != 1 || calls.proxy[0] != "openpanel" {
		t.Fatalf("expected the reset command to run, got %+v", calls.proxy)
	}
	if _, err := os.Stat(openadminFlag); err != nil {
		t.Fatalf("expected the openadmin restart flag written since the proxy really changed, err=%v", err)
	}
}
