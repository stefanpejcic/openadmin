package handlers

import (
	"encoding/json"
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
	"openadmin/internal/license"
)

func withScratchCustomCodePaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origPaths := customCodeFilePaths
	origFlag := CustomCodeRestartFlagPath
	newPaths := make(map[string]string, len(origPaths))
	for k := range origPaths {
		newPaths[k] = filepath.Join(dir, k+".txt")
	}
	customCodeFilePaths = newPaths
	CustomCodeRestartFlagPath = filepath.Join(dir, "restart_needed")
	t.Cleanup(func() {
		customCodeFilePaths = origPaths
		CustomCodeRestartFlagPath = origFlag
	})
}

func newCustomCodeTestServer(t *testing.T, c *CustomCode, role string) (*httptest.Server, *http.Client) {
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
	c.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/custom-code", c.ServeCustomCode)
	mux.HandleFunc("POST /settings/custom-code", c.ServeCustomCode)
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

func TestServeCustomCodeGetCommunityHidesEnterpriseFields(t *testing.T) {
	withScratchCustomCodePaths(t)

	c := &CustomCode{} // LicenseChecker nil -> Community
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.Get(srv.URL + "/settings/custom-code")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if strings.Contains(got, `name="custom_css"`) {
		t.Fatalf("expected Enterprise-only fields hidden on Community, got %s", truncate(got))
	}
	for _, want := range []string{`name="post_update"`, "Custom Code", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeCustomCodeGetJSONIncludesAllKeys(t *testing.T) {
	withScratchCustomCodePaths(t)
	os.WriteFile(customCodeFilePaths["custom_css"], []byte("body{}"), 0644)

	c := &CustomCode{}
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.Get(srv.URL + "/settings/custom-code?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", body, err)
	}
	if parsed["custom_css"] != "body{}" {
		t.Fatalf("expected custom_css content in JSON regardless of license, got %+v", parsed)
	}
	if _, ok := parsed["wp_themes"]; !ok {
		t.Fatalf("expected community field present too, got %+v", parsed)
	}
}

func TestServeCustomCodePostCommunitySkipsEnterpriseFields(t *testing.T) {
	withScratchCustomCodePaths(t)

	c := &CustomCode{} // Community
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.PostForm(srv.URL+"/settings/custom-code", url.Values{
		"custom_css":  {"body{color:red}"},
		"post_update": {"echo hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := os.Stat(customCodeFilePaths["custom_css"]); !os.IsNotExist(err) {
		t.Fatalf("expected custom_css NOT to be written on Community edition, err=%v", err)
	}
	saved, err := os.ReadFile(customCodeFilePaths["post_update"])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "echo hi" {
		t.Fatalf("expected the Community field to still save, got %q", saved)
	}
}

func TestServeCustomCodePostEnterpriseSavesAllFields(t *testing.T) {
	withScratchCustomCodePaths(t)
	withMockLicenseAPI(t, "Active")

	c := &CustomCode{LicenseChecker: license.NewChecker("enterprise-test", "203.0.113.1")}
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.PostForm(srv.URL+"/settings/custom-code", url.Values{
		"custom_css": {"body{color:blue}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, err := os.ReadFile(customCodeFilePaths["custom_css"])
	if err != nil {
		t.Fatalf("expected custom_css to be saved on Enterprise: %v", err)
	}
	if string(saved) != "body{color:blue}" {
		t.Fatalf("expected saved content to match, got %q", saved)
	}
}

func TestServeCustomCodePostResellerBlockedEvenWithValidLicense(t *testing.T) {
	withScratchCustomCodePaths(t)
	withMockLicenseAPI(t, "Active")

	c := &CustomCode{LicenseChecker: license.NewChecker("enterprise-test", "203.0.113.1")}
	srv, client := newCustomCodeTestServer(t, c, "reseller")

	resp, err := client.PostForm(srv.URL+"/settings/custom-code", url.Values{
		"custom_css": {"body{color:green}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	if _, err := os.Stat(customCodeFilePaths["custom_css"]); !os.IsNotExist(err) {
		t.Fatalf("expected a reseller to be blocked from Enterprise-only fields even with a valid license, err=%v", err)
	}
}

func TestServeCustomCodePostAlwaysWritesRestartFlag(t *testing.T) {
	withScratchCustomCodePaths(t)

	c := &CustomCode{}
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.PostForm(srv.URL+"/settings/custom-code", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Files updated successfully!") {
		t.Fatalf("expected the success flash even with an empty POST, got %s", truncate(string(body)))
	}

	if _, err := os.Stat(CustomCodeRestartFlagPath); err != nil {
		t.Fatalf("expected the restart flag to always be written on POST, err=%v", err)
	}
}

func TestServeCustomCodePostEmptyStringStillSaves(t *testing.T) {
	withScratchCustomCodePaths(t)
	os.WriteFile(customCodeFilePaths["post_update"], []byte("old content"), 0644)

	c := &CustomCode{}
	srv, client := newCustomCodeTestServer(t, c, "admin")

	resp, err := client.PostForm(srv.URL+"/settings/custom-code", url.Values{"post_update": {""}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, err := os.ReadFile(customCodeFilePaths["post_update"])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != "" {
		t.Fatalf("expected a present-but-empty field to clear the file (matching `is not None`), got %q", saved)
	}
}
