package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/config"
)

func newAPIServerOpsMux(a *APIServerOps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/timezone", a.ServeTimezone)
	mux.HandleFunc("POST /api/server/timezone", a.ServeTimezone)
	mux.HandleFunc("POST /api/server/root-password", a.ServeRootPassword)
	mux.HandleFunc("POST /api/server/reboot", a.ServeReboot)
	return mux
}

// --- timezone ---

func TestAPIServeTimezoneGet(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "UTC"})
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) { return "UTC", "", nil })

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/timezone")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["current_timezone"] != "UTC" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestAPIServeTimezonePostInvalidTimezone(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "UTC"})
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) { return "UTC", "", nil })

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/timezone", "application/json", strings.NewReader(`{"timezone":"Not/Real"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeTimezonePostSuccess(t *testing.T) {
	withFixtureTimezones(t, []string{"Europe/Belgrade", "UTC"})
	var gotArgs []string
	withStubbedTimedatectl(t, func(args ...string) (string, string, error) {
		gotArgs = args
		return "UTC", "", nil
	})

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/timezone", "application/json", strings.NewReader(`{"timezone":"Europe/Belgrade"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "set-timezone" || gotArgs[1] != "Europe/Belgrade" {
		t.Fatalf("unexpected timedatectl invocation: %+v", gotArgs)
	}
}

// --- node ---

func withScratchAPIAdminConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = orig })
}

// --- root password ---

func TestAPIServeRootPasswordEmptyPassword(t *testing.T) {
	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/root-password", "application/json", strings.NewReader(`{"password":""}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeRootPasswordSuccess(t *testing.T) {
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		if args[0] == "--status" {
			return "root P 01/01/2024 0 99999 7 -1", "", nil
		}
		return "", "", nil
	})

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/root-password", "application/json", strings.NewReader(`{"password":"newpass"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "SSH password changed successfully!") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeRootPasswordChangeFails(t *testing.T) {
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		return "", "authentication failure", errors.New("exit status 1")
	})

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/root-password", "application/json", strings.NewReader(`{"password":"newpass"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Error changing password: authentication failure") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeRootPasswordVerificationFails(t *testing.T) {
	withStubbedPasswd(t, func(stdin string, args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "--status" {
			return "root L 01/01/2024 0 99999 7 -1", "", nil
		}
		return "", "", nil
	})

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/root-password", "application/json", strings.NewReader(`{"password":"newpass"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Password change verification failed.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- reboot ---

func TestAPIServeRebootDisabledFlag(t *testing.T) {
	dir := t.TempDir()
	orig := RebootDisableFlagPath
	RebootDisableFlagPath = filepath.Join(dir, "disable_openadmin_reboot_ui")
	t.Cleanup(func() { RebootDisableFlagPath = orig })
	if err := os.WriteFile(RebootDisableFlagPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/reboot", "application/json", strings.NewReader(`{"reboot_type":"graceful"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAPIServeRebootInvalidType(t *testing.T) {
	dir := t.TempDir()
	orig := RebootDisableFlagPath
	RebootDisableFlagPath = filepath.Join(dir, "does-not-exist")
	t.Cleanup(func() { RebootDisableFlagPath = orig })

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/reboot", "application/json", strings.NewReader(`{"reboot_type":"nonsense"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeRebootGracefulRunsAndBlocksUntilDone(t *testing.T) {
	dir := t.TempDir()
	origFlag := RebootDisableFlagPath
	RebootDisableFlagPath = filepath.Join(dir, "does-not-exist")
	t.Cleanup(func() { RebootDisableFlagPath = origFlag })

	origGraceful := apiRebootGracefulRun
	called := false
	apiRebootGracefulRun = func() error { called = true; return nil }
	t.Cleanup(func() { apiRebootGracefulRun = origGraceful })

	a := &APIServerOps{}
	srv := httptest.NewServer(newAPIServerOpsMux(a))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/reboot", "application/json", strings.NewReader(`{"reboot_type":"graceful"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !called {
		t.Fatal("expected apiRebootGracefulRun to be called")
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != true || body["reboot_started"] != true {
		t.Fatalf("unexpected body: %+v", body)
	}
}
