package handlers

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func withScratchAutologinConfig(t *testing.T, impersonate string) {
	t.Helper()
	dir := t.TempDir()
	origPath := config.AdminConfigPath
	path := filepath.Join(dir, "admin.ini")
	content := "[USERS]\n"
	if impersonate != "" {
		content += "impersonate=" + impersonate + "\n"
	}
	os.WriteFile(path, []byte(content), 0644)
	config.AdminConfigPath = path
	t.Cleanup(func() { config.AdminConfigPath = origPath })
}

func newAutologinTestServer(t *testing.T, a *Autologin, loginAsRole string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, loginAsRole)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	a.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login/token/{username}", a.ServeLoginToken)
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

func TestCheckIfOwnerForUserAdminAlwaysOwns(t *testing.T) {
	adminUser := &admindb.User{Username: "caller", Role: "admin"}
	if !checkIfOwnerForUser(nil, "anyone", adminUser) {
		t.Fatal("expected an admin caller to always be treated as owner")
	}
}

func TestCheckIfOwnerForUserResellerRequiresNilDBFalse(t *testing.T) {
	resellerUser := &admindb.User{Username: "reseller1", Role: "reseller"}
	if checkIfOwnerForUser(nil, "someuser", resellerUser) {
		t.Fatal("expected a reseller caller with no DB connection to be denied")
	}
}

func TestGenerateRandomTokenLengthAndCharset(t *testing.T) {
	tok := generateRandomToken(30)
	if len(tok) != 30 {
		t.Fatalf("expected length 30, got %d", len(tok))
	}
	for _, c := range tok {
		if !strings.ContainsRune(autologinTokenCharset, c) {
			t.Fatalf("unexpected character %q in token %q", c, tok)
		}
	}
}

func withScratchAutologinTokenDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := AutologinTokenBaseDir
	AutologinTokenBaseDir = dir
	t.Cleanup(func() { AutologinTokenBaseDir = orig })
	return dir
}

// newUserExistsMock returns a *sql.DB whose only expectation is the
// userExists lookup ServeLoginToken now runs before writing a token file,
// answering with WillReturnRows(one row) if exists is true, or
// sql.ErrNoRows otherwise.
func newUserExistsMock(t *testing.T, username string, exists bool) *sql.DB {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	expectation := mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE username = ? LIMIT 1`)).
		WithArgs(username)
	if exists {
		expectation.WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	} else {
		expectation.WillReturnError(sql.ErrNoRows)
	}
	return db
}

func TestServeLoginTokenRedirectsWithLink(t *testing.T) {
	withScratchAutologinConfig(t, "no")
	tokenDir := withScratchAutologinTokenDir(t)

	origDomain, origPort, origSSL := autologinOpenpanelDomainRun, autologinOpenpanelPortRun, autologinCheckSSLExistsRun
	autologinOpenpanelDomainRun = func() string { return "" }
	autologinOpenpanelPortRun = func() string { return "2083" }
	autologinCheckSSLExistsRun = func(domain string) bool { return false }
	t.Cleanup(func() {
		autologinOpenpanelDomainRun, autologinOpenpanelPortRun, autologinCheckSSLExistsRun = origDomain, origPort, origSSL
	})

	a := &Autologin{PublicIP: "198.51.100.5", AdminPort: "2087", MySQL: newUserExistsMock(t, "testuser", true)}
	srv, client := newAutologinTestServer(t, a, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/login/token/testuser?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for JSON output, got %d: %s", resp.StatusCode, body)
	}
	got := string(body)
	if !strings.Contains(got, "http://198.51.100.5:2083") {
		t.Fatalf("expected http scheme with public IP fallback, got %s", got)
	}
	if !strings.Contains(got, "admin_port=2087") {
		t.Fatalf("expected admin_port in link, got %s", got)
	}
	if !strings.Contains(got, "username=testuser") {
		t.Fatalf("expected username in link, got %s", got)
	}
	if strings.Contains(got, "impersonate=yes") {
		t.Fatalf("expected impersonate=yes to be absent when config says 'no', got %s", got)
	}

	tokenContent, err := os.ReadFile(filepath.Join(tokenDir, "testuser", "logintoken.txt"))
	if err != nil {
		t.Fatalf("expected a login token file written, err=%v", err)
	}
	if len(tokenContent) != 30 {
		t.Fatalf("expected a 30-character token, got %q", tokenContent)
	}
}

func TestServeLoginTokenImpersonateModeAndHTTPS(t *testing.T) {
	withScratchAutologinConfig(t, "yes")
	withScratchAutologinTokenDir(t)

	origDomain, origPort, origSSL := autologinOpenpanelDomainRun, autologinOpenpanelPortRun, autologinCheckSSLExistsRun
	autologinOpenpanelDomainRun = func() string { return "panel.example.com" }
	autologinOpenpanelPortRun = func() string { return "2083" }
	autologinCheckSSLExistsRun = func(domain string) bool { return domain == "panel.example.com" }
	t.Cleanup(func() {
		autologinOpenpanelDomainRun, autologinOpenpanelPortRun, autologinCheckSSLExistsRun = origDomain, origPort, origSSL
	})

	a := &Autologin{PublicIP: "198.51.100.5", AdminPort: "2087", MySQL: newUserExistsMock(t, "testuser", true)}
	srv, client := newAutologinTestServer(t, a, "admin")

	resp, err := client.Get(srv.URL + "/login/token/testuser?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, "https://panel.example.com:2083") {
		t.Fatalf("expected https scheme with custom domain, got %s", got)
	}
	if !strings.Contains(got, "impersonate=yes") {
		t.Fatalf("expected impersonate=yes when config says 'yes', got %s", got)
	}
}

func TestServeLoginTokenUnknownUsernameNotFound(t *testing.T) {
	withScratchAutologinConfig(t, "no")
	tokenDir := withScratchAutologinTokenDir(t)

	a := &Autologin{PublicIP: "198.51.100.5", AdminPort: "2087", MySQL: newUserExistsMock(t, "ghost", false)}
	srv, client := newAutologinTestServer(t, a, "admin")

	resp, err := client.Get(srv.URL + "/login/token/ghost")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a username with no matching users row, got %d", resp.StatusCode)
	}

	if _, err := os.Stat(filepath.Join(tokenDir, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("expected no token file/dir to be written for a nonexistent username, err=%v", err)
	}
}

func TestServeLoginTokenResellerDeniedForNonOwnedUser(t *testing.T) {
	withScratchAutologinConfig(t, "no")

	a := &Autologin{PublicIP: "198.51.100.5", AdminPort: "2087"} // nil MySQL -> reseller always denied
	srv, client := newAutologinTestServer(t, a, "reseller")

	resp, err := client.Get(srv.URL + "/login/token/testuser")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller without a verifiable ownership record, got %d", resp.StatusCode)
	}
}
