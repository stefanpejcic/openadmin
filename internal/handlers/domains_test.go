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

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

// phpVersionsEOLFetch is protected by a package-level sync.Once, so it can
// only be meaningfully stubbed once for the whole test binary. Setting it
// here in init() guarantees no test in this package ever makes a real
// network call to api.openpanel.com, regardless of test run order.
func init() {
	phpVersionsEOLFetch = func() map[string]interface{} {
		return map[string]interface{}{"8.2": map[string]interface{}{"statusLabel": "stub"}}
	}
}

func newDomainsTestServer(t *testing.T, d *Domains, role string) (*httptest.Server, *http.Client) {
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
	d.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains", d.ServeList)
	mux.HandleFunc("POST /domains/add", d.HandleAdd)
	mux.HandleFunc("POST /domains/{feature}/toggle", d.HandleToggleFeature)
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

func withScratchCaddyDomainsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := CaddyDomainsConfDir
	CaddyDomainsConfDir = dir
	t.Cleanup(func() { CaddyDomainsConfDir = orig })
	return dir
}

func TestReadCaddyFileForDomainDetectsFields(t *testing.T) {
	dir := withScratchCaddyDomainsDir(t)
	os.WriteFile(filepath.Join(dir, "example.com.conf"), []byte(`
example.com {
	tls {
		on_demand
	}
	reverse_proxy localhost:8080
	header Strict-Transport-Security "max-age=31536000"
}
`), 0644)

	ssl, status, waf, hsts := readCaddyFileForDomain("example.com")
	if ssl != "automatic" || status != "active" || hsts != "on" {
		t.Fatalf("unexpected: ssl=%q status=%q waf=%q hsts=%q", ssl, status, waf, hsts)
	}
	if waf != "none" {
		t.Fatalf("expected default waf=none when no SecRuleEngine directive present, got %q", waf)
	}
}

func TestReadCaddyFileForDomainDefaultsWhenMissing(t *testing.T) {
	withScratchCaddyDomainsDir(t)

	ssl, status, waf, hsts := readCaddyFileForDomain("nonexistent.com")
	if ssl != "none" || status != "suspended" || waf != "none" || hsts != "off" {
		t.Fatalf("unexpected defaults: ssl=%q status=%q waf=%q hsts=%q", ssl, status, waf, hsts)
	}
}

func TestDomainsListJSON(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.Get(srv.URL + "/domains?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"domain_url":"example.com"`) {
		t.Fatalf("expected domain in JSON, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), `"status":"suspended"`) {
		t.Fatalf("expected default status (no caddy conf on disk), got %s", truncate(string(body)))
	}
}

func TestDomainsListRendersHTML(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(int64(1), "/var/www/html", "example.com", "8.2", "alice"))

	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.Get(srv.URL + "/domains")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"example.com", "alice", "function domainsTable", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestDomainsListMySQLDownFallsBackGracefully(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnError(sqlErrConnRefused{})

	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.Get(srv.URL + "/domains")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected graceful 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Is MySQL running?") {
		t.Fatalf("expected mysql-down warning, got %s", truncate(string(body)))
	}
}

func TestDomainsAddRequiresBothFields(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.PostForm(srv.URL+"/domains/add", url.Values{"domain": {"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Domain and username are required.") {
		t.Fatalf("expected validation flash, got %s", truncate(string(body)))
	}
}

func TestDomainsToggleFeatureDNSRejectsInvalidAction(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.PostForm(srv.URL+"/domains/dns/toggle", url.Values{
		"domain_name": {"example.com"}, "dns_action": {"bogus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "is not a valid DNS action") {
		t.Fatalf("expected invalid-action flash, got %s", truncate(string(body)))
	}
}

func TestDomainsToggleFeatureUnknownFeature(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	resp, err := client.PostForm(srv.URL+"/domains/bogus/toggle", url.Values{"domain_name": {"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Unknown feature: bogus") {
		t.Fatalf("expected unknown-feature flash, got %s", truncate(string(body)))
	}
}

func TestDomainsToggleFeatureSuspendGracefulWithoutOpenCLI(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	withScratchCaddyDomainsDir(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")

	// opencli isn't installed in this test environment -- this exercises
	// the graceful-failure path, matching the pattern used for other
	// opencli-backed handlers in this package
	resp, err := client.PostForm(srv.URL+"/domains/suspend/toggle", url.Values{"domain_name": {"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains" {
		t.Fatalf("expected redirect back to /domains, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Failed to executed SUSPEND") {
		t.Fatalf("expected the graceful-failure flash, got %s", truncate(string(body)))
	}
}
