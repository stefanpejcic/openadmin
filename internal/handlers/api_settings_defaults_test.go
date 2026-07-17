package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestAPISettingsDefaultsGetReturnsGroupsAndServices(t *testing.T) {
	withScratchDefaultsPaths(t)
	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) { return map[string]phpVersionStatus{}, nil }
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	os.WriteFile(DefaultsEnvPath, []byte("VARNISH=\"0\"\n"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n  nginx:\n    image: nginx\n"), 0644)

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/defaults", nil)
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaults(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	services, _ := body["autostart_available_services"].([]interface{})
	if len(services) != 1 || services[0] != "nginx" {
		t.Fatalf("expected [nginx], got %v", body["autostart_available_services"])
	}
}

func TestAPISettingsDefaultsGetPHPWatchErrorReturns500(t *testing.T) {
	withScratchDefaultsPaths(t)
	origPHP := defaultsPHPWatchRun
	defaultsPHPWatchRun = func() (map[string]phpVersionStatus, error) { return nil, &ftpStubError{"boom"} }
	t.Cleanup(func() { defaultsPHPWatchRun = origPHP })

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/defaults", nil)
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaults(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestAPISettingsDefaultsPostMissingEnvFileReturns404(t *testing.T) {
	withScratchDefaultsPaths(t)
	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/defaults", strings.NewReader(`{"values":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaults(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPISettingsDefaultsPostUpdatesValuesAndServices(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte("VARNISH=\"0\"\nSOME_RAM=\"512\"\n#PROXY_HTTP_PORT=8080\n"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services:\n  nginx:\n    image: nginx\n  apache:\n    image: apache\n"), 0644)

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/defaults",
		strings.NewReader(`{"values":{"VARNISH":"1","SOME_RAM":"1024"},"services":["nginx","bogus"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaults(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	saved, _ := os.ReadFile(DefaultsEnvPath)
	if !strings.Contains(string(saved), `VARNISH="1"`) {
		t.Fatalf("expected VARNISH updated, got %s", saved)
	}
	if !strings.Contains(string(saved), "PROXY_HTTP_PORT=8080") || strings.Contains(string(saved), "#PROXY_HTTP_PORT=8080") {
		t.Fatalf("expected PROXY_HTTP_PORT uncommented once varnish enabled, got %s", saved)
	}
	autostart, _ := os.ReadFile(DefaultsAutostartServicesPath)
	if strings.TrimSpace(string(autostart)) != "nginx" {
		t.Fatalf("expected only the valid service nginx to be saved, got %q", autostart)
	}
}

func TestAPISettingsDefaultsFilesGetReturnsContent(t *testing.T) {
	withScratchDefaultsPaths(t)
	os.WriteFile(DefaultsEnvPath, []byte("A=1\n"), 0644)
	os.WriteFile(DefaultsComposeFilePath, []byte("services: {}\n"), 0644)

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/defaults/files", nil)
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFiles(rec, req)

	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["env"] != "A=1\n" || body["compose"] != "services: {}\n" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestAPISettingsDefaultsFilesPostWritesFiles(t *testing.T) {
	withScratchDefaultsPaths(t)
	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/defaults/files",
		strings.NewReader(`{"env":"NEW=1\n","compose":"services:\n  x: {}\n"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	saved, _ := os.ReadFile(DefaultsEnvPath)
	if string(saved) != "NEW=1\n" {
		t.Fatalf("expected env file to be overwritten, got %q", saved)
	}
}

func TestAPISettingsDefaultsFilesDeleteResetsFromRemote(t *testing.T) {
	withScratchDefaultsPaths(t)
	origFetch := defaultsFetchRemoteRun
	defaultsFetchRemoteRun = func(url string) (string, int, error) {
		return "remote content for " + url, http.StatusOK, nil
	}
	t.Cleanup(func() { defaultsFetchRemoteRun = origFetch })

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodDelete, "/api/settings/defaults/files", nil)
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	saved, _ := os.ReadFile(DefaultsEnvPath)
	if !strings.Contains(string(saved), "remote content for") {
		t.Fatalf("expected env file reset from remote, got %q", saved)
	}
}

func TestAPISettingsDefaultsFilesDeleteRemoteFailureReturns500(t *testing.T) {
	withScratchDefaultsPaths(t)
	origFetch := defaultsFetchRemoteRun
	defaultsFetchRemoteRun = func(url string) (string, int, error) {
		return "", http.StatusNotFound, nil
	}
	t.Cleanup(func() { defaultsFetchRemoteRun = origFetch })

	d := &APISettingsDefaults{}
	req := httptest.NewRequest(http.MethodDelete, "/api/settings/defaults/files", nil)
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFiles(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAPISettingsDefaultsFilesForUserFoundContextReturns200 only checks
// that a resolved context reaches the (empty, since /home/<context> won't
// exist in the test sandbox) file-content response rather than a 404 --
// the handler builds its file paths from a hardcoded "/home/" prefix, so
// exercising a real read/write round trip isn't safe to do from a test.
func TestAPISettingsDefaultsFilesForUserFoundContextReturns200(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery("SELECT server FROM users").
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx-that-does-not-exist-on-disk"))

	d := &APISettingsDefaults{MySQL: mysqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/defaults/files/alice", nil)
	req.SetPathValue("username", "alice")
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFilesForUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["env"] != "" || body["compose"] != "" {
		t.Fatalf("expected empty content for a nonexistent context dir, got %v", body)
	}
}

func TestAPISettingsDefaultsFilesForUserUnknownUserReturns404(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery("SELECT server FROM users").
		WithArgs("nobody").
		WillReturnError(sql.ErrNoRows)

	d := &APISettingsDefaults{MySQL: mysqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/defaults/files/nobody", nil)
	req.SetPathValue("username", "nobody")
	rec := httptest.NewRecorder()
	d.ServeSettingsDefaultsFilesForUser(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
