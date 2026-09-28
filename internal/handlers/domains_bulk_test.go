package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseBulkDomains(t *testing.T) {
	text := "domain1.com|milan\n" +
		"domain2.rs|stefan|/var/www/html/somedir/here/\n" +
		"domain3.rs|stefan|/var/www/html/somedir/here/|8.3\n" +
		"\n# comment\n" +
		"domain4.rs stefan 8.2\n" +
		"Domain5.RS  stefan   8.1   /var/www/html/five\n" +
		"domain6.rs|stefan||8.4\n"
	lines, errs := parseBulkDomains(text, true)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []bulkDomainLine{
		{1, "domain1.com", "milan", "", ""},
		{2, "domain2.rs", "stefan", "/var/www/html/somedir/here/", ""},
		{3, "domain3.rs", "stefan", "/var/www/html/somedir/here/", "8.3"},
		{6, "domain4.rs", "stefan", "", "8.2"},
		{7, "domain5.rs", "stefan", "/var/www/html/five", "8.1"},
		{8, "domain6.rs", "stefan", "", "8.4"},
	}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d: %+v", len(want), len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: expected %+v, got %+v", i, want[i], lines[i])
		}
	}
}

func TestParseBulkDomainsErrors(t *testing.T) {
	cases := map[string]string{
		"onlydomain.com":                        "expected <domain>|<username>",
		"a.com|u|/var/www/html/x|8.3|extra":     "too many fields",
		"not_a_domain|stefan":                   "not a valid domain",
		"a.com|1stefan":                         "not a valid username",
		"a.com|stefan|/etc/passwd":              "must start with /var/www/html/",
		"a.com|stefan|/var/www/html/../etc":     "can't contain '..'",
		"a.com|stefan|8.3|8.2":                  "more than one PHP version",
		"a.com|stefan|latest":                   "neither a docroot",
		"a.com|stefan\nA.com|milan":             "already on line 1",
		"   \n# just a comment":                 "No domains to add",
		"a.com|stefan|/var/www/html/x;rm -rf /": "can only contain",
	}
	for text, want := range cases {
		_, errs := parseBulkDomains(text, true)
		if !strings.Contains(strings.Join(errs, " "), want) {
			t.Errorf("%q: expected an error containing %q, got %v", text, want, errs)
		}
	}

	if _, errs := parseBulkDomains("a.com|stefan|/var/www/html/x", false); !strings.Contains(strings.Join(errs, " "), "Enterprise") {
		t.Errorf("expected a custom docroot to be rejected without Enterprise, got %v", errs)
	}
}

func TestHandleBulkAddRunsEachDomain(t *testing.T) {
	origRun, origLicense := bulkDomainsRun, chromeSite.LicenseType
	t.Cleanup(func() { bulkDomainsRun, chromeSite.LicenseType = origRun, origLicense })
	chromeSite.LicenseType = "Enterprise"

	var calls []string
	bulkDomainsRun = func(args ...string) (bool, string) {
		calls = append(calls, strings.Join(args, " "))
		if args[2] == "fail.rs" {
			return false, "Domain already exists"
		}
		return true, "added"
	}

	d := &Domains{}
	post := func(text string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/domains/bulk-add", strings.NewReader(url.Values{"domains": {text}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		d.HandleBulkAdd(rec, req)
		return rec
	}

	if rec := post("bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid line, got %d", rec.Code)
	}

	if rec := post("ok.rs|stefan|/var/www/html/ok|8.3\nfail.rs stefan"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result bulkDomainsResult
	for i := 0; i < 100; i++ {
		pendingBulkDomainsMu.Lock()
		result = *pendingBulkDomains
		pendingBulkDomainsMu.Unlock()
		if result.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !result.Done || len(result.Added) != 1 || len(result.Failed) != 1 || result.Failed[0].Line != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if calls[0] != "opencli domains-add ok.rs stefan --docroot /var/www/html/ok --php_version 8.3" || calls[1] != "opencli domains-add fail.rs stefan" {
		t.Errorf("unexpected opencli calls: %v", calls)
	}
}
