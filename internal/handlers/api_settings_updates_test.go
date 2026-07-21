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

func newAPISettingsUpdatesTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsUpdates{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/updates", a.Serve)
	mux.HandleFunc("POST /api/settings/updates", a.Serve)
	mux.HandleFunc("POST /api/settings/updates/now", a.ServeUpdateNow)
	mux.HandleFunc("GET /api/settings/updates/tags", a.ServeTags)
	mux.HandleFunc("POST /api/settings/updates/tags", a.ServeTags)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsUpdatesGetReturnsConfigVersionAndLogs(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("[PANEL]\nautoupdate=on\nautopatch=off\n"), 0644)

	origHub := updatesDockerHubTagsRun
	updatesDockerHubTagsRun = func() ([]string, error) { return []string{"9.9.9"}, nil }
	t.Cleanup(func() { updatesDockerHubTagsRun = origHub })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/updates")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		ConfigData    map[string]map[string]string `json:"config_data"`
		LatestVersion string                       `json:"latest_version"`
		UpdateLogs    []updateLogEntry             `json:"update_logs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("expected valid JSON: %v (%s)", err, body)
	}
	if out.ConfigData["PANEL"]["autoupdate"] != "on" {
		t.Fatalf("expected full config_data section exposed, got %+v", out.ConfigData)
	}
	if out.LatestVersion != "9.9.9" {
		t.Fatalf("expected latest_version 9.9.9, got %q", out.LatestVersion)
	}
	if out.UpdateLogs == nil {
		t.Fatal("expected update_logs to be an empty list, not null")
	}
}

func TestAPISettingsUpdatesPostRequiresJSON(t *testing.T) {
	withScratchUpdatesPaths(t)
	srv, client := newAPISettingsUpdatesTestServer(t)

	resp, err := client.Post(srv.URL+"/api/settings/updates", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPISettingsUpdatesPostInvalidPreference(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("autoupdate=on\nautopatch=off\n"), 0644)
	srv, client := newAPISettingsUpdatesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/updates", `{"preference": "nonsense"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid preference.") {
		t.Fatalf("expected invalid-preference message, got %s", body)
	}
}

func TestAPISettingsUpdatesPostAppliesPreference(t *testing.T) {
	withScratchUpdatesPaths(t)
	os.WriteFile(UpdatesConfigFilePath, []byte("autoupdate=on\nautopatch=on\n"), 0644)
	srv, client := newAPISettingsUpdatesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/updates", `{"preference": "minor_only"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	saved, _ := os.ReadFile(UpdatesConfigFilePath)
	if !strings.Contains(string(saved), "autoupdate=off") || !strings.Contains(string(saved), "autopatch=on") {
		t.Fatalf("expected minor_only preference applied, got %q", saved)
	}
}

func TestAPISettingsUpdatesNowSuccess(t *testing.T) {
	orig := updatesUpdateNowRun
	updatesUpdateNowRun = func() error { return nil }
	t.Cleanup(func() { updatesUpdateNowRun = orig })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp, err := client.Post(srv.URL+"/api/settings/updates/now", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Update process started successfully.") {
		t.Fatalf("expected success message, got %s", body)
	}
}

func TestAPISettingsUpdatesNowFailure(t *testing.T) {
	orig := updatesUpdateNowRun
	updatesUpdateNowRun = func() error { return &ftpStubError{"exec failed"} }
	t.Cleanup(func() { updatesUpdateNowRun = orig })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp, err := client.Post(srv.URL+"/api/settings/updates/now", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed to start the update process. Details: exec failed") {
		t.Fatalf("expected failure message, got %s", body)
	}
}

func TestAPISettingsUpdatesTagsGet(t *testing.T) {
	orig := updatesFetchTagsRun
	updatesFetchTagsRun = func() ([]string, error) { return []string{"1.0", "latest", "2.0"}, nil }
	t.Cleanup(func() { updatesFetchTagsRun = orig })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/updates/tags")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "latest") {
		t.Fatalf("expected 'latest' excluded, got %s", body)
	}
	if !strings.Contains(string(body), "1.0") || !strings.Contains(string(body), "2.0") {
		t.Fatalf("expected version tags listed, got %s", body)
	}
}

func TestAPISettingsUpdatesTagsPostMissingVersion(t *testing.T) {
	withScratchUpdatesPaths(t)
	srv, client := newAPISettingsUpdatesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/updates/tags", `{}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "version is required") {
		t.Fatalf("expected version-required message, got %s", body)
	}
}

func TestAPISettingsUpdatesTagsPostInvalidFormat(t *testing.T) {
	withScratchUpdatesPaths(t)
	srv, client := newAPISettingsUpdatesTestServer(t)

	resp := postJSON(t, client, srv.URL+"/api/settings/updates/tags", `{"version": "not-a-version"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid version format") {
		t.Fatalf("expected invalid-format message, got %s", body)
	}
}

func TestAPISettingsUpdatesTagsPostSuccessPullsAndComposes(t *testing.T) {
	withScratchUpdatesPaths(t)

	var pulledVersion string
	composeCalled := false
	origPull, origCompose := updatesPullImageRun, updatesComposeUpRun
	updatesPullImageRun = func(v string) error { pulledVersion = v; return nil }
	updatesComposeUpRun = func() error { composeCalled = true; return nil }
	t.Cleanup(func() { updatesPullImageRun, updatesComposeUpRun = origPull, origCompose })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/updates/tags", `{"version": "1.2.3"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if pulledVersion != "1.2.3" || !composeCalled {
		t.Fatalf("expected pull+compose called, pulledVersion=%q composeCalled=%v", pulledVersion, composeCalled)
	}
	if !strings.Contains(string(body), "Downgraded to version 1.2.3 successfully.") {
		t.Fatalf("expected success message, got %s", body)
	}
	saved, _ := os.ReadFile(UpdatesEnvPath)
	if !strings.Contains(string(saved), `VERSION="1.2.3"`) {
		t.Fatalf("expected .env updated, got %q", saved)
	}
}

func TestAPISettingsUpdatesTagsPostPullFailure(t *testing.T) {
	withScratchUpdatesPaths(t)

	origPull := updatesPullImageRun
	updatesPullImageRun = func(v string) error { return &ftpStubError{"no such image"} }
	t.Cleanup(func() { updatesPullImageRun = origPull })

	srv, client := newAPISettingsUpdatesTestServer(t)
	resp := postJSON(t, client, srv.URL+"/api/settings/updates/tags", `{"version": "1.2.3"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Command failed: no such image") {
		t.Fatalf("expected pull-failure message, got %s", body)
	}
}
