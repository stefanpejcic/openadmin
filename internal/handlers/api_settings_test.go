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

func newAPISettingsTestServer(t *testing.T) (*httptest.Server, *http.Client) {
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
	h := &APISettings{Sessions: sessions}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/api", h.ServeAPISettings)
	mux.HandleFunc("POST /settings/api", h.ServeAPISettings)
	mux.HandleFunc("GET /settings/api/endpoints", h.ServeAPIEndpointsList)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(RecoverMiddleware(mux))
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

func withScratchAPISettingsConfig(t *testing.T, panelAPI, basicAuth string) {
	t.Helper()
	dir := t.TempDir()
	origPath := config.OpenpanelConfigPath
	path := filepath.Join(dir, "openpanel.config")
	content := "[PANEL]\napi=" + panelAPI + "\nbasic_auth=" + basicAuth + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	config.OpenpanelConfigPath = path
	t.Cleanup(func() { config.OpenpanelConfigPath = origPath })
}

func TestServeAPISettingsGetRendersEnabledForm(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "no")
	srv, client := newAPISettingsTestServer(t)

	resp, err := client.Get(srv.URL + "/settings/api")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Disable API access") {
		t.Fatalf("expected the enabled-state form, got %s", truncate(string(body)))
	}
}

func TestServeAPISettingsGetRendersDisabledPanel(t *testing.T) {
	withScratchAPISettingsConfig(t, "off", "no")
	srv, client := newAPISettingsTestServer(t)

	resp, err := client.Get(srv.URL + "/settings/api")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Enable API access") {
		t.Fatalf("expected the disabled-state panel, got %s", truncate(string(body)))
	}
}

func TestServeAPISettingsGetRendersBasicAuthPanelWhenBasicAuthOn(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "yes")
	srv, client := newAPISettingsTestServer(t)

	resp, err := client.Get(srv.URL + "/settings/api")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "API Access is Disabled because BasicAuth is enabled") {
		t.Fatalf("expected the basic-auth-conflict panel, got %s", truncate(string(body)))
	}
}

func TestServeAPISettingsPostInvalidActionReturns400(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "no")
	srv, client := newAPISettingsTestServer(t)

	resp, err := client.PostForm(srv.URL+"/settings/api", url.Values{"action": {"toggle"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

// TestServeAPISettingsPostFallsBackToFlashWhenOpenCLIMissing exercises the
// real opencli exec path. There's no opencli binary in the test sandbox,
// so this deterministically hits the "Error executing opencli" flash
// branch and re-renders the page rather than redirecting.
func TestServeAPISettingsPostFallsBackToFlashWhenOpenCLIMissing(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "no")
	srv, client := newAPISettingsTestServer(t)

	resp, err := client.PostForm(srv.URL+"/settings/api", url.Values{"action": {"off"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered page after the opencli failure), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Error executing opencli") {
		t.Fatalf("expected the opencli-error flash, got %s", truncate(string(body)))
	}
}

func TestServeAPISettingsViewLogParam(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "no")
	srv, client := newAPISettingsTestServer(t)

	dir := t.TempDir()
	origLog := APILogPath
	APILogPath = filepath.Join(dir, "api.log")
	os.WriteFile(APILogPath, []byte("log line one\nlog line two\n"), 0644)
	t.Cleanup(func() { APILogPath = origLog })

	resp, err := client.Get(srv.URL + "/settings/api?view=api_log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "log line one") {
		t.Fatalf("expected log contents, got %s", truncate(string(body)))
	}
}

func TestServeAPISettingsViewLogParamMissingFileReturns500(t *testing.T) {
	withScratchAPISettingsConfig(t, "on", "no")
	srv, client := newAPISettingsTestServer(t)

	dir := t.TempDir()
	origLog := APILogPath
	APILogPath = filepath.Join(dir, "does-not-exist.log")
	t.Cleanup(func() { APILogPath = origLog })

	resp, err := client.Get(srv.URL + "/settings/api?view=api_log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Unable to read the api log file") {
		t.Fatalf("expected the read-error message, got %s", truncate(string(body)))
	}
}

func TestServeAPIEndpointsListReturnsErrorWhenOpenCLIMissing(t *testing.T) {
	h := &APISettings{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/api/endpoints", h.ServeAPIEndpointsList)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/settings/api/endpoints")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (opencli not present in test sandbox), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Error executing opencli api list") {
		t.Fatalf("expected the opencli-error body, got %s", truncate(string(body)))
	}
}
