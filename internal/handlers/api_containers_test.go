package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newAPIContainersTestServer(t *testing.T, c *APIContainers) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{username}/containers", c.ServeUserContainers)
	mux.HandleFunc("POST /users/{username}/containers/{action}/{container_name}", c.ServeManageContainer)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// --- pure-function tests ---

func TestApiIsJSONContentType(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/vnd.api+json", true},
		{"application/x-www-form-urlencoded", false},
		{"", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/x", nil)
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		if got := apiIsJSONContentType(req); got != c.want {
			t.Fatalf("Content-Type %q: got %v, want %v", c.contentType, got, c.want)
		}
	}
}

func TestApiJSONGetSilentReturnsCrashedForInvalidJSON(t *testing.T) {
	if _, crashed := apiJSONGetSilent([]byte("not json"), "value"); !crashed {
		t.Fatal("expected crashed=true for invalid JSON")
	}
	if _, crashed := apiJSONGetSilent([]byte(`["a","b"]`), "value"); !crashed {
		t.Fatal("expected crashed=true for a non-object JSON body")
	}
	v, crashed := apiJSONGetSilent([]byte(`{"value":"2"}`), "value")
	if crashed {
		t.Fatal("expected crashed=false for a valid object")
	}
	if v != "2" {
		t.Fatalf("expected value \"2\", got %v", v)
	}
	v, crashed = apiJSONGetSilent([]byte(`{"other":"x"}`), "value")
	if crashed || v != nil {
		t.Fatalf("expected (nil, false) for a missing key, got (%v, %v)", v, crashed)
	}
}

func TestApiJSONValueToString(t *testing.T) {
	if got := apiJSONValueToString("2"); got != "2" {
		t.Fatalf("got %q", got)
	}
	if got := apiJSONValueToString(2.0); got != "2" {
		t.Fatalf("expected whole-number float to render without a decimal point, got %q", got)
	}
}

func TestApiJSONTruthy(t *testing.T) {
	falsy := []interface{}{nil, false, float64(0), "", []interface{}{}, map[string]interface{}{}}
	for _, v := range falsy {
		if apiJSONTruthy(v) {
			t.Fatalf("expected %#v to be falsy", v)
		}
	}
	truthy := []interface{}{true, float64(1), "x", []interface{}{1}}
	for _, v := range truthy {
		if !apiJSONTruthy(v) {
			t.Fatalf("expected %#v to be truthy", v)
		}
	}
}

func TestApiContainersDisplayUsername(t *testing.T) {
	if got := apiContainersDisplayUsername("bob"); got != "bob" {
		t.Fatalf("got %q", got)
	}
	if got := apiContainersDisplayUsername("SUSPENDED_20240101_bob"); got != "bob" {
		t.Fatalf("expected text after the last underscore, got %q", got)
	}
}

// --- GET /users/{username}/containers ---

func TestServeUserContainersStatsMissingContextReturns404(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("ghost").
		WillReturnError(errors.New("no rows"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/ghost/containers?stats=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "No context found for user") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestServeUserContainersStatsSuccess(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "{\"Name\":\"web\"}\n", 0, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	// A non-empty but "falsy-looking" value like stats=0 still triggers the
	// stats branch -- any non-empty string does.
	resp, err := client.Get(srv.URL + "/users/alice/containers?stats=0")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded struct {
		ContainerStats []map[string]interface{} `json:"container_stats"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	if len(decoded.ContainerStats) != 1 {
		t.Fatalf("unexpected stats: %+v", decoded.ContainerStats)
	}
}

func TestServeUserContainersStatsFailureReturns500(t *testing.T) {
	orig := containerStatsRun
	containerStatsRun = func(context string) (string, int, error) {
		return "", 1, nil
	}
	t.Cleanup(func() { containerStatsRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/alice/containers?stats=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeUserContainersNoStatsMissingContextReturns200WithErrorBody(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("SUSPENDED_20240101_bob").
		WillReturnError(errors.New("no rows"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	// Unlike the ?stats= branch, a missing context here is reported inside
	// the JSON body with a 200 status -- get_containers() returns a plain
	// error dict rather than the route setting a status code.
	resp, err := client.Get(srv.URL + "/users/SUSPENDED_20240101_bob/containers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["error"] != "No context found for user" {
		t.Fatalf("unexpected body: %s", body)
	}
	if decoded["details"] != "username: bob" {
		t.Fatalf("expected the SUSPENDED_ prefix stripped in details, got %s", body)
	}
}

func TestServeUserContainersNoStatsSuccessReturnsComposeConfig(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return `{"services":{"web":{}}}`, "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/alice/containers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"web"`) {
		t.Fatalf("expected services in response, got %s", body)
	}
}

func TestServeUserContainersNoStatsMissingServicesKeyIsInvalidFormat(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return `{"other":"stuff"}`, "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/alice/containers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (error surfaced in body, not status), got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid data format") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestServeUserContainersNoStatsComposeFailureReturns200WithErrorDetails(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "boom", &exec.ExitError{}
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/alice/containers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Failed to fetch container data") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestServeUserContainersNoStatsUnparsableOutputCrashesTo500(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "not json", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Get(srv.URL + "/users/alice/containers")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// json.loads() failure isn't caught by get_containers()'s
	// except-CalledProcessError, so this is an unhandled crash, not a JSON
	// error body.
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- POST /users/{username}/containers/{action}/{container_name} ---

func expectContext(mock sqlmock.Sqlmock, username, context string) {
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs(username).
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow(context))
}

func TestServeManageContainerAPIInvalidActionReturns400(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/bogus/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServeManageContainerMissingContextReturns404(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("ghost").
		WillReturnError(errors.New("no rows"))

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/ghost/containers/start/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeManageContainerCPUMissingValueReturns400(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/cpu/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "value is required") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestServeManageContainerCPUEnvFileNotFoundReturns500JSON(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "definitely-nonexistent-user-xyz", "definitely-nonexistent-user-xyz")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(
		srv.URL+"/users/definitely-nonexistent-user-xyz/containers/cpu/web",
		"application/json",
		strings.NewReader(`{"value":"1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["success"] != false {
		t.Fatalf("expected success:false, got %s", body)
	}
	if !strings.Contains(decoded["message"].(string), ".env file not found at") {
		t.Fatalf("unexpected message: %s", body)
	}
}

func TestServeManageContainerCPUCrashesOnMalformedJSONBody(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/cpu/web", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (mirrors the AttributeError crash on a malformed JSON body), got %d", resp.StatusCode)
	}
}

func TestServeManageContainerRestartExitErrorReturns500JSON(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "", &exec.ExitError{}
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/restart/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	if decoded["success"] != false {
		t.Fatalf("expected a JSON success:false body (caught by the route's own try/except), got %s", body)
	}
}

func TestServeManageContainerRestartNonExitErrorCrashesTo500(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "", errors.New("compose down failed")
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/restart/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "success") {
		t.Fatalf("expected a plain-text crash body (not caught by except CalledProcessError), got %s", body)
	}
}

func TestServeManageContainerRestartSuccess(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/restart/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "restarted successfully") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestServeManageContainerStartWithPullTrueInsertsPullFlag(t *testing.T) {
	orig := containerComposeCaptureRun
	var gotArgs []string
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/start/web", "application/json", strings.NewReader(`{"pull":true}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if strings.Join(gotArgs, " ") != "--pull up -d web" {
		t.Fatalf("expected --pull inserted, got %q", strings.Join(gotArgs, " "))
	}
	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)
	response, ok := decoded["response"].(map[string]interface{})
	if !ok || response["success"] != true {
		t.Fatalf("expected the whole start_or_stop_container dict nested under \"response\", got %s", body)
	}
}

func TestServeManageContainerStopFailureReturns500JSON(t *testing.T) {
	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "no such container", &exec.ExitError{}
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	expectContext(mock, "alice", "alice-ctx")

	c := &APIContainers{MySQL: mysqlDB}
	srv, client := newAPIContainersTestServer(t, c)

	resp, err := client.Post(srv.URL+"/users/alice/containers/stop/web", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Command failed with error") {
		t.Fatalf("unexpected body: %s", body)
	}
}
