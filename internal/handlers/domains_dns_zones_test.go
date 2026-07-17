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

func withScratchBindZonesDir(t *testing.T) (zonesDir, backupDir string) {
	t.Helper()
	dir := t.TempDir()
	zonesDir = filepath.Join(dir, "zones")
	backupDir = filepath.Join(dir, "backups")
	if err := os.MkdirAll(zonesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	origZones, origBackup := BindZonesDir, DNSZoneBackupDir
	BindZonesDir, DNSZoneBackupDir = zonesDir, backupDir
	t.Cleanup(func() {
		BindZonesDir, DNSZoneBackupDir = origZones, origBackup
	})
	return zonesDir, backupDir
}

// stubDNSZoneValidate installs a dnsZoneValidateRun/dnsZoneReloadRun pair
// that never shells out to a real podman/named-checkzone/rndc binary, and
// restores the originals on test cleanup.
func stubDNSZoneValidate(t *testing.T, exitCode int, stderr string, reloadErr error) {
	t.Helper()
	origValidate, origReload := dnsZoneValidateRun, dnsZoneReloadRun
	dnsZoneValidateRun = func(domainName, zonePath string) (string, string, int, error) {
		return "", stderr, exitCode, nil
	}
	dnsZoneReloadRun = func() error { return reloadErr }
	t.Cleanup(func() {
		dnsZoneValidateRun = origValidate
		dnsZoneReloadRun = origReload
	})
}

func newDNSZoneEditorTestServer(t *testing.T, h *DNSZoneEditor) (*httptest.Server, *http.Client) {
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
	h.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/dns", h.ServeEditDNSZone)
	mux.HandleFunc("GET /domains/dns/{domain_name}", h.ServeEditDNSZone)
	mux.HandleFunc("POST /domains/dns/{domain_name}", h.ServeEditDNSZone)
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

func TestDNSZoneEditorListsDomainsWhenNoDomainGiven(t *testing.T) {
	withScratchBindZonesDir(t)

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html", "example.com", "8.2", "alice"))

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/dns")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Select domain name", "example.com", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestDNSZoneEditorInvalidDomainRedirectsWithFlash(t *testing.T) {
	withScratchBindZonesDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/dns/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/dns" {
		t.Fatalf("expected redirect to /domains/dns, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Invalid domain name format.") {
		t.Fatalf("expected invalid-domain flash, got %s", truncate(string(body)))
	}
}

func TestDNSZoneEditorGetReadsExistingZoneFile(t *testing.T) {
	zonesDir, _ := withScratchBindZonesDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	os.WriteFile(filepath.Join(zonesDir, "example.com.zone"), []byte("$TTL 3600\n@ IN SOA ns1 admin\n"), 0644)

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/dns/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"example.com", "$TTL 3600", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestDNSZoneEditorGetMissingZoneFileFlashesError(t *testing.T) {
	withScratchBindZonesDir(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/dns/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error reading DNS zone file for example.com.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestDNSZoneEditorPostSuccessWritesFileAndCleansBackup(t *testing.T) {
	zonesDir, backupDir := withScratchBindZonesDir(t)
	stubDNSZoneValidate(t, 0, "", nil)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	zonePath := filepath.Join(zonesDir, "example.com.zone")
	os.WriteFile(zonePath, []byte("old content\n"), 0644)

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/dns/example.com", url.Values{
		"bind_content": {"new zone content\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "saved successfully and DNS service reloaded.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	written, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "new zone content\n" {
		t.Fatalf("expected zone file to be updated, got %q", string(written))
	}

	leftoverBackups, _ := filepath.Glob(filepath.Join(backupDir, "example.com.zone.backup_*"))
	if len(leftoverBackups) != 0 {
		t.Fatalf("expected backup to be cleaned up after success, found %v", leftoverBackups)
	}
}

func TestDNSZoneEditorPostValidationFailureRevertsFile(t *testing.T) {
	zonesDir, _ := withScratchBindZonesDir(t)
	stubDNSZoneValidate(t, 1, "zone example.com/IN: NS 'ns1.example.com' has no address records", nil)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	zonePath := filepath.Join(zonesDir, "example.com.zone")
	os.WriteFile(zonePath, []byte("original content\n"), 0644)

	h := &DNSZoneEditor{MySQL: mysqlDB}
	srv, client := newDNSZoneEditorTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/dns/example.com", url.Values{
		"bind_content": {"broken zone content\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Zone file validation failed. Changes reverted. Error:") {
		t.Fatalf("expected validation-failure flash, got %s", truncate(string(body)))
	}

	reverted, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(reverted) != "original content\n" {
		t.Fatalf("expected zone file reverted to original content, got %q", string(reverted))
	}
}
