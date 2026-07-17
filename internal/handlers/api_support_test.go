package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAPISupportTestServer(t *testing.T, a *APISupport) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/support/report", a.ServeSupportReport)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISupportReportSuccess(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "report contents\n", "", nil
	})
	a := &APISupport{}
	srv, client := newAPISupportTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/support/report")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":true`) || !strings.Contains(string(body), `"message":"report contents"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPISupportReportFailureReturnsJSON500(t *testing.T) {
	withStubLicenseRunOpenCLI(t, func(args ...string) (string, string, error) {
		return "", "boom", &ftpStubError{"boom"}
	})
	a := &APISupport{}
	srv, client := newAPISupportTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/support/report")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":false`) || !strings.Contains(string(body), "Generating report failed") {
		t.Fatalf("unexpected body: %s", body)
	}
}
