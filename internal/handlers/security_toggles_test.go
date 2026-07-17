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
	"openadmin/internal/bootstrap"
	"openadmin/internal/config"
)

func newSecurityTogglesTestServer(t *testing.T, st *SecurityToggles, role string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	st.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /security/disable-admin", st.ServeDisableAdmin)
	mux.HandleFunc("POST /security/disable-admin", st.ServeDisableAdmin)
	mux.HandleFunc("GET /security/basic_auth", st.ServeBasicAuth)
	mux.HandleFunc("POST /security/basic_auth", st.ServeBasicAuth)
	mux.HandleFunc("GET /security/blacklist-useragents", st.ServeBlacklistUseragents)
	mux.HandleFunc("POST /security/blacklist-useragents", st.ServeBlacklistUseragents)
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

func TestDisableAdminRequiresAdminRole(t *testing.T) {
	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "user")

	resp, err := client.Get(srv.URL + "/security/disable-admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin role, got %d", resp.StatusCode)
	}
}

func TestDisableAdminFlashesOnPost(t *testing.T) {
	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "admin")

	resp, err := client.PostForm(srv.URL+"/security/disable-admin", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "OpenAdmin is now disabled") {
		t.Fatalf("expected the disabled flash, got %s", truncate(string(body)))
	}
}

func withScratchAdminIni(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = orig })
	return config.AdminConfigPath
}

func TestBasicAuthServeReflectsConfig(t *testing.T) {
	path := withScratchAdminIni(t)
	os.WriteFile(path, []byte("[SECURITY]\nbasic_auth=yes\nbasic_auth_username=admin\nbasic_auth_password=secret\n"), 0644)

	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "admin")

	resp, err := client.Get(srv.URL + "/security/basic_auth")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `value="admin"`) {
		t.Fatalf("expected username in page, got %s", truncate(string(body)))
	}
}

func TestBasicAuthUpdatePreservesOtherSectionsAndSetsRestartFlag(t *testing.T) {
	path := withScratchAdminIni(t)
	os.WriteFile(path, []byte("[USERS]\nreseller=yes\n\n[SECURITY]\nbasic_auth=no\n"), 0644)

	dir := t.TempDir()
	origFlag := bootstrap.RestartFlagPath
	bootstrap.RestartFlagPath = filepath.Join(dir, "restart_needed")
	t.Cleanup(func() { bootstrap.RestartFlagPath = origFlag })

	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "admin")

	resp, err := client.PostForm(srv.URL+"/security/basic_auth", url.Values{
		"basic_auth":          {"yes"},
		"basic_auth_username": {"newadmin"},
		"basic_auth_password": {"newsecret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	written := config.Load(path)
	if written.Get("SECURITY", "basic_auth_username", "") != "newadmin" {
		t.Fatalf("expected updated username, got %+v", written)
	}
	if written.Get("USERS", "reseller", "") != "yes" {
		t.Fatalf("expected the unrelated USERS section to survive the rewrite, got %+v", written)
	}
	if _, err := os.Stat(bootstrap.RestartFlagPath); err != nil {
		t.Fatalf("expected the restart-needed flag to be written: %v", err)
	}
}

func TestBlacklistUseragentsRequiresAdminRole(t *testing.T) {
	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "user")

	resp, err := client.Get(srv.URL + "/security/blacklist-useragents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin role, got %d", resp.StatusCode)
	}
}

func TestBlacklistUseragentsSavesFileAndFlagsRestart(t *testing.T) {
	dir := t.TempDir()
	origFile := BlacklistUseragentsFilePath
	BlacklistUseragentsFilePath = filepath.Join(dir, "blacklist_useragents.txt")
	t.Cleanup(func() { BlacklistUseragentsFilePath = origFile })

	origFlag := OpenpanelRestartFlagPath
	OpenpanelRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	t.Cleanup(func() { OpenpanelRestartFlagPath = origFlag })

	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "admin")

	resp, err := client.PostForm(srv.URL+"/security/blacklist-useragents", url.Values{
		"blacklist_useragents": {"BadBot/1.0\nEvilCrawler"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	written, err := os.ReadFile(BlacklistUseragentsFilePath)
	if err != nil {
		t.Fatalf("expected the blacklist file to be written: %v", err)
	}
	if !strings.Contains(string(written), "BadBot/1.0") {
		t.Fatalf("expected the new content to be written, got %q", written)
	}
	if !strings.Contains(string(body), "Saved blacklisted useragents.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(OpenpanelRestartFlagPath); err != nil {
		t.Fatalf("expected the OpenPanel restart flag to be written: %v", err)
	}
}

func TestBlacklistUseragentsMissingFileWarnsButDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	origFile := BlacklistUseragentsFilePath
	BlacklistUseragentsFilePath = filepath.Join(dir, "does-not-exist.txt")
	t.Cleanup(func() { BlacklistUseragentsFilePath = origFile })

	st := &SecurityToggles{}
	srv, client := newSecurityTogglesTestServer(t, st, "admin")

	resp, err := client.Get(srv.URL + "/security/blacklist-useragents")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 even with a missing blacklist file, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "A new one will be created on save") {
		t.Fatalf("expected the missing-file warning flash, got %s", truncate(string(body)))
	}
}
