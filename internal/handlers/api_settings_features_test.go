package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

func newAPISettingsFeaturesTestServer(t *testing.T) (*APISettingsFeatures, func(username, role string) string) {
	t.Helper()
	db := newAPITestDB(t)
	a := &APIAuth{DB: db, SecretKey: "test-secret"}
	f := &APISettingsFeatures{Auth: a}
	return f, func(username, role string) string {
		hash, err := auth.GeneratePasswordHash("pw")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.CreateUser(username, hash, role); err != nil {
			t.Fatal(err)
		}
		token, err := createAPIToken(username, "test-secret")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
}

func newAPISettingsFeaturesMux(f *APISettingsFeatures) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/features", f.ServeSettingsFeatures)
	mux.HandleFunc("POST /api/settings/features", f.ServeSettingsFeatures)
	mux.HandleFunc("GET /api/settings/features/{plan}", f.ServeSettingsFeatures)
	mux.HandleFunc("POST /api/settings/features/{plan}", f.ServeSettingsFeatures)
	return mux
}

func TestAPISettingsFeaturesListEmptyIndex(t *testing.T) {
	withScratchFeaturesPaths(t)
	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/settings/features", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var files []string
	json.NewDecoder(resp.Body).Decode(&files)
	if len(files) != 0 {
		t.Fatalf("expected no feature sets, got %v", files)
	}
}

func TestAPISettingsFeaturesCreateNewSet(t *testing.T) {
	withScratchFeaturesPaths(t)
	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/settings/features", strings.NewReader(`{"feature_name":"myset"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(FeaturesDir, "myset.txt")); err != nil {
		t.Fatalf("expected myset.txt to be created: %v", err)
	}
}

func TestAPISettingsFeaturesInvalidPlanNameReturns400(t *testing.T) {
	withScratchFeaturesPaths(t)
	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/settings/features/bad%20name", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPISettingsFeaturesPlanNotFoundReturns404(t *testing.T) {
	withScratchFeaturesPaths(t)
	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/settings/features/doesnotexist", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPISettingsFeaturesGetPlanReturnsFeaturesAndPlugins(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte("dns\n"), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[{"name":"dns"},{"name":"mail"}]`), 0644)

	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/settings/features/default", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["plan"] != "default" {
		t.Fatalf("expected plan default, got %v", body["plan"])
	}
	enabled, _ := body["enabled_modules"].([]interface{})
	if len(enabled) != 1 || enabled[0] != "dns" {
		t.Fatalf("expected enabled_modules [dns], got %v", body["enabled_modules"])
	}
}

func TestAPISettingsFeaturesUpdateWritesFile(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f, createUser := newAPISettingsFeaturesTestServer(t)
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/settings/features/default",
		strings.NewReader(`{"action":"update","features":["dns","mail"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, mustReadAll(t, resp))
	}
	saved, _ := os.ReadFile(filepath.Join(FeaturesDir, "default.txt"))
	if string(saved) != "dns\nmail\n" {
		t.Fatalf("expected saved content 'dns\\nmail\\n', got %q", saved)
	}
}

func TestAPISettingsFeaturesDeleteDefaultRejected(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)

	f, createUser := newAPISettingsFeaturesTestServer(t)
	f.MySQL = nil
	adminToken := createUser("admin1", "admin")
	srv := httptest.NewServer(newAPISettingsFeaturesMux(f))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/settings/features/default", strings.NewReader(`{"action":"delete"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func mustReadAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}
