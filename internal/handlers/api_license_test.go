package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAPILicenseTestServer(t *testing.T, a *APILicense) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/license", a.ServeLicense)
	mux.HandleFunc("POST /api/license", a.ServeLicense)
	mux.HandleFunc("DELETE /api/license", a.ServeLicense)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPILicenseGetReturnsKey(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "enterprise-abc123\n", "", nil
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/license")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key":"enterprise-abc123"`) {
		t.Fatalf("expected key in body, got %s", body)
	}
}

func TestAPILicenseGetFailureReturns500(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "", "boom", &ftpStubError{"boom"}
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/license")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed to read license key") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPILicenseDeleteReturnsResult(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "deleted", "", nil
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/license", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"delete":"deleted"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPILicenseDeleteFailureReturns500(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "", "boom", &ftpStubError{"boom"}
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/license", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed to delete license") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPILicensePostRejectsNonJSON(t *testing.T) {
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/license", "text/plain", strings.NewReader(`{"key":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid JSON format") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPILicensePostMissingKey(t *testing.T) {
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/license", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Missing key in request") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPILicensePostSuccessInvokesOpenCLI(t *testing.T) {
	var gotArgs []string
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		gotArgs = args
		return "activated", "", nil
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/license", "application/json", strings.NewReader(`{"key":"enterprise-xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"response":"activated"`) {
		t.Fatalf("unexpected body: %s", body)
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

func TestAPILicensePostValidationFailureReturns500(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "", "boom", &ftpStubError{"boom"}
	})
	a := &APILicense{}
	srv, client := newAPILicenseTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/license", "application/json", strings.NewReader(`{"key":"bad"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "License key validation failed") {
		t.Fatalf("unexpected body: %s", body)
	}
}
