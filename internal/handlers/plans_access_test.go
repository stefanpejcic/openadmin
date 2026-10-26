package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/paneldb"
)

// fakeOpenCLI puts an opencli on PATH that appends its args to a log and succeeds, returns a func reading the calls
func fakeOpenCLI(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + logFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencli"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		data, _ := os.ReadFile(logFile)
		var calls []string
		for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if l != "" {
				calls = append(calls, l)
			}
		}
		return calls
	}
}

// resellerAllowedPlans writes the reseller config that AllowedPlansForReseller reads
func resellerAllowedPlans(t *testing.T, reseller string, ids ...int) {
	t.Helper()
	dir := t.TempDir()
	orig := paneldb.ResellerConfigDir
	paneldb.ResellerConfigDir = dir
	t.Cleanup(func() { paneldb.ResellerConfigDir = orig })
	raw, _ := json.Marshal(map[string]interface{}{"allowed_plans": ids})
	if err := os.WriteFile(filepath.Join(dir, reseller+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func callsWith(calls []string, prefix string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// --- web routes ---

func TestPlansResellerCannotViewPlanNotAllowed(t *testing.T) {
	resellerAllowedPlans(t, "caller", 5)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).WithArgs("7").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "cpu"}).AddRow(7, "admin-only", "8"))

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "reseller")
	resp, err := client.Get(srv.URL + "/plans/7?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "admin-only") {
		t.Fatalf("reseller could read a plan outside allowed_plans: %s", body)
	}
}

func TestPlansResellerCannotEditPlanNotAllowed(t *testing.T) {
	calls := fakeOpenCLI(t)
	resellerAllowedPlans(t, "caller", 5)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "reseller")
	resp, err := client.PostForm(srv.URL+"/plans/7", url.Values{"name": {"admin-only"}, "cpu": {"1"}, "ram": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := callsWith(calls(), "plan-edit"); len(got) != 0 {
		t.Fatalf("plan-edit ran for a plan outside the reseller's allowed_plans: %v", got)
	}
}

func TestPlansResellerCannotDeletePlanNotAllowed(t *testing.T) {
	calls := fakeOpenCLI(t)
	resellerAllowedPlans(t, "caller", 5)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT id FROM plans WHERE name = \?`).WithArgs("admin-only").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("7"))

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "reseller")
	resp, err := client.PostForm(srv.URL+"/plan/delete/admin-only", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := callsWith(calls(), "plan-delete"); len(got) != 0 {
		t.Fatalf("plan-delete ran for a plan outside the reseller's allowed_plans: %v", got)
	}
}

// plan-edit re-applies limits to every account on the plan, so an allowed plan that also has another owner's accounts can't be changed
func TestPlansResellerCannotEditSharedPlan(t *testing.T) {
	calls := fakeOpenCLI(t)
	resellerAllowedPlans(t, "caller", 5)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT 1 FROM users WHERE plan_id = \?`).WithArgs("5", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "reseller")
	resp, err := client.PostForm(srv.URL+"/plans/5", url.Values{"name": {"shared"}, "cpu": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := callsWith(calls(), "plan-edit"); len(got) != 0 {
		t.Fatalf("plan-edit ran for a plan that has another owner's accounts: %v", got)
	}
}

func TestPlansResellerCanEditOwnPlan(t *testing.T) {
	calls := fakeOpenCLI(t)
	resellerAllowedPlans(t, "caller", 5)
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT 1 FROM users WHERE plan_id = \?`).WithArgs("5", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "reseller")
	resp, err := client.PostForm(srv.URL+"/plans/5", url.Values{"name": {"mine"}, "cpu": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := callsWith(calls(), "plan-edit id=5"); len(got) != 1 {
		t.Fatalf("expected plan-edit to run once for the reseller's own plan, got %v", calls())
	}
}

func TestPlansAdminCanEditAnyPlan(t *testing.T) {
	calls := fakeOpenCLI(t)
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	srv, client := newPlansTestServer(t, &Plans{MySQL: mysqlDB}, "admin")
	resp, err := client.PostForm(srv.URL+"/plans/7", url.Values{"name": {"admin-only"}, "cpu": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := callsWith(calls(), "plan-edit id=7"); len(got) != 1 {
		t.Fatalf("expected plan-edit to run for an admin, got %v", calls())
	}
}

// --- JSON API ---

func TestAPIPlansListScopedForReseller(t *testing.T) {
	resellerAllowedPlans(t, "res1", 5)
	srv, client, mock := newAPIPlansTestServer(t, &admindb.User{Username: "res1", Role: "reseller"})
	mock.ExpectQuery(`SELECT \* FROM plans WHERE plans\.id IN \(\?\)`).WithArgs(5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(5, "mine"))

	resp, err := client.Get(srv.URL + "/api/plans")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("reseller plan list wasn't scoped to allowed_plans: %v (body %s)", err, body)
	}
}

func TestAPIPlansResellerCannotDeletePlanNotAllowed(t *testing.T) {
	resellerAllowedPlans(t, "res1", 5)
	srv, client, _ := newAPIPlansTestServer(t, &admindb.User{Username: "res1", Role: "reseller"})
	called := false
	orig := apiCheckOutputRun
	apiCheckOutputRun = func(args ...string) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { apiCheckOutputRun = orig })

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/plans/7", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if called {
		t.Fatal("plan-delete ran for a plan outside the reseller's allowed_plans")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPIPlansResellerCannotEditSharedPlan(t *testing.T) {
	resellerAllowedPlans(t, "res1", 5)
	srv, client, mock := newAPIPlansTestServer(t, &admindb.User{Username: "res1", Role: "reseller"})
	mock.ExpectQuery(`SELECT 1 FROM users WHERE plan_id = \?`).WithArgs("5", "res1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	called := false
	orig := apiCheckOutputRun
	apiCheckOutputRun = func(args ...string) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { apiCheckOutputRun = orig })

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/plans/5", strings.NewReader(`{"name":"shared","cpu":1}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if called {
		t.Fatal("plan-edit ran for a plan that has another owner's accounts")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}
