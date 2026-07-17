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

func withScratchLocalesPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origDir, origFile := TranslationsDir, DefaultLocaleFilePath
	TranslationsDir = filepath.Join(dir, "translations")
	DefaultLocaleFilePath = filepath.Join(dir, "conf", "default_locale")
	os.MkdirAll(TranslationsDir, 0755)
	t.Cleanup(func() {
		TranslationsDir = origDir
		DefaultLocaleFilePath = origFile
	})
}

func withScratchLocalesFetch(t *testing.T, items []githubContentItem, status int, err error) {
	t.Helper()
	orig := localesFetchFolders
	localesFetchFolders = func() ([]githubContentItem, int, error) { return items, status, err }
	t.Cleanup(func() { localesFetchFolders = orig })
}

func newLocalesTestServer(t *testing.T, l *Locales) (*httptest.Server, *http.Client) {
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
	l.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/locales", l.ServeLocales)
	mux.HandleFunc("POST /settings/locales", l.ServeLocales)
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

func postJSON(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestServeLocalesGetFetchFailureReturns500(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, nil, 503, nil)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/locales")
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

func TestServeLocalesGetRendersInstalledAndDefault(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "en"), 0755)
	os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
	os.WriteFile(DefaultLocaleFilePath, []byte("en"), 0644)
	withScratchLocalesFetch(t, []githubContentItem{
		{Name: "en-us", Type: "dir"},
		{Name: "fr-fr", Type: "dir"},
		{Name: "README.md", Type: "file"},
	}, 200, nil)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, "en-us") || !strings.Contains(got, "fr-fr") {
		t.Fatalf("expected both dir locales listed, got %s", truncate(got))
	}
	if strings.Contains(got, "README.md") {
		t.Fatalf("expected non-dir entries excluded, got %s", truncate(got))
	}
}

func TestServeLocalesGetJSON(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "en"), 0755)
	withScratchLocalesFetch(t, []githubContentItem{{Name: "en-us", Type: "dir"}}, 200, nil)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/locales?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"default_locale":"en"`) {
		t.Fatalf("expected default_locale 'en' fallback, got %s", body)
	}
	if !strings.Contains(string(body), `"provider":"OpenPanel"`) {
		t.Fatalf("expected en-us to be attributed to OpenPanel, got %s", body)
	}
}

func TestServeLocalesPostInstallFormSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, nil, 200, nil)

	var installed string
	origRun := localesInstallRun
	localesInstallRun = func(locale string) error { installed = locale; return nil }
	t.Cleanup(func() { localesInstallRun = origRun })

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/settings/locales", url.Values{"locale": {"fr-fr"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if installed != "fr-fr" {
		t.Fatalf("expected localesInstallRun called with fr-fr, got %q", installed)
	}
	if !strings.Contains(string(body), "installed successfully") {
		t.Fatalf("expected success flash after redirect, got %s", truncate(string(body)))
	}
}

func TestServeLocalesPostInstallJSONSuccess(t *testing.T) {
	withScratchLocalesPaths(t)

	origRun := localesInstallRun
	localesInstallRun = func(locale string) error { return nil }
	t.Cleanup(func() { localesInstallRun = origRun })

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"locale": "de-de"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "de-de") {
		t.Fatalf("expected JSON success message, got %s", body)
	}
}

func TestServeLocalesPostInstallInvalidFormat(t *testing.T) {
	withScratchLocalesPaths(t)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"locale": "not_a_locale"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid format, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeLocalesPostInstallFailureJSON(t *testing.T) {
	withScratchLocalesPaths(t)

	origRun := localesInstallRun
	localesInstallRun = func(locale string) error { return &ftpStubError{"opencli failed"} }
	t.Cleanup(func() { localesInstallRun = origRun })

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"locale": "de-de"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "opencli failed") {
		t.Fatalf("expected error detail in JSON, got %s", body)
	}
}

func TestServeLocalesPostSetDefaultSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "de"), 0755)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"default": "de-de"}`)
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

func TestServeLocalesPostSetDefaultNotInstalled(t *testing.T) {
	withScratchLocalesPaths(t) // "es" dir never created

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"default": "es-es"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when locale isn't installed locally, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not installed") {
		t.Fatalf("expected not-installed message, got %s", body)
	}
}

func TestServeLocalesGetRendersHTML(t *testing.T) {
	withScratchLocalesPaths(t)
	os.MkdirAll(filepath.Join(TranslationsDir, "en"), 0755)
	os.MkdirAll(filepath.Join(TranslationsDir, "fr"), 0755)
	os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
	os.WriteFile(DefaultLocaleFilePath, []byte("en"), 0644)
	withScratchLocalesFetch(t, []githubContentItem{
		{Name: "en-us", Type: "dir"},
		{Name: "fr-fr", Type: "dir"},
	}, 200, nil)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{
		"Languages (Locales)",
		"en-us",
		"fr-fr",
		"Set as Default",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeLocalesPostMissingParamsReturns400JSON(t *testing.T) {
	withScratchLocalesPaths(t)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.PostForm(srv.URL+"/settings/locales", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a POST with neither locale nor default, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Missing 'locale' or 'default' parameter.") {
		t.Fatalf("expected the missing-params JSON error even for a non-JSON request, got %s", body)
	}
}
