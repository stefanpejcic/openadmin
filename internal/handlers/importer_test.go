package handlers

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newImporterTestServer(t *testing.T, im *Importer, role string) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origImportDir := ImporterOpenPanelImportLogDir
	ImporterOpenPanelImportLogDir = filepath.Join(dir, "imports") + string(os.PathSeparator)
	os.MkdirAll(ImporterOpenPanelImportLogDir, 0755)
	t.Cleanup(func() { ImporterOpenPanelImportLogDir = origImportDir })

	origTransferDir := ImporterTransferLogDir
	ImporterTransferLogDir = filepath.Join(dir, "transfers") + string(os.PathSeparator)
	os.MkdirAll(ImporterTransferLogDir, 0755)
	t.Cleanup(func() { ImporterTransferLogDir = origTransferDir })

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
	im.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/import", im.ServeImportUser)
	mux.HandleFunc("GET /import/{panel_type}", im.ServeImportFromBackup)
	mux.HandleFunc("POST /import/{panel_type}", im.ServeImportFromBackup)
	mux.HandleFunc("GET /import/user/log/{log_filename...}", im.ServeViewTransferImportLog)
	mux.HandleFunc("GET /import/account/log/{log_filename...}", im.ServeViewAccountImportLog)
	mux.HandleFunc("GET /json/backup-files", im.ServeListBackupFiles)
	mux.HandleFunc("GET /json/transfers", im.ServeListTransfers)
	mux.HandleFunc("GET /json/transfers/{username}", im.ServeListTransfersFor)
	mux.HandleFunc("GET /import/transfer/", im.ServeImportTransfer)
	mux.HandleFunc("POST /import/transfer/", im.ServeImportTransfer)
	usersStub := func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			io.WriteString(w, f.Message)
		}
	}
	mux.HandleFunc("GET /users/{username}", usersStub)
	mux.HandleFunc("GET /users/", usersStub)
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

func TestIsDomain(t *testing.T) {
	cases := map[string]bool{
		"example.com": true, "sub.example.com": true,
		"192.168.1.1": false, "not a domain": false, "": false,
	}
	for in, want := range cases {
		if got := isDomain(in); got != want {
			t.Errorf("isDomain(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDetermineLogStatusCompletedAndFailed(t *testing.T) {
	dir := t.TempDir()

	completed := filepath.Join(dir, "completed.log")
	os.WriteFile(completed, []byte("Starting import\nPID: 999999999\nSUCCESS: done\n"), 0644)
	if got := determineLogStatus(completed); got != "completed" {
		t.Fatalf("expected completed, got %q", got)
	}

	failed := filepath.Join(dir, "failed.log")
	os.WriteFile(failed, []byte("Starting import\nPID: 999999999\nFATAL ERROR: boom\n"), 0644)
	if got := determineLogStatus(failed); got != "failed" {
		t.Fatalf("expected failed, got %q", got)
	}

	unknown := filepath.Join(dir, "unknown.log")
	os.WriteFile(unknown, []byte("Starting import\nPID: 999999999\nstill going\n"), 0644)
	if got := determineLogStatus(unknown); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}

	if got := determineLogStatus(filepath.Join(dir, "missing.log")); got != "unknown" {
		t.Fatalf("expected unknown for a missing file, got %q", got)
	}
}

func TestDetermineLogStatusRunningWhenPidAlive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "running.log")
	// Our own test process's PID is definitely alive.
	os.WriteFile(path, []byte("Starting\nPID: "+strconv.Itoa(os.Getpid())+"\n"), 0644)
	if got := determineLogStatus(path); got != "running" {
		t.Fatalf("expected running, got %q", got)
	}
}

// newImporterPlansStubDB returns a sqlmock *sql.DB that answers
// ServeImportUser's paneldb.GetAllPlans query with zero rows -- used instead
// of a nil MySQL field since a real (even if empty) *sql.DB is what
// production always has (sql.Open never returns nil), and a literal nil
// panics inside database/sql rather than surfacing as a handled query error.
func newImporterPlansStubDB(t *testing.T) *sql.DB {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM plans`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	return db
}

func TestServeImportUserRendersPlans(t *testing.T) {
	im := &Importer{MySQL: newImporterPlansStubDB(t)}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/user/import")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{
		"Import Account from backup",
		"Backup file",
		"Backup type",
		"No plans available.",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

// TestServeImportUserRendersPlanCards exercises the plan-card branch (not
// covered by TestServeImportUserRendersPlans's zero-row stub), including a
// plan whose cpu/ram/disk_limit/inodes_limit/etc. are all at their
// "unlimited" sentinel values, to guard against the template panicking or
// silently truncating mid-render on the infinity-symbol branches.
func TestServeImportUserRendersPlanCards(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM plans`)).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "name", "description", "disk_limit", "inodes_limit", "cpu", "ram", "bandwidth",
			"domains_limit", "websites_limit", "db_limit", "email_limit", "ftp_limit"}).
		AddRow(int64(1), "Starter", "Basic plan", "5 GB", int64(2000000), "1", "1g", int64(100),
			int64(5), int64(5), int64(1), int64(1), int64(1)).
		AddRow(int64(2), "Unlimited", "No limits", "0 GB", int64(0), "0", "0G", int64(0),
			int64(0), int64(0), int64(0), int64(0), int64(0)))

	im := &Importer{MySQL: db}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/user/import")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{
		"Starter",
		"Basic plan",
		"Unlimited",
		"No limits",
		"&#8734;", // the infinity symbol shown for the Unlimited plan's zero-valued limits
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImportFromBackupUnsupportedPanelType(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/import/plesk")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeImportFromBackupGETListsLogs(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	os.WriteFile(filepath.Join(ImporterOpenPanelImportLogDir, "a.log"), []byte("x\nPID: 999999999\nSUCCESS: ok\n"), 0644)

	resp, err := client.Get(srv.URL + "/import/openpanel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{
		"Account Imports",
		"a.log",
		"Completed",
		"Import Account",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImportFromBackupGETNoLogsFound(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/import/openpanel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"No log files found.", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImportFromBackupOpenPanelPOSTMissingPath(t *testing.T) {
	im := &Importer{MySQL: newImporterPlansStubDB(t)}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/openpanel", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Backup file is required") {
		t.Fatalf("expected the missing-path flash, got %s", truncate(string(body)))
	}
}

func TestServeImportFromBackupOpenPanelPOSTSuccess(t *testing.T) {
	origRun := importerRestoreOpenPanelBackupRun
	var captured string
	importerRestoreOpenPanelBackupRun = func(backupPath string) error {
		captured = backupPath
		return nil
	}
	t.Cleanup(func() { importerRestoreOpenPanelBackupRun = origRun })

	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/openpanel", url.Values{"path": {"/root/backup.tar.gz"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "has started") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if captured != "/root/backup.tar.gz" {
		t.Fatalf("expected the backup path passed through, got %q", captured)
	}
}

func TestServeImportFromBackupCpanelPOSTMissingFields(t *testing.T) {
	im := &Importer{MySQL: newImporterPlansStubDB(t)}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/cpanel", url.Values{"path": {"/home/backup-x.tar.gz"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Backup path and plan name are required") {
		t.Fatalf("expected the missing-fields flash, got %s", truncate(string(body)))
	}
}

func TestServeImportFromBackupCpanelPOSTCloneFailureIsWarning(t *testing.T) {
	origRun := importerCloneAndRunImportScriptRun
	importerCloneAndRunImportScriptRun = func(displayName, backupPath, planName string) (bool, error) {
		return true, errTestCloneFailed
	}
	t.Cleanup(func() { importerCloneAndRunImportScriptRun = origRun })

	im := &Importer{MySQL: newImporterPlansStubDB(t)}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/cpanel", url.Values{
		"path": {"/home/backup-x.tar.gz"}, "plan_name": {"basic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error during execution") {
		t.Fatalf("expected the clone-failure warning flash, got %s", truncate(string(body)))
	}
}

func TestServeViewAccountImportLogServesContent(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	os.WriteFile(filepath.Join(ImporterOpenPanelImportLogDir, "test.log"), []byte("hello log"), 0644)

	resp, err := client.Get(srv.URL + "/import/account/log/test.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello log" {
		t.Fatalf("expected log content served, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeViewAccountImportLogMissingFileJSON(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/import/account/log/nonexistent.log?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"status":"error"`) {
		t.Fatalf("expected JSON error shape, got %s", truncate(string(body)))
	}
}

func TestServeListBackupFilesMatchesPatterns(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "backup-alice.tar.gz"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "bob_2024-01-15_10-30-00.tar.gz"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "not-a-backup.txt"), []byte(""), 0644)

	origDirs := importerBackupSearchDirs
	importerBackupSearchDirs = []string{dir}
	t.Cleanup(func() { importerBackupSearchDirs = origDirs })

	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/json/backup-files")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "backup-alice.tar.gz") {
		t.Fatalf("expected cPanel-style backup matched, got %s", truncate(string(body)))
	}
	if !strings.Contains(string(body), "bob_2024-01-15_10-30-00.tar.gz") {
		t.Fatalf("expected OpenPanel-style backup matched, got %s", truncate(string(body)))
	}
	if strings.Contains(string(body), "not-a-backup.txt") {
		t.Fatalf("expected non-matching file excluded, got %s", truncate(string(body)))
	}
}

func TestServeListTransfersGlobsLogDir(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "alice_2024.log"), []byte(""), 0644)

	resp, err := client.Get(srv.URL + "/json/transfers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "alice_2024.log") {
		t.Fatalf("expected the transfer log listed, got %s", truncate(string(body)))
	}
}

func TestServeListTransfersForStripsSuspendedPrefix(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "bob_2024.log"), []byte("started\nPID: 999999999\nSUCCESS: ok\n"), 0644)

	resp, err := client.Get(srv.URL + "/json/transfers/SUSPENDED_bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "bob_2024.log") {
		t.Fatalf("expected the un-suspended username's transfer log found, got %s", truncate(string(body)))
	}
}

func TestServeListTransfersForStatuses(t *testing.T) {
	origAlive := importerPidAlive
	t.Cleanup(func() { importerPidAlive = origAlive })

	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_success.log"), []byte("start\nPID: 111\nSUCCESS: done\n"), 0644)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_failed.log"), []byte("start\nPID: 222\nsomething else\n"), 0644)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_inprogress.log"), []byte("start\nPID: 333\n"), 0644)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_noPID.log"), []byte("start\nno pid here\n"), 0644)

	importerPidAlive = func(pid int) error {
		if pid == 333 {
			return nil // alive
		}
		return syscall.ESRCH // not running
	}

	resp, err := client.Get(srv.URL + "/json/transfers/carol")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	got := string(body)
	if !strings.Contains(got, `"filename":"carol_success.log","file":"`) && !strings.Contains(got, `"success"`) {
		t.Fatalf("expected a success entry, got %s", truncate(got))
	}
	if !strings.Contains(got, `"in progress"`) {
		t.Fatalf("expected an in-progress entry, got %s", truncate(got))
	}
	if !strings.Contains(got, `"failed"`) {
		t.Fatalf("expected a failed entry, got %s", truncate(got))
	}
}

func TestServeListTransfersForForbiddenWhenNotOwner(t *testing.T) {
	// reseller role queries the DB via paneldb.CheckIfOwnerForUser; a query
	// that finds no matching row denies ownership.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1`)).
		WithArgs("bob", "caller").
		WillReturnError(sql.ErrNoRows)

	im := &Importer{MySQL: db}
	srv, client := newImporterTestServer(t, im, "reseller")

	resp, err := client.Get(srv.URL + "/json/transfers/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeImportTransferGETListsLogs(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "x.log"), []byte("y\nPID: 999999999\nSUCCESS: ok\n"), 0644)

	resp, err := client.Get(srv.URL + "/import/transfer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{
		"Review Account Transfers",
		"x.log",
		"Completed",
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImportTransferGETNoLogsFound(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")

	resp, err := client.Get(srv.URL + "/import/transfer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"No log files found.", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImportTransferPOSTMissingFields(t *testing.T) {
	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/transfer/", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Server IP and OpenPanel username are required") {
		t.Fatalf("expected the missing-fields flash, got %s", truncate(string(body)))
	}
}

func TestServeImportTransferPOSTSuccessSkipsIptablesForDomain(t *testing.T) {
	origTransfer := importerStartTransferRun
	var capturedArgs []string
	importerStartTransferRun = func(args []string) error {
		capturedArgs = args
		return nil
	}
	t.Cleanup(func() { importerStartTransferRun = origTransfer })

	origIptables := configureIptablesRun
	iptablesCalled := false
	configureIptablesRun = func(server string) bool {
		iptablesCalled = true
		return true
	}
	t.Cleanup(func() { configureIptablesRun = origIptables })

	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/transfer/", url.Values{
		"openpanel_username": {"bob"}, "server": {"example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "started in the background") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if iptablesCalled {
		t.Fatal("expected iptables NOT to be configured for a domain target")
	}
	found := false
	for _, a := range capturedArgs {
		if a == "bob" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the account name passed through, got %v", capturedArgs)
	}
}

func TestServeImportTransferPOSTConfiguresIptablesForIP(t *testing.T) {
	origTransfer := importerStartTransferRun
	importerStartTransferRun = func(args []string) error { return nil }
	t.Cleanup(func() { importerStartTransferRun = origTransfer })

	origIptables := configureIptablesRun
	iptablesCalled := false
	var capturedServer string
	configureIptablesRun = func(server string) bool {
		iptablesCalled = true
		capturedServer = server
		return true
	}
	t.Cleanup(func() { configureIptablesRun = origIptables })

	im := &Importer{}
	srv, client := newImporterTestServer(t, im, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.PostForm(srv.URL+"/import/transfer/", url.Values{
		"openpanel_username": {"bob"}, "server": {"203.0.113.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if !iptablesCalled || capturedServer != "203.0.113.5" {
		t.Fatalf("expected iptables configured for the IP target, called=%v server=%q", iptablesCalled, capturedServer)
	}
}

var errTestCloneFailed = errCloneFailedForTest{}

type errCloneFailedForTest struct{}

func (errCloneFailedForTest) Error() string { return "git clone failed" }
