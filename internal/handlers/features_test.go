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

func withScratchFeaturesPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origDir, origJSON, origFlag, origModulesConfig := FeaturesDir, FeaturesJSONPath, FeaturesOpenpanelRestartFlagPath, ModulesConfigFilePath
	FeaturesDir = filepath.Join(dir, "features") + "/"
	FeaturesJSONPath = filepath.Join(dir, "features.json")
	FeaturesOpenpanelRestartFlagPath = filepath.Join(dir, "openpanel_restart_needed")
	ModulesConfigFilePath = filepath.Join(dir, "openpanel.config")
	os.MkdirAll(FeaturesDir, 0755)
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="dns"`+"\n"), 0644)

	origRedis := invalidateOpenpanelUserFeaturesCacheRun
	invalidateOpenpanelUserFeaturesCacheRun = func() bool { return true } // default: redis "succeeds", no restart flag
	t.Cleanup(func() {
		FeaturesDir, FeaturesJSONPath, FeaturesOpenpanelRestartFlagPath, ModulesConfigFilePath = origDir, origJSON, origFlag, origModulesConfig
		invalidateOpenpanelUserFeaturesCacheRun = origRedis
	})
}

func newFeaturesTestServer(t *testing.T, f *Features, role string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	f.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /features/", f.ServeFeatures)
	mux.HandleFunc("POST /features/", f.ServeFeatures)
	mux.HandleFunc("GET /features/{plan}", f.ServeFeatures)
	mux.HandleFunc("POST /features/{plan}", f.ServeFeatures)
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

func TestServeFeaturesInvalidPlanBadRequest(t *testing.T) {
	withScratchFeaturesPaths(t)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.Get(srv.URL + "/features/bad%20plan!")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid plan name, got %d", resp.StatusCode)
	}
}

func TestServeFeaturesIndexGetListsFilesForAdmin(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.Get(srv.URL + "/features/?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "default") {
		t.Fatalf("expected default feature set listed, got %s", body)
	}
}

func TestServeFeaturesIndexGetCreatesResellerDir(t *testing.T) {
	withScratchFeaturesPaths(t)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "reseller")

	resp, err := client.Get(srv.URL + "/features/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if info, err := os.Stat(FeaturesDir + "caller/"); err != nil || !info.IsDir() {
		t.Fatalf("expected reseller's own feature dir to be created, err=%v", err)
	}
}

func TestServeFeaturesIndexPostCreatesFile(t *testing.T) {
	withScratchFeaturesPaths(t)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/", url.Values{"feature_name": {"myset"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Feature set created successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(filepath.Join(FeaturesDir, "myset.txt")); err != nil {
		t.Fatalf("expected file created, err=%v", err)
	}
}

func TestServeFeaturesIndexPostAlreadyExists(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "myset.txt"), []byte(""), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/", url.Values{"feature_name": {"myset"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Feature set already exists.") {
		t.Fatalf("expected already-exists flash, got %s", truncate(string(body)))
	}
}

func TestServeFeaturesIndexPostMissingAndInvalidName(t *testing.T) {
	withScratchFeaturesPaths(t)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Name for feature set is required.") {
		t.Fatalf("expected required-name flash, got %s", truncate(string(body)))
	}

	resp2, err := client.PostForm(srv.URL+"/features/", url.Values{"feature_name": {"bad name!"}})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), "Invalid feature set name.") {
		t.Fatalf("expected invalid-name flash, got %s", truncate(string(body2)))
	}
}

func TestServeFeaturesPlanNotFound(t *testing.T) {
	withScratchFeaturesPaths(t)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.Get(srv.URL + "/features/doesnotexist")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeFeaturesPlanPostMissingActionReturns500(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when 'action' is entirely absent (matching the original's crash), got %d", resp.StatusCode)
	}
}

func TestServeFeaturesPlanPostInvalidActionFlash(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"bogus"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid action.") {
		t.Fatalf("expected invalid-action flash, got %s", truncate(string(body)))
	}
}

func TestServeFeaturesPlanPostUpdateWritesSelectedFeatures(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{
		"action": {"update"}, "waf": {"waf"}, "dns": {"dns"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Features updated successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	// Fixed from the original: "action" is now excluded too, alongside
	// "csrf_token" (see the comment on the "update" case in features.go).
	saved, _ := os.ReadFile(filepath.Join(FeaturesDir, "default.txt"))
	if string(saved) != "dns\nwaf\n" {
		t.Fatalf("expected only the sorted feature names written (no spurious 'action' line), got %q", saved)
	}
}

func TestServeFeaturesPlanPostDisableAll(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte("waf\ndns\n"), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"disable_all"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(filepath.Join(FeaturesDir, "default.txt"))
	if string(saved) != "" {
		t.Fatalf("expected file emptied, got %q", saved)
	}
}

func TestServeFeaturesPlanPostEnableAllWritesAllFeatureNames(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[{"name":"waf"},{"name":"dns"}]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"enable_all"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(filepath.Join(FeaturesDir, "default.txt"))
	if string(saved) != "waf\ndns\n" {
		t.Fatalf("expected all feature names written in features.json order, got %q", saved)
	}
}

func TestServeFeaturesPlanPostDeleteDefaultRejected(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"delete"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "default features set can not be deleted") {
		t.Fatalf("expected default-protection flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(filepath.Join(FeaturesDir, "default.txt")); err != nil {
		t.Fatal("expected default.txt to still exist")
	}
}

func TestServeFeaturesPlanPostDeleteSuccess(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "custom.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{} // nil MySQL -> checkIfFeatureInUse always false
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/custom", url.Values{"action": {"delete"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "features set custom deleted successfully.") {
		t.Fatalf("expected delete success flash, got %s", truncate(string(body)))
	}
	if _, err := os.Stat(filepath.Join(FeaturesDir, "custom.txt")); !os.IsNotExist(err) {
		t.Fatal("expected custom.txt to be removed")
	}
}

func TestServeFeaturesPlanPostWritesRestartFlagWhenRedisFails(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)
	invalidateOpenpanelUserFeaturesCacheRun = func() bool { return false }

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"disable_all"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := os.Stat(FeaturesOpenpanelRestartFlagPath); err != nil {
		t.Fatalf("expected restart flag written when redis invalidation fails, err=%v", err)
	}
}

func TestServeFeaturesPlanPostNoRestartFlagWhenRedisSucceeds(t *testing.T) {
	withScratchFeaturesPaths(t) // default stub returns true
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte(""), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.PostForm(srv.URL+"/features/default", url.Values{"action": {"disable_all"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := os.Stat(FeaturesOpenpanelRestartFlagPath); !os.IsNotExist(err) {
		t.Fatal("expected no restart flag when redis invalidation succeeds")
	}
}

func TestServeFeaturesPlanGetJSONStatusFields(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte("dns\n"), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[{"name":"dns"},{"name":"waf"}]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.Get(srv.URL + "/features/default?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, `"name":"dns","status":true`) {
		t.Fatalf("expected dns enabled+status true, got %s", got)
	}
	if !strings.Contains(got, `"name":"waf","status":false`) {
		t.Fatalf("expected waf status false, got %s", got)
	}
	if !strings.Contains(got, `"module_enabled":true`) {
		t.Fatalf("expected dns module_enabled true (from ModulesConfigFilePath's enabled_modules), got %s", got)
	}
}

func TestServeFeaturesIndexAndPlanRenderHTML(t *testing.T) {
	withScratchFeaturesPaths(t)
	os.WriteFile(filepath.Join(FeaturesDir, "default.txt"), []byte("dns\n"), 0644)
	os.WriteFile(FeaturesJSONPath, []byte(`[
		{"name":"dns","title":"DNS Zone Editor","description":"Manage DNS zones","type":"community","link":"/domains/dns"},
		{"name":"waf","title":"CorazaWAF","description":"Web application firewall","type":"enterprise","link":"/security/waf"}
	]`), 0644)

	f := &Features{}
	srv, client := newFeaturesTestServer(t, f, "admin")

	resp, err := client.Get(srv.URL + "/features/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for the index page, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "default") {
		t.Fatalf("expected the default feature set listed, got %s", truncate(string(body)))
	}

	resp, err = client.Get(srv.URL + "/features/default")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for the plan page, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"DNS Zone Editor", "CorazaWAF", "Enterprise"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected plan page to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestCheckIfFeatureInUseNilMySQL(t *testing.T) {
	f := &Features{}
	inUse, err := f.checkIfFeatureInUse("anything")
	if err != nil || inUse {
		t.Fatalf("expected false/nil with no MySQL configured, got %v %v", inUse, err)
	}
}
