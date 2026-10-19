package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/auth"
)

// newEmailsForAdd fakes a running mailserver and one domain owned by bob
func newEmailsForAdd(t *testing.T) (*Emails, *[][]string) {
	t.Helper()
	dir := t.TempDir()
	origCompose, origPs, origRun := EmailsMailComposeFile, emailsPodmanPsRun, emailAddRun
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	os.WriteFile(EmailsMailComposeFile, nil, 0o644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return emailsMailserverContainerName, nil }
	var calls [][]string
	emailAddRun = func(args ...string) (string, string, error) {
		calls = append(calls, args)
		return "", "", nil
	}
	t.Cleanup(func() { EmailsMailComposeFile, emailsPodmanPsRun, emailAddRun = origCompose, origPs, origRun })

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html/example.com", "Example.com", "8.3", "bob"))
	return &Emails{MySQL: db, Sessions: auth.NewManager("test-secret", false)}, &calls
}

func postEmailNew(e *Emails, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/emails/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeEmailsNew(rec, req)
	return rec
}

func TestServeEmailsNewRunsOpencli(t *testing.T) {
	e, calls := newEmailsForAdd(t)
	rec := postEmailNew(e, url.Values{"domain": {"example.com"}, "username": {"info"}, "password": {"Secret123x"}, "quota": {"2"}, "unit": {"G"}, "send_welcome": {"1"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	want := [][]string{
		{"email-setup", "email", "add", "info@example.com", "Secret123x", "--wait", "--send-welcome"},
		{"email-setup", "quota", "set", "info@example.com", "2G"},
	}
	if len(*calls) != 2 || strings.Join((*calls)[0], " ") != strings.Join(want[0], " ") || strings.Join((*calls)[1], " ") != strings.Join(want[1], " ") {
		t.Errorf("got opencli calls %v", *calls)
	}
}

func TestServeEmailsNewRejects(t *testing.T) {
	cases := map[string]url.Values{
		"unknown domain": {"domain": {"other.com"}, "username": {"info"}, "password": {"Secret123x"}},
		"bad username":   {"domain": {"example.com"}, "username": {"a@b"}, "password": {"Secret123x"}},
		"short password": {"domain": {"example.com"}, "username": {"info"}, "password": {"short"}},
	}
	for name, form := range cases {
		e, calls := newEmailsForAdd(t)
		postEmailNew(e, form)
		if len(*calls) != 0 {
			t.Errorf("%s: opencli should not run, got %v", name, *calls)
		}
	}
}

func TestCheckDeliverability(t *testing.T) {
	dir := t.TempDir()
	orig, origLookup := DKIMKeysBasePath, lookupTXT
	DKIMKeysBasePath = dir
	t.Cleanup(func() { DKIMKeysBasePath, lookupTXT = orig, origLookup })
	os.MkdirAll(filepath.Join(dir, "good.com"), 0o755)
	os.WriteFile(filepath.Join(dir, "good.com", "mail.txt"), []byte(`mail._domainkey IN TXT ( "v=DKIM1; k=rsa; " "p=ABC" )`), 0o644)

	dns := map[string][]string{
		"mail._domainkey.good.com": {"v=DKIM1; k=rsa; p=ABC"},
		"good.com":                 {"google-site-verification=x", "v=spf1 ip4:1.2.3.4 +a +mx ~all"},
		"_dmarc.good.com":          {"v=DMARC1; p=none;"},
		"bad.com":                  {"v=spf1 ip4:9.9.9.9 ~all"},
	}
	lookupTXT = func(ctx context.Context, name string) []string { return dns[name] }

	good := checkDeliverability(context.Background(), "good.com", "1.2.3.4", `@ IN TXT "v=spf1 ip4:{server_ip} -all"`)
	if !good.OK || good.SPF.Expected != "v=spf1 ip4:1.2.3.4 -all" {
		t.Errorf("good.com: %+v", good)
	}
	bad := checkDeliverability(context.Background(), "bad.com", "1.2.3.4", "")
	if bad.OK || bad.DKIM.Status != "missing" || bad.SPF.Status != "wrong_ip" || bad.DMARC.Status != "missing" {
		t.Errorf("bad.com: %+v", bad)
	}
}
