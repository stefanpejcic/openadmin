package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func newAPIServicesMux(s *APIServices) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services", s.ServeServicesFile)
	mux.HandleFunc("PUT /api/services", s.ServeServicesFile)
	mux.HandleFunc("GET /api/services/status", s.ServeServicesStatus)
	mux.HandleFunc("POST /api/service/{action}/{service_name}", s.HandleManageService)
	return mux
}

// aRealExitError returns a genuine *exec.ExitError from a command that
// always exits nonzero, for tests that need to exercise the
// "nonzero exit vs hard invocation failure" branch precisely.
func aRealExitError(t *testing.T) error {
	t.Helper()
	err := exec.Command("false").Run()
	if err == nil {
		t.Fatal("expected `false` to exit nonzero")
	}
	return err
}

func TestAPIServeServicesFileGet(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "caddy") {
		t.Fatalf("expected file content passed through, got %s", body)
	}
}

func TestAPIServeServicesFileGetMissingIs404(t *testing.T) {
	dir := t.TempDir()
	orig := ServicesConfigPath
	ServicesConfigPath = dir + "/does-not-exist.json"
	t.Cleanup(func() { ServicesConfigPath = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAPIServeServicesFilePutWritesFile(t *testing.T) {
	path := withScratchServicesConfig(t, "")

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/services", strings.NewReader(`{"svc1":{"name":"One"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	written, _ := os.ReadFile(path)
	if !strings.Contains(string(written), "svc1") {
		t.Fatalf("expected the new config written to disk, got %s", written)
	}
}

func TestAPIServeServicesFilePutRejectsNonJSONContentType(t *testing.T) {
	withScratchServicesConfig(t, "")

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/services", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeServicesFilePutRejectsInvalidJSONBody(t *testing.T) {
	withScratchServicesConfig(t, "")

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/services", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeServicesStatusFiltersOnDashboardAndChecksBoth(t *testing.T) {
	withScratchServicesConfig(t, `[
		{"name":"Caddy","real_name":"caddy","type":"docker","on_dashboard":true},
		{"name":"SSH","real_name":"sshd","type":"system","on_dashboard":true},
		{"name":"Hidden","real_name":"hidden","type":"docker","on_dashboard":false}
	]`)

	origDocker := apiServicesDockerPSNamesRun
	apiServicesDockerPSNamesRun = func() ([]string, error) { return []string{"caddy"}, nil }
	t.Cleanup(func() { apiServicesDockerPSNamesRun = origDocker })

	origSystemctl := apiSystemctlIsActiveRun
	apiSystemctlIsActiveRun = func(name string) (bool, error) { return name == "sshd", nil }
	t.Cleanup(func() { apiSystemctlIsActiveRun = origSystemctl })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 2 {
		t.Fatalf("expected only the 2 on_dashboard entries, got %+v", got)
	}
	for _, entry := range got {
		switch entry["real_name"] {
		case "caddy":
			if entry["status"] != "Active" {
				t.Fatalf("expected caddy Active, got %+v", entry)
			}
		case "sshd":
			if entry["status"] != "Active" {
				t.Fatalf("expected sshd Active, got %+v", entry)
			}
		default:
			t.Fatalf("unexpected entry in output: %+v", entry)
		}
	}
}

func TestAPIServeServicesStatusPodmanChecksSocketUnit(t *testing.T) {
	withScratchServicesConfig(t, `[
		{"name":"Podman","real_name":"podman","type":"system","on_dashboard":true}
	]`)

	origDocker := apiServicesDockerPSNamesRun
	apiServicesDockerPSNamesRun = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { apiServicesDockerPSNamesRun = origDocker })

	origSystemctl := apiSystemctlIsActiveRun
	// podman.service is transient/socket-activated and is "inactive" almost
	// always even when Podman is fully up -- only podman.socket reflects
	// real liveness, so that's the only unit this stub reports active.
	apiSystemctlIsActiveRun = func(name string) (bool, error) { return name == "podman.socket", nil }
	t.Cleanup(func() { apiSystemctlIsActiveRun = origSystemctl })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 1 || got[0]["status"] != "Active" {
		t.Fatalf("expected podman Active via podman.socket, got %+v", got)
	}
}

func TestAPIServeServicesStatusDockerCheckErrorSurfacesAsErrorString(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker","on_dashboard":true}]`)

	origDocker := apiServicesDockerPSNamesRun
	apiServicesDockerPSNamesRun = func() ([]string, error) { return nil, errors.New("podman not found") }
	t.Cleanup(func() { apiServicesDockerPSNamesRun = origDocker })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 1 || got[0]["status"] != "Error: podman not found" {
		t.Fatalf("expected an Error: status string, got %+v", got)
	}
}

func TestAPIServeServicesStatusUnreadableConfigIs500(t *testing.T) {
	dir := t.TempDir()
	orig := ServicesConfigPath
	ServicesConfigPath = dir + "/missing.json"
	t.Cleanup(func() { ServicesConfigPath = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/services/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestAPIHandleManageServiceInvalidAction(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":true}]`)

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/reboot/caddy", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIHandleManageServiceInvalidServiceName(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":true}]`)

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/start/not-monitored", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIHandleManageServiceExcludesNonDashboardServices(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":false}]`)

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/start/caddy", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a service not flagged on_dashboard, got %d", resp.StatusCode)
	}
}

func TestAPIHandleManageServiceDockerBackedSuccess(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":true}]`)

	orig := containerComposeCaptureRun
	var gotArgs []string
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		gotArgs = args
		return "ok", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/start/caddy", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != "Caddy started successfully" {
		t.Fatalf("unexpected success message: %+v", body)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "up" || gotArgs[1] != "-d" {
		t.Fatalf("expected an `up -d` compose invocation, got %+v", gotArgs)
	}
}

func TestAPIHandleManageServiceDockerBackedNonzeroExitIs400(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":true}]`)

	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		return "", "boom", aRealExitError(t)
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/start/caddy", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "boom") {
		t.Fatalf("expected stderr in the error message, got %s", body)
	}
}

func TestAPIHandleManageServiceRestartAbortsOn500WhenDownFails(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"caddy","on_dashboard":true}]`)

	orig := containerComposeCaptureRun
	containerComposeCaptureRun = func(context, dir string, args ...string) (string, string, error) {
		if args[0] == "down" {
			return "", "down failed", aRealExitError(t)
		}
		t.Fatal("expected the 'up' step to never run once 'down' has failed")
		return "", "", nil
	}
	t.Cleanup(func() { containerComposeCaptureRun = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/restart/caddy", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (mirroring check=True raising), got %d", resp.StatusCode)
	}
}

func TestAPIHandleManageServiceGenericServiceRoutesThroughServiceCommand(t *testing.T) {
	withScratchServicesConfig(t, `[{"real_name":"netdata","on_dashboard":true}]`)

	orig := apiManageServiceGenericRun
	var gotName, gotAction string
	apiManageServiceGenericRun = func(name, action string) (string, string, error) {
		gotName, gotAction = name, action
		return "", "", nil
	}
	t.Cleanup(func() { apiManageServiceGenericRun = orig })

	s := &APIServices{}
	srv := httptest.NewServer(newAPIServicesMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/service/stop/netdata", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if gotName != "netdata" || gotAction != "stop" {
		t.Fatalf("expected the generic runner called with (netdata, stop), got (%q, %q)", gotName, gotAction)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	// str.capitalize()-style casing: only the first letter is uppercased.
	if body["success"] != "Netdata stoped successfully" {
		t.Fatalf("unexpected success message: %+v", body)
	}
}
