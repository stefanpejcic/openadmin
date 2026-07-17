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

func withScratchModulesPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origConfig, origFeatures, origCompose, origPlugins, origRndc, origFlag :=
		ModulesConfigFilePath, ModulesFeaturesJSONPath, ModulesDockerComposePath,
		ModulesPluginsBaseDir, ModulesRndcKeyPath, ModulesOpenpanelRestartFlagPath

	ModulesConfigFilePath = filepath.Join(dir, "openpanel.config")
	ModulesFeaturesJSONPath = filepath.Join(dir, "features.json")
	ModulesDockerComposePath = filepath.Join(dir, "docker-compose.yml")
	ModulesPluginsBaseDir = filepath.Join(dir, "plugins")
	ModulesRndcKeyPath = filepath.Join(dir, "rndc.key")
	ModulesOpenpanelRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	os.MkdirAll(ModulesPluginsBaseDir, 0755)

	t.Cleanup(func() {
		ModulesConfigFilePath, ModulesFeaturesJSONPath, ModulesDockerComposePath,
			ModulesPluginsBaseDir, ModulesRndcKeyPath, ModulesOpenpanelRestartFlagPath =
			origConfig, origFeatures, origCompose, origPlugins, origRndc, origFlag
	})
}

func newModulesTestServer(t *testing.T, m *Modules) (*httptest.Server, *http.Client) {
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
	m.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/modules", m.ServeModules)
	mux.HandleFunc("POST /settings/modules", m.ServeModules)
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

func TestUpdateServiceInDockerComposeEnablesAndDisables(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesDockerComposePath, []byte("services:\n  app:\n    depends_on:\n      #    - phpmyadmin\n      - clamav\n"), 0644)

	updateServiceInDockerCompose("phpmyadmin", true)
	content, _ := os.ReadFile(ModulesDockerComposePath)
	if strings.Contains(string(content), "#    - phpmyadmin") || !strings.Contains(string(content), "- phpmyadmin") {
		t.Fatalf("expected phpmyadmin to be uncommented, got %q", content)
	}

	updateServiceInDockerCompose("clamav", false)
	content, _ = os.ReadFile(ModulesDockerComposePath)
	if !strings.Contains(string(content), "# - clamav") {
		t.Fatalf("expected clamav to be commented out, got %q", content)
	}
}

func TestModulesEnabledList(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte("[DEFAULT]\nenabled_modules=\"dns,malware_scan\"\nother=1\n"), 0644)
	if got := modulesEnabledList(ModulesConfigFilePath); len(got) != 2 || got[0] != "dns" || got[1] != "malware_scan" {
		t.Fatalf("expected [dns malware_scan], got %+v", got)
	}

	os.WriteFile(ModulesConfigFilePath, []byte("enabled_modules=\"\"\n"), 0644)
	if got := modulesEnabledList(ModulesConfigFilePath); got != nil {
		t.Fatalf("expected nil for empty value, got %+v", got)
	}

	if got := modulesEnabledList(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("expected nil for missing file, got %+v", got)
	}
}

func TestParsePluginReadme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readme.txt")
	os.WriteFile(path, []byte("# comment\nname=My Plugin\nversion=1.0\n\n"), 0644)

	meta := parsePluginReadme(path)
	if meta["name"] != "My Plugin" || meta["version"] != "1.0" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestGetAllPluginsFindsOnlyDirsWithReadme(t *testing.T) {
	withScratchModulesPaths(t)
	os.MkdirAll(filepath.Join(ModulesPluginsBaseDir, "with-readme"), 0755)
	os.WriteFile(filepath.Join(ModulesPluginsBaseDir, "with-readme", "readme.txt"), []byte("name=Foo\n"), 0644)
	os.MkdirAll(filepath.Join(ModulesPluginsBaseDir, "without-readme"), 0755)
	os.WriteFile(filepath.Join(ModulesPluginsBaseDir, "stray-file.txt"), []byte("x"), 0644)

	plugins := getAllPlugins(ModulesPluginsBaseDir)
	if len(plugins) != 1 {
		t.Fatalf("expected exactly 1 plugin, got %+v", plugins)
	}
	if plugins[0]["folder"] != "with-readme" || plugins[0]["name"] != "Foo" {
		t.Fatalf("unexpected plugin entry: %+v", plugins[0])
	}
}

func TestServeModulesGetRendersFeaturesWithStatus(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="dns"`+"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[{"name":"dns","title":"DNS","description":"desc"},{"name":"waf","title":"WAF","description":"desc2"}]`), 0644)

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.Get(srv.URL + "/settings/modules")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, `name="dns"`) || !strings.Contains(got, "checked") {
		t.Fatalf("expected dns checkbox checked, got %s", truncate(got))
	}
}

func TestServeModulesGetJSON(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="dns"`+"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[{"name":"dns","title":"DNS"},{"name":"waf","title":"WAF"}]`), 0644)

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.Get(srv.URL + "/settings/modules?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"name":"dns","status":true`) {
		t.Fatalf("expected dns status true in JSON, got %s", body)
	}
	if !strings.Contains(string(body), `"name":"waf","status":false`) {
		t.Fatalf("expected waf status false in JSON, got %s", body)
	}
}

func TestServeModulesGetRendersHTML(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="dns"`+"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[
		{"name":"dns","title":"DNS","description":"desc","link":"/dns","type":"community","help_link":"https://example.com/dns"},
		{"name":"docker","title":"Docker (Containers)","description":"desc2","link":"/containers","type":"enterprise","help_link":""},
		{"name":"backups","title":"Backups","description":"desc3","link":"/backups","type":"beta","help_link":""}
	]`), 0644)
	os.MkdirAll(filepath.Join(ModulesPluginsBaseDir, "my-plugin"), 0755)
	os.WriteFile(filepath.Join(ModulesPluginsBaseDir, "my-plugin", "readme.txt"), []byte("name=My Plugin\ndescription=does things\n"), 0644)

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.Get(srv.URL + "/settings/modules")
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
		"Modules",
		"DNS",
		"Docker (Containers)",
		"Backups",
		"My Plugin",
		"Save Changes",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeModulesPostUpdatesConfigAndTogglesServices(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte("[DEFAULT]\nenabled_modules=\"\"\nother=1\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[]`), 0644)
	os.WriteFile(ModulesDockerComposePath, []byte("      #    - phpmyadmin\n      #    - clamav\n"), 0644)
	os.WriteFile(ModulesRndcKeyPath, []byte("existing"), 0644) // present -> DNS gen skipped

	origRndc := modulesRndcGenRun
	rndcCalled := false
	modulesRndcGenRun = func() { rndcCalled = true }
	t.Cleanup(func() { modulesRndcGenRun = origRndc })

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/settings/modules", url.Values{
		"phpmyadmin": {"phpmyadmin"}, "malware_scan": {"malware_scan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(ModulesConfigFilePath)
	if !strings.Contains(string(saved), `enabled_modules="malware_scan,phpmyadmin"`) {
		t.Fatalf("expected sorted enabled_modules value saved, got %q", saved)
	}
	if !strings.Contains(string(saved), "other=1") {
		t.Fatalf("expected untouched lines preserved, got %q", saved)
	}

	compose, _ := os.ReadFile(ModulesDockerComposePath)
	if !strings.Contains(string(compose), "- phpmyadmin") || strings.Contains(string(compose), "#    - phpmyadmin") {
		t.Fatalf("expected phpmyadmin uncommented, got %q", compose)
	}
	if !strings.Contains(string(compose), "- clamav") || strings.Contains(string(compose), "#    - clamav") {
		t.Fatalf("expected clamav uncommented, got %q", compose)
	}

	if rndcCalled {
		t.Fatal("expected rndc generation to be skipped since dns wasn't enabled")
	}

	flag, err := os.ReadFile(ModulesOpenpanelRestartFlagPath)
	if err != nil || string(flag) != "Restart needed for OpenPanel service." {
		t.Fatalf("expected restart flag written with exact message, got %q err=%v", flag, err)
	}
}

func TestServeModulesPostDisablesUnselectedServices(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="phpmyadmin"`+"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[]`), 0644)
	os.WriteFile(ModulesDockerComposePath, []byte("      - phpmyadmin\n      - clamav\n"), 0644)

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/settings/modules", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	compose, _ := os.ReadFile(ModulesDockerComposePath)
	if !strings.Contains(string(compose), "# - phpmyadmin") {
		t.Fatalf("expected phpmyadmin commented out when not selected, got %q", compose)
	}
	if !strings.Contains(string(compose), "# - clamav") {
		t.Fatalf("expected clamav commented out when not selected, got %q", compose)
	}
}

func TestServeModulesPostDNSGeneratesRndcKeyWhenMissing(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules=""`+"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[]`), 0644)
	os.WriteFile(ModulesDockerComposePath, []byte(""), 0644)
	// ModulesRndcKeyPath not created -> missing

	rndcCalled := false
	origRndc := modulesRndcGenRun
	modulesRndcGenRun = func() { rndcCalled = true }
	t.Cleanup(func() { modulesRndcGenRun = origRndc })

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/settings/modules", url.Values{"dns": {"dns"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if !rndcCalled {
		t.Fatal("expected rndc key generation to be triggered when dns is enabled and the key is missing")
	}
}

func TestServeModulesPostMissingConfigFileReturns500(t *testing.T) {
	withScratchModulesPaths(t) // config file never created

	m := &Modules{}
	srv, client := newModulesTestServer(t, m)

	resp, err := client.PostForm(srv.URL+"/settings/modules", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing config file, got %d", resp.StatusCode)
	}
}
