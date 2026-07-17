package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newAPISystemMux(s *APISystem) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docker/info", s.ServeDockerInfo)
	mux.HandleFunc("GET /api/ips", s.ServeIPs)
	mux.HandleFunc("GET /api/system", s.ServeSystemInfo)
	mux.HandleFunc("GET /api/usage/cpu", s.ServeCPUUsage)
	mux.HandleFunc("GET /api/usage/memory", s.ServeMemoryUsage)
	mux.HandleFunc("GET /api/usage/server", s.ServeDiskUsage)
	return mux
}

func TestAPIServeDockerInfoSuccess(t *testing.T) {
	orig := apiDockerInfoRun
	apiDockerInfoRun = func() ([]byte, error) {
		return []byte(`{"host":{"os":"linux"}}`), nil
	}
	t.Cleanup(func() { apiDockerInfoRun = orig })

	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/docker/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	host, _ := body["host"].(map[string]interface{})
	if host == nil || host["os"] != "linux" {
		t.Fatalf("expected passthrough of podman info JSON, got %+v", body)
	}
}

func TestAPIServeDockerInfoCommandFailureIs500(t *testing.T) {
	orig := apiDockerInfoRun
	apiDockerInfoRun = func() ([]byte, error) { return nil, errors.New("podman not found") }
	t.Cleanup(func() { apiDockerInfoRun = orig })

	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/docker/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestAPIServeDockerInfoUnparseableOutputIs500(t *testing.T) {
	orig := apiDockerInfoRun
	apiDockerInfoRun = func() ([]byte, error) { return []byte("not json"), nil }
	t.Cleanup(func() { apiDockerInfoRun = orig })

	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/docker/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestAPIServeIPsReadsPerUserIPFile(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cols := []string{"id", "username", "plan_id", "name", "disk_limit", "inodes_limit", "cpu", "ram"}
	mock.ExpectQuery(`SELECT users\.\*`).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(1, "alice", 1, "basic", "1", "1", "1", "1").
			AddRow(2, "bob", 1, "basic", "1", "1", "1", "1"))

	baseDir := t.TempDir()
	origBase := apiIPFileBaseDir
	apiIPFileBaseDir = baseDir
	t.Cleanup(func() { apiIPFileBaseDir = origBase })

	os.MkdirAll(filepath.Join(baseDir, "alice"), 0755)
	os.WriteFile(filepath.Join(baseDir, "alice", "ip.json"), []byte(`{"ip":"1.2.3.4"}`), 0644)
	// bob has no ip.json at all -- should simply be omitted.

	s := &APISystem{MySQL: db}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ips")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if body["alice"] != "1.2.3.4" {
		t.Fatalf("expected alice's IP, got %+v", body)
	}
	if _, ok := body["bob"]; ok {
		t.Fatalf("expected bob (no ip.json) to be omitted, got %+v", body)
	}
}

func TestAPIServeSystemInfoReturnsExpectedFields(t *testing.T) {
	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/system")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	for _, key := range []string{"hostname", "os", "time", "kernel", "cpu", "openpanel_version", "uptime", "running_processes", "package_updates"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("expected field %q in response, got %+v", key, body)
		}
	}
}

func TestAPIServeMemoryUsageShape(t *testing.T) {
	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	ramInfo, _ := body["ram_info"].(map[string]interface{})
	if ramInfo == nil {
		t.Fatalf("expected a ram_info key, got %+v", body)
	}
	for _, key := range []string{"total", "available", "used", "percent"} {
		if _, ok := ramInfo[key]; !ok {
			t.Fatalf("expected ram_info.%s, got %+v", key, ramInfo)
		}
	}
	human, _ := body["human_readable_info"].(map[string]interface{})
	if human == nil {
		t.Fatalf("expected a human_readable_info key, got %+v", body)
	}
	if _, ok := body["swap_info"]; ok {
		t.Fatalf("expected no swap_info key on this endpoint's response, got %+v", body)
	}
}

func TestAPIServeDiskUsageServerShapeExcludesSnap(t *testing.T) {
	s := &APISystem{}
	srv := httptest.NewServer(newAPISystemMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/usage/server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	for _, entry := range body {
		mp, _ := entry["mountpoint"].(string)
		if len(mp) >= 5 && mp[:5] == "/snap" {
			t.Fatalf("expected /snap mountpoints excluded, got %+v", entry)
		}
		for _, key := range []string{"device", "mountpoint", "fstype", "total", "used", "free", "percent"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("expected field %q, got %+v", key, entry)
			}
		}
	}
}
