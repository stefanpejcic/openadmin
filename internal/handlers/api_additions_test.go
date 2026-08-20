// This file tests the JSON REST API wrappers added around Swap, the
// reseller enable/disable toggle, Podman vulnerability scanning, Backups,
// and user Export/permissions-reset -- the API-layer coverage for
// everything added earlier directly on the HTML session-based pages.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/config"
)

// withScratchAdminConfig points config.AdminConfigPath at an empty scratch
// file for the duration of the test, so resellersEnabled() starts from a
// clean "disabled" default instead of touching the real system path.
func withScratchAdminConfig(t *testing.T, dir string) {
	t.Helper()
	orig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = orig })
}

// --- Users: permissions reset ---

func newAPIUsersExtraTestServer(t *testing.T, actingUser *admindb.User) (*httptest.Server, *http.Client, sqlmock.Sqlmock) {
	t.Helper()
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDB.Close() })

	u := &APIUsers{MySQL: mysqlDB}
	ue := &APIUserExport{Users: &Users{MySQL: mysqlDB}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/users/{username}/permissions/reset", withTestActingUser(actingUser, u.ServePermissionsReset))
	mux.HandleFunc("GET /api/users/{username}/export/status", withTestActingUser(actingUser, ue.ServeStatus))
	mux.HandleFunc("POST /api/users/{username}/export/create", withTestActingUser(actingUser, ue.ServeCreate))
	mux.HandleFunc("POST /api/users/{username}/export/delete", withTestActingUser(actingUser, ue.ServeDelete))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client(), mock
}

func expectAliceUserDataForAPI(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(1), "alice"))
}

func TestAPIServePermissionsResetSucceeds(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, mock := newAPIUsersExtraTestServer(t, admin)
	expectAliceUserDataForAPI(mock)

	resp, err := client.Post(srv.URL+"/api/users/alice/permissions/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["success"] != true {
		t.Fatalf("expected success:true, got %s", body)
	}
}

func TestAPIServePermissionsResetUserNotFound(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, mock := newAPIUsersExtraTestServer(t, admin)
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("ghost", "SUSPENDED_%_ghost").
		WillReturnError(sqlErrConnRefused{})

	resp, err := client.Post(srv.URL+"/api/users/ghost/permissions/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- Users: export (backup wizard) ---

func TestAPIServeUserExportStatusDoesNotPanicWithoutSessionCookie(t *testing.T) {
	// This is the specific regression withActingUser guards against:
	// user_export.go's handlers call auth.CurrentUser(r) internally, which
	// is nil for a JWT-authenticated API request with no session cookie at
	// all -- without injecting the acting user onto the request the same
	// way a session would, that call panics on currentUser.Username.
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, mock := newAPIUsersExtraTestServer(t, admin)
	expectAliceUserDataForAPI(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.Get(srv.URL + "/api/users/alice/export/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (no panic), got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIServeUserExportCreateStartsBackupCommand(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, mock := newAPIUsersExtraTestServer(t, admin)
	expectAliceUserDataForAPI(mock)

	origHome := userExportHomeRoot
	userExportHomeRoot = t.TempDir()
	t.Cleanup(func() { userExportHomeRoot = origHome })

	origInProgress := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = origInProgress })

	var gotUsername string
	origCmd := userExportBackupCmdRun
	userExportBackupCmdRun = func(username string) error { gotUsername = username; return nil }
	t.Cleanup(func() { userExportBackupCmdRun = origCmd })

	resp, err := client.Post(srv.URL+"/api/users/alice/export/create", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if gotUsername != "alice" {
		t.Fatalf("expected backup command called with alice, got %q", gotUsername)
	}
}

func TestAPIServeUserExportDeleteTranslatesJSONBodyToFormValue(t *testing.T) {
	admin := &admindb.User{Username: "admin1", Role: "admin"}
	srv, client, mock := newAPIUsersExtraTestServer(t, admin)
	expectAliceUserDataForAPI(mock)

	orig := userExportIsBackupInProgressRun
	userExportIsBackupInProgressRun = func(string) bool { return false }
	t.Cleanup(func() { userExportIsBackupInProgressRun = orig })

	resp, err := client.Post(srv.URL+"/api/users/alice/export/delete", "application/json",
		strings.NewReader(`{"filename":"../../../etc/passwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// A path-traversal filename is rejected downstream in
	// ServeUserExportDelete -- reaching that 400 (rather than e.g. a 500
	// from a missing form value) confirms the JSON body's "filename" field
	// actually made it into r.PostForm/r.Form as user_export.go expects.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a path-traversal filename, got %d: %s", resp.StatusCode, body)
	}
}

// --- Resellers: enable/disable toggle ---

func TestAPIServeSettingsResellersEnabledGetReflectsCurrentState(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	_ = createUser

	withScratchAdminConfig(t, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/settings/resellers/enabled", nil)
	rec := httptest.NewRecorder()
	h.ServeSettingsResellersEnabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["enabled"] != false {
		t.Fatalf("expected disabled by default, got %v", out)
	}
}

func TestAPIServeSettingsResellersEnabledTogglesOnThenBlocksOffWhileResellersExist(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	withScratchAdminConfig(t, t.TempDir())

	post := func(enabled bool) *httptest.ResponseRecorder {
		body := `{"enabled":true}`
		if !enabled {
			body = `{"enabled":false}`
		}
		req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers/enabled", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeSettingsResellersEnabled(rec, req)
		return rec
	}

	rec := post(true)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 enabling, got %d: %s", rec.Code, rec.Body.String())
	}

	createUser("blocker", "reseller")

	rec = post(false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 while a reseller exists, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Swap ---

func TestAPIServeSwapActionResizeTranslatesJSONBodyToFormValue(t *testing.T) {
	a := &APIServerSwap{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/swap/action/{action}", a.ServeSwapAction)
	mux.HandleFunc("GET /api/server/swap/action-status", a.ServeSwapActionStatus)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var gotSizeMB int64
	origResize := swapResizeRun
	swapResizeRun = func(sizeMB int64) error { gotSizeMB = sizeMB; return nil }
	t.Cleanup(func() { swapResizeRun = origResize })

	resp, err := http.Post(srv.URL+"/api/server/swap/action/resize", "application/json", strings.NewReader(`{"size_mb":2048}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := http.Get(srv.URL + "/api/server/swap/action-status")
		if err != nil {
			t.Fatal(err)
		}
		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()
		if strings.Contains(string(statusBody), `"done":true`) {
			if gotSizeMB != 2048 {
				t.Fatalf("expected swapResizeRun called with 2048 (from the JSON body), got %d", gotSizeMB)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for resize action to complete")
}

func TestAPIServeSwapStatusReturnsGB(t *testing.T) {
	a := &APIServerSwap{}
	origFree, origShow := swapFreeRun, swapShowRun
	swapFreeRun = func() (string, error) { return "Swap:           1024           0        1024\n", nil }
	swapShowRun = func() (string, error) { return "", nil }
	t.Cleanup(func() { swapFreeRun, swapShowRun = origFree, origShow })

	req := httptest.NewRequest(http.MethodGet, "/api/server/swap", nil)
	rec := httptest.NewRecorder()
	a.ServeSwap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["total_mb"] != float64(1024) {
		t.Fatalf("expected total_mb 1024, got %v", out)
	}
}

// --- Podman ---

func TestAPIServicesPodmanImagesBulkActionRejectsInvalidAction(t *testing.T) {
	a := &APIServicesPodman{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/services/podman/images/bulk/{action}", a.ServeImagesBulkAction)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/api/services/podman/images/bulk/bogus", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIServicesPodmanImageVulnerabilitiesReturnsCachedDetails(t *testing.T) {
	ref := "docker.io/library/nginx:api-test"
	podmanVulnStatusMu.Lock()
	podmanVulnStatusCache[ref] = podmanVulnStatus{Count: 1, Details: []podmanVulnDetail{{ID: "CVE-2024-9", Package: "openssl", Severity: "HIGH"}}}
	podmanVulnStatusMu.Unlock()
	t.Cleanup(func() {
		podmanVulnStatusMu.Lock()
		delete(podmanVulnStatusCache, ref)
		podmanVulnStatusMu.Unlock()
	})

	a := &APIServicesPodman{}
	req := httptest.NewRequest(http.MethodGet, "/api/services/podman/images/vulnerabilities?ref="+url.QueryEscape(ref), nil)
	rec := httptest.NewRecorder()
	a.ServeImageVulnerabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "CVE-2024-9") {
		t.Fatalf("expected cached CVE in response, got %s", rec.Body.String())
	}
}

// --- Backups ---

func TestAPIBackupsSystemBackupsReturnsDestinationAndArchives(t *testing.T) {
	dir := t.TempDir()
	origConfig := BackupsConfigPath
	BackupsConfigPath = filepath.Join(dir, "backups.ini")
	t.Cleanup(func() { BackupsConfigPath = origConfig })

	a := &APIBackups{}
	req := httptest.NewRequest(http.MethodGet, "/api/backups/system", nil)
	rec := httptest.NewRecorder()
	a.ServeSystemBackups(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["retention_days"] != "-1" {
		t.Fatalf("expected default retention_days -1, got %v", out)
	}
}

func TestAPIBackupsUserBackupsReflectsScheduleChoice(t *testing.T) {
	dir := t.TempDir()
	origCron := CronFilePath
	CronFilePath = filepath.Join(dir, "openpanel")
	t.Cleanup(func() { CronFilePath = origCron })

	a := &APIBackups{}
	req := httptest.NewRequest(http.MethodGet, "/api/backups/user", nil)
	rec := httptest.NewRecorder()
	a.ServeUserBackups(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"schedule_choice":"disabled"`) {
		t.Fatalf("expected disabled default (no cron file), got %s", rec.Body.String())
	}
}
