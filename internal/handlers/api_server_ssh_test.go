package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withScratchAPISSHPaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()

	origConfig := SSHDConfigPath
	SSHDConfigPath = filepath.Join(dir, "sshd_config")
	t.Cleanup(func() { SSHDConfigPath = origConfig })

	origKeys := SSHAuthorizedKeysPath
	SSHAuthorizedKeysPath = filepath.Join(dir, "authorized_keys")
	t.Cleanup(func() { SSHAuthorizedKeysPath = origKeys })

	origStatus := sshStatusRun
	sshStatusRun = func() string { return "active" }
	t.Cleanup(func() { sshStatusRun = origStatus })

	origRestart := sshRestartServiceRun
	sshRestartServiceRun = func() {}
	t.Cleanup(func() { sshRestartServiceRun = origRestart })

	origAction := sshExecuteActionRun
	sshExecuteActionRun = func(action string) {}
	t.Cleanup(func() { sshExecuteActionRun = origAction })

	os.WriteFile(SSHDConfigPath, []byte("Port 22\nPasswordAuthentication yes\nPubkeyAuthentication no\nPermitRootLogin yes\n"), 0644)
}

func newAPIServerSSHMux(s *APIServerSSH) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/ssh", s.ServeSSH)
	mux.HandleFunc("POST /api/server/ssh", s.ServeSSH)
	mux.HandleFunc("GET /api/server/ssh/config", s.ServeSSHConfig)
	mux.HandleFunc("POST /api/server/ssh/config", s.ServeSSHConfig)
	return mux
}

func TestAPIServeSSHGetReturnsStatusConfigKeysAndSettings(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/ssh")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "active" || body["port"] != "22" || body["password_auth"] != "yes" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestAPIServeSSHPostInvalidPort(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh", "application/json", strings.NewReader(`{"port":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeSSHPostAction(t *testing.T) {
	withScratchAPISSHPaths(t)

	var gotAction string
	orig := sshExecuteActionRun
	sshExecuteActionRun = func(action string) { gotAction = action }
	t.Cleanup(func() { sshExecuteActionRun = orig })

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh", "application/json", strings.NewReader(`{"action":"restart"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if gotAction != "restart" {
		t.Fatalf("expected the restart action to run, got %q", gotAction)
	}
	if !strings.Contains(string(body), "SSH service has been restarted.") {
		t.Fatalf("unexpected message: %s", body)
	}
}

func TestAPIServeSSHPostNewKey(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh", "application/json", strings.NewReader(`{"new_key":"ssh-ed25519 AAAA test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	written, _ := os.ReadFile(SSHAuthorizedKeysPath)
	if !strings.Contains(string(written), "ssh-ed25519 AAAA test") {
		t.Fatalf("expected the new key to be appended, got %q", written)
	}
}

func TestAPIServeSSHPostNoRecognizedParameters(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "No recognized parameters provided.") {
		t.Fatalf("unexpected error message: %s", body)
	}
}

func TestAPIServeSSHConfigGet(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/ssh/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["config"], "Port 22") {
		t.Fatalf("unexpected config body: %+v", body)
	}
}

func TestAPIServeSSHConfigPostRequiresConfig(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh/config", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeSSHConfigPostOverwritesFile(t *testing.T) {
	withScratchAPISSHPaths(t)

	s := &APIServerSSH{}
	srv := httptest.NewServer(newAPIServerSSHMux(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/ssh/config", "application/json", strings.NewReader(`{"config":"Port 2222\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	written, _ := os.ReadFile(SSHDConfigPath)
	if string(written) != "Port 2222\n" {
		t.Fatalf("expected the config file overwritten, got %q", written)
	}
}
