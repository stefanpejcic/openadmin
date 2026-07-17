package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newAPIUsersTestServer(t *testing.T) (*httptest.Server, *http.Client, sqlmock.Sqlmock) {
	t.Helper()
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })

	u := &APIUsers{MySQL: mysqlDB, PublicIP: "203.0.113.5"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users", u.ServeUsers)
	mux.HandleFunc("POST /api/users", u.ServeUsers)
	mux.HandleFunc("GET /api/users/{username}", u.ServeUsers)
	mux.HandleFunc("POST /api/users/{username}", u.ServeUsers)
	mux.HandleFunc("DELETE /api/users/{username}", u.ServeUsers)
	mux.HandleFunc("PATCH /api/users/{username}", u.ServeUsers)
	mux.HandleFunc("PUT /api/users/{username}", u.ServeUsers)
	mux.HandleFunc("POST /api/users/{username}/autologin", u.ServeAutologin)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client(), mock
}

func withUsersAPIScratchQuotaReport(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "quota_report.json")
	if content != "" {
		os.WriteFile(path, []byte(content), 0644)
	}
	orig := QuotaReportPath
	QuotaReportPath = path
	t.Cleanup(func() { QuotaReportPath = orig })
}

func withScratchForbiddenUsernames(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "forbidden_usernames.txt")
	os.WriteFile(path, []byte(content), 0644)
	orig := ForbiddenUsernamesPath
	ForbiddenUsernamesPath = path
	t.Cleanup(func() { ForbiddenUsernamesPath = orig })
}

func stubAPICheckOutputRun(t *testing.T, output string, err error) {
	t.Helper()
	orig := apiCheckOutputRun
	apiCheckOutputRun = func(args ...string) (string, error) { return output, err }
	t.Cleanup(func() { apiCheckOutputRun = orig })
}

func TestServeUsersListAlwaysEmpty(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	resp, err := client.Get(srv.URL + "/api/users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	users, ok := body["users"].([]interface{})
	if !ok || len(users) != 0 {
		t.Fatalf("expected an empty users array, got %v", body)
	}
}

func TestServeUsersGetDetailNotFound(t *testing.T) {
	srv, client, mock := newAPIUsersTestServer(t)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"user_id"}))

	resp, err := client.Get(srv.URL + "/api/users/nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeUsersGetDetailReturnsNestedUser(t *testing.T) {
	srv, client, mock := newAPIUsersTestServer(t)
	withUsersAPIScratchQuotaReport(t, `{"users":[]}`)

	cols := []string{"user_id", "username", "email", "owner", "user_domains", "twofa_enabled",
		"registered_date", "server", "plan_id", "plan_id", "plan_name", "plan_description",
		"domains_limit", "websites_limit", "email_limit", "ftp_limit", "disk_limit", "inodes_limit",
		"db_limit", "cpu", "ram", "bandwidth", "feature_set"}
	rows := sqlmock.NewRows(cols).AddRow(
		1, "bob", "bob@example.com", nil, "", 0, "2024-01-01", "bob", 2, 2, "starter", "desc",
		5, 5, 5, 5, 5000, 100000, 5, 1, 1, 100, "default")
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	domainRows := sqlmock.NewRows([]string{"domain_id", "docroot", "domain_url", "php_version",
		"site_id", "site_name", "admin_email", "version", "site_created", "type", "ports", "path", "container"})
	mock.ExpectQuery("SELECT").WillReturnRows(domainRows)

	resp, err := client.Get(srv.URL + "/api/users/bob")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		User struct {
			User map[string]interface{} `json:"user"`
			Plan map[string]interface{} `json:"plan"`
		} `json:"user"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.User.User["username"] != "bob" {
		t.Fatalf("expected nested user.user.username=bob, got %+v", body)
	}
	if body.User.Plan["name"] != "starter" {
		t.Fatalf("expected nested user.plan.name=starter, got %+v", body)
	}
}

func TestServeUsersCreateSuccess(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	withScratchForbiddenUsernames(t, "root\nadmin\n")
	stubAPICheckOutputRun(t, "User created.\n", nil)

	body := `{"email":"a@b.com","username":"newuser","password":"pw","plan_name":"default"}`
	resp, err := client.Post(srv.URL+"/api/users", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
}

func TestServeUsersCreateMissingFieldsReturns400(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	resp, err := client.Post(srv.URL+"/api/users", "application/json", strings.NewReader(`{"username":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeUsersCreateForbiddenUsername(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	withScratchForbiddenUsernames(t, "root\nreserved\n")

	body := `{"email":"a@b.com","username":"Reserved","password":"pw","plan_name":"default"}`
	resp, err := client.Post(srv.URL+"/api/users", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] != "Username is not allowed" {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestServeUsersCreateViaUsernamePathIgnoresPathSegment(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	withScratchForbiddenUsernames(t, "")
	stubAPICheckOutputRun(t, "ok\n", nil)

	body := `{"email":"a@b.com","username":"bodyuser","password":"pw","plan_name":"default"}`
	resp, err := client.Post(srv.URL+"/api/users/pathuser", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestServeUsersChangePlanMissingPlanNameReturns400(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/users/bob", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeUsersChangePlanSuccess(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	stubAPICheckOutputRun(t, "Plan changed.\n", nil)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/users/bob", strings.NewReader(`{"plan_name":"pro"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestServeUsersPatchSuspend(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	stubAPICheckOutputRun(t, "User suspended.\n", nil)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/users/bob", strings.NewReader(`{"action":"suspend"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestServeUsersPatchInvalidActionReturns400(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/users/bob", strings.NewReader(`{"action":"reboot"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeUsersPatchNeitherPasswordNorActionReturns400(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/users/bob", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] != "Something went wrong.." {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestServeUsersDeleteRequiresJSONContentType(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/users/bob", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeUsersDeleteSuccess(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	stubAPICheckOutputRun(t, "User deleted.\n", nil)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/users/bob", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestServeAutologinReturnsLink(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	withScratchAutologinTokenDir(t)

	origDomain, origPort := autologinOpenpanelDomainRun, autologinOpenpanelPortRun
	autologinOpenpanelDomainRun = func() string { return "" }
	autologinOpenpanelPortRun = func() string { return "2083" }
	t.Cleanup(func() {
		autologinOpenpanelDomainRun = origDomain
		autologinOpenpanelPortRun = origPort
	})

	resp, err := client.Post(srv.URL+"/api/users/bob/autologin", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(out["link"], "http://203.0.113.5:2083/login_autologin?username=bob&admin_token=") {
		t.Fatalf("unexpected link: %v", out)
	}
}

func TestServeAutologinRequiresJSONContentType(t *testing.T) {
	srv, client, _ := newAPIUsersTestServer(t)
	resp, err := client.Post(srv.URL+"/api/users/bob/autologin", "text/plain", strings.NewReader(``))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
