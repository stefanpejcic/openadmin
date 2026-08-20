package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newUserExportTestServer(t *testing.T, u *Users, role string) (*httptest.Server, *http.Client, sqlmock.Sqlmock) {
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

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })
	u.MySQL = mysqlDB

	sessions := auth.NewManager("test-secret", false)
	u.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/export/status/{username}", u.ServeUserExportStatus)
	mux.HandleFunc("POST /user/export/create/{username}", u.ServeUserExportCreate)
	mux.HandleFunc("GET /user/export/download/{username}/{filename...}", u.ServeUserExportDownload)
	mux.HandleFunc("POST /user/export/delete/{username}", u.ServeUserExportDelete)
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
	return srv, client, mock
}

func expectAliceUserData(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(1), "alice"))
}

func TestUserExportFormatSize(t *testing.T) {
	cases := map[float64]string{
		512:             "512.0 B",
		2048:            "2.0 KB",
		5 * 1024 * 1024: "5.0 MB",
	}
	for in, want := range cases {
		if got := userExportFormatSize(in); got != want {
			t.Fatalf("userExportFormatSize(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestServeUserExportStatusForbiddenForNonOwningReseller(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "reseller")
	mock.ExpectQuery(`SELECT 1 FROM users`).
		WithArgs("alice", "caller").
		WillReturnError(sqlErrConnRefused{})

	resp, err := client.Get(srv.URL + "/user/export/status/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserExportStatusReturnsBackupList(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.Get(srv.URL + "/user/export/status/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"in_progress":false`) {
		t.Fatalf("expected in_progress:false, got %s", body)
	}
	if !strings.Contains(string(body), `"backups":[]`) {
		t.Fatalf("expected empty backups list (no such /home/alice dir in sandbox), got %s", body)
	}
}

func TestServeUserExportCreateRefusesWhenAlreadyInProgress(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return true }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.PostForm(srv.URL+"/user/export/create/alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when a backup is already in progress, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserExportCreateStartsBackupCommand(t *testing.T) {
	origHome := userExportHomeRoot
	userExportHomeRoot = t.TempDir()
	t.Cleanup(func() { userExportHomeRoot = origHome })

	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	origInProgress := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = origInProgress })

	var gotUsername string
	origCmd := userExportBackupCmdRun
	userExportBackupCmdRun = func(username string) error { gotUsername = username; return nil }
	t.Cleanup(func() { userExportBackupCmdRun = origCmd })

	resp, err := client.PostForm(srv.URL+"/user/export/create/alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if gotUsername != "alice" {
		t.Fatalf("expected userExportBackupCmdRun called with alice, got %q", gotUsername)
	}
	if !strings.Contains(string(body), `"scheduled":true`) {
		t.Fatalf("expected scheduled response, got %s", body)
	}
}

func TestServeUserExportDeleteRejectsPathTraversal(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.PostForm(srv.URL+"/user/export/delete/alice", url.Values{"filename": {"../../../etc/passwd"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a path-traversal filename, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserExportDeleteNotFound(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.PostForm(srv.URL+"/user/export/delete/alice", url.Values{"filename": {"does-not-exist.tar.gz"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserExportDownloadRejectsPathTraversal(t *testing.T) {
	u := &Users{}
	srv, client, mock := newUserExportTestServer(t, u, "admin")
	expectAliceUserData(mock)

	resp, err := client.Get(srv.URL + "/user/export/download/alice/..%2f..%2fetc%2fpasswd")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected a non-200 for a path-traversal filename, got 200: %s", body)
	}
}

func TestUserExportDetailPageShowsExportTabWhenEnterprise(t *testing.T) {
	origLicense := chromeSite.LicenseType
	InitChromeSiteInfo("", "", "", "", "Enterprise", false, "")
	t.Cleanup(func() { InitChromeSiteInfo("", "", "", "", origLicense, false, "") })

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
	got := string(body)
	for _, want := range []string{"Export", "Generate full account backup", "Transfer to another server", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}
