package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withStubLicenseRunOpenCLI(t *testing.T, fn func(args ...string) (string, string, error)) {
	t.Helper()
	orig := licenseRunOpenCLI
	licenseRunOpenCLI = fn
	t.Cleanup(func() { licenseRunOpenCLI = orig })
}

func newLicensePageTestServer(t *testing.T, l *LicensePage) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	l.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /license", l.ServeLicense)
	mux.HandleFunc("GET /license/key", l.ServeLicenseKey)
	mux.HandleFunc("POST /license/key", l.ServeLicenseKey)
	mux.HandleFunc("GET /license/info", l.ServeLicenseInfo)
	mux.HandleFunc("POST /license/verify", l.ServeLicenseVerify)
	mux.HandleFunc("DELETE /license/delete", l.ServeLicenseDelete)
	mux.HandleFunc("GET /support/report", l.ServeSupportReport)
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

func TestServeLicenseGetHTMLAndJSON(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "enterprise-abc123\n", "", nil
	})
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Get(srv.URL + "/license")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"enterprise-abc123", "License key", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}

	resp2, err := client.Get(srv.URL + "/license?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), `"key":"enterprise-abc123"`) {
		t.Fatalf("expected JSON key, got %s", body2)
	}
}

func TestServeLicenseGetKeyFailure(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "", "boom", &ftpStubError{"boom"}
	})
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Get(srv.URL + "/license/key")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed to read license key") {
		t.Fatalf("expected error message, got %s", body)
	}
}

func TestServeLicenseKeyPostMissingKey(t *testing.T) {
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Post(srv.URL+"/license/key", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeLicenseKeyPostSuccess(t *testing.T) {
	var gotArgs []string
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		gotArgs = args
		return "activated", "", nil
	})
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Post(srv.URL+"/license/key", "application/json", strings.NewReader(`{"key":"enterprise-xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"response":"activated"`) {
		t.Fatalf("expected response JSON, got %s", body)
	}
	want := []string{"opencli", "license", "enterprise-xyz", "--json", "--no-restart"}
	if len(gotArgs) != len(want) {
		t.Fatalf("expected args %v, got %v", want, gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, gotArgs)
		}
	}
}

func TestServeLicenseInfoVerifyDelete(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		switch args[2] {
		case "info":
			return "info-output", "", nil
		case "verify":
			return "verify-output", "", nil
		case "delete":
			return "delete-output", "", nil
		}
		return "", "", nil
	})
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Get(srv.URL + "/license/info")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"info":"info-output"`) {
		t.Fatalf("expected info JSON, got %s", body)
	}

	resp2, err := client.Post(srv.URL+"/license/verify", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), `"verify":"verify-output"`) {
		t.Fatalf("expected verify JSON, got %s", body2)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/license/delete", nil)
	resp3, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if !strings.Contains(string(body3), `"delete":"delete-output"`) {
		t.Fatalf("expected delete JSON, got %s", body3)
	}
}

func TestServeSupportReportSuccessAndFailure(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "report contents", "", nil
	})
	l := &LicensePage{}
	srv, client := newLicensePageTestServer(t, l)

	resp, err := client.Get(srv.URL + "/support/report?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"message":"report contents"`) {
		t.Fatalf("expected JSON message, got %s", body)
	}

	licenseRunOpenCLI = func(args ...string) (string, string, error) {
		return "", "err", &ftpStubError{"err"}
	}
	resp2, err := client.Get(srv.URL + "/support/report")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), "Generating report failed") {
		t.Fatalf("expected failure flash, got %s", truncate(string(body2)))
	}
}
