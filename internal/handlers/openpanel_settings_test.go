package handlers

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/config"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchOpenpanelSettingsPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origConfig, origFlag := OpenpanelSettingsConfigPath, OpenpanelSettingsRestartFlagPath
	OpenpanelSettingsConfigPath = filepath.Join(dir, "openpanel.config")
	OpenpanelSettingsRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	t.Cleanup(func() {
		OpenpanelSettingsConfigPath, OpenpanelSettingsRestartFlagPath = origConfig, origFlag
	})
}

func newOpenpanelSettingsTestServer(t *testing.T, o *OpenpanelSettings) (*httptest.Server, *http.Client) {
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
	o.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/open-panel", o.ServeOpenpanelSettings)
	mux.HandleFunc("POST /settings/open-panel", o.ServeOpenpanelSettings)
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

// validOpenpanelForm returns a complete, valid submission for every one of
// the ~20 eagerly-int-converted fields plus the enum/other string fields,
// so tests can override just the field(s) they care about.
func validOpenpanelForm() url.Values {
	v := url.Values{}
	for _, key := range openpanelIntFields {
		v.Set(key, "5")
	}
	v.Set("brand_name", "OpenPanel")
	v.Set("avatar_type", "gravatar")
	v.Set("resource_usage_charts_mode", "one")
	v.Set("password_reset", "yes")
	v.Set("password_strength", "50")
	v.Set("permit_username_change_by_user", "yes")
	v.Set("permit_subdomain_sharing", "yes")
	v.Set("twofa_nag", "yes")
	v.Set("twofa_enforce", "no")
	v.Set("how_to_guides", "yes")
	v.Set("found_a_bug_link", "yes")
	v.Set("ip_county_flag", "yes")
	v.Set("mysql_restricted_usernames", "root admin")
	v.Set("mysql_restricted_databases", "mysql sys")
	v.Set("filemanager_buttons_style", "classic")
	v.Set("filemanager_edit_extensions", ".txt .conf")
	v.Set("filemanager_image_extensions", ".jpg .png")
	v.Set("filemanager_archives_extensions", ".zip .tar")
	v.Set("logout_url", "/logout")
	return v
}

func TestValidateOpenpanelValueEnum(t *testing.T) {
	if _, ok, _ := validateOpenpanelValue("avatar_type", "icon"); !ok {
		t.Fatal("expected 'icon' to be valid")
	}
	if _, ok, msg := validateOpenpanelValue("avatar_type", "bogus"); ok || !strings.Contains(msg, "not a valid value") {
		t.Fatalf("expected rejection, got ok=%v msg=%q", ok, msg)
	}
}

func TestValidateOpenpanelValueNonNegativeInt(t *testing.T) {
	if v, ok, _ := validateOpenpanelValue("autopurge_trash", "5"); !ok || v != "5" {
		t.Fatalf("expected 5 valid, got %q %v", v, ok)
	}
	if _, ok, _ := validateOpenpanelValue("autopurge_trash", "-1"); ok {
		t.Fatal("expected negative to be rejected")
	}
	if _, ok, _ := validateOpenpanelValue("autopurge_trash", "abc"); ok {
		t.Fatal("expected non-numeric to be rejected")
	}
}

func TestValidateOpenpanelValueOneToHundred(t *testing.T) {
	if _, ok, _ := validateOpenpanelValue("password_strength", "0"); ok {
		t.Fatal("expected 0 to be out of range")
	}
	if _, ok, _ := validateOpenpanelValue("password_strength", "101"); ok {
		t.Fatal("expected 101 to be out of range")
	}
	if v, ok, _ := validateOpenpanelValue("password_strength", "50"); !ok || v != "50" {
		t.Fatalf("expected 50 valid, got %q %v", v, ok)
	}
}

func TestValidateOpenpanelValueSpaceSeparatedList(t *testing.T) {
	if v, ok, _ := validateOpenpanelValue("mysql_restricted_usernames", "root  admin_1"); !ok || v != "root admin_1" {
		t.Fatalf("expected normalized list, got %q %v", v, ok)
	}
	if _, ok, _ := validateOpenpanelValue("mysql_restricted_usernames", "root; drop"); ok {
		t.Fatal("expected invalid characters to be rejected")
	}
}

func TestValidateOpenpanelValueSpaceSeparatedExtensions(t *testing.T) {
	in := ".txt .md error_log .log env gitconfig cfg htaccess .ini .php .sh .html .json .htm .html5 .xml .py .php5 .php7 .php8 .sql .css .js .conf"
	if v, ok, _ := validateOpenpanelValue("filemanager_edit_extensions", in); !ok || v != in {
		t.Fatalf("expected valid space-separated extensions regardless of leading dot, got %q %v", v, ok)
	}
	if _, ok, _ := validateOpenpanelValue("filemanager_edit_extensions", "conf1"); !ok {
		t.Fatal("expected an extension with digits and no leading dot to be accepted")
	}
	if _, ok, _ := validateOpenpanelValue("filemanager_edit_extensions", "no spaces allowed*"); ok {
		t.Fatal("expected a token with an invalid character to be rejected")
	}
}

func TestOpenpanelSectionForKey(t *testing.T) {
	cases := map[string]string{
		"brand_name":                   "DEFAULT",
		"logout_url":                   "DEFAULT",
		"mysql_startup_time":           "DATABASES",
		"filemanager_upload_size":      "FILES",
		"autopurge_trash":              "FILES",
		"terminal_timeout":             "PANEL",
		"menu_style":                   "PANEL",
		"applications_dashboard_items": "PANEL",
		"login_ratelimit":              "USERS",
	}
	for key, want := range cases {
		if got := openpanelSectionForKey(key); got != want {
			t.Errorf("openpanelSectionForKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestServeOpenpanelSettingsPostMissingIntFieldFlashesInsteadOfCrashing(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[FILES]\nautopurge_trash=9\n"), 0644)

	form := validOpenpanelForm()
	form.Del("autopurge_trash") // simulate a malformed direct POST missing a numeric field

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (fixed from the original's 500 crash) for a missing numeric field, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "must be a non-negative integer for autopurge_trash") {
		t.Fatalf("expected a graceful validation flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "autopurge_trash=9") {
		t.Fatalf("expected the old value to be preserved, got %q", saved)
	}
}

func TestServeOpenpanelSettingsPostNegativeIntSkipped(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[FILES]\nautopurge_trash=7\n"), 0644)

	form := validOpenpanelForm()
	form.Set("autopurge_trash", "-3")

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "must be a non-negative integer for autopurge_trash") {
		t.Fatalf("expected validation error flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "autopurge_trash=7") {
		t.Fatalf("expected the old value to be preserved when the new one is rejected, got %q", saved)
	}
}

func TestServeOpenpanelSettingsPostValidSavesConfig(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[DEFAULT]\nbrand_name=Old\n"), 0644)

	form := validOpenpanelForm()
	form.Set("brand_name", "MyBrand")

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Configuration saved successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "brand_name=MyBrand") {
		t.Fatalf("expected brand_name updated, got %q", saved)
	}

	if _, err := os.Stat(OpenpanelSettingsRestartFlagPath); err != nil {
		t.Fatalf("expected restart flag written, err=%v", err)
	}
}

func TestServeOpenpanelSettingsPostWeakpassNowSaves(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[USERS]\n"), 0644)

	form := validOpenpanelForm()
	form.Set("weakpass", "1") // "checked"

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Configuration saved successfully.") {
		t.Fatalf("expected a plain success flash (fixed from the original's always-invalid weakpass error), got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "weakpass=yes") {
		t.Fatalf("expected weakpass=yes to actually be saved when checked, got %q", saved)
	}
}

func TestServeOpenpanelSettingsPostWeakpassUncheckedSavesNo(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[USERS]\nweakpass=yes\n"), 0644)

	form := validOpenpanelForm()
	form.Del("weakpass") // unchecked checkbox -> absent from the submitted form

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "weakpass=no") {
		t.Fatalf("expected weakpass=no to be saved when unchecked, got %q", saved)
	}
}

func TestServeOpenpanelSettingsGetRendersHTML(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte(
		"[DEFAULT]\nbrand_name=MyBrand\nns1=ns1.example.com\n"+
			"[USERS]\navatar_type=gravatar\npassword_strength=60\nweakpass=yes\n"+
			"[FILES]\nfilemanager_buttons_style=modern\n"), 0644)

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.Get(srv.URL + "/settings/open-panel")
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
		"OpenPanel settings",
		`value="MyBrand"`,
		`value="ns1.example.com"`,
		`value="60"`,
		"Save changes",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeOpenpanelSettingsGetRendersStrippedQuotes(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[DEFAULT]\nbrand_name=\"Quoted Brand\"\n"), 0644)

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.Get(srv.URL + "/settings/open-panel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `value="Quoted Brand"`) {
		t.Fatalf("expected quotes stripped from the displayed value, got %s", truncate(string(body)))
	}
}

func TestServeOpenpanelSettingsRecommendations(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[DEFAULT]\nns1=\n[USERS]\npassword_reset=yes\n"), 0644)

	dir := t.TempDir()
	origMain, origModules := config.OpenpanelConfigPath, ModulesConfigFilePath
	config.OpenpanelConfigPath = filepath.Join(dir, "main.config")
	ModulesConfigFilePath = filepath.Join(dir, "modules.config")
	t.Cleanup(func() { config.OpenpanelConfigPath, ModulesConfigFilePath = origMain, origModules })
	os.WriteFile(config.OpenpanelConfigPath, []byte("[SMTP]\nmail_server=\n"), 0644)
	os.WriteFile(ModulesConfigFilePath, []byte("enabled_modules=\"domains,dns,crons\"\n"), 0644)

	get := func() string {
		srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})
		resp, err := client.Get(srv.URL + "/settings/open-panel")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}

	body := get()
	for _, want := range []string{"No nameservers are set but the DNS module is enabled", `href="/settings/modules"`, "SMTP isn't configured", `href="/settings/notifications#smtp"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in the page", want)
		}
	}

	os.WriteFile(config.OpenpanelConfigPath, []byte("[SMTP]\nmail_server=smtp.example.com\n"), 0644)
	os.WriteFile(ModulesConfigFilePath, []byte("enabled_modules=\"domains,crons\"\n"), 0644)
	body = get()
	if strings.Contains(body, "No nameservers are set") || strings.Contains(body, "SMTP isn't configured") {
		t.Fatal("expected no hints with SMTP set up and the DNS module off")
	}
}

func TestServeOpenpanelSettingsWarnsWhenFileSizesExceedContainerRAM(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[FILES]\nfilemanager_upload_size=3000\n"), 0644)
	origEnv := GlobalEnvPath
	GlobalEnvPath = filepath.Join(t.TempDir(), ".env")
	t.Cleanup(func() { GlobalEnvPath = origEnv })
	os.WriteFile(GlobalEnvPath, []byte("OPENPANEL_RAM=\"2g\"\nOPENPANEL_CPUS=\"1.0\"\n"), 0644)

	srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})
	resp, err := client.Get(srv.URL + "/settings/open-panel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if n := strings.Count(string(body), "Number(v) > 2048"); n != 4 {
		t.Fatalf("expected the 2 GB limit checked on all 4 size fields, got %d", n)
	}
	if !strings.Contains(string(body), "more than the 2 GB RAM limit of the OpenPanel container") || !strings.Contains(string(body), `href="/services/limits"`) {
		t.Fatal("expected the warning text with a link to Service Limits")
	}
	if !strings.Contains(string(body), "Trash is never purged") || !strings.Contains(string(body), `href="/server/crons"`) {
		t.Fatal("expected the auto-purge trash hints")
	}
	if !strings.Contains(string(body), "Number(v) * 1024 > 2048") || !strings.Contains(string(body), "MySQL needs time to initialize") {
		t.Fatal("expected the MySQL import and startup hints")
	}
	for _, want := range []string{"Below 50, weak passwords are accepted", "passwords found in known data breaches", "can be reused by an attacker from any IP", "in both the OpenPanel web terminal and OpenAdmin"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in the page", want)
		}
	}
}

func TestMySQLCustomVersion(t *testing.T) {
	orig := DefaultsEnvPath
	DefaultsEnvPath = filepath.Join(t.TempDir(), ".env")
	t.Cleanup(func() { DefaultsEnvPath = orig })

	for content, want := range map[string]string{
		"MYSQL_VERSION=\"latest\"\n": "",
		"MYSQL_VERSION=\"8.0.36\"\n": "8.0.36",
		"MYSQL_TYPE=\"mariadb\"\n":   "",
		"MYSQL_VERSION=11.4\n":       "11.4",
	} {
		os.WriteFile(DefaultsEnvPath, []byte(content), 0644)
		if got := mysqlCustomVersion(); got != want {
			t.Fatalf("mysqlCustomVersion() with %q = %q, want %q", content, got, want)
		}
	}
}

func TestServeOpenpanelSettingsPagespeedHint(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[PANEL]\n"), 0644)
	orig := defaultsSiteTypesRun
	t.Cleanup(func() { defaultsSiteTypesRun = orig })

	get := func() string {
		srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})
		resp, err := client.Get(srv.URL + "/settings/open-panel")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}

	defaultsSiteTypesRun = func(_ *sql.DB) map[string]int { return map[string]int{"WordPress": 9, "Joomla": 3} }
	if !strings.Contains(get(), "There are 12 sites on this server but no PageSpeed API key") {
		t.Fatal("expected the PageSpeed hint with more than 10 sites")
	}
	defaultsSiteTypesRun = func(_ *sql.DB) map[string]int { return map[string]int{"WordPress": 10} }
	if strings.Contains(get(), "no PageSpeed API key") {
		t.Fatal("expected no PageSpeed hint with 10 sites or fewer")
	}
}

func TestValidateOpenpanelValueAI(t *testing.T) {
	if _, ok, _ := validateOpenpanelValue("ai_provider", "anthropic"); !ok {
		t.Error("anthropic should be a valid provider")
	}
	if _, ok, _ := validateOpenpanelValue("ai_provider", "gemini"); ok {
		t.Error("unknown provider accepted")
	}
	if _, ok, _ := validateOpenpanelValue("ai_max_tokens", "0"); ok {
		t.Error("ai_max_tokens 0 accepted")
	}
	if _, ok, _ := validateOpenpanelValue("ai_max_tokens", "64001"); ok {
		t.Error("ai_max_tokens over 64000 accepted")
	}
	if got, ok, _ := validateOpenpanelValue("ai_instructions", "line one\r\nline  two\n"); !ok || got != "line one line two" {
		t.Errorf("ai_instructions = %q, want it flattened to one line", got)
	}
	if openpanelSectionForKey("ai_api_key") != "AI" {
		t.Error("ai_ keys should go to the [AI] section")
	}
}

func TestServeOpenpanelSettingsAISectionSavesAndRenders(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[DEFAULT]\nbrand_name=Old\n"), 0644)

	form := validOpenpanelForm()
	form.Set("ai_provider", "anthropic")
	form.Set("ai_api_key", "sk-test")
	form.Set("ai_model", "claude-opus-5-5")
	form.Set("ai_monthly_limit", "50")
	form.Set("ai_max_tokens", "4096")
	form.Set("ai_instructions", "Support: help@example.com\nBe nice")

	o := &OpenpanelSettings{}
	srv, client := newOpenpanelSettingsTestServer(t, o)

	resp, err := client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, "Configuration saved successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(got))
	}

	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	for _, want := range []string{"[AI]", "ai_provider=anthropic", "ai_api_key=sk-test", "ai_monthly_limit=50", "ai_max_tokens=4096", "ai_instructions=Support: help@example.com Be nice"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("saved config missing %q:\n%s", want, saved)
		}
	}
	for _, want := range []string{`id="ai_assistant"`, `name="ai_provider"`, `value="sk-test"`, "The AI Assistant module is disabled"} {
		if !strings.Contains(got, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
