package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newAPIDomainFilesTestServer(t *testing.T, a *APIDomainFiles) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/{domain_name}/caddy", a.ServeDomainCaddyConfig)
	mux.HandleFunc("POST /domains/{domain_name}/caddy", a.ServeDomainCaddyConfig)
	mux.HandleFunc("GET /domains/{domain_name}/vhost/{username}", a.ServeDomainVHostConfig)
	mux.HandleFunc("POST /domains/{domain_name}/vhost/{username}", a.ServeDomainVHostConfig)
	mux.HandleFunc("GET /domains/file-templates", a.ServeDomainFileTemplates)
	mux.HandleFunc("POST /domains/file-templates", a.ServeDomainFileTemplates)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// --- GET/POST /domains/{domain_name}/caddy ---

func TestAPIDomainCaddyConfigInvalidDomainReturns400(t *testing.T) {
	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/not-a-domain/caddy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid domain name.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainCaddyConfigGetMissingFileReturns404(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/caddy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Caddy file for domain example.com not found") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainCaddyConfigGetReturnsExistingContent(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	os.WriteFile(filepath.Join(confDir, "example.com.conf"), []byte("example.com {\n}\n"), 0644)

	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/caddy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["domain"] != "example.com" || decoded["content"] != "example.com {\n}\n" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainCaddyConfigPostRequiresContent(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/caddy", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "content is required") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainCaddyConfigPostSuccessWritesAndCleansBackup(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	backupDir := withScratchCaddyBackupDir(t)
	stubCaddyValidate(t, 0, "", nil)

	confPath := filepath.Join(confDir, "example.com.conf")
	os.WriteFile(confPath, []byte("old\n"), 0644)

	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/caddy", "application/json", strings.NewReader(`{"content":"new\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "saved successfully and reloaded.") {
		t.Fatalf("unexpected body: %s", body)
	}

	written, _ := os.ReadFile(confPath)
	if string(written) != "new\n" {
		t.Fatalf("expected file updated, got %q", written)
	}
	leftovers, _ := filepath.Glob(filepath.Join(backupDir, "example.com.conf.backup_*"))
	if len(leftovers) != 0 {
		t.Fatalf("expected backup cleaned up, found %v", leftovers)
	}
}

func TestAPIDomainCaddyConfigPostValidationFailureReverts(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	withScratchCaddyBackupDir(t)
	stubCaddyValidate(t, 1, "adapting config using caddyfile: parse error", nil)

	confPath := filepath.Join(confDir, "example.com.conf")
	os.WriteFile(confPath, []byte("original\n"), 0644)

	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/caddy", "application/json", strings.NewReader(`{"content":"broken\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Caddyfile validation failed. Changes were reverted. Error:") {
		t.Fatalf("unexpected body: %s", body)
	}
	reverted, _ := os.ReadFile(confPath)
	if string(reverted) != "original\n" {
		t.Fatalf("expected file reverted, got %q", reverted)
	}
}

// --- GET/POST /domains/{domain_name}/vhost/{username} ---

func TestAPIDomainVHostConfigMissingUserReturns404(t *testing.T) {
	withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("nobody").
		WillReturnError(sql.ErrNoRows)

	a := &APIDomainFiles{MySQL: mysqlDB}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/vhost/nobody")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "No context found for user") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainVHostConfigGetMissingFileReturns404(t *testing.T) {
	withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	a := &APIDomainFiles{MySQL: mysqlDB}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/vhost/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "VirtualHosts file for domain example.com not found") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainVHostConfigGetReturnsExistingContent(t *testing.T) {
	dir := withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	confDir := filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data")
	os.MkdirAll(confDir, 0755)
	os.WriteFile(filepath.Join(confDir, "example.com.conf"), []byte("<VirtualHost *:80>\n</VirtualHost>\n"), 0644)

	a := &APIDomainFiles{MySQL: mysqlDB}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/vhost/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["domain"] != "example.com" || !strings.Contains(decoded["content"].(string), "VirtualHost") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainVHostConfigPostWritesAndRestarts(t *testing.T) {
	dir := withScratchVHostHomeDir(t)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	os.MkdirAll(filepath.Join(dir, "alice-ctx"), 0755)
	os.WriteFile(filepath.Join(dir, "alice-ctx", ".env"), []byte("WEB_SERVER=nginx\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data"), 0755)

	origRestart := apiVHostRestartRun
	var restartedContext, restartedWebserver string
	apiVHostRestartRun = func(context, webserver string) error {
		restartedContext, restartedWebserver = context, webserver
		return nil
	}
	t.Cleanup(func() { apiVHostRestartRun = origRestart })

	a := &APIDomainFiles{MySQL: mysqlDB}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/vhost/alice", "application/json", strings.NewReader(`{"content":"<VirtualHost *:80>\nServerName example.com\n</VirtualHost>\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "saved successfully and nginx restarted.") {
		t.Fatalf("unexpected body: %s", body)
	}
	if restartedContext != "alice-ctx" || restartedWebserver != "nginx" {
		t.Fatalf("expected restart of alice-ctx/nginx, got %q/%q", restartedContext, restartedWebserver)
	}

	confPath := filepath.Join(dir, "alice-ctx", "docker-data", "volumes", "alice-ctx_webserver_data", "_data", "example.com.conf")
	written, _ := os.ReadFile(confPath)
	if !strings.Contains(string(written), "ServerName example.com") {
		t.Fatalf("expected file written, got %q", written)
	}
}

// --- GET/POST /domains/file-templates ---

func TestAPIDomainFileTemplatesGet(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	os.WriteFile(domainTemplateFilePaths["docker_caddy"], []byte("caddy tmpl"), 0644)

	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/file-templates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]string
	json.Unmarshal(body, &decoded)
	if decoded["docker_caddy"] != "caddy tmpl" || decoded["default_page"] != "" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainFileTemplatesPostRequiresJSON(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/file-templates", "application/x-www-form-urlencoded", strings.NewReader("default_page=x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDomainFileTemplatesPostUpdatesOnlySubmittedFields(t *testing.T) {
	withScratchDomainTemplatePaths(t)
	os.WriteFile(domainTemplateFilePaths["default_page"], []byte("old default"), 0644)
	os.WriteFile(domainTemplateFilePaths["docker_varnish"], []byte("old varnish"), 0644)

	a := &APIDomainFiles{}
	srv, client := newAPIDomainFilesTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/file-templates", "application/json", strings.NewReader(`{"default_page":"new default"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Templates updated successfully!") {
		t.Fatalf("unexpected body: %s", body)
	}

	saved, _ := os.ReadFile(domainTemplateFilePaths["default_page"])
	if string(saved) != "new default" {
		t.Fatalf("expected default_page updated, got %q", saved)
	}
	varnish, _ := os.ReadFile(domainTemplateFilePaths["docker_varnish"])
	if string(varnish) != "old varnish" {
		t.Fatalf("expected docker_varnish untouched, got %q", varnish)
	}
}
