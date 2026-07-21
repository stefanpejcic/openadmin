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

func withScratchDefaultsPaths(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origEnv, origCompose, origAutostart, origTmp := DefaultsEnvPath, DefaultsComposeFilePath, DefaultsAutostartServicesPath, DefaultsTmpDir
	DefaultsEnvPath = filepath.Join(dir, ".env")
	DefaultsComposeFilePath = filepath.Join(dir, "docker-compose.yml")
	DefaultsAutostartServicesPath = filepath.Join(dir, "autostart.services")
	DefaultsTmpDir = filepath.Join(dir, "tmp_user_defaults")
	t.Cleanup(func() {
		DefaultsEnvPath, DefaultsComposeFilePath, DefaultsAutostartServicesPath, DefaultsTmpDir = origEnv, origCompose, origAutostart, origTmp
	})
	return dir
}

func newDefaultsTestServer(t *testing.T, d *Defaults) (*httptest.Server, *http.Client) {
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
	d.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/defaults", d.ServeDefaults)
	mux.HandleFunc("POST /settings/defaults", d.ServeDefaults)
	mux.HandleFunc("GET /settings/defaults/files", d.ServeDefaultsFiles)
	mux.HandleFunc("POST /settings/defaults/files", d.ServeDefaultsFiles)
	mux.HandleFunc("PUT /settings/defaults/files", d.ServeDefaultsFiles)
	mux.HandleFunc("DELETE /settings/defaults/files", d.ServeDefaultsFiles)
	mux.HandleFunc("GET /settings/defaults/files/{username}", d.ServeUserFiles)
	mux.HandleFunc("POST /settings/defaults/files/{username}", d.ServeUserFiles)
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

func doMethodDefaults(t *testing.T, client *http.Client, method, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestReadDefaultsEnvGroupsMissingFile(t *testing.T) {
	withScratchDefaultsPaths(t)
	if got := readDefaultsEnvGroups(); got != nil {
		t.Fatalf("expected nil for missing file, got %+v", got)
	}
}

func TestReadDefaultsEnvGroupsVarnishAndDefaults(t *testing.T) {
	withScratchDefaultsPaths(t)
	content := `WEB_SERVER="nginx"
DEFAULT_PHP_VERSION="8.2"
MYSQL_TYPE="mariadb"
#PROXY_HTTP_PORT="8080"
NGINX_CPU="1.5"
PHP_FPM_8_2_CPU="1"
PHP_FPM_8_2_RAM="512M"
`
	os.WriteFile(DefaultsEnvPath, []byte(content), 0644)

	groups := readDefaultsEnvGroups()
	if groups["DEFAULTS"]["WEB_SERVER"] != "nginx" {
		t.Fatalf("expected WEB_SERVER=nginx, got %+v", groups["DEFAULTS"])
	}
	if groups["DEFAULTS"]["PHP_VERSION"] != "8.2" {
		t.Fatalf("expected PHP_VERSION=8.2, got %+v", groups["DEFAULTS"])
	}
	// A commented-out PROXY_HTTP_PORT line is filtered out by the earlier
	// "skip comments" gate before the VARNISH-detection code ever runs --
	// that "even if commented" branch is unreachable dead code, so no
	// VARNISH key is set at all here.
	if _, ok := groups["DEFAULTS"]["VARNISH"]; ok {
		t.Fatalf("expected no VARNISH key for a fully commented-out line, got %+v", groups["DEFAULTS"]["VARNISH"])
	}
	nginx, ok := groups["NGINX"]["CPU"]
	if !ok || nginx != "1.5" {
		t.Fatalf("expected NGINX.CPU=1.5, got %+v", groups["NGINX"])
	}
	fpm, ok := groups["PHP_FPM"]["8.2"].(map[string]interface{})
	if !ok || fpm["CPU"] != "1" || fpm["RAM"] != "512M" {
		t.Fatalf("expected PHP_FPM 8.2 group, got %+v", groups["PHP_FPM"])
	}
}

func TestReadDefaultsEnvGroupsVarnishEnabled(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte(`PROXY_HTTP_PORT="8080"`+"\n"), 0644)
	groups := readDefaultsEnvGroups()
	if v, ok := groups["DEFAULTS"]["VARNISH"].(bool); !ok || v != true {
		t.Fatalf("expected VARNISH=true for uncommented PROXY_HTTP_PORT, got %+v", groups["DEFAULTS"]["VARNISH"])
	}
}

func TestNormalizeRAM(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"0":      "0",
		"0.0":    "0.0",
		"512m":   "512M",
		"1.5G":   "1.5G",
		"1024":   "1024G",
		"\"2\"":  "2G",
		"custom": "CUSTOM",
	}
	for in, want := range cases {
		if got := normalizeRAM(in); got != want {
			t.Errorf("normalizeRAM(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetAvailableServicesParsesComposeFile(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsComposeFilePath, []byte("version: '3'\nservices:\n  nginx:\n    image: nginx\n  docker-proxy:\n    image: x\n  clamav:\n    image: y\nvolumes:\n  data:\n"), 0644)

	got := getAvailableServices()
	if len(got) != 2 || got[0] != "nginx" || got[1] != "clamav" {
		t.Fatalf("expected [nginx clamav] excluding docker-proxy, got %+v", got)
	}
}

func TestGetAvailableServicesMissingFileReturnsEmptySlice(t *testing.T) {
	withScratchDefaultsPaths(t)
	got := getAvailableServices()
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %+v", got)
	}
}

func TestGetActiveServices(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsAutostartServicesPath, []byte("nginx\n#disabled\nclamav\n"), 0644)
	got := getActiveServices()
	if len(got) != 2 || got[0] != "nginx" || got[1] != "clamav" {
		t.Fatalf("expected [nginx clamav], got %+v", got)
	}
}

func TestServeDefaultsGetJSON(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte(`WEB_SERVER="nginx"`+"\n"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n  nginx:\n"), 0644)

	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) { return map[string]phpVersionStatus{}, nil }
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.Get(srv.URL + "/settings/defaults?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"WEB_SERVER":"nginx"`) {
		t.Fatalf("expected defaults JSON, got %s", body)
	}
	if !strings.Contains(string(body), `"autostart_available_services":["nginx"]`) {
		t.Fatalf("expected available services JSON, got %s", body)
	}
}

func TestServeDefaultsGetPropagatesNonTimeoutPHPWatchError(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte(`WEB_SERVER="nginx"`+"\n"), 0644)

	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) { return nil, &ftpStubError{"connection refused"} }
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.Get(srv.URL + "/settings/defaults")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a non-timeout PHP-watch error (uncaught, not treated as non-fatal), got %d", resp.StatusCode)
	}
}

func TestServeDefaultsPostRewritesEnvAndTogglesVarnish(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte("NGINX_CPU=\"1\"\nNGINX_RAM=\"256M\"\n#PROXY_HTTP_PORT=\"8080\"\nOTHER=\"unchanged\"\n"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n  nginx:\n  clamav:\n"), 0644)

	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) { return map[string]phpVersionStatus{}, nil }
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.PostForm(srv.URL+"/settings/defaults", url.Values{
		"NGINX_CPU": {"2"}, "NGINX_RAM": {"1024"}, "VARNISH": {"1"}, "services": {"nginx,clamav,bogus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "New defaults saved successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(DefaultsEnvPath)
	got := string(saved)
	if !strings.Contains(got, `NGINX_CPU="2"`) {
		t.Fatalf("expected NGINX_CPU updated, got %q", got)
	}
	if !strings.Contains(got, `NGINX_RAM="1024G"`) {
		t.Fatalf("expected RAM normalized with G suffix, got %q", got)
	}
	if !strings.Contains(got, `OTHER="unchanged"`) {
		t.Fatalf("expected untouched line preserved, got %q", got)
	}
	if strings.Contains(got, `#PROXY_HTTP_PORT`) {
		t.Fatalf("expected PROXY_HTTP_PORT to be uncommented since VARNISH=1, got %q", got)
	}

	autostart, _ := os.ReadFile(DefaultsAutostartServicesPath)
	if strings.Contains(string(autostart), "bogus") {
		t.Fatalf("expected invalid service name filtered out, got %q", autostart)
	}
	if !strings.Contains(string(autostart), "nginx") || !strings.Contains(string(autostart), "clamav") {
		t.Fatalf("expected valid services written, got %q", autostart)
	}
}

func TestServeDefaultsPostMissingEnvFileFlashesAndRedirects(t *testing.T) {
	withScratchDefaultsPaths(t) // no env file created
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n"), 0644)

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/settings/defaults", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Environment file not found.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestServeDefaultsGetHTML(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte(`WEB_SERVER="nginx"
DEFAULT_PHP_VERSION="8.2"
MYSQL_TYPE="mariadb"
PROXY_HTTP_PORT="8080"
PHP_FPM_8_2_CPU="1.5G"
PHP_FPM_8_2_RAM="512M"
PHP_FPM_7_4_CPU="1G"
NGINX_CPU="2G"
NGINX_ENABLED="true"
`), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n  nginx:\n  clamav:\n"), 0644)

	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) {
		return map[string]phpVersionStatus{
			"8.2": {StatusLabel: "Latest", IsLatestVersion: true, ActiveSupportEndDate: "2026-12-31"},
		}, nil
	}
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.Get(srv.URL + "/settings/defaults")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(got))
	}
	for _, want := range []string{
		"Edit Defaults", "Nginx", "MariaDB",
		`value="1.5" required`, // PHP_FPM CPU stripped of trailing G
		`value="1" required`,   // PHP_FPM 7.4 CPU stripped, no status
		`value="2" required`,   // NGINX CPU stripped
		"PHP Version  8.2", "Status: <b>Latest</b>", "Supported until: 2026-12-31",
		"NGINX", "</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeDefaultsFilesGetAndPost(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte("old-env"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("old-compose"), 0644)

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.PostForm(srv.URL+"/settings/defaults/files", url.Values{"env": {"new-env-content"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Files updated successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(DefaultsEnvPath)
	if string(saved) != "new-env-content" {
		t.Fatalf("expected env file updated, got %q", saved)
	}
	composeUnchanged, _ := os.ReadFile(DefaultsComposeFilePath)
	if string(composeUnchanged) != "old-compose" {
		t.Fatalf("expected compose file untouched (not submitted), got %q", composeUnchanged)
	}

	got := string(body)
	for _, want := range []string{"Edit Defaults", "docker-compose.yml", "new-env-content", "old-compose", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected rendered page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeDefaultsFilesPutValidatesViaComposeConfig(t *testing.T) {
	withScratchDefaultsPaths(t)

	var gotComposePath, gotTmpDir string
	origRun := defaultsComposeConfigRun
	defaultsComposeConfigRun = func(composePath, tmpDir string) (string, string, int, error) {
		gotComposePath, gotTmpDir = composePath, tmpDir
		return "resolved config", "", 0, nil
	}
	t.Cleanup(func() { defaultsComposeConfigRun = origRun })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	form := url.Values{"env": {"E=1"}, "compose": {"services:\n  x:\n"}}
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/settings/defaults/files", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "resolved config") {
		t.Fatalf("expected compose-config stdout in response, got %s", body)
	}
	if gotComposePath == "" || gotTmpDir != DefaultsTmpDir {
		t.Fatalf("expected compose path/tmpDir passed through, got %q %q", gotComposePath, gotTmpDir)
	}

	savedEnv, err := os.ReadFile(filepath.Join(DefaultsTmpDir, ".env"))
	if err != nil || string(savedEnv) != "E=1" {
		t.Fatalf("expected env written to tmp dir, err=%v content=%q", err, savedEnv)
	}
	if _, err := os.Stat(filepath.Join(DefaultsTmpDir, "sockets", "mysqld")); err != nil {
		t.Fatalf("expected socket dirs created, err=%v", err)
	}
}

func TestServeDefaultsFilesDeleteResetsFromRemote(t *testing.T) {
	withScratchDefaultsPaths(t)

	origFetch := defaultsFetchRemoteRun
	defaultsFetchRemoteRun = func(url string) (string, int, error) {
		if strings.Contains(url, "docker-compose.yml") {
			return "remote-compose", http.StatusOK, nil
		}
		return "remote-env", http.StatusOK, nil
	}
	t.Cleanup(func() { defaultsFetchRemoteRun = origFetch })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp := doMethodDefaults(t, client, http.MethodDelete, srv.URL+"/settings/defaults/files", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Defaults reset successfully from remote source!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	envSaved, _ := os.ReadFile(DefaultsEnvPath)
	if string(envSaved) != "remote-env" {
		t.Fatalf("expected env reset from remote, got %q", envSaved)
	}
	composeSaved, _ := os.ReadFile(DefaultsComposeFilePath)
	if string(composeSaved) != "remote-compose" {
		t.Fatalf("expected compose reset from remote, got %q", composeSaved)
	}
}

func TestServeDefaultsFilesDeletePartialFailure(t *testing.T) {
	withScratchDefaultsPaths(t)

	origFetch := defaultsFetchRemoteRun
	defaultsFetchRemoteRun = func(url string) (string, int, error) {
		if strings.Contains(url, "docker-compose.yml") {
			return "", http.StatusNotFound, nil
		}
		return "remote-env", http.StatusOK, nil
	}
	t.Cleanup(func() { defaultsFetchRemoteRun = origFetch })

	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp := doMethodDefaults(t, client, http.MethodDelete, srv.URL+"/settings/defaults/files", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Failed to fetch compose file from Github. Status code: 404") {
		t.Fatalf("expected per-file failure flash, got %s", truncate(string(body)))
	}
	if strings.Contains(string(body), "Defaults reset successfully") {
		t.Fatalf("expected no overall-success flash on partial failure, got %s", truncate(string(body)))
	}
}

func TestServeUserFilesGetAndPost(t *testing.T) {
	// query_context_by_username needs a MySQL connection; with nil MySQL it
	// returns an error and an empty context, so files resolve under "/home/".
	d := &Defaults{}
	srv, client := newDefaultsTestServer(t, d)

	resp, err := client.Get(srv.URL + "/settings/defaults/files/someuser")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"env"`) || !strings.Contains(string(body), `"compose"`) {
		t.Fatalf("expected env/compose JSON keys, got %s", body)
	}
}
