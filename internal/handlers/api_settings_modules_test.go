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

func newAPISettingsModulesTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsModules{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/modules", a.Serve)
	mux.HandleFunc("POST /api/settings/modules", a.Serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsModulesGetSuccess(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte("enabled_modules=\"dns,phpmyadmin\"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[{"name":"dns"},{"name":"malware_scan"}]`), 0644)

	srv, client := newAPISettingsModulesTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/modules")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Features []map[string]interface{} `json:"features"`
		Plugins  []map[string]string       `json:"plugins"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("expected valid JSON: %v (%s)", err, body)
	}
	if len(out.Features) != 2 {
		t.Fatalf("expected 2 features, got %+v", out.Features)
	}
	if out.Features[0]["status"] != true {
		t.Fatalf("expected dns feature marked enabled, got %+v", out.Features[0])
	}
	if out.Features[1]["status"] != false {
		t.Fatalf("expected malware_scan feature marked disabled, got %+v", out.Features[1])
	}
	if out.Plugins == nil {
		t.Fatal("expected plugins to be an empty list, not null")
	}
}

func TestAPISettingsModulesPostRequiresJSON(t *testing.T) {
	withScratchModulesPaths(t)
	srv, client := newAPISettingsModulesTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/modules", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsModulesPostUpdatesConfigPreservingOrder(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte("some_other=1\nenabled_modules=\"old\"\nafter=2\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[]`), 0644)

	srv, client := newAPISettingsModulesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/modules", `{"enabled_modules": ["phpmyadmin", "dns", "malware_scan"]}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Modules updated successfully.") {
		t.Fatalf("expected success message, got %s", body)
	}

	saved, err := os.ReadFile(ModulesConfigFilePath)
	if err != nil {
		t.Fatal(err)
	}
	// The submitted list order must be preserved exactly (no sorting), unlike
	// the HTML admin page's POST handler which sorts form keys.
	if !strings.Contains(string(saved), `enabled_modules="phpmyadmin,dns,malware_scan"`) {
		t.Fatalf("expected enabled_modules line to preserve submitted order, got %q", saved)
	}
	if !strings.Contains(string(saved), "some_other=1") || !strings.Contains(string(saved), "after=2") {
		t.Fatalf("expected other config lines untouched, got %q", saved)
	}

	if _, err := os.Stat(ModulesOpenpanelRestartFlagPath); err != nil {
		t.Fatal("expected restart-needed flag file to be written")
	}
}

func TestAPISettingsModulesPostDefaultsToEmptyList(t *testing.T) {
	withScratchModulesPaths(t)
	os.WriteFile(ModulesConfigFilePath, []byte("enabled_modules=\"old\"\n"), 0644)
	os.WriteFile(ModulesFeaturesJSONPath, []byte(`[]`), 0644)

	srv, client := newAPISettingsModulesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/modules", `{}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	saved, err := os.ReadFile(ModulesConfigFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `enabled_modules=""`) {
		t.Fatalf("expected enabled_modules cleared, got %q", saved)
	}
}
