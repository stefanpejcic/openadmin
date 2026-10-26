package handlers

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestParseFTPAccountsQuotaField(t *testing.T) {
	raw := `USERS="a@example.com|p|/home/bob/docker-data/volumes/bob_html_data/_data/|1000|1000|500 b@example.com|p|/home/bob/docker-data/volumes/bob_html_data/_data/x|1000|1000 c@example.com|p|/home/bob/x_data/|1000|1000|junk"`
	accounts := parseFTPAccounts(raw)
	if len(accounts) != 3 {
		t.Fatalf("expected 6-field entries to be kept, got %d: %+v", len(accounts), accounts)
	}
	if accounts[0].QuotaMB != 500 || accounts[1].QuotaMB != 0 || accounts[2].QuotaMB != 0 {
		t.Fatalf("unexpected quotas: %d %d %d", accounts[0].QuotaMB, accounts[1].QuotaMB, accounts[2].QuotaMB)
	}
}

func TestSetFTPQuotaUsage(t *testing.T) {
	withScratchFTPPaths(t)
	os.MkdirAll(filepath.Join(FTPUsersDir, "bob"), 0755)
	os.WriteFile(filepath.Join(FTPUsersDir, "bob", "quota.usage"), []byte("a@example.com|2048|1|1\nb@example.com|5|0|0\n"), 0644)

	accounts := parseFTPAccounts(`USERS="a@example.com|p|/home/bob/docker-data/volumes/bob_html_data/_data/|1000|1000|1 b@example.com|p|/home/bob/docker-data/volumes/bob_html_data/_data/|1000|1000"`)
	setFTPQuotaUsage(accounts)

	a := accounts[0]
	if !a.HasUsage || !a.OverQuota || a.UsedKB != 2048 || a.UsedPercent() != 100 {
		t.Fatalf("expected a@ over quota at 100%%: %+v", a)
	}
	if a.UsedLabel() != "2.0 MB" || a.QuotaLabel() != "1 MB" || a.QuotaValue() != "1M" {
		t.Fatalf("labels: %q %q %q", a.UsedLabel(), a.QuotaLabel(), a.QuotaValue())
	}
	if accounts[1].HasUsage {
		t.Fatalf("unlimited account should not show usage: %+v", accounts[1])
	}
	if got := (ftpAccount{QuotaMB: 2048}).QuotaValue(); got != "2G" {
		t.Fatalf("QuotaValue(2048) = %q", got)
	}
}

func TestNormalizeFTPQuota(t *testing.T) {
	cases := map[string]string{"": "unlimited", "0": "unlimited", "unlimited": "unlimited", "500": "500M", "500m": "500M", "2G": "2G", "2gb": "2G"}
	for in, want := range cases {
		if got, ok := normalizeFTPQuota(in); !ok || got != want {
			t.Errorf("normalizeFTPQuota(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"abc", "-1", "1.5G", "5T", "1; reboot"} {
		if _, ok := normalizeFTPQuota(bad); ok {
			t.Errorf("normalizeFTPQuota(%q) should be rejected", bad)
		}
	}
}

func TestServeQuotaRunsOpencliWithOwner(t *testing.T) {
	usersFile, _ := withScratchFTPPaths(t)
	os.WriteFile(usersFile, []byte(`USERS="ftp1@example.com|p|/home/alice/docker-data/volumes/alice_html_data/_data/|1000|1000"`), 0644)

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html/example.com", "example.com", "8.3", "alice"))

	var got []string
	origRun := ftpQuotaRun
	ftpQuotaRun = func(user, quota, owner string) (string, error) {
		got = []string{user, quota, owner}
		return "Success: FTP quota for user '" + user + "' set to 2G.", nil
	}
	t.Cleanup(func() { ftpQuotaRun = origRun })

	f := &FTP{MySQL: mysqlDB}
	srv, client := newFTPTestServer(t, f)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.PostForm(srv.URL+"/services/ftp/quota", url.Values{"user": {"ftp1@example.com"}, "quota": {"2g"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", resp.StatusCode)
	}
	if strings.Join(got, " ") != "ftp1@example.com 2G alice" {
		t.Fatalf("opencli called with %v", got)
	}
}

func TestServeQuotaRejectsBadInput(t *testing.T) {
	withScratchFTPPaths(t)
	called := false
	origRun := ftpQuotaRun
	ftpQuotaRun = func(string, string, string) (string, error) { called = true; return "", nil }
	t.Cleanup(func() { ftpQuotaRun = origRun })

	f := &FTP{}
	srv, client := newFTPTestServer(t, f)
	for _, form := range []url.Values{
		{"user": {"not an account"}, "quota": {"1G"}},
		{"user": {"ftp1@example.com"}, "quota": {"lots"}},
		{"user": {"missing@example.com"}, "quota": {"1G"}},
	} {
		resp, err := client.PostForm(srv.URL+"/services/ftp/quota", form)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if called {
		t.Fatal("opencli should not run for invalid input or unknown accounts")
	}
}

func TestFTPBulkRoute(t *testing.T) {
	call, _ := ftpBulkRoute("quota", "500M", "a@example.com")
	if call == nil || call.Path != "/services/ftp/quota" || call.Form.Get("quota") != "500M" || call.Form.Get("user") != "a@example.com" {
		t.Fatalf("unexpected quota call: %+v", call)
	}
	call, _ = ftpBulkRoute("quota_remove", "", "a@example.com")
	if call == nil || call.Form.Get("quota") != "unlimited" {
		t.Fatalf("unexpected quota_remove call: %+v", call)
	}
	if call, _ := ftpBulkRoute("nope", "", "a@example.com"); call != nil {
		t.Fatal("unknown action should be skipped")
	}
}
