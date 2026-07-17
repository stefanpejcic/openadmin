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

func newAPISettingsCustomCodeTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsCustomCode{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/custom-code", a.Serve)
	mux.HandleFunc("POST /api/settings/custom-code", a.Serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsCustomCodeGetReturnsAllFields(t *testing.T) {
	withScratchCustomCodePaths(t)
	os.WriteFile(customCodeFilePaths["custom_css"], []byte("body{}"), 0644)

	srv, client := newAPISettingsCustomCodeTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/custom-code")
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
	if out["custom_css"] != "body{}" {
		t.Fatalf("expected custom_css content, got %+v", out)
	}
	if len(out) != len(customCodeFieldOrder) {
		t.Fatalf("expected all %d fields present, got %d: %+v", len(customCodeFieldOrder), len(out), out)
	}
}

func TestAPISettingsCustomCodePostRequiresJSON(t *testing.T) {
	withScratchCustomCodePaths(t)
	srv, client := newAPISettingsCustomCodeTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/custom-code", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

// TestAPISettingsCustomCodePostWritesEnterpriseFieldWithoutGating locks in
// the deliberate divergence from the HTML admin page: this API endpoint
// applies no Enterprise-license/role gating, so an Enterprise-only field
// like custom_js gets written unconditionally.
func TestAPISettingsCustomCodePostWritesEnterpriseFieldWithoutGating(t *testing.T) {
	withScratchCustomCodePaths(t)
	srv, client := newAPISettingsCustomCodeTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/custom-code", `{"custom_js": "alert(1)"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	saved, err := os.ReadFile(customCodeFilePaths["custom_js"])
	if err != nil {
		t.Fatalf("expected custom_js written: %v", err)
	}
	if string(saved) != "alert(1)" {
		t.Fatalf("expected alert(1) written, got %q", saved)
	}
	if _, err := os.Stat(CustomCodeRestartFlagPath); err != nil {
		t.Fatal("expected restart-needed flag file to be written")
	}
}

func TestAPISettingsCustomCodePostSkipsAbsentFields(t *testing.T) {
	withScratchCustomCodePaths(t)
	os.WriteFile(customCodeFilePaths["custom_css"], []byte("original"), 0644)

	srv, client := newAPISettingsCustomCodeTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/custom-code", `{"custom_js": "new"}`)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, err := os.ReadFile(customCodeFilePaths["custom_css"])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "original" {
		t.Fatalf("expected custom_css left untouched since it was absent from the body, got %q", saved)
	}
}
