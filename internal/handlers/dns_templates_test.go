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

func withScratchDNSTemplatePaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origIPv4, origIPv6 := DNSZoneTemplateIPv4Path, DNSZoneTemplateIPv6Path
	DNSZoneTemplateIPv4Path = filepath.Join(dir, "zone_template.txt")
	DNSZoneTemplateIPv6Path = filepath.Join(dir, "zone_template_ipv6.txt")
	t.Cleanup(func() {
		DNSZoneTemplateIPv4Path, DNSZoneTemplateIPv6Path = origIPv4, origIPv6
	})
}

func newDNSTemplatesTestServer(t *testing.T, d *DNSTemplates) (*httptest.Server, *http.Client) {
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
	d.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/zone-templates", d.ServeDNSZoneTemplates)
	mux.HandleFunc("POST /domains/zone-templates", d.ServeDNSZoneTemplates)
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

func TestServeDNSZoneTemplatesGetJSON(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	os.WriteFile(DNSZoneTemplateIPv4Path, []byte("ipv4 content"), 0644)
	os.WriteFile(DNSZoneTemplateIPv6Path, []byte("ipv6 content"), 0644)

	d := &DNSTemplates{}
	srv, client := newDNSTemplatesTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/zone-templates?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"zone_template_ipv4":"ipv4 content"`) {
		t.Fatalf("expected ipv4 content in JSON, got %s", body)
	}
}

func TestServeDNSZoneTemplatesRendersHTML(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	os.WriteFile(DNSZoneTemplateIPv4Path, []byte("ipv4 content"), 0644)
	os.WriteFile(DNSZoneTemplateIPv6Path, []byte("ipv6 content"), 0644)

	d := &DNSTemplates{}
	srv, client := newDNSTemplatesTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/zone-templates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"ipv4 content", "ipv6 content", "Edit Zone Templates", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeDNSZoneTemplatesPostUpdatesOnlySubmittedFields(t *testing.T) {
	withScratchDNSTemplatePaths(t)
	os.WriteFile(DNSZoneTemplateIPv4Path, []byte("old ipv4"), 0644)
	os.WriteFile(DNSZoneTemplateIPv6Path, []byte("old ipv6"), 0644)

	d := &DNSTemplates{}
	srv, client := newDNSTemplatesTestServer(t, d)

	resp, err := client.PostForm(srv.URL+"/domains/zone-templates", url.Values{"zone_template_ipv4": {"new ipv4"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Template updated successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	ipv4, _ := os.ReadFile(DNSZoneTemplateIPv4Path)
	if string(ipv4) != "new ipv4" {
		t.Fatalf("expected ipv4 updated, got %q", ipv4)
	}
	ipv6, _ := os.ReadFile(DNSZoneTemplateIPv6Path)
	if string(ipv6) != "old ipv6" {
		t.Fatalf("expected ipv6 untouched (not submitted), got %q", ipv6)
	}
}
