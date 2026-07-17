package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

func newPlansTestServer(t *testing.T, plans *Plans, role string) (*httptest.Server, *http.Client) {
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
	plans.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /plans", plans.ServeList)
	mux.HandleFunc("GET /plans/new", plans.ServeNewForm)
	mux.HandleFunc("POST /plans/new", plans.HandleCreate)
	mux.HandleFunc("GET /plans/{plan_id}", plans.ServeEdit)
	mux.HandleFunc("POST /plans/{plan_id}", plans.ServeEdit)
	mux.HandleFunc("POST /plan/delete/{plan_name}", plans.HandleDelete)
	mux.HandleFunc("GET /plan/apply/{filename}", plans.ServeApplyLog)
	mux.HandleFunc("GET /system/ips/{username}", plans.ServeIPAddresses)
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

func TestPlansListAdmin(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT\s+plans\.\*`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "name", "disk_limit", "cpu", "ram", "user_count"}).
		AddRow(1, "Starter", "5000", "1", "1", 3))

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plans?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var decoded struct {
		Plans []paneldb.RowMap `json:"plans"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.Plans) != 1 || decoded.Plans[0]["name"] != "Starter" {
		t.Fatalf("unexpected plans: %+v", decoded.Plans)
	}
}

func TestPlansListRendersHTML(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT\s+plans\.\*`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "name", "description", "disk_limit", "inodes_limit", "cpu", "ram", "bandwidth",
			"domains_limit", "websites_limit", "db_limit", "email_limit", "max_email_quota",
			"max_hourly_email", "ftp_limit", "feature_set", "user_count"}).
		AddRow(int64(1), "Starter", "Basic plan", "5 GB", int64(2000000), "1", "1g", int64(100),
			int64(5), int64(5), int64(1), int64(0), int64(0), int64(0), int64(1), "default", int64(3)).
		AddRow(int64(2), "Unlimited", "No limits", "0 GB", int64(0), "0", "0g", int64(0),
			int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), "default", int64(0)))

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plans")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Starter", "Unlimited", "2M", "&#8734;", "function plansTable", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestPlansListResellerWithoutAllowedPlansIsEmpty(t *testing.T) {
	dir := t.TempDir()
	origDir := paneldb.ResellerConfigDir
	paneldb.ResellerConfigDir = dir
	t.Cleanup(func() { paneldb.ResellerConfigDir = origDir })
	// no bob.json written -> AllowedPlansForReseller returns ok=false

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	// no ExpectQuery set up -- the handler must not even query MySQL when a
	// reseller has no allowed_plans configured; it returns early instead

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "reseller")

	resp, err := client.Get(srv.URL + "/plans?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var decoded struct {
		Plans []paneldb.RowMap `json:"plans"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.Plans) != 0 {
		t.Fatalf("expected no plans for a reseller with no allowed_plans, got %+v", decoded.Plans)
	}
}

func TestPlansEditFormRendersExistingPlan(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).
		WithArgs("7").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "disk_limit"}).AddRow(7, "Business", "50000"))

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plans/7?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var decoded struct {
		Plan paneldb.RowMap `json:"plan"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if decoded.Plan["name"] != "Business" {
		t.Fatalf("unexpected plan: %+v", decoded.Plan)
	}
}

func TestPlansNewFormRendersHTML(t *testing.T) {
	plans := &Plans{}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plans/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "New Package") {
		t.Fatalf("expected the new-plan form, got %s", truncate(string(body)))
	}
}

func TestPlansEditFormRendersHTML(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).
		WithArgs("7").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "disk_limit", "ram"}).AddRow(7, "Business", "50 GB", "2g"))

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plans/7")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Business", `value='50'`, `value='2'`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestPlansCreateAndDeleteFallBackGracefullyWithoutOpenCLI(t *testing.T) {
	// opencli isn't installed in this test environment, so create/delete
	// must fail gracefully (flash an error, redirect) rather than crash.
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.PostForm(srv.URL+"/plans/new", url.Values{"name": {"NewPlan"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.URL.Path != "/plans" {
		t.Fatalf("expected redirect back to /plans, ended at %q", resp.Request.URL.Path)
	}
	resp.Body.Close()

	resp, err = client.PostForm(srv.URL+"/plan/delete/NewPlan", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.URL.Path != "/plans" {
		t.Fatalf("expected redirect back to /plans, ended at %q", resp.Request.URL.Path)
	}
	resp.Body.Close()
}

func TestServeApplyLogServesFileByBaseName(t *testing.T) {
	dir := t.TempDir()
	origTmp := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", dir)
	t.Cleanup(func() { os.Setenv("TMPDIR", origTmp) })
	os.WriteFile(filepath.Join(dir, "plan-create.log"), []byte("plan created ok"), 0644)

	plans := &Plans{}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plan/apply/plan-create.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "plan created ok" {
		t.Fatalf("expected 200 with file contents, got %d %q", resp.StatusCode, body)
	}
}

func TestServeApplyLogRejectsPathTraversal(t *testing.T) {
	plans := &Plans{}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/plan/apply/..%2F..%2Fetc%2Fpasswd")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected a path-traversal attempt to be rejected, got 200")
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"10.0.0.5", false},
		{"192.168.1.1", false},
		{"172.16.0.1", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
	}
	for _, c := range cases {
		got := isPublicIP(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("isPublicIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestServeIPAddressesFiltersToPublicOnly(t *testing.T) {
	origRun := planIPAddressesRun
	planIPAddressesRun = func() (string, error) { return "10.0.0.5 8.8.8.8 192.168.1.1\n", nil }
	t.Cleanup(func() { planIPAddressesRun = origRun })

	plans := &Plans{}
	srv, client := newPlansTestServer(t, plans, "admin")

	resp, err := client.Get(srv.URL + "/system/ips/someuser")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded struct {
		IPAddresses []string `json:"ip_addresses"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", body, err)
	}
	if len(decoded.IPAddresses) != 1 || decoded.IPAddresses[0] != "8.8.8.8" {
		t.Fatalf("expected only the public IP, got %+v", decoded.IPAddresses)
	}
}

func TestServeIPAddressesResellerForbiddenForNonOwnedUser(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).WithArgs("someoneelse", "caller").WillReturnError(sql.ErrNoRows)

	plans := &Plans{MySQL: mysqlDB}
	srv, client := newPlansTestServer(t, plans, "reseller")

	resp, err := client.Get(srv.URL + "/system/ips/someoneelse")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller requesting a non-owned user's IPs, got %d", resp.StatusCode)
	}
}

func TestSortRowMaps(t *testing.T) {
	rows := []paneldb.RowMap{
		{"name": "Charlie", "id": int64(3)},
		{"name": "Alice", "id": int64(1)},
		{"name": "Bob", "id": int64(2)},
	}
	sortRowMaps(rows, "name", false)
	if rows[0]["name"] != "Alice" || rows[1]["name"] != "Bob" || rows[2]["name"] != "Charlie" {
		t.Fatalf("expected ascending name sort, got %v, %v, %v", rows[0]["name"], rows[1]["name"], rows[2]["name"])
	}

	sortRowMaps(rows, "id", true)
	if rows[0]["id"] != int64(3) || rows[2]["id"] != int64(1) {
		t.Fatalf("expected descending id sort, got %v, %v, %v", rows[0]["id"], rows[1]["id"], rows[2]["id"])
	}
}
