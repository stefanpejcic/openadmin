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
)

func newAPISettingsLocalesTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsLocales{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/locales", a.Serve)
	mux.HandleFunc("POST /api/settings/locales", a.Serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsLocalesGetSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "en"), 0755)
	withScratchLocalesFetch(t, []githubContentItem{
		{Name: "en-us", Type: "dir"},
		{Name: "fr-fr", Type: "dir"},
		{Name: "README.md", Type: "file"},
	}, 200, nil)

	srv, client := newAPISettingsLocalesTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		DefaultLocale string      `json:"default_locale"`
		Translations  []localeRow `json:"translations"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("expected valid JSON: %v (%s)", err, body)
	}
	if out.DefaultLocale != "en" {
		t.Fatalf("expected default_locale 'en', got %q", out.DefaultLocale)
	}
	if len(out.Translations) != 2 {
		t.Fatalf("expected 2 dir entries (README.md excluded), got %+v", out.Translations)
	}
}

func TestAPISettingsLocalesGetFetchFailureReturns500(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, nil, 503, nil)

	srv, client := newAPISettingsLocalesTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "503") {
		t.Fatalf("expected github status reflected in error, got %s", body)
	}
}

func TestAPISettingsLocalesPostRequiresJSON(t *testing.T) {
	withScratchLocalesPaths(t)
	srv, client := newAPISettingsLocalesTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/locales", "application/x-www-form-urlencoded", strings.NewReader("locale=fr-fr"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-JSON body, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid JSON format") {
		t.Fatalf("expected 'Invalid JSON format', got %s", body)
	}
}

func TestAPISettingsLocalesPostInstallSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	var installed string
	origRun := localesInstallRun
	localesInstallRun = func(locale string) error { installed = locale; return nil }
	t.Cleanup(func() { localesInstallRun = origRun })

	srv, client := newAPISettingsLocalesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/locales", `{"locale": "de-de"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if installed != "de-de" {
		t.Fatalf("expected localesInstallRun called with de-de, got %q", installed)
	}
}

func TestAPISettingsLocalesPostInstallInvalidFormat(t *testing.T) {
	withScratchLocalesPaths(t)
	srv, client := newAPISettingsLocalesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/locales", `{"locale": "not_a_locale"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsLocalesPostSetDefaultSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "de"), 0755)

	srv, client := newAPISettingsLocalesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/locales", `{"default": "de-de"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	saved, err := os.ReadFile(DefaultLocaleFilePath)
	if err != nil {
		t.Fatalf("expected default_locale file written: %v", err)
	}
	if string(saved) != "de" {
		t.Fatalf("expected base locale 'de' saved, got %q", saved)
	}
}

func TestAPISettingsLocalesPostSetDefaultNotInstalled(t *testing.T) {
	withScratchLocalesPaths(t)
	srv, client := newAPISettingsLocalesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/locales", `{"default": "es-es"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not installed") {
		t.Fatalf("expected not-installed message, got %s", body)
	}
}

func TestAPISettingsLocalesPostMissingParams(t *testing.T) {
	withScratchLocalesPaths(t)
	srv, client := newAPISettingsLocalesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/locales", `{}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Missing 'locale' or 'default' parameter.") {
		t.Fatalf("expected missing-params message, got %s", body)
	}
}
