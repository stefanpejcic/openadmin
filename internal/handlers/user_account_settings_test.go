package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestGetSetUserEnvValueRoundTrip exercises the same parse/rewrite
// primitives getUserEnvValue/setUserEnvValue use (quickStartParseEnv +
// splitFileLinesPreserving) directly against a temp file, since those two
// functions hardcode the "/home/<context>/.env" path and so aren't
// themselves parameterizable in a test.
func TestGetSetUserEnvValueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("WEB_SERVER=\"nginx\"\nMYSQL_TYPE=\"mysql\"\nOTHER=\"x\"\n"), 0644)

	got := quickStartParseEnv(path)
	if got["WEB_SERVER"] != "nginx" {
		t.Fatalf("expected nginx, got %q", got["WEB_SERVER"])
	}

	raw, _ := os.ReadFile(path)
	lines := splitFileLinesPreserving(string(raw))
	for i, line := range lines {
		if strings.HasPrefix(line, "WEB_SERVER=") {
			lines[i] = "WEB_SERVER=\"apache\"\n"
		}
	}
	os.WriteFile(path, []byte(strings.Join(lines, "")), 0644)

	got = quickStartParseEnv(path)
	if got["WEB_SERVER"] != "apache" {
		t.Fatalf("expected apache after rewrite, got %q", got["WEB_SERVER"])
	}
	if got["MYSQL_TYPE"] != "mysql" {
		t.Fatalf("expected MYSQL_TYPE untouched, got %q", got["MYSQL_TYPE"])
	}
}

func TestInstalledLocaleCodesFallsBackToEnglish(t *testing.T) {
	origDir := TranslationsDir
	TranslationsDir = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { TranslationsDir = origDir })

	codes := installedLocaleCodes()
	if len(codes) != 1 || codes[0] != "en" {
		t.Fatalf("expected fallback [en], got %v", codes)
	}
}

func TestInstalledLocaleCodesListsTwoLetterDirs(t *testing.T) {
	origDir := TranslationsDir
	dir := t.TempDir()
	TranslationsDir = dir
	t.Cleanup(func() { TranslationsDir = origDir })

	os.MkdirAll(filepath.Join(dir, "en"), 0755)
	os.MkdirAll(filepath.Join(dir, "sr"), 0755)
	os.WriteFile(filepath.Join(dir, "not-a-dir"), []byte(""), 0644) // ignored: not a dir

	codes := installedLocaleCodes()
	if len(codes) != 2 {
		t.Fatalf("expected 2 codes, got %v", codes)
	}
}

func TestUserDomainCountQueriesJoin(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM domains d`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := userDomainCount(mysqlDB, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}
}

func expectContextQuery(mock sqlmock.Sqlmock, username, context string) {
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs(username).
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow(context))
}

// newAccountSettingTestServer stops at the first redirect (like the
// resellers-toggle tests elsewhere in this package) so the assertions
// below can inspect ServeUserAccountSetting's own 303 + flash instead of
// whatever the followed /users/{username} page happens to render.
func newAccountSettingTestServer(t *testing.T, u *Users, role string) (*httptest.Server, *http.Client) {
	srv, client := newUsersTestServer(t, u, role)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	return srv, client
}

func postAccountSetting(t *testing.T, client *http.Client, srv *httptest.Server, username, field, value string) (*http.Response, string) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+"/users/"+username+"/account-setting/"+field, url.Values{"value": {value}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestServeUserAccountSettingUnknownField(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, body := postAccountSetting(t, client, srv, "alice", "bogus", "x")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserAccountSettingLocaleInvalidRejected(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")

	origDir := TranslationsDir
	TranslationsDir = filepath.Join(t.TempDir(), "empty")
	t.Cleanup(func() { TranslationsDir = origDir })

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, _ := postAccountSetting(t, client, srv, "alice", "locale", "zz")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for a non-installed locale, got %d", resp.StatusCode)
	}
}

func TestServeUserAccountSettingWebserverBlockedByDomains(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM domains d`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, _ := postAccountSetting(t, client, srv, "alice", "webserver", "apache")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 while domains exist, got %d", resp.StatusCode)
	}
}

func TestServeUserAccountSettingWebserverInvalidValue(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, _ := postAccountSetting(t, client, srv, "alice", "webserver", "iis")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for an invalid webserver, got %d", resp.StatusCode)
	}
}

func TestServeUserAccountSettingDatabaseTypeBlockedWhileRunning(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")

	orig := containerIsRunningRun
	containerIsRunningRun = func(context, containerName string) bool { return true }
	t.Cleanup(func() { containerIsRunningRun = orig })

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, _ := postAccountSetting(t, client, srv, "alice", "database_type", "mariadb")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 while the current DB container is running, got %d", resp.StatusCode)
	}
}

func TestServeUserAccountSettingVarnishInvalidValue(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContextQuery(mock, "alice", "alice")

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "admin")

	resp, _ := postAccountSetting(t, client, srv, "alice", "varnish", "maybe")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for an invalid varnish value, got %d", resp.StatusCode)
	}
}

func TestServeUserAccountSettingForbiddenForNonOwningReseller(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("alice", "caller").
		WillReturnError(sqlErrConnRefused{})

	u := &Users{MySQL: mysqlDB}
	srv, client := newAccountSettingTestServer(t, u, "reseller")

	resp, _ := postAccountSetting(t, client, srv, "alice", "twofa", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}
