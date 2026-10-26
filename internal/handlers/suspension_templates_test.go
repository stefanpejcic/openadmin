package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withScratchSuspensionTemplatesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := SuspensionTemplatesDir
	SuspensionTemplatesDir = dir
	t.Cleanup(func() { SuspensionTemplatesDir = orig })
	return dir
}

func TestSuspensionSlugFromName(t *testing.T) {
	cases := map[string]string{
		"Copyright / DMCA Violation": "copyright-dmca-violation",
		"  Overdue Payment!! ":       "overdue-payment",
		"---":                        "",
		strings.Repeat("a", 80):      strings.Repeat("a", 64),
	}
	for in, want := range cases {
		if got := suspensionSlugFromName(in); got != want {
			t.Errorf("suspensionSlugFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanSuspensionNameCantBreakOutOfComment(t *testing.T) {
	got := cleanSuspensionName("Bad --> name\n# suspended_by: user")
	if strings.Contains(got, "-->") || strings.Contains(got, "\n") {
		t.Fatalf("unsafe name kept: %q", got)
	}
}

func TestSuspensionTemplatesRoundTrip(t *testing.T) {
	dir := withScratchSuspensionTemplatesDir(t)
	if err := saveSuspensionTemplate("dmca", "Copyright / DMCA", "<h1>DMCA</h1>\n"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "legacy.html"), []byte("<p>no header</p>"), 0644)
	os.WriteFile(filepath.Join(dir, "Bad Name.html"), []byte("x"), 0644)

	got := listSuspensionTemplates()
	want := []suspensionTemplate{
		{Slug: "dmca", Name: "Copyright / DMCA", Content: "<h1>DMCA</h1>\n"},
		{Slug: "legacy", Name: "legacy", Content: "<p>no header</p>"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !suspensionTemplateExists("dmca") || suspensionTemplateExists("../dmca") || suspensionTemplateExists("nope") {
		t.Fatal("suspensionTemplateExists gave the wrong answer")
	}
}

func TestReadDomainSuspension(t *testing.T) {
	dir := withScratchCaddyDomainsDir(t)
	conf := "# comment: unpaid since may\n# suspended_by: admin\n# reason: Overdue Payment\na.com {\n# suspended_by: user\n}\n"
	os.WriteFile(filepath.Join(dir, "a.com.conf"), []byte(conf), 0644)

	got := readDomainSuspension("a.com")
	want := domainSuspension{By: "admin", Reason: "Overdue Payment", Comment: "unpaid since may"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if (readDomainSuspension("missing.com") != domainSuspension{}) {
		t.Fatal("missing file should give an empty suspension")
	}
}

func TestSuspensionTemplatesFormAddRenameDelete(t *testing.T) {
	dir := withScratchSuspensionTemplatesDir(t)
	caddyDir := withScratchCaddyDomainsDir(t)

	post := func(form url.Values) (string, string) {
		r := httptest.NewRequest(http.MethodPost, "/domains/file-templates", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.ParseForm()
		return saveSuspensionTemplatesForm(r)
	}

	if msg, kind := post(url.Values{"new_reason_name": {"Phishing"}, "new_reason_content": {"<p>bad</p>"}}); kind != "success" {
		t.Fatalf("add failed: %s", msg)
	}
	if _, kind := post(url.Values{"new_reason_name": {"phishing"}}); kind != "error" {
		t.Fatal("duplicate slug should be refused")
	}

	post(url.Values{"reason_name__phishing": {"Phishing / Malware"}, "reason_content__phishing": {"<p>new</p>"}})
	b, _ := os.ReadFile(filepath.Join(dir, "phishing.html"))
	if string(b) != "<!-- name: Phishing / Malware -->\n<p>new</p>" {
		t.Fatalf("rename/edit not saved, got %q", b)
	}

	os.WriteFile(filepath.Join(caddyDir, "a.com.conf"), []byte("rewrite * /suspended/phishing.html\n"), 0644)
	if msg, kind := post(url.Values{"delete_reason": {"phishing"}}); kind != "error" || !strings.Contains(msg, "a.com") {
		t.Fatalf("in-use template should not be deleted, got %q %q", msg, kind)
	}
	os.Remove(filepath.Join(caddyDir, "a.com.conf"))
	if _, kind := post(url.Values{"delete_reason": {"phishing"}}); kind != "success" {
		t.Fatal("delete failed")
	}
	if suspensionTemplateExists("phishing") {
		t.Fatal("template still on disk")
	}
}

func TestHandleDomainActionSuspendPassesAdminAndTemplate(t *testing.T) {
	withScratchSuspensionTemplatesDir(t)
	saveSuspensionTemplate("dmca", "DMCA", "x")
	srv, client, _ := newAPIDomainsTestServer(t)

	var got []string
	orig := apiRunCapture
	apiRunCapture = func(args ...string) (string, string, int) { got = args; return "ok", "", 0 }
	t.Cleanup(func() { apiRunCapture = orig })

	body, _ := json.Marshal(map[string]string{"template": "dmca", "comment": "ticket 42"})
	resp, err := client.Post(srv.URL+"/api/domains/suspend/a.com", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	want := []string{"opencli", "domains-suspend", "a.com", "--by=admin", "--template=dmca", "--comment=ticket 42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	resp, _ = client.Post(srv.URL+"/api/domains/suspend/a.com", "application/json", strings.NewReader(`{"template":"nope"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown template should be 400, got %d", resp.StatusCode)
	}

	resp, _ = client.Post(srv.URL+"/api/domains/unsuspend/a.com", "application/json", nil)
	resp.Body.Close()
	if !reflect.DeepEqual(got, []string{"opencli", "domains-unsuspend", "a.com", "--by=admin"}) {
		t.Fatalf("unsuspend args: %v", got)
	}
}
