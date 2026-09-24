package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	origFlush := localesFlushCacheRun
	localesFlushCacheRun = func() {}
	t.Cleanup(func() {
		TranslationsDir = origDir
		DefaultLocaleFilePath = origFile
		localesFlushCacheRun = origFlush
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
	if !strings.Contains(string(body), "Missing 'locale', 'update', 'delete' or 'default' parameter.") {
		t.Fatalf("expected the missing-params JSON error even for a non-JSON request, got %s", body)
	}
}

// fakes opencli by writing messages.po for every locale except the ones in skip
func withScratchLocalesInstallAll(t *testing.T, skip ...string) *[]string {
	t.Helper()
	var got []string
	orig := localesInstallAllRun
	localesInstallAllRun = func(locales []string) error {
		got = locales
		for _, l := range locales {
			if slices.Contains(skip, l) {
				continue
			}
			dir := filepath.Join(TranslationsDir, strings.SplitN(l, "-", 2)[0], "LC_MESSAGES")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, "messages.po"), []byte("x"), 0644)
		}
		return nil
	}
	t.Cleanup(func() { localesInstallAllRun = orig })
	return &got
}

var installAllItems = []githubContentItem{
	{Name: ".github", Type: "dir"},
	{Name: "scripts", Type: "dir"},
	{Name: "install.sh", Type: "file"},
	{Name: "de-de", Type: "dir"},
	{Name: "sr-rs", Type: "dir"},
}

func TestServeLocalesPostInstallAllFormSuccess(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, installAllItems, 200, nil)
	got := withScratchLocalesInstallAll(t)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/settings/locales", url.Values{"locale": {"all"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Join(*got, ",") != "de-de,sr-rs" {
		t.Fatalf("expected only cc-cc dirs passed to opencli, got %v", *got)
	}
	if !strings.Contains(string(body), "Installed 2 locales: de-de, sr-rs.") {
		t.Fatalf("expected install-all flash, got %s", truncate(string(body)))
	}
}

func TestServeLocalesPostInstallAllPartialFailureJSON(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, installAllItems, 200, nil)
	withScratchLocalesInstallAll(t, "sr-rs")

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"locale": "all"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 on partial failure, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed: sr-rs.") {
		t.Fatalf("expected failed locale in message, got %s", body)
	}
}

func writeLocalePo(t *testing.T, locale, content string) {
	t.Helper()
	dir := filepath.Join(TranslationsDir, strings.SplitN(locale, "-", 2)[0], "LC_MESSAGES")
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(filepath.Join(dir, "messages.po"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func blobShaOf(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "po")
	os.WriteFile(p, []byte(content), 0644)
	return gitBlobSha(p)
}

func TestServeLocalesGetFlagsUpdates(t *testing.T) {
	withScratchLocalesPaths(t)
	writeLocalePo(t, "de-de", "same")
	writeLocalePo(t, "sr-rs", "old")
	withScratchLocalesFetch(t, []githubContentItem{
		{Name: "de-de", Type: "dir", Sha: blobShaOf(t, "same")},
		{Name: "sr-rs", Type: "dir", Sha: blobShaOf(t, "new")},
		{Name: "fr-fr", Type: "dir", Sha: blobShaOf(t, "new")},
	}, 200, nil)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp, err := client.Get(srv.URL + "/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Update available for sr-rs.") {
		t.Fatalf("expected update notice for sr-rs only, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), "Update All (1)") {
		t.Fatalf("expected Update All button, got %s", truncate(string(body)))
	}
}

func TestServeLocalesPostUpdateAllOnlyOutdated(t *testing.T) {
	withScratchLocalesPaths(t)
	writeLocalePo(t, "de-de", "same")
	writeLocalePo(t, "sr-rs", "old")
	withScratchLocalesFetch(t, []githubContentItem{
		{Name: "de-de", Type: "dir", Sha: blobShaOf(t, "same")},
		{Name: "sr-rs", Type: "dir", Sha: blobShaOf(t, "new")},
	}, 200, nil)
	var got []string
	orig := localesInstallAllRun
	localesInstallAllRun = func(locales []string) error {
		got = locales
		writeLocalePo(t, "sr-rs", "new")
		return nil
	}
	t.Cleanup(func() { localesInstallAllRun = orig })

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"update": "all"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Updated 1 locale: sr-rs.") {
		t.Fatalf("expected sr-rs updated, got %d: %s", resp.StatusCode, body)
	}
	if strings.Join(got, ",") != "sr-rs" {
		t.Fatalf("expected only outdated locales passed to opencli, got %v", got)
	}
}

func TestServeLocalesPostUpdateStillOutdatedFails(t *testing.T) {
	withScratchLocalesPaths(t)
	writeLocalePo(t, "sr-rs", "old")
	withScratchLocalesFetch(t, []githubContentItem{{Name: "sr-rs", Type: "dir", Sha: blobShaOf(t, "new")}}, 200, nil)
	withScratchLocalesInstallAll(t, "sr-rs")

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"update": "sr-rs"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "Failed: sr-rs.") {
		t.Fatalf("expected failure when file still differs, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeLocalesPostDelete(t *testing.T) {
	withScratchLocalesPaths(t)
	writeLocalePo(t, "sr-rs", "x")

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"delete": "sr-rs"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(TranslationsDir, "sr")); !os.IsNotExist(err) {
		t.Fatalf("expected sr dir removed, stat err: %v", err)
	}
}

func TestServeLocalesPostDeleteDefaultRefused(t *testing.T) {
	withScratchLocalesPaths(t)
	writeLocalePo(t, "sr-rs", "x")
	os.MkdirAll(filepath.Dir(DefaultLocaleFilePath), 0755)
	os.WriteFile(DefaultLocaleFilePath, []byte("sr"), 0644)

	l := &Locales{}
	srv, client := newLocalesTestServer(t, l)

	resp := postJSON(t, client, srv.URL+"/settings/locales", `{"delete": "sr-rs"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting the default, got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(TranslationsDir, "sr")); err != nil {
		t.Fatalf("default locale dir should still exist: %v", err)
	}
}
