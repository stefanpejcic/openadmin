package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newAPIImportTestServer(t *testing.T, a *APIImport) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()

	origImportDir := ImporterOpenPanelImportLogDir
	ImporterOpenPanelImportLogDir = filepath.Join(dir, "imports") + string(os.PathSeparator)
	os.MkdirAll(ImporterOpenPanelImportLogDir, 0755)
	t.Cleanup(func() { ImporterOpenPanelImportLogDir = origImportDir })

	origTransferDir := ImporterTransferLogDir
	ImporterTransferLogDir = filepath.Join(dir, "transfers") + string(os.PathSeparator)
	os.MkdirAll(ImporterTransferLogDir, 0755)
	t.Cleanup(func() { ImporterTransferLogDir = origTransferDir })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/import/{panel_type}", a.ServeImportFromBackup)
	mux.HandleFunc("POST /api/import/{panel_type}", a.ServeImportFromBackup)
	mux.HandleFunc("GET /api/import/logs/account/{log_filename...}", a.ServeAccountImportLog)
	mux.HandleFunc("GET /api/import/logs/transfer/{log_filename...}", a.ServeTransferImportLog)
	mux.HandleFunc("GET /api/import/backup-files", a.ServeListBackupFiles)
	mux.HandleFunc("GET /api/import/transfers", a.ServeTransfers)
	mux.HandleFunc("POST /api/import/transfers", a.ServeTransfers)
	mux.HandleFunc("GET /api/import/transfers/{username}", a.ServeTransfersForUser)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPIImportFromBackupUnsupportedPanelType(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	// "openpanel" is intentionally rejected here even though the HTML
	// admin route accepts it -- the JSON API only ever drives cPanel and
	// CyberPanel migrations through this route.
	for _, panelType := range []string{"openpanel", "plesk"} {
		resp, err := client.Get(srv.URL + "/api/import/" + panelType)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("panel type %q: expected 400, got %d: %s", panelType, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "Unsupported panel type: "+panelType) {
			t.Fatalf("unexpected body: %s", body)
		}
	}
}

func TestAPIImportFromBackupGETListsLogs(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)
	os.WriteFile(filepath.Join(ImporterOpenPanelImportLogDir, "a.log"), []byte("x\nPID: 999999999\nSUCCESS: ok\n"), 0644)

	resp, err := client.Get(srv.URL + "/api/import/cpanel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"filename":"a.log"`) || !strings.Contains(string(body), `"status":"completed"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportFromBackupPOSTMissingFields(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/cpanel", "application/json", strings.NewReader(`{"path":"/home/backup-x.tar.gz"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Backup path and plan name are required.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportFromBackupPOSTRejectsNonJSON(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/cpanel", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid JSON format") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportFromBackupPOSTSuccess(t *testing.T) {
	origRun := importerCloneAndRunImportScriptRun
	var gotDisplayName, gotPath, gotPlan string
	importerCloneAndRunImportScriptRun = func(displayName, backupPath, planName string) (bool, error) {
		gotDisplayName, gotPath, gotPlan = displayName, backupPath, planName
		return false, nil
	}
	t.Cleanup(func() { importerCloneAndRunImportScriptRun = origRun })

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/cyberpanel", "application/json",
		strings.NewReader(`{"path":"/root/backup-user.tar.gz","plan_name":"default_plan_apache"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":true`) || !strings.Contains(string(body), "has started") {
		t.Fatalf("unexpected body: %s", body)
	}
	if gotDisplayName != "CyberPanel" || gotPath != "/root/backup-user.tar.gz" || gotPlan != "default_plan_apache" {
		t.Fatalf("unexpected args passed through: %q %q %q", gotDisplayName, gotPath, gotPlan)
	}
}

func TestAPIImportFromBackupPOSTCloneFailure(t *testing.T) {
	origRun := importerCloneAndRunImportScriptRun
	importerCloneAndRunImportScriptRun = func(displayName, backupPath, planName string) (bool, error) {
		return true, errTestCloneFailed
	}
	t.Cleanup(func() { importerCloneAndRunImportScriptRun = origRun })

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/cpanel", "application/json",
		strings.NewReader(`{"path":"/home/backup-x.tar.gz","plan_name":"basic"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":false`) || !strings.Contains(string(body), "Error during execution") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportAccountLogServesContent(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)
	os.WriteFile(filepath.Join(ImporterOpenPanelImportLogDir, "test.log"), []byte("hello log"), 0644)

	resp, err := client.Get(srv.URL + "/api/import/logs/account/test.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"status":"success"`) || !strings.Contains(string(body), "hello log") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportAccountLogMissingFile(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/import/logs/account/nonexistent.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Log file does not exist.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportAccountLogPathTraversalReturns404(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/import/logs/account/../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an escaping path, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIImportTransferLogServesContent(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "xfer.log"), []byte("transfer log"), 0644)

	resp, err := client.Get(srv.URL + "/api/import/logs/transfer/xfer.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "transfer log") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportBackupFilesMatchesPatterns(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "backup-alice.tar.gz"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "bob_2024-01-15_10-30-00.tar.gz"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "not-a-backup.txt"), []byte(""), 0644)

	origDirs := importerBackupSearchDirs
	importerBackupSearchDirs = []string{dir}
	t.Cleanup(func() { importerBackupSearchDirs = origDirs })

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Get(srv.URL + "/api/import/backup-files")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "backup-alice.tar.gz") || !strings.Contains(string(body), "bob_2024-01-15_10-30-00.tar.gz") {
		t.Fatalf("unexpected body: %s", body)
	}
	if strings.Contains(string(body), "not-a-backup.txt") {
		t.Fatalf("expected non-matching file excluded, got %s", body)
	}
}

func TestAPIImportTransfersGETListsLogs(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "alice_2024.log"), []byte(""), 0644)

	resp, err := client.Get(srv.URL + "/api/import/transfers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "alice_2024.log") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportTransfersPOSTMissingFields(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/transfers", "application/json", strings.NewReader(`{"server":"1.2.3.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Server IP and OpenPanel username are required.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportTransfersPOSTSuccessSkipsIptablesForDomain(t *testing.T) {
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

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/transfers", "application/json",
		strings.NewReader(`{"openpanel_username":"newuser","server":"example.com","password":"secret","port":2222,"live_transfer":true}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "started in the background") {
		t.Fatalf("unexpected body: %s", body)
	}
	if iptablesCalled {
		t.Fatal("expected iptables NOT to be configured for a domain target")
	}
	joined := strings.Join(capturedArgs, " ")
	for _, want := range []string{"--account newuser", "--host example.com", "--username root", "--password secret", "--port 2222", "--live-transfer"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected args to contain %q, got %v", want, capturedArgs)
		}
	}
}

func TestAPIImportTransfersPOSTConfiguresIptablesForIP(t *testing.T) {
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

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/transfers", "application/json",
		strings.NewReader(`{"openpanel_username":"bob","server":"203.0.113.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if !iptablesCalled || capturedServer != "203.0.113.5" {
		t.Fatalf("expected iptables configured for the IP target, called=%v server=%q", iptablesCalled, capturedServer)
	}
}

func TestAPIImportTransfersPOSTStartFailureReturnsJSON500(t *testing.T) {
	origTransfer := importerStartTransferRun
	importerStartTransferRun = func(args []string) error { return &ftpStubError{"boom"} }
	t.Cleanup(func() { importerStartTransferRun = origTransfer })

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	resp, err := client.Post(srv.URL+"/api/import/transfers", "application/json",
		strings.NewReader(`{"openpanel_username":"bob","server":"example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":false`) || !strings.Contains(string(body), "Error starting transfer") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportTransfersForUserStripsSuspendedPrefix(t *testing.T) {
	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "bob_2024.log"), []byte("started\nPID: 999999999\nSUCCESS: ok\n"), 0644)

	resp, err := client.Get(srv.URL + "/api/import/transfers/SUSPENDED_bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "bob_2024.log") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIImportTransfersForUserStatuses(t *testing.T) {
	origAlive := importerPidAlive
	t.Cleanup(func() { importerPidAlive = origAlive })

	a := &APIImport{}
	srv, client := newAPIImportTestServer(t, a)

	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_success.log"), []byte("start\nPID: 111\nSUCCESS: done\n"), 0644)
	os.WriteFile(filepath.Join(ImporterTransferLogDir, "carol_inprogress.log"), []byte("start\nPID: 333\n"), 0644)

	importerPidAlive = func(pid int) error {
		if pid == 333 {
			return nil
		}
		return syscall.ESRCH
	}

	resp, err := client.Get(srv.URL + "/api/import/transfers/carol")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	got := string(body)
	if !strings.Contains(got, `"success"`) {
		t.Fatalf("expected a success entry, got %s", got)
	}
	if !strings.Contains(got, `"in progress"`) {
		t.Fatalf("expected an in-progress entry, got %s", got)
	}
}
