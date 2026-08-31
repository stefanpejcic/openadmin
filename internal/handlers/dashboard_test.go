package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

// dashboardTestServer logs a real user in through a real admindb (same
// pattern as login_test.go) and wraps the dashboard handler with the real
// WithUserLoader middleware, so these tests exercise the actual
// session -> CurrentUser(r) plumbing rather than a test-only backdoor.
func dashboardTestServer(t *testing.T, dash *Dashboard, role string) (*httptest.Server, *http.Client) {
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
	if err := db.CreateUser("testuser", hash, role); err != nil {
		t.Fatal(err)
	}
	u, err := db.UserByUsername("testuser")
	if err != nil {
		t.Fatal(err)
	}

	dash.Sessions = auth.NewManager("test-secret", false)

	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard", dash.ServeDashboard)
	mux.HandleFunc("/onboarding", dash.ServeOnboardingPage)
	mux.HandleFunc("/json/system", dash.ServeSystemInfo)
	mux.HandleFunc("/json/combined_activity", dash.ServeCombinedActivity)
	mux.HandleFunc("/json/user_activity_status", dash.ServeUserActivityStatus)
	mux.HandleFunc("/server/resource-usage", dash.ServeResourceUsagePage)
	mux.HandleFunc("/server/resource-usage/history", dash.ServeResourceUsageHistory)
	mux.HandleFunc("/server/resource-usage/history/data", dash.ServeResourceUsageHistoryData)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, dash.Sessions, u, "203.0.113.1")
	})

	handler := auth.WithUserLoader(dash.Sessions, db)(mux)
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

func TestServeDashboardAdminJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"user_count", "plan_count", "site_count", "domain_count"}).AddRow(5, 2, 9, 4))
	mock.ExpectQuery(`SELECT DISTINCT server`).WillReturnRows(sqlmock.NewRows([]string{"server"}))

	dash := &Dashboard{MySQL: db}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/dashboard?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var got dashboardAdminData
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UserCount != 5 || got.PlanCount != 2 || got.SiteCount != 9 || got.DomainCount != 4 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	if got.ServerCount != 1 {
		t.Fatalf("expected server_count 1 (just the default context), got %d", got.ServerCount)
	}
}

type sqlErrConnRefused struct{}

func (sqlErrConnRefused) Error() string { return "connection refused" }

func TestServeDashboardAdminHTMLOnQueryFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT`).WillReturnError(sqlErrConnRefused{})

	dash := &Dashboard{MySQL: db}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected graceful 200 fallback page, got %d", resp.StatusCode)
	}
}

func TestServeDashboardResellerJSON(t *testing.T) {
	dir := t.TempDir()
	origLog := LoginLogPath
	LoginLogPath = filepath.Join(dir, "login.log")
	t.Cleanup(func() { LoginLogPath = origLog })
	os.WriteFile(LoginLogPath, []byte("2026-01-01 10:00:00 testuser 203.0.113.4\n2026-01-02 11:00:00 testuser 203.0.113.5\n"), 0644)

	dash := &Dashboard{}
	srv, client := dashboardTestServer(t, dash, "reseller")

	resp, err := client.Get(srv.URL + "/dashboard?output=json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var got dashboardResellerData
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.LastIP != "203.0.113.5" {
		t.Fatalf("expected the most recent login IP (last matching line), got %q", got.LastIP)
	}
	if got.MaxAccounts != "unlimited" {
		t.Fatalf("expected default max_accounts, got %q", got.MaxAccounts)
	}
}

func TestServeSystemInfo(t *testing.T) {
	dash := &Dashboard{}
	req := httptest.NewRequest(http.MethodGet, "/json/system", nil)
	rec := httptest.NewRecorder()

	dash.ServeSystemInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got systemInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hostname == "" {
		t.Fatal("expected a non-empty hostname")
	}
}

// TestCPUModelNameIgnoresHostLocale guards against a real bug: lscpu prints
// localized field labels on a non-English locale (e.g. LANG=sr_RS.UTF-8
// prints "Назив модела:" instead of "Model name:"), which would make this
// always report "Unavailable" without the LC_ALL=C override.
func TestCPUModelNameIgnoresHostLocale(t *testing.T) {
	if _, err := exec.LookPath("lscpu"); err != nil {
		t.Skip("lscpu not installed on this host")
	}
	got := cpuModelName()
	if got == "Unavailable" || strings.Contains(got, "Model name:") {
		t.Fatalf("expected a real CPU model name, got %q", got)
	}
}

func withScratchResourceUsageSnapshotFile(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	origPath := ResourceUsageSnapshotFile
	ResourceUsageSnapshotFile = filepath.Join(dir, "sentinel_snapshots.jsonl")
	t.Cleanup(func() { ResourceUsageSnapshotFile = origPath })
	if content != "" {
		os.WriteFile(ResourceUsageSnapshotFile, []byte(content), 0644)
	}
}

func TestServeResourceUsagePageRendersHTML(t *testing.T) {
	dash := &Dashboard{}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/server/resource-usage")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(got))
	}
	for _, want := range []string{"Resource Usage", "Load", "CPU", "RAM", "Disk", "Network", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestLoadResourceUsageSnapshotsFiltersByLatestTimestamp(t *testing.T) {
	withScratchResourceUsageSnapshotFile(t, ""+
		`{"ts":"2026-01-01 10:00:00","cpu_pct":10}`+"\n"+
		`{"ts":"2026-01-01 10:30:00","cpu_pct":20}`+"\n"+
		`{"ts":"2026-01-01 11:00:00","cpu_pct":30}`+"\n")

	got := loadResourceUsageSnapshots(30)
	if len(got) != 2 {
		t.Fatalf("expected 2 snapshots within the last 30 minutes of the latest ts, got %d: %s", len(got), got)
	}
	if !strings.Contains(string(got[0]), `"cpu_pct":20`) || !strings.Contains(string(got[1]), `"cpu_pct":30`) {
		t.Fatalf("expected the two most recent snapshots in file order, got %s", got)
	}
}

func TestLoadResourceUsageSnapshotsMissingFileReturnsEmpty(t *testing.T) {
	withScratchResourceUsageSnapshotFile(t, "")
	got := loadResourceUsageSnapshots(60)
	if got == nil || len(got) != 0 {
		t.Fatalf("expected an empty non-nil slice, got %+v", got)
	}
}

func TestServeResourceUsageHistoryRendersHTMLAndJSON(t *testing.T) {
	withScratchResourceUsageSnapshotFile(t, `{"ts":"2026-01-01 10:00:00","cpu_pct":42}`+"\n")

	dash := &Dashboard{}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/server/resource-usage/history?minutes=60&view=grid")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(got))
	}
	for _, want := range []string{"Resource Usage History", "Last 1 hour", `"cpu_pct":42`, "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(got))
		}
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("expected no-store Cache-Control, got %q", cc)
	}

	jsonResp, err := client.Get(srv.URL + "/server/resource-usage/history?minutes=60&output=json")
	if err != nil {
		t.Fatal(err)
	}
	jsonBody, _ := io.ReadAll(jsonResp.Body)
	jsonResp.Body.Close()
	var decoded []map[string]interface{}
	if err := json.Unmarshal(jsonBody, &decoded); err != nil {
		t.Fatalf("expected a JSON array, got %s: %v", jsonBody, err)
	}
	if len(decoded) != 1 || decoded[0]["cpu_pct"].(float64) != 42 {
		t.Fatalf("unexpected snapshots JSON: %+v", decoded)
	}
}

func TestServeResourceUsageHistoryDataJSON(t *testing.T) {
	withScratchResourceUsageSnapshotFile(t, `{"ts":"2026-01-01 10:00:00","cpu_pct":7}`+"\n")

	dash := &Dashboard{}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/server/resource-usage/history/data?minutes=60")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded struct {
		Minutes   int                      `json:"minutes"`
		Count     int                      `json:"count"`
		Snapshots []map[string]interface{} `json:"snapshots"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", body, err)
	}
	if decoded.Minutes != 60 || decoded.Count != 1 || len(decoded.Snapshots) != 1 {
		t.Fatalf("unexpected response: %+v", decoded)
	}
}

func TestServeCombinedActivityResellerOnlySeesOwnedUsers(t *testing.T) {
	dir := t.TempDir()
	origLogsDir := CombinedActivityLogsDir
	CombinedActivityLogsDir = dir
	t.Cleanup(func() { CombinedActivityLogsDir = origLogsDir })

	for user, action := range map[string]string{
		"ownedclient": "2026-08-18 10:00:00 1.2.3.4 User ownedclient created domain owned.example.com",
		"otherclient": "2026-08-18 10:00:00 1.2.3.4 User otherclient created domain other.example.com",
	} {
		userDir := filepath.Join(dir, user)
		if err := os.MkdirAll(userDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(userDir, "activity.log"), []byte(action+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT users\.\*.*WHERE users\.owner = \?`).
		WithArgs("testuser").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(1, "ownedclient"))

	dash := &Dashboard{MySQL: db}
	srv, client := dashboardTestServer(t, dash, "reseller")

	resp, err := client.Get(srv.URL + "/json/combined_activity")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var got map[string][]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", body, err)
	}
	logs := got["combined_logs"]
	if len(logs) != 1 || !strings.Contains(logs[0], "ownedclient") {
		t.Fatalf("expected only ownedclient's activity, got %v", logs)
	}
	for _, l := range logs {
		if strings.Contains(l, "otherclient") {
			t.Fatalf("reseller must not see other accounts' activity, got %v", logs)
		}
	}
}

func TestServeCombinedActivityMissingDirReturnsEmpty(t *testing.T) {
	dash := &Dashboard{}
	req := httptest.NewRequest(http.MethodGet, "/json/combined_activity", nil)
	rec := httptest.NewRecorder()

	dash.ServeCombinedActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got["combined_logs"]) != 0 {
		t.Fatalf("expected empty combined_logs when the logs dir doesn't exist, got %v", got["combined_logs"])
	}
}

func TestServeUserActivityStatusReturnsActiveUsers(t *testing.T) {
	orig := activeSessionUsernamesRun
	activeSessionUsernamesRun = func() (map[string]string, error) {
		return map[string]string{"alice": "active", "bob": "active"}, nil
	}
	defer func() { activeSessionUsernamesRun = orig }()

	dash := &Dashboard{}
	req := httptest.NewRequest(http.MethodGet, "/json/user_activity_status", nil)
	rec := httptest.NewRecorder()

	dash.ServeUserActivityStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["alice"] != "active" || got["bob"] != "active" {
		t.Fatalf("expected both users marked active, got %+v", got)
	}
}

func TestServeUserActivityStatusRedisErrorReturnsEmptyObject(t *testing.T) {
	orig := activeSessionUsernamesRun
	activeSessionUsernamesRun = func() (map[string]string, error) {
		return nil, errors.New("redis unreachable")
	}
	defer func() { activeSessionUsernamesRun = orig }()

	dash := &Dashboard{}
	req := httptest.NewRequest(http.MethodGet, "/json/user_activity_status", nil)
	rec := httptest.NewRecorder()

	dash.ServeUserActivityStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "{}\n" {
		t.Fatalf("expected an empty JSON object on redis error, got %q", rec.Body.String())
	}
}

// TestHandleQuickStartDismissCreatesSkipFile mirrors
// TestHandleTourCompleteCreatesSkipFile in login_test.go -- same
// skip-file-on-first-POST pattern, different feature.
func TestHandleQuickStartDismissCreatesSkipFile(t *testing.T) {
	dir := t.TempDir()
	origPath := ChromeQuickStartSkipFilePath
	ChromeQuickStartSkipFilePath = filepath.Join(dir, "quickstart.skip")
	t.Cleanup(func() { ChromeQuickStartSkipFilePath = origPath })

	dash := &Dashboard{}
	req := httptest.NewRequest(http.MethodPost, "/api/quickstart/dismiss", nil)
	rec := httptest.NewRecorder()
	dash.HandleQuickStartDismiss(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected 200 {\"ok\":true}, got %d %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(ChromeQuickStartSkipFilePath); err != nil {
		t.Fatalf("expected quick start skip file to be created, err=%v", err)
	}

	// A second call with the file already present should still succeed.
	rec2 := httptest.NewRecorder()
	dash.HandleQuickStartDismiss(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on second call, got %d", rec2.Code)
	}
}

func TestServeOnboardingPageRendersForAdmin(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"user_count", "plan_count", "site_count", "domain_count"}).AddRow(1, 2, 3, 4))

	dash := &Dashboard{MySQL: db}
	srv, client := dashboardTestServer(t, dash, "admin")

	resp, err := client.Get(srv.URL + "/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// The intro screen plus all 3 steps are rendered server-side up front
	// (Alpine just toggles which one is visible client-side), so every
	// screen's heading should be present in the raw HTML regardless of JS
	// execution.
	for _, want := range []string{"Let's get your server ready", "Enable modules", "Server configuration", "Users and plans"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected onboarding page to contain %q, got %q", want, truncate(string(body)))
		}
	}
}

func TestServeOnboardingPageRedirectsResellerToDashboard(t *testing.T) {
	dash := &Dashboard{}
	srv, client := dashboardTestServer(t, dash, "reseller")

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Get(srv.URL + "/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %q", loc)
	}
}
