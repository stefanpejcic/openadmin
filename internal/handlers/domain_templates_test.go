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

func withScratchDomainTemplatePaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := domainTemplateFilePaths
	newPaths := make(map[string]string, len(orig))
	for k := range orig {
		newPaths[k] = filepath.Join(dir, k+".txt")
	}
	domainTemplateFilePaths = newPaths
	t.Cleanup(func() { domainTemplateFilePaths = orig })
}

func newDomainTemplatesTestServer(t *testing.T, d *DomainTemplates) (*httptest.Server, *http.Client) {
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
	mux.HandleFunc("GET /domains/file-templates", d.ServeDomainTemplates)
	mux.HandleFunc("POST /domains/file-templates", d.ServeDomainTemplates)
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

func TestServeDomainTemplatesGetJSON(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	os.WriteFile(domainTemplateFilePaths["docker_caddy"], []byte("caddy content"), 0644)

	d := &DomainTemplates{}
	srv, client := newDomainTemplatesTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/file-templates?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"docker_caddy":"caddy content"`) {
		t.Fatalf("expected docker_caddy content in JSON, got %s", body)
	}
	if !strings.Contains(string(body), `"default_page":""`) {
		t.Fatalf("expected other fields present but empty, got %s", body)
	}
}

func TestServeDomainTemplatesRendersHTML(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	os.WriteFile(domainTemplateFilePaths["docker_caddy"], []byte("caddy content"), 0644)

	d := &DomainTemplates{}
	srv, client := newDomainTemplatesTestServer(t, d)

	resp, err := client.Get(srv.URL + "/domains/file-templates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"caddy content", "Edit Domain Templates", "function restoreComponentFor", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeDomainTemplatesPostUpdatesOnlySubmittedFields(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	os.WriteFile(domainTemplateFilePaths["default_page"], []byte("old default"), 0644)
	os.WriteFile(domainTemplateFilePaths["docker_varnish"], []byte("old varnish"), 0644)

	d := &DomainTemplates{}
	srv, client := newDomainTemplatesTestServer(t, d)

	resp, err := client.PostForm(srv.URL+"/domains/file-templates", url.Values{"default_page": {"new default"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Templates updated successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, _ := os.ReadFile(domainTemplateFilePaths["default_page"])
	if string(saved) != "new default" {
		t.Fatalf("expected default_page updated, got %q", saved)
	}
	varnish, _ := os.ReadFile(domainTemplateFilePaths["docker_varnish"])
	if string(varnish) != "old varnish" {
		t.Fatalf("expected docker_varnish untouched (not submitted), got %q", varnish)
	}
}
