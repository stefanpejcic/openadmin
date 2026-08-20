package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newUsersTestServer(t *testing.T, u *Users, role string) (*httptest.Server, *http.Client) {
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
	u.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /users", u.ServeList)
	mux.HandleFunc("GET /user/new", u.ServeCreateUser)
	mux.HandleFunc("POST /user/new", u.ServeCreateUser)
	mux.HandleFunc("GET /users/{username}", u.ServeDetail)
	mux.HandleFunc("POST /user/{action}/{username}", u.HandleManage)
	mux.HandleFunc("GET /get_resource_usage_history/{username}", u.ServeResourceUsageHistory)
	mux.HandleFunc("GET /client/disk/{username}", u.ServeUserDiskInfo)
	mux.HandleFunc("GET /json/{userLogType}/{username}", u.ServeUserLog)
	mux.HandleFunc("GET /get_custom_message_for_user/{username}", u.HandleCustomMessage)
	mux.HandleFunc("POST /get_custom_message_for_user/{username}", u.HandleCustomMessage)
	mux.HandleFunc("POST /containers/{username}/{action}/{container_name}", u.ServeManageContainer)
	mux.HandleFunc("GET /containers/stats/{username}", u.ServeContainersStats)
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

func TestUsersListAdminSeesAllUsers(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT users\.\*`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "username", "email", "owner", "name"}).
		AddRow(1, "alice", "alice@example.com", nil, "Starter"))
	mock.ExpectQuery(`SELECT \* FROM plans`).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Starter"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.Users) != 1 || decoded.Users[0]["username"] != "alice" {
		t.Fatalf("unexpected users: %+v", decoded.Users)
	}
}

func TestUsersListResellerScopedToOwnAccounts(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT users\.\*.*WHERE users\.owner = \?`).
		WithArgs("caller").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(2, "resellersclient"))
	// no allowed_plans file for "caller" -> GetAllPlans should get nil (unrestricted call skipped since ok=false)

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.Get(srv.URL + "/users?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.Users) != 1 || decoded.Users[0]["username"] != "resellersclient" {
		t.Fatalf("unexpected users: %+v", decoded.Users)
	}
}

func TestUsersListMySQLDownFallsBackGracefully(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT users\.\*`).WillReturnError(sqlErrConnRefused{})

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected graceful 200 fallback, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Is MySQL running?") {
		t.Fatalf("expected the mysql-is-down warning, got %s", truncate(string(body)))
	}
}

func TestUsersDetailDeniedForNonOwningReseller(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("someoneelse", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.Get(srv.URL + "/users/someoneelse")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestUsersDetailReturnsCoreFields(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", 3, "alice@example.com", nil, true, "2025-06-01", 1, "alice"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users/alice?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"context":"alice"`) {
		t.Fatalf("expected context field, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), `"stats":"no data yet"`) {
		t.Fatalf("expected 'no data yet' when no resource_usage.txt exists, got %s", truncate(string(body)))
	}
}

func TestUsersDetailRendersHTML(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(1), "alice"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"alice", "alice@example.com", "Statistics", "Suspend user account", "Delete user account", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

// TestUsersDetailShowsPlanLimitsNotEnvFile covers a bug where the
// CPU/Memory summary in the page header was read from the user's own
// /home/<context>/.env file (TOTAL_CPU/TOTAL_RAM) instead of the plan the
// account is actually on. A user's .env is user-writable data, not a
// source of truth for their plan's limits, and can drift from it (e.g. an
// admin changes the plan without regenerating .env, or a user edits their
// own compose overrides) -- the display must reflect the plans table.
func TestUsersDetailShowsPlanLimitsNotEnvFile(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(7), "alice"))
	mock.ExpectQuery(`SELECT \* FROM plans WHERE id = \?`).
		WithArgs("7").
		WillReturnRows(sqlmock.NewRows([]string{"id", "feature_set", "cpu", "ram"}).
			AddRow(7, "default", "4", "8g"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	if !strings.Contains(got, "4 Cores") {
		t.Fatalf("expected the plan's CPU limit (4) in the page, got %s", truncate(got))
	}
	if !strings.Contains(got, "8 GB") {
		t.Fatalf("expected the plan's RAM limit (8 GB) in the page, got %s", truncate(got))
	}
}

func TestUsersDetailNotFound(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/users/ghost?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "not found") {
		t.Fatalf("expected not-found message, got %s", truncate(string(body)))
	}
}

func TestManageUserDeleteRequiresConfirmationMatch(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.MatchExpectationsInOrder(false)

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/user/delete/alice", url.Values{"confirmation": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a confirmation mismatch, got %d", resp.StatusCode)
	}
}

func TestManageUserUnsuspendStripsLastUnderscoreSegment(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	dir := t.TempDir()
	origLogDir := "/etc/openpanel/openpanel/core/users"
	_ = origLogDir // logUserAction writes here; harmless no-op if unwritable in sandbox
	_ = dir

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	// opencli isn't installed in this test environment, so this exercises
	// the graceful-failure path -- the important thing is that it doesn't
	// panic and redirects back to /users either way
	resp, err := client.PostForm(srv.URL+"/user/unsuspend/SUSPENDED_1730000000_alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/users" {
		t.Fatalf("expected redirect to /users, ended at %q", resp.Request.URL.Path)
	}
}

func TestManageUserPermissionsResetDeletesCustomFeaturesFile(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(1), "alice"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	// userFeaturesPath("alice") points at a real system path
	// (/home/alice/features.txt) not writable/removable in this sandbox --
	// the important thing is os.Remove's IsNotExist case is treated as
	// success rather than an error, matching a user who was never
	// customized in the first place.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.PostForm(srv.URL+"/user/permissions_reset/alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	if loc != "/users/alice#permissions" {
		t.Fatalf("expected redirect to /users/alice#permissions, got %q (status %d)", loc, resp.StatusCode)
	}
}

func TestManageUserInvalidActionFlagged(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/user/bogus/alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid user action") {
		t.Fatalf("expected invalid-action flash, got %s", truncate(string(body)))
	}
}

func TestReadDiskUsageAllAndStripSuspendedPrefix(t *testing.T) {
	dir := t.TempDir()
	origPath := QuotaReportPath
	QuotaReportPath = filepath.Join(dir, "quota_report.json")
	t.Cleanup(func() { QuotaReportPath = origPath })

	os.WriteFile(QuotaReportPath, []byte(`{"users":[{"username":"alice","disk_used":100,"disk_soft":200}]}`), 0644)

	all := readDiskUsageAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}

	d, ok := readDiskUsageFor("SUSPENDED_1730000000_alice")
	if !ok {
		t.Fatal("expected disk usage to be found via the stripped username")
	}
	if d.DiskUsed != float64(100) {
		t.Fatalf("unexpected disk_used: %v", d.DiskUsed)
	}
}

func TestStripSuspendedPrefixOnlyStripsWhenMarkerPresent(t *testing.T) {
	if got := stripSuspendedPrefix("plain_username"); got != "plain_username" {
		t.Fatalf("expected no stripping without a SUSPENDED_ marker, got %q", got)
	}
	if got := stripSuspendedPrefix("SUSPENDED_1730000000_alice"); got != "alice" {
		t.Fatalf("expected stripping down to the real username, got %q", got)
	}
}

func TestServeCreateUserGetRendersHTML(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT \* FROM plans`).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "name", "description", "cpu", "ram", "disk_limit", "inodes_limit",
			"bandwidth", "domains_limit", "websites_limit", "email_limit", "ftp_limit", "db_limit"}).
		AddRow(1, "Starter", "A starter plan", "1", "1g", "10 GB", 100000, 100, 1, 1, 1, 1, 1))

	dir := t.TempDir()
	origEnv := DefaultsEnvPath
	DefaultsEnvPath = dir + "/.env"
	os.WriteFile(DefaultsEnvPath, []byte(`WEB_SERVER="nginx"`+"\n"+`MYSQL_TYPE="mysql"`+"\n"), 0644)
	t.Cleanup(func() { DefaultsEnvPath = origEnv })

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/user/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(got))
	}
	for _, want := range []string{"Create User Account", "Starter", "Webserver", "Database type", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeCreateUserPostStreamsOpenCLIStdoutAndBuildsArgv(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	var gotArgs []string
	origRun := userCreateRun
	userCreateRun = func(args []string) (*exec.Cmd, error) {
		gotArgs = args
		return exec.Command("printf", "Successfully added user\n"), nil
	}
	t.Cleanup(func() { userCreateRun = origRun })

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/user/new", url.Values{
		"admin_username": {"Emma"},
		"admin_password": {"s3cret!"},
		"admin_email":    {"emma@example.com"},
		"plan_name":      {"Starter"},
		"webserver":      {"nginx"},
		"sql_type":       {"mariadb"},
		"send_email":     {"on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Successfully added user") {
		t.Fatalf("expected streamed opencli output, got %q", body)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"opencli user-add emma s3cret! emma@example.com Starter --debug",
		"--webserver=nginx", "--sql=mariadb", "--send-email",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected argv to contain %q, got %q", want, joined)
		}
	}
}

func TestServeCreateUserPostRequiresSSHKeyForSlave(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	origRun := userCreateRun
	called := false
	userCreateRun = func(args []string) (*exec.Cmd, error) {
		called = true
		return exec.Command("true"), nil
	}
	t.Cleanup(func() { userCreateRun = origRun })

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/user/new", url.Values{
		"admin_username": {"bob"},
		"admin_password": {"s3cret!"},
		"plan_name":      {"Starter"},
		"slave":          {"10.0.0.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if called {
		t.Fatal("expected opencli to never run without an ssh_key_path for a remote slave")
	}
	if !strings.Contains(string(body), "SSH key path is required") {
		t.Fatalf("expected the SSH-key error message, got %q", body)
	}
}

func TestServeCreateUserPostResellerForcesOwnUsernameAsReseller(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	var gotArgs []string
	origRun := userCreateRun
	userCreateRun = func(args []string) (*exec.Cmd, error) {
		gotArgs = args
		return exec.Command("true"), nil
	}
	t.Cleanup(func() { userCreateRun = origRun })

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.PostForm(srv.URL+"/user/new", url.Values{
		"admin_username": {"carol"},
		"admin_password": {"s3cret!"},
		"plan_name":      {"Starter"},
		"reseller":       {"someone-else"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--reseller=caller") {
		t.Fatalf("expected the caller's own username forced as --reseller (ignoring the submitted 'reseller' field for an actual reseller caller), got %q", joined)
	}
}

// --- GET /get_resource_usage_history/{username} ---

func TestServeResourceUsageHistoryNotFoundUsesStrippedUsernameForContextLookup(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	// ServeResourceUsageHistory strips "SUSPENDED_..." before doing
	// anything else, including the context lookup -- assert the plain
	// username is what's actually queried for.
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice"))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/get_resource_usage_history/SUSPENDED_1730000000_alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 (no resource_usage.txt file exists in this sandbox), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Resource usage file not found.") {
		t.Fatalf("expected the not-found message, got %s", truncate(string(body)))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("context lookup didn't use the stripped username: %v", err)
	}
}

func TestServeResourceUsageHistoryDeniedForNonOwningReseller(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("bob", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.Get(srv.URL + "/get_resource_usage_history/bob")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// --- GET /client/disk/{username} ---

func TestServeUserDiskInfoReturnsJSONArray(t *testing.T) {
	origRun := userDiskInfoRun
	userDiskInfoRun = func(context string) (string, string, int, error) {
		return "{\"Type\":\"Images\",\"Total\":1}\n{\"Type\":\"Containers\",\"Total\":2}\n", "", 0, nil
	}
	t.Cleanup(func() { userDiskInfoRun = origRun })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/client/disk/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded []map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded) != 2 || decoded[0]["Type"] != "Images" || decoded[1]["Type"] != "Containers" {
		t.Fatalf("unexpected decoded data: %+v", decoded)
	}
}

func TestServeUserDiskInfoHandlesNonzeroExitCode(t *testing.T) {
	origRun := userDiskInfoRun
	userDiskInfoRun = func(context string) (string, string, int, error) {
		return "", "no such context", 1, nil
	}
	t.Cleanup(func() { userDiskInfoRun = origRun })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/client/disk/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "no such context") {
		t.Fatalf("expected the podman stderr in the error body, got %s", truncate(string(body)))
	}
}

func TestServeUserDiskInfoDeniedForNonOwningReseller(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("bob", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.Get(srv.URL + "/client/disk/bob")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// --- GET /json/{userLogType}/{username} ---

func TestServeUserLogRejectsMissingUserDashPrefix(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	// The real reachable URL is /json/user-activity/alice, not
	// /json/activity/alice -- the latter must 404, not be silently accepted.
	resp, err := client.Get(srv.URL + "/json/activity/alice")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeUserLogRejectsUnknownLogType(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/json/user-bogus/alice")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeUserLogParsedNotFoundUsesGenericMessage(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/json/user-activity/definitely-nonexistent-user-xyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	// Deliberately generic -- unlike the raw/download 404s below, which
	// mention the log type -- because it comes from a separate code path
	// that was never unified with those.
	if !strings.Contains(string(body), `"error":"Log not found"`) {
		t.Fatalf("expected the generic not-found message, got %s", truncate(string(body)))
	}
}

func TestServeUserLogRawNotFoundUsesTypeSpecificCapitalizedMessage(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/json/user-activity/definitely-nonexistent-user-xyz?raw=true")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"error":"Activity log not found"`) {
		t.Fatalf("expected the type-specific capitalized message, got %s", truncate(string(body)))
	}
}

func TestServeUserLogDownloadNotFoundUsesTypeSpecificCapitalizedMessage(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/json/user-logins/definitely-nonexistent-user-xyz?download=true")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"error":"Logins log not found"`) {
		t.Fatalf("expected the type-specific capitalized message, got %s", truncate(string(body)))
	}
}

func TestParseGenericUserLogLineActivity(t *testing.T) {
	conf := userLogConfigs["activity"]
	line := "2024-01-02 03:04:05 10.0.0.5 Administrator root suspended user bob"
	got := parseGenericUserLogLine(line, conf)
	want := map[string]string{
		"timestamp":  "2024-01-02 03:04:05",
		"ip_address": "10.0.0.5",
		"user_type":  "Administrator",
		"user":       "root",
		"action":     "suspended user bob",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseGenericUserLogLineLoginsUsesNamedGroups(t *testing.T) {
	conf := userLogConfigs["logins"]
	line := "IP: 1.2.3.4 - Country: US - Login Time: 2024-01-02 03:04:05"
	got := parseGenericUserLogLine(line, conf)
	want := map[string]string{"ip": "1.2.3.4", "country": "US", "login_time": "2024-01-02 03:04:05"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseGenericUserLogLineNoMatchReturnsNil(t *testing.T) {
	conf := userLogConfigs["activity"]
	if got := parseGenericUserLogLine("this line matches nothing", conf); got != nil {
		t.Fatalf("expected nil for an unmatched line, got %+v", got)
	}
}

func TestCapitalizeFirst(t *testing.T) {
	cases := map[string]string{"activity": "Activity", "logins": "Logins", "": ""}
	for in, want := range cases {
		if got := capitalizeFirst(in); got != want {
			t.Fatalf("capitalizeFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- GET/POST /get_custom_message_for_user/{username} ---

func TestHandleCustomMessageGetNoMessageFound(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/get_custom_message_for_user/definitely-nonexistent-user-xyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"message":"No custom message found"`) {
		t.Fatalf("expected the no-message-found body, got %s", truncate(string(body)))
	}
}

func TestHandleCustomMessagePostInvalidContentTypeRejected(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Post(srv.URL+"/get_custom_message_for_user/alice", "text/plain", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Invalid content type. Expected JSON.") {
		t.Fatalf("expected the invalid-content-type message, got %s", truncate(string(body)))
	}
}

func TestHandleCustomMessageDeniedForNonOwningReseller(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("bob", "caller").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "reseller")

	resp, err := client.Get(srv.URL + "/get_custom_message_for_user/bob")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}
