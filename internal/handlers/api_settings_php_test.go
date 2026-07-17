package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newAPISettingsPHPTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsPHP{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/php", a.Serve)
	mux.HandleFunc("POST /api/settings/php", a.Serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsPHPGetReturnsAllFiles(t *testing.T) {
	withScratchPHPPaths(t)
	os.WriteFile(phpOptionsPath, []byte("memory_limit=256M"), 0644)
	os.WriteFile(phpIniPaths["php82"], []byte("upload_max_filesize=64M"), 0644)

	srv, client := newAPISettingsPHPTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/php")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("expected valid JSON: %v (%s)", err, body)
	}
	if out["options"] != "memory_limit=256M" {
		t.Fatalf("expected options content, got %+v", out)
	}
	if out["php82"] != "upload_max_filesize=64M" {
		t.Fatalf("expected php82 content, got %+v", out)
	}
	if out["php56"] != "" {
		t.Fatalf("expected empty string for a missing file, got %q", out["php56"])
	}
}

func TestAPISettingsPHPPostRequiresJSON(t *testing.T) {
	withScratchPHPPaths(t)
	srv, client := newAPISettingsPHPTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/php", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsPHPPostUpdatesOptionsAndVersions(t *testing.T) {
	withScratchPHPPaths(t)
	srv, client := newAPISettingsPHPTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/php", `{"options": "memory_limit=512M", "php82": "opcache.enable=1"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Success bool     `json:"success"`
		Updated []string `json:"updated"`
	}
	json.Unmarshal(body, &out)
	if !out.Success {
		t.Fatal("expected success=true")
	}
	if len(out.Updated) != 2 {
		t.Fatalf("expected 2 updated entries, got %+v", out.Updated)
	}

	savedOptions, _ := os.ReadFile(phpOptionsPath)
	if string(savedOptions) != "memory_limit=512M" {
		t.Fatalf("expected options.txt written, got %q", savedOptions)
	}
	savedIni, _ := os.ReadFile(phpIniPaths["php82"])
	if string(savedIni) != "opcache.enable=1" {
		t.Fatalf("expected php82 ini written, got %q", savedIni)
	}
}

func TestAPISettingsPHPPostIgnoresUnknownKeys(t *testing.T) {
	withScratchPHPPaths(t)
	srv, client := newAPISettingsPHPTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/php", `{"php99": "irrelevant"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Updated []string `json:"updated"`
	}
	json.Unmarshal(body, &out)
	if len(out.Updated) != 0 {
		t.Fatalf("expected no fields updated for an unrecognized key, got %+v", out.Updated)
	}
}
