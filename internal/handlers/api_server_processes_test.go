package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func newAPIServerProcessesMux(p *APIServerProcesses) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/processes", p.ServeProcesses)
	mux.HandleFunc("POST /api/server/processes/{pid}/{action}", p.ServeProcessAction)
	return mux
}

func TestAPIServeProcessesDefaultsToCPUSort(t *testing.T) {
	p := &APIServerProcesses{}
	srv := httptest.NewServer(newAPIServerProcessesMux(p))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var procs []processInfo
	if err := json.NewDecoder(resp.Body).Decode(&procs); err != nil {
		t.Fatal(err)
	}
	if len(procs) == 0 {
		t.Fatal("expected at least the current test process to be listed")
	}
}

func TestAPIServeProcessActionInvalidAction(t *testing.T) {
	p := &APIServerProcesses{}
	srv := httptest.NewServer(newAPIServerProcessesMux(p))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/processes/1/reboot", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid action, only 'kill' is permitted.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeProcessActionBadPid(t *testing.T) {
	p := &APIServerProcesses{}
	srv := httptest.NewServer(newAPIServerProcessesMux(p))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/processes/notanumber/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeProcessActionKillFailureForBogusPid(t *testing.T) {
	p := &APIServerProcesses{}
	srv := httptest.NewServer(newAPIServerProcessesMux(p))
	defer srv.Close()

	// A pid this large cannot correspond to a real process.
	resp, err := http.Post(srv.URL+"/api/server/processes/999999999/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != false {
		t.Fatalf("expected success=false, got %+v", body)
	}
}

func TestAPIServeProcessActionKillSuccess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a scratch process to kill: %v", err)
	}
	defer cmd.Wait()

	p := &APIServerProcesses{}
	srv := httptest.NewServer(newAPIServerProcessesMux(p))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/processes/"+strconv.Itoa(cmd.Process.Pid)+"/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != true {
		t.Fatalf("expected success=true, got %+v", body)
	}
}
