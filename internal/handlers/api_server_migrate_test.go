package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newAPIServerMigrateMux(m *APIServerMigrate) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/migrate", m.ServeMigrate)
	mux.HandleFunc("POST /api/server/migrate", m.ServeMigrate)
	return mux
}

func TestAPIServeMigratePostMissingFieldsRejected(t *testing.T) {
	withScratchMigratePaths(t)

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/migrate", "application/json", strings.NewReader(`{"host":"1.2.3.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "host, root and password are all required.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeMigratePostStartsProcess(t *testing.T) {
	withScratchMigratePaths(t)

	orig := migrateStartRun
	var gotHost, gotRoot, gotPassword string
	migrateStartRun = func(host, root, password string) error {
		gotHost, gotRoot, gotPassword = host, root, password
		return nil
	}
	t.Cleanup(func() { migrateStartRun = orig })

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/migrate", "application/json",
		strings.NewReader(`{"host":"1.2.3.4","root":"root","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if gotHost != "1.2.3.4" || gotRoot != "root" || gotPassword != "secret" {
		t.Fatalf("unexpected args: %s %s %s", gotHost, gotRoot, gotPassword)
	}
}

func TestAPIServeMigratePostStartFailureIs500(t *testing.T) {
	withScratchMigratePaths(t)

	orig := migrateStartRun
	migrateStartRun = func(host, root, password string) error { return errors.New("boom") }
	t.Cleanup(func() { migrateStartRun = orig })

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/migrate", "application/json",
		strings.NewReader(`{"host":"1.2.3.4","root":"root","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestAPIServeMigrateGetUnknownStatusWithoutPidFile(t *testing.T) {
	withScratchMigratePaths(t)

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/migrate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "unknown" {
		t.Fatalf("expected status=unknown, got %+v", body)
	}
}

func TestAPIServeMigrateGetFinishedWhenProcessGone(t *testing.T) {
	withScratchMigratePaths(t)
	os.WriteFile(MigrateLogPath, []byte("migration output"), 0644)
	os.WriteFile(MigrateProcessPIDFile, []byte("999999999"), 0644)

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/migrate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "finished" || body["output"] != "migration output" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestAPIServeMigrateGetCorruptPidFileIs500(t *testing.T) {
	withScratchMigratePaths(t)
	os.WriteFile(MigrateProcessPIDFile, []byte("not-a-pid"), 0644)

	m := &APIServerMigrate{}
	srv := httptest.NewServer(newAPIServerMigrateMux(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/migrate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}
