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
)

func newAPIDomainStatsTestServer(t *testing.T, a *APIDomainStats) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/{domain_name}/ssl", a.ServeDomainSSL)
	mux.HandleFunc("POST /domains/{domain_name}/ssl", a.ServeDomainSSL)
	mux.HandleFunc("GET /domains/{domain_name}/log", a.ServeDomainAccessLog)
	mux.HandleFunc("GET /domains/{domain_name}/stats/{username}", a.ServeDomainStats)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// --- GET/POST /domains/{domain_name}/ssl ---

func TestAPIDomainSSLInvalidDomainReturns400(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/not-a-domain/ssl")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid domain name.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLGetReturnsStatusAndInfo(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AUTOSSL\n", exitCode: 0},
		"example.com info":   {stdout: "issuer: Let's Encrypt\n", exitCode: 0},
	})

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/ssl")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["domain"] != "example.com" || decoded["status"] != "autossl" || decoded["info"] != "issuer: Let's Encrypt" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLGetStatusFailureReturns500(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stderr: "domain not found", exitCode: 1},
	})

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/ssl")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "domain not found") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLPostRequiresJSON(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/x-www-form-urlencoded", strings.NewReader("action=autossl"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDomainSSLPostAutoSSLSuccess(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com auto": {stdout: "AutoSSL requested.\n", exitCode: 0},
	})

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/json", strings.NewReader(`{"action":"autossl"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "AutoSSL requested.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLPostCustomRequiresPaths(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/json", strings.NewReader(`{"action":"custom"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "public_path and private_path must be provided.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLPostCustomRejectsPathOutsideHomeDir(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/json", strings.NewReader(
		`{"action":"custom","public_path":"/etc/passwd","private_path":"/var/www/html/example.com/key.pem"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "public_path must be inside '/var/www/html/' directory.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLPostLogsAction(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com logs 1000": {stdout: "some ssl log lines\n", exitCode: 0},
	})

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/json", strings.NewReader(`{"action":"logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["logs"] != "some ssl log lines" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainSSLPostInvalidActionReturns400(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/domains/example.com/ssl", "application/json", strings.NewReader(`{"action":"bogus"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid action. Use 'autossl', 'custom' or 'logs'.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- GET /domains/{domain_name}/log ---

func TestAPIDomainAccessLogInvalidDomainReturns400(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/not-a-domain/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDomainAccessLogMissingFileReturns404(t *testing.T) {
	withScratchAccessLogsDir(t)
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Log file not found for domain example.com") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainAccessLogEmptyFileReturnsZeroLines(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), nil, 0644)

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["total_lines"] != float64(0) {
		t.Fatalf("unexpected body: %s", body)
	}
	logs, ok := decoded["logs"].([]interface{})
	if !ok || len(logs) != 0 {
		t.Fatalf("expected empty logs array, got %s", body)
	}
}

func TestAPIDomainAccessLogReturnsReversedPaginatedEntries(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	content := `{"n":1}` + "\n" + `{"n":2}` + "\n" + `{"n":3}` + "\n"
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), []byte(content), 0644)

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["total_lines"] != float64(3) {
		t.Fatalf("unexpected total_lines: %s", body)
	}
	logs, ok := decoded["logs"].([]interface{})
	if !ok || len(logs) != 3 {
		t.Fatalf("expected 3 log entries, got %s", body)
	}
	first := logs[0].(map[string]interface{})
	if first["n"] != float64(3) {
		t.Fatalf("expected logs reversed (newest first), got %s", body)
	}
}

func TestAPIDomainAccessLogMalformedLineReturns500(t *testing.T) {
	dir := withScratchAccessLogsDir(t)
	os.MkdirAll(filepath.Join(dir, "example.com"), 0755)
	os.WriteFile(filepath.Join(dir, "example.com", "access.log"), []byte("not json\n"), 0644)

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Error reading log file:") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- GET /domains/{domain_name}/stats/{username} ---

func TestAPIDomainStatsInvalidDomainReturns400(t *testing.T) {
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/not-a-domain/stats/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIDomainStatsMissingFileReturns404(t *testing.T) {
	withScratchGoAccessStatsDir(t)
	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/stats/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Data is generated every 24h.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIDomainStatsReturnsHTMLContent(t *testing.T) {
	dir := withScratchGoAccessStatsDir(t)
	os.MkdirAll(filepath.Join(dir, "alice"), 0755)
	os.WriteFile(filepath.Join(dir, "alice", "example.com.html"), []byte("<html>report</html>"), 0644)

	a := &APIDomainStats{}
	srv, client := newAPIDomainStatsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/domains/example.com/stats/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["domain"] != "example.com" || decoded["html"] != "<html>report</html>" {
		t.Fatalf("unexpected body: %s", body)
	}
}
