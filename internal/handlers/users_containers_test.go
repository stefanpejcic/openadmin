package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// --- pure-function tests (no HTTP, no filesystem) ---

func TestSplitFileLinesPreserving(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"empty", "", nil},
		{"trailing newline", "A=1\nB=2\n", []string{"A=1\n", "B=2\n"}},
		{"no trailing newline", "A=1\nB=2", []string{"A=1\n", "B=2"}},
		{"single line no newline", "A=1", []string{"A=1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitFileLinesPreserving(c.content)
			if len(got) != len(c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %#v, want %#v", got, c.want)
				}
			}
		})
	}
}

func TestContainerEnvVarSanitizeRe(t *testing.T) {
	got := containerEnvVarSanitizeRe.ReplaceAllString(strings.ToUpper("myapp-web.1"), "_")
	if got != "MYAPP_WEB_1" {
		t.Fatalf("got %q, want MYAPP_WEB_1", got)
	}
}

func TestUpdateContainerRAMOrCPUEnvFileNotFound(t *testing.T) {
	result, err := updateContainerRAMOrCPU("definitely-not-a-real-context-xyz", "web", "ram", "512")
	if err != nil {
		t.Fatalf("expected no unhandled error, got %v", err)
	}
	if !result.IsDict || result.Success {
		t.Fatalf("expected a failure dict, got %+v", result)
	}
	if !strings.Contains(result.Message, ".env file not found at") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}

func TestUpdateContainerRAMOrCPUUnsupportedActionMessage(t *testing.T) {
	// ServeManageContainer itself never lets an unsupported action reach
	// updateContainerRAMOrCPU in the real app (it's pre-validated), but this
	// re-checks anyway as a safety net for direct callers.
	result, err := updateContainerRAMOrCPU("whatever", "web", "bogus", "1")
	if err != nil {
		t.Fatalf("expected no unhandled error, got %v", err)
	}
	if !result.IsDict || result.Success {
		t.Fatalf("expected a failure dict, got %+v", result)
	}
	if result.Message != "Unsupported action: bogus. Use 'ram' or 'cpu'." {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}

func TestStartOrStopContainerActivateWithPullBuildsArgvAndCannedMessage(t *testing.T) {
	orig := containerComposeCaptureRun
	var gotDir string
	var gotArgs []string
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		gotDir = dir
		gotArgs = args
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	result := startOrStopContainer("alice", "web", "activate", true)
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if result.Message != "Container 'web' activated successfully." {
		t.Fatalf("expected the canned success sentence when stdout is empty, got %q", result.Message)
	}
	if gotDir != "/home/alice" {
		t.Fatalf("unexpected dir: %q", gotDir)
	}
	if strings.Join(gotArgs, " ") != "--pull up -d web" {
		t.Fatalf("expected --pull inserted before up -d web, got %q", strings.Join(gotArgs, " "))
	}
}

func TestStartOrStopContainerDeactivateFailureUsesStderr(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "boom: no such container", &exec.ExitError{}
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	result := startOrStopContainer("alice", "web", "deactivate", false)
	if result.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(result.Message, "Command failed with error:\nboom: no such container") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}

func TestRestartContainerCmdRunsDownThenUp(t *testing.T) {
	orig := containerComposeCaptureRun
	var calls [][]string
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		calls = append(calls, args)
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	if err := restartContainerCmd("alice", "web"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (down, up -d), got %d: %+v", len(calls), calls)
	}
	if strings.Join(calls[0], " ") != "down web" {
		t.Fatalf("unexpected first call: %+v", calls[0])
	}
	if strings.Join(calls[1], " ") != "up -d web" {
		t.Fatalf("unexpected second call: %+v", calls[1])
	}
}

func TestRestartContainerCmdNeverRunsUpAfterDownFails(t *testing.T) {
	orig := containerComposeCaptureRun
	calls := 0
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		calls++
		return "", "", errors.New("down failed")
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	if err := restartContainerCmd("alice", "web"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("expected 'up' to never run after 'down' fails, got %d calls", calls)
	}
}

func TestGetAllContainersStatsSkipsInvalidJSONLines(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "{\"Name\":\"web\"}\nnot json\n{\"Name\":\"db\"}\n", 0, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	stats, ok := getAllContainersStats("alice")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(stats) != 2 {
		t.Fatalf("expected the invalid line to be skipped, got %d entries: %+v", len(stats), stats)
	}
}

func TestGetAllContainersStatsEmptyStdoutReturnsEmptySlice(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "", 0, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	stats, ok := getAllContainersStats("alice")
	if !ok || stats == nil || len(stats) != 0 {
		t.Fatalf("expected ok=true with an empty (non-nil) slice, got ok=%v stats=%+v", ok, stats)
	}
}

func TestGetAllContainersStatsNonzeroExitReturnsNotOK(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "", 1, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	if _, ok := getAllContainersStats("alice"); ok {
		t.Fatal("expected ok=false for a nonzero exit code")
	}
}

// --- HTTP-level tests ---

func TestServeManageContainerInvalidActionReturns400(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/containers/alice/bogus/web", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeManageContainerDeniedForNonOwningReseller(t *testing.T) {
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

	resp, err := client.PostForm(srv.URL+"/containers/bob/start/web", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestServeManageContainerRestartFailurePropagatesAsUnhandled500(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "", errors.New("compose down failed")
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/containers/alice/restart/web", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// restartContainerCmd has no error recovery of its own, so a failure
	// here is an unhandled exception, not a flash+redirect.
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestServeManageContainerRestartSuccessAlwaysShowsErrorOccurredWarning(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/containers/alice/restart/web", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect chain, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	// restartContainerCmd returns nil (not a result value) on success, so
	// the flash logic always falls through to "Error occurred!" -- even on
	// a genuine success.
	if !strings.Contains(string(body), "Error occurred!") {
		t.Fatalf("expected the 'Error occurred!' flash even on a successful restart, got %s", truncate(string(body)))
	}
}

func TestServeManageContainerStartAlwaysFlashesRawMessageAsError(t *testing.T) {
	orig := containerComposeCaptureRun
	var gotArgs []string
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/containers/alice/start/web", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect chain, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	// html/template escapes the apostrophes in the flashed message.
	if !strings.Contains(string(body), "activated successfully.") || !strings.Contains(string(body), "web") {
		t.Fatalf("expected the canned success sentence to be shown (as a flash), got %s", truncate(string(body)))
	}
	if strings.Join(gotArgs, " ") != "up -d web" {
		t.Fatalf("unexpected podman-compose argv: %q", strings.Join(gotArgs, " "))
	}
}

func TestServeManageContainerCPUEnvFileNotFoundFlashesErrorMessage(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.PostForm(srv.URL+"/containers/definitely-nonexistent-user-xyz/cpu/web", url.Values{"value": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after following the redirect chain, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), ".env file not found at") {
		t.Fatalf("expected the .env-not-found flash, got %s", truncate(string(body)))
	}
}

// --- GET /containers/stats/{username} ---

func TestServeContainersStatsReturnsJSONArray(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "{\"Name\":\"web\",\"CPU\":\"1.5%\"}\n", 0, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/containers/stats/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var decoded struct {
		ContainerStats []map[string]interface{} `json:"container_stats"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.ContainerStats) != 1 || decoded.ContainerStats[0]["Name"] != "web" {
		t.Fatalf("unexpected decoded stats: %+v", decoded.ContainerStats)
	}
}

func TestServeContainersStatsFailureReturns500(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "", 1, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	u := &Users{MySQL: mysqlDB}
	srv, client := newUsersTestServer(t, u, "admin")

	resp, err := client.Get(srv.URL + "/containers/stats/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Could not retrieve container stats") {
		t.Fatalf("unexpected error body: %s", truncate(string(body)))
	}
}

// --- composeServicesForUser / composeSizeToBytes (pure functions) ---

func TestComposeSizeToBytes(t *testing.T) {
	cases := []struct {
		name      string
		in        interface{}
		wantBytes float64
		wantOK    bool
	}{
		{"zero float", float64(0), 0, false},
		{"nil", nil, 0, false},
		{"bytes as float64", float64(536870912), 536870912, true},
		{"zero string", "0", 0, false},
		{"empty string", "", 0, false},
		{"gigabyte string", "0.5G", 0.5 * (1 << 30), true},
		{"megabyte string with B suffix", "512MB", 512 * (1 << 20), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotBytes, gotOK := composeSizeToBytes(c.in)
			if gotOK != c.wantOK || gotBytes != c.wantBytes {
				t.Fatalf("composeSizeToBytes(%#v) = (%v, %v), want (%v, %v)", c.in, gotBytes, gotOK, c.wantBytes, c.wantOK)
			}
		})
	}
}

func TestComposeServicesForUserParsesResolvedConfig(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice"))

	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return `{"services":{"mysql":{
			"container_name":"mysql",
			"image":"mysql:8",
			"ports":[{"host_ip":"127.0.0.1","published":"3306","target":3306},{"host_ip":"","published":"0","target":0}],
			"environment":{"MYSQL_ROOT_PASSWORD":"secret"},
			"deploy":{"resources":{"limits":{"cpus":"0.5","memory":536870912}}}
		}}}`, "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	views := composeServicesForUser(mysqlDB, "alice")
	if len(views) != 1 {
		t.Fatalf("expected 1 service, got %d: %#v", len(views), views)
	}
	v := views[0]
	if v.Name != "mysql" || v.ContainerName != "mysql" || v.Image != "mysql:8" {
		t.Fatalf("unexpected service identity: %#v", v)
	}
	if len(v.Ports) != 2 || v.Ports[0].Published != "3306" || v.Ports[0].Target != "3306" {
		t.Fatalf("unexpected ports: %#v", v.Ports)
	}
	if len(v.Environment) != 1 || v.Environment[0].Key != "MYSQL_ROOT_PASSWORD" || v.Environment[0].Value != "secret" {
		t.Fatalf("unexpected environment: %#v", v.Environment)
	}
	if v.CPULimit != "0.5" {
		t.Fatalf("expected CPULimit 0.5, got %q", v.CPULimit)
	}
	if v.MemoryLimitGB != "0.50" {
		t.Fatalf("expected MemoryLimitGB 0.50, got %q", v.MemoryLimitGB)
	}
}

func TestComposeServicesForUserEmptyOnMissingComposeFile(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice"))

	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "no such file or directory", &exec.ExitError{}
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	views := composeServicesForUser(mysqlDB, "alice")
	if views != nil {
		t.Fatalf("expected nil services for a missing compose file, got %#v", views)
	}
}

// --- HTML rendering with real service data (catches template errors that
// only surface when the {{range .Services}} branch actually executes) ---

func TestUsersDetailRendersServicesTable(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT u.username, u.id, u.email`).
		WithArgs("alice", "SUSPENDED_%_alice").
		WillReturnRows(sqlmock.NewRows([]string{"username", "id", "email", "owner", "twofa_enabled", "registered_date", "plan_id", "server"}).
			AddRow("alice", int64(3), "alice@example.com", nil, true, "2025-06-01 10:00:00", int64(1), "alice"))
	mock.ExpectQuery(`SELECT server FROM users`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice"))

	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return `{"services":{"mysql":{
			"container_name":"mysql",
			"image":"mysql:8",
			"ports":[{"host_ip":"127.0.0.1","published":"3306","target":3306}],
			"environment":{"MYSQL_ROOT_PASSWORD":"secret"},
			"deploy":{"resources":{"limits":{"cpus":"0.5","memory":536870912}}}
		}}}`, "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

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
	for _, want := range []string{"mysql:8", "MYSQL_ROOT_PASSWORD", "/containers/alice/cpu/mysql", "/containers/alice/start/mysql", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeContainersStatsDeniedForNonOwningReseller(t *testing.T) {
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

	resp, err := client.Get(srv.URL + "/containers/stats/bob")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}
