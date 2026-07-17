package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newAPIDomainsTestServer(t *testing.T) (*httptest.Server, *http.Client, sqlmock.Sqlmock) {
	t.Helper()
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })

	d := &APIDomains{MySQL: mysqlDB}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/domains", d.ServeDomains)
	mux.HandleFunc("POST /api/domains/new", d.HandleAddDomain)
	mux.HandleFunc("GET /api/domains/docroot/{domain}", d.ServeDomainDocroot)
	mux.HandleFunc("POST /api/domains/docroot/{domain}", d.ServeDomainDocroot)
	mux.HandleFunc("POST /api/domains/{action}/{domain}", d.HandleDomainAction)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client(), mock
}

func stubAPIRunCapture(t *testing.T, stdout, stderr string, returncode int) {
	t.Helper()
	orig := apiRunCapture
	apiRunCapture = func(args ...string) (string, string, int) { return stdout, stderr, returncode }
	t.Cleanup(func() { apiRunCapture = orig })
}

func TestServeDomainsListsAllDomains(t *testing.T) {
	srv, client, mock := newAPIDomainsTestServer(t)
	rows := sqlmock.NewRows([]string{"domain_id", "docroot", "domain_url", "php_version", "username"}).
		AddRow(1, "/var/www/html/a.com", "a.com", "8.2", "bob")
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	resp, err := client.Get(srv.URL + "/api/domains")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Domains []map[string]interface{} `json:"domains"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Domains) != 1 || out.Domains[0]["domain_url"] != "a.com" {
		t.Fatalf("unexpected body: %+v", out)
	}
}

func TestServeDomainsQueryErrorReturnsEmptyList(t *testing.T) {
	srv, client, mock := newAPIDomainsTestServer(t)
	mock.ExpectQuery("SELECT").WillReturnError(sqlmockErr("boom"))

	resp, err := client.Get(srv.URL + "/api/domains")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Domains []map[string]interface{} `json:"domains"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Domains) != 0 {
		t.Fatalf("expected an empty domains array, got %+v", out)
	}
}

func TestHandleAddDomainMissingFieldsReturns400(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	resp, err := client.Post(srv.URL+"/api/domains/new", "application/json", strings.NewReader(`{"domain":"a.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleAddDomainRejectsDocrootOutsideWebroot(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	body := `{"username":"bob","domain":"a.com","docroot":"/etc/passwd"}`
	resp, err := client.Post(srv.URL+"/api/domains/new", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleAddDomainSuccess(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	stubAPIRunCapture(t, "added\n", "", 0)

	body := `{"username":"bob","domain":"a.com"}`
	resp, err := client.Post(srv.URL+"/api/domains/new", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["returncode"] != float64(0) || out["stdout"] != "added\n" {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestHandleDomainActionInvalidActionReturns400(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	resp, err := client.Post(srv.URL+"/api/domains/reboot/a.com", "application/json", strings.NewReader(``))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleDomainActionSuspendSuccess(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	stubAPIRunCapture(t, "suspended\n", "", 0)

	resp, err := client.Post(srv.URL+"/api/domains/suspend/a.com", "application/json", strings.NewReader(``))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["stdout"] != "suspended\n" {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestServeDomainDocrootGet(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	stubAPIRunCapture(t, "/var/www/html/a.com\n", "", 0)

	resp, err := client.Get(srv.URL + "/api/domains/docroot/a.com")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServeDomainDocrootPostRequiresDocroot(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	resp, err := client.Post(srv.URL+"/api/domains/docroot/a.com", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeDomainDocrootPostRejectsOutsideWebroot(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	resp, err := client.Post(srv.URL+"/api/domains/docroot/a.com", "application/json", strings.NewReader(`{"docroot":"/etc/passwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeDomainDocrootPostSuccess(t *testing.T) {
	srv, client, _ := newAPIDomainsTestServer(t)
	stubAPIRunCapture(t, "updated\n", "", 0)

	resp, err := client.Post(srv.URL+"/api/domains/docroot/a.com", "application/json",
		strings.NewReader(`{"docroot":"/var/www/html/newroot"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

type sqlmockErrType string

func (e sqlmockErrType) Error() string { return string(e) }

func sqlmockErr(msg string) error { return sqlmockErrType(msg) }
