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

func withScratchFTPPaths(t *testing.T) (usersFile, confFile string) {
	t.Helper()
	dir := t.TempDir()
	origUsers, origConf := FTPUsersFilePath, FTPConfPath
	FTPUsersFilePath = filepath.Join(dir, "all.users")
	FTPConfPath = filepath.Join(dir, "vsftpd.conf")
	t.Cleanup(func() {
		FTPUsersFilePath = origUsers
		FTPConfPath = origConf
	})
	return FTPUsersFilePath, FTPConfPath
}

func newFTPTestServer(t *testing.T, f *FTP) (*httptest.Server, *http.Client) {
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
	f.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/ftp/refresh", f.ServeRefresh)
	mux.HandleFunc("POST /services/ftp/refresh", f.ServeRefresh)
	mux.HandleFunc("GET /services/ftp", f.ServeAccounts)
	mux.HandleFunc("POST /services/ftp", f.ServeAccounts)
	mux.HandleFunc("GET /services/ftp/settings", f.ServeSettings)
	mux.HandleFunc("POST /services/ftp/settings", f.ServeSettings)
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

func TestParseFTPAccountsValidAndMalformed(t *testing.T) {
	raw := `USERS="user1.owner1|pass1|/data/user1_data/dir|1000|1000 malformed|entry user2|pass2|/data/user2_data/x|1001|1001"`
	accounts := parseFTPAccounts(raw)
	if len(accounts) != 2 {
		t.Fatalf("expected 2 valid accounts (malformed skipped), got %d: %+v", len(accounts), accounts)
	}
	if accounts[0].User != "user1.owner1" || accounts[0].Owner != "owner1" {
		t.Fatalf("unexpected first account: %+v", accounts[0])
	}
	if accounts[0].Path != "/var/www/html/dir" {
		t.Fatalf("expected _data/ prefix mapped to /var/www/html/, got %q", accounts[0].Path)
	}
	if accounts[1].User != "user2" || accounts[1].Owner != "user2" {
		t.Fatalf("expected owner to fall back to full username when there's no dot, got %+v", accounts[1])
	}
}

func TestCheckFTPServerStatusNotInstalled(t *testing.T) {
	withScratchFTPPaths(t)
	if got := checkFTPServerStatus(); got != "not_installed" {
		t.Fatalf("expected not_installed when all.users is missing, got %q", got)
	}
}

func TestCheckFTPServerStatusRunning(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte("USERS=\"a|b|c|d|e\""), 0644)

	origRun := ftpPsRun
	ftpPsRun = func() (string, error) { return "openadmin_ftp\n", nil }
	t.Cleanup(func() { ftpPsRun = origRun })

	if got := checkFTPServerStatus(); got != "running" {
		t.Fatalf("expected running, got %q", got)
	}
}

func TestCheckFTPServerStatusStopped(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte("USERS=\"a|b|c|d|e\""), 0644)

	origRun := ftpPsRun
	ftpPsRun = func() (string, error) { return "", nil }
	t.Cleanup(func() { ftpPsRun = origRun })

	if got := checkFTPServerStatus(); got != "stopped" {
		t.Fatalf("expected stopped, got %q", got)
	}
}

func TestServeAccountsJSONNotRunning(t *testing.T) {
	withScratchFTPPaths(t) // no all.users file -> not_installed

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"not_installed"`) {
		t.Fatalf("expected not_installed status, got %s", truncate(string(body)))
	}
}

func TestServeAccountsJSONRunningWithAccounts(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte("USERS=\"bob.alice|secret|/data/bob_data/site|1000|1000\""), 0644)

	origRun := ftpPsRun
	ftpPsRun = func() (string, error) { return "openadmin_ftp\n", nil }
	t.Cleanup(func() { ftpPsRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"user":"bob.alice"`) || !strings.Contains(string(body), `"owner":"alice"`) {
		t.Fatalf("expected bob.alice account with owner alice, got %s", truncate(string(body)))
	}
}

func TestServeAccountsHTMLEmptyState(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte("USERS=\"\""), 0644)

	origRun := ftpPsRun
	ftpPsRun = func() (string, error) { return "openadmin_ftp\n", nil }
	t.Cleanup(func() { ftpPsRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"Click to refresh data", "FTP", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeAccountsHTMLRunningWithAccounts(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte(`USERS="alice.bob|pw|/home/bob/alice_data/|1000|1000"`), 0644)

	origRun := ftpPsRun
	ftpPsRun = func() (string, error) { return "openadmin_ftp\n", nil }
	t.Cleanup(func() { ftpPsRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"alice.bob", "/var/www/html/", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeRefreshSuccess(t *testing.T) {
	origRun := ftpRefreshRun
	ftpRefreshRun = func() (string, error) { return "5 accounts refreshed", nil }
	t.Cleanup(func() { ftpRefreshRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp/refresh")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "5 accounts refreshed" {
		t.Fatalf("expected raw opencli output, got %q", body)
	}
}

func TestServeRefreshFailure(t *testing.T) {
	origRun := ftpRefreshRun
	ftpRefreshRun = func() (string, error) { return "boom", errFTPRefreshStub }
	t.Cleanup(func() { ftpRefreshRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp/refresh")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Error executing opencli ftp-users: boom") {
		t.Fatalf("expected error message, got %q", body)
	}
}

func TestServeSettingsGetReadsExistingConf(t *testing.T) {
	_, confFile := withScratchFTPPaths(t)
	os.WriteFile(confFile, []byte("listen=YES\nanonymous_enable=NO\n"), 0644)

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.Get(srv.URL + "/services/ftp/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"anonymous_enable=NO", "FTP", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeSettingsPostSavesAndRestarts(t *testing.T) {
	_, confFile := withScratchFTPPaths(t)
	os.WriteFile(confFile, []byte("old content\n"), 0644)

	restarted := false
	origRun := ftpRestartRun
	ftpRestartRun = func() error { restarted = true; return nil }
	t.Cleanup(func() { ftpRestartRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.PostForm(srv.URL+"/services/ftp/settings", url.Values{"config_content": {"listen=YES\r\nanon=NO\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !restarted {
		t.Fatal("expected ftpRestartRun to be called after a successful save")
	}
	if !strings.Contains(string(body), "Config updated successfully. FTP container restarted to apply changes.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(confFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "\r") {
		t.Fatalf("expected CRLF to be normalized to LF, got %q", saved)
	}
	if !strings.Contains(string(saved), "listen=YES\nanon=NO\n") {
		t.Fatalf("expected normalized content to be saved, got %q", saved)
	}

	backup, err := os.ReadFile(confFile + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "old content\n" {
		t.Fatalf("expected .bak to hold the previous content, got %q", backup)
	}
}

func TestServeSettingsPostRestartFailureStillSaves(t *testing.T) {
	_, confFile := withScratchFTPPaths(t)

	origRun := ftpRestartRun
	ftpRestartRun = func() error { return errFTPRefreshStub }
	t.Cleanup(func() { ftpRestartRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.PostForm(srv.URL+"/services/ftp/settings", url.Values{"config_content": {"new config"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Config updated, but failed to restart FTP container.") {
		t.Fatalf("expected restart-failure flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(confFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "new config" {
		t.Fatalf("expected the config to still be saved despite the restart failure, got %q", saved)
	}
}

func TestServeSettingsPostRejectsMissingField(t *testing.T) {
	withScratchFTPPaths(t)

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)

	resp, err := client.PostForm(srv.URL+"/services/ftp/settings", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error saving FTP configuration - no content provided!") {
		t.Fatalf("expected no-content flash, got %s", truncate(string(body)))
	}
}

var errFTPRefreshStub = &ftpStubError{"stub error"}

type ftpStubError struct{ msg string }

func (e *ftpStubError) Error() string { return e.msg }
