package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
)

// withTestActingUser stashes user on the request context exactly like
// RequireAPIToken would, without needing a real bearer token in these
// tests -- user may be nil to simulate a token whose acting user no longer
// exists in admindb.
func withTestActingUser(user *admindb.User, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := ""
		if user != nil {
			username = user.Username
		}
		next(w, withAPIUser(withAPIUsername(r, username), user))
	}
}

func newAPIPlansTestServer(t *testing.T, actingUser *admindb.User) (*httptest.Server, *http.Client, sqlmock.Sqlmock) {
	t.Helper()
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })

	p := &APIPlans{MySQL: mysqlDB}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plans", withTestActingUser(actingUser, p.ServeList))
	mux.HandleFunc("POST /api/plans", withTestActingUser(actingUser, p.ServeList))
	mux.HandleFunc("GET /api/plans/{plan_id}", withTestActingUser(actingUser, p.ServeDetail))
	mux.HandleFunc("PUT /api/plans/{plan_id}", withTestActingUser(actingUser, p.ServeDetail))
	mux.HandleFunc("PATCH /api/plans/{plan_id}", withTestActingUser(actingUser, p.ServeDetail))
	mux.HandleFunc("DELETE /api/plans/{plan_id}", withTestActingUser(actingUser, p.ServeDetail))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client(), mock
}

func TestServePlansListReturnsAllPlansRegardlessOfRole(t *testing.T) {
	reseller := &admindb.User{Username: "res1", Role: "reseller"}
	srv, client, mock := newAPIPlansTestServer(t, reseller)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "starter").AddRow(2, "pro")
	mock.ExpectQuery("SELECT \\* FROM plans").WillReturnRows(rows)

	resp, err := client.Get(srv.URL + "/api/plans")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Plans []map[string]interface{} `json:"plans"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Plans) != 2 {
		t.Fatalf("expected 2 plans (unfiltered even for a reseller), got %+v", out)
	}
}

func TestServePlansCreateRequiresActingUser(t *testing.T) {
	srv, client, _ := newAPIPlansTestServer(t, nil)

	resp, err := client.Post(srv.URL+"/api/plans", "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServePlansCreateRequiresJSONContentType(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, _ := newAPIPlansTestServer(t, admin)

	resp, err := client.Post(srv.URL+"/api/plans", "text/plain", strings.NewReader(``))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServePlansCreateSuccessAppendsResellerArg(t *testing.T) {
	reseller := &admindb.User{Username: "res1", Role: "reseller"}
	srv, client, _ := newAPIPlansTestServer(t, reseller)

	var gotArgs []string
	orig := apiRunCapture
	apiRunCapture = func(args ...string) (string, string, int) {
		gotArgs = args
		return "Plan created.\n", "", 0
	}
	t.Cleanup(func() { apiRunCapture = orig })

	body := `{"name":"Starter","disk_limit":"5000"}`
	resp, err := client.Post(srv.URL+"/api/plans", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	found := false
	for _, a := range gotArgs {
		if a == "reseller=res1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reseller=res1 in args, got %v", gotArgs)
	}
}

func TestServePlansCreateFailureReturns500(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, _ := newAPIPlansTestServer(t, admin)

	orig := apiRunCapture
	apiRunCapture = func(args ...string) (string, string, int) { return "", "boom", 1 }
	t.Cleanup(func() { apiRunCapture = orig })

	resp, err := client.Post(srv.URL+"/api/plans", "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestServePlanDetailNonNumericIDReturns404(t *testing.T) {
	srv, client, _ := newAPIPlansTestServer(t, nil)
	resp, err := client.Get(srv.URL + "/api/plans/abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServePlanDetailGetNotFound(t *testing.T) {
	srv, client, mock := newAPIPlansTestServer(t, nil)
	mock.ExpectQuery("SELECT \\* FROM plans WHERE id = ?").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	resp, err := client.Get(srv.URL + "/api/plans/99")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServePlanDetailGetFound(t *testing.T) {
	srv, client, mock := newAPIPlansTestServer(t, nil)
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "starter")
	mock.ExpectQuery("SELECT \\* FROM plans WHERE id = ?").WillReturnRows(rows)

	resp, err := client.Get(srv.URL + "/api/plans/1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServePlanDetailDeleteSuccess(t *testing.T) {
	srv, client, _ := newAPIPlansTestServer(t, nil)
	stubAPICheckOutputRun(t, "deleted", nil)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/plans/1", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["message"] != "Plan deleted successfully." || out["output"] != "deleted" {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestServePlanDetailEditRequiresJSONContentType(t *testing.T) {
	srv, client, _ := newAPIPlansTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/plans/1", strings.NewReader(``))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServePlanDetailEditMissingNameRendersNone(t *testing.T) {
	srv, client, _ := newAPIPlansTestServer(t, nil)

	var gotArgs []string
	orig := apiCheckOutputRun
	apiCheckOutputRun = func(args ...string) (string, error) {
		gotArgs = args
		return "edited", nil
	}
	t.Cleanup(func() { apiCheckOutputRun = orig })

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/plans/1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	found := false
	for _, a := range gotArgs {
		if a == "name=None" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected name=None in args, got %v", gotArgs)
	}
}
