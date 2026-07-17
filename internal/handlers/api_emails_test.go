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

	"openadmin/internal/config"
)

func newAPIEmailsTestServer(t *testing.T, a *APIEmails) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()

	origCompose := EmailsMailComposeFile
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	t.Cleanup(func() { EmailsMailComposeFile = origCompose })

	origEnv := EmailsMailserverEnvFile
	EmailsMailserverEnvFile = filepath.Join(dir, "mailserver.env")
	t.Cleanup(func() { EmailsMailserverEnvFile = origEnv })

	origPostfwd := PostfwdConfigPath
	PostfwdConfigPath = filepath.Join(dir, "postfwd.cf")
	t.Cleanup(func() { PostfwdConfigPath = origPostfwd })

	origCaddy := EmailsCaddyConfigDir
	EmailsCaddyConfigDir = filepath.Join(dir, "caddy") + string(os.PathSeparator)
	os.MkdirAll(EmailsCaddyConfigDir, 0755)
	t.Cleanup(func() { EmailsCaddyConfigDir = origCaddy })

	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })

	origRestart := emailsRestartMailserverRun
	emailsRestartMailserverRun = func() {}
	t.Cleanup(func() { emailsRestartMailserverRun = origRestart })

	origAccounts := emailsListAccountsRun
	emailsListAccountsRun = func() []emailQuota { return nil }
	t.Cleanup(func() { emailsListAccountsRun = origAccounts })

	origPs := emailsPodmanPsRun
	emailsPodmanPsRun = func(args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { emailsPodmanPsRun = origPs })

	origOpencli := emailsAPIOpencliRun
	emailsAPIOpencliRun = func(args ...string) error { return nil }
	t.Cleanup(func() { emailsAPIOpencliRun = origOpencli })

	origToggle := emailsAPIPostfwdToggleRun
	emailsAPIPostfwdToggleRun = func(action string) {}
	t.Cleanup(func() { emailsAPIPostfwdToggleRun = origToggle })

	origQueuePodman := emailsAPIQueuePodmanRun
	emailsAPIQueuePodmanRun = func(args ...string) error { return nil }
	t.Cleanup(func() { emailsAPIQueuePodmanRun = origQueuePodman })

	origHup := hupPostfwdRun
	hupPostfwdRun = func() {}
	t.Cleanup(func() { hupPostfwdRun = origHup })

	origScript := runRatelimitScriptRun
	runRatelimitScriptRun = func(skipReload bool, args ...string) (bool, string) { return true, "" }
	t.Cleanup(func() { runRatelimitScriptRun = origScript })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /emails/settings", a.ServeSettings)
	mux.HandleFunc("POST /emails/settings", a.ServeSettings)
	mux.HandleFunc("GET /emails/accounts", a.ServeAccounts)
	mux.HandleFunc("POST /emails/accounts", a.ServeAccounts)
	mux.HandleFunc("DELETE /emails/accounts", a.ServeAccounts)
	mux.HandleFunc("GET /emails/queue", a.ServeQueue)
	mux.HandleFunc("POST /emails/queue", a.ServeQueue)
	mux.HandleFunc("GET /emails/domain-limits", a.ServeDomainLimits)
	mux.HandleFunc("POST /emails/domain-limits", a.ServeDomainLimits)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// --- /emails/settings ---

func TestAPIServeSettingsGET(t *testing.T) {
	a := &APIEmails{PublicIP: "203.0.113.9"}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/emails/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", body, err)
	}
	for _, key := range []string{"webmail-status", "webmail-domain", "webmail-selected", "mailserver-status", "emails", "config_data", "email_storage_location"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("expected key %q in response, got %s", key, body)
		}
	}
}

func TestAPIServeSettingsPOSTNonJSONReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/x-www-form-urlencoded", strings.NewReader("x=1"))
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

func TestAPIServeSettingsPOSTStorageLocationRejectedWithExistingAccounts(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)
	emailsListAccountsRun = func() []emailQuota { return []emailQuota{{Email: "a@b.com"}} }

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"storage_type":"user_dir","email_storage_location":"user_dir"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "cannot be changed when email accounts already exist") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeSettingsPOSTStorageLocationSuccess(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"storage_type":"custom","email_storage_location":"/mnt/mail/"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "storage location updated successfully") {
		t.Fatalf("unexpected body: %s", body)
	}
	if got := getEmailStorageLocation(); got != "/mnt/mail/" {
		t.Fatalf("expected storage location persisted, got %q", got)
	}
}

func TestAPIServeSettingsPOSTWebmailDomainAlreadyConfigured(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)
	os.WriteFile(filepath.Join(EmailsCaddyConfigDir, "webmail.conf"), []byte("webmail.example.com { }"), 0644)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"webmail_domain":"https://webmail.example.com/"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "already exists in Caddy configuration") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeSettingsPOSTWebmailDomainSuccess(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"webmail_domain":"webmail.example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Webmail domain updated successfully") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeSettingsPOSTWebmailSoftwareInvalid(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"webmail_software":"squirrelmail"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid webmail client selected") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeSettingsPOSTChecklistTogglesAndRestarts(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)
	os.WriteFile(EmailsMailserverEnvFile, []byte("ENABLE_CLAMAV=0\nENABLE_FAIL2BAN=0\n"), 0644)

	restarted := false
	emailsRestartMailserverRun = func() { restarted = true }

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json",
		strings.NewReader(`{"ENABLE_CLAMAV":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !restarted {
		t.Fatal("expected the mailserver restart to be triggered")
	}
	if !strings.Contains(string(body), "ClamAV") {
		t.Fatalf("expected the ClamAV-specific message, got %s", body)
	}
	data, _ := parseMailserverEnvFile(EmailsMailserverEnvFile)
	if data["ENABLE_CLAMAV"] != "1" {
		t.Fatalf("expected ENABLE_CLAMAV persisted as 1, got %+v", data)
	}
}

func TestAPIServeSettingsPOSTNoRecognizedFieldsReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/settings", "application/json", strings.NewReader(`{"unrelated":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "No recognized settings provided") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- /emails/accounts ---

func TestAPIServeAccountsGETNotRunning(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)
	// compose.yml doesn't exist -> not_installed.

	resp, err := client.Get(srv.URL + "/emails/accounts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"not_installed"`) || !strings.Contains(string(body), `"emails":[]`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsGETRunningListsAccounts(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	emailsListAccountsRun = func() []emailQuota { return []emailQuota{{Email: "a@b.com", Quota: "1024"}} }

	resp, err := client.Get(srv.URL + "/emails/accounts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"email":"a@b.com"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsDELETENoEmailsReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/emails/accounts", strings.NewReader(`{"emails":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "No emails provided") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsDELETESuccess(t *testing.T) {
	orig := runEmailsCmd
	var capturedArgs []string
	runEmailsCmd = func(args []string) (bool, string) {
		capturedArgs = args
		return true, ""
	}
	t.Cleanup(func() { runEmailsCmd = orig })

	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/emails/accounts", strings.NewReader(`{"emails":["a@b.com","c@d.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Deleted 2 account(s)") {
		t.Fatalf("unexpected body: %s", body)
	}
	if strings.Join(capturedArgs, " ") != "opencli email-setup email del a@b.com c@d.com" {
		t.Fatalf("unexpected args: %v", capturedArgs)
	}
}

func TestAPIServeAccountsPOSTPasswordMissingFieldsReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/accounts", "application/json",
		strings.NewReader(`{"action":"password","email":"a@b.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Email and password are required") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsPOSTQuotaSetSuccess(t *testing.T) {
	orig := runEmailsCmd
	runEmailsCmd = func(args []string) (bool, string) { return true, "" }
	t.Cleanup(func() { runEmailsCmd = orig })

	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/accounts", "application/json",
		strings.NewReader(`{"action":"quota-set","email":"a@b.com","quota":"2048"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Quota updated") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsPOSTRestrictInvalidParamsReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/accounts", "application/json",
		strings.NewReader(`{"action":"restrict","email":"a@b.com","restrict_action":"add","type":"bogus"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid parameters") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeAccountsPOSTInvalidActionReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/accounts", "application/json", strings.NewReader(`{"action":"bogus"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid action. Use 'password', 'quota-set', 'quota-del', or 'restrict'.") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- /emails/queue ---

func TestAPIServeQueueGET(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/emails/queue")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"mailserver_status"`) || !strings.Contains(string(body), `"queue"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeQueuePOSTInvalidActionScopeReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/queue", "application/json", strings.NewReader(`{"action":"bogus","scope":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid action/scope") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeQueuePOSTRetryAllSuccess(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	// newAPIEmailsTestServer already stubs emailsAPIQueuePodmanRun; override
	// it again here (after the helper, so this wins) to capture the args.
	var capturedArgs []string
	emailsAPIQueuePodmanRun = func(args ...string) error {
		capturedArgs = args
		return nil
	}

	resp, err := client.Post(srv.URL+"/emails/queue", "application/json", strings.NewReader(`{"action":"retry","scope":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":true`) {
		t.Fatalf("unexpected body: %s", body)
	}
	if strings.Join(capturedArgs, " ") != "exec "+emailsMailserverContainerName+" postqueue -f" {
		t.Fatalf("unexpected args: %v", capturedArgs)
	}
}

func TestAPIServeQueuePOSTDeleteSelectedFailureReturns500(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	emailsAPIQueuePodmanRun = func(args ...string) error {
		return &os.PathError{Op: "exec", Path: "podman", Err: os.ErrNotExist}
	}

	resp, err := client.Post(srv.URL+"/emails/queue", "application/json",
		strings.NewReader(`{"action":"delete","scope":"selected","queue_ids":["ABC123"]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"success":false`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

// --- /emails/domain-limits ---

func TestAPIServeDomainLimitsGETHitsRequiresDomain(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/emails/domain-limits")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"rules"`) || !strings.Contains(string(body), `"usernames"`) || !strings.Contains(string(body), `"raw_content"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeDomainLimitsGETHitsReturnsLines(t *testing.T) {
	origHits := getLimitHitsRun
	getLimitHitsRun = func(domain string, n int) []string { return []string{"line1 reached limit of 10"} }
	t.Cleanup(func() { getLimitHitsRun = origHits })

	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Get(srv.URL + "/emails/domain-limits?hits=example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"domain":"example.com"`) || !strings.Contains(string(body), "line1") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIServeDomainLimitsPOSTUpdateDomain(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json",
		strings.NewReader(`{"action":"update-domain","domain":"acme.com","username":"acme","limit":30}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected body: %s", body)
	}
	content := readPostfwdRaw()
	if !strings.Contains(content, "id=limit_acme_acme_com") {
		t.Fatalf("expected rule written, got:\n%s", content)
	}
}

func TestAPIServeDomainLimitsPOSTUpdateDomainInvalidLimit(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json",
		strings.NewReader(`{"action":"update-domain","domain":"acme.com","username":"acme","limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIServeDomainLimitsPOSTRawContent(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	hupCalled := false
	hupPostfwdRun = func() { hupCalled = true }

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json",
		strings.NewReader(`{"raw_content":"id=limit_x_y ; sender=~.+@y ; protocol_state==RCPT\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "File saved and postfwd reloaded") {
		t.Fatalf("unexpected body: %s", body)
	}
	if !hupCalled {
		t.Fatal("expected postfwd HUP to be triggered")
	}
	if !strings.Contains(readPostfwdRaw(), "id=limit_x_y") {
		t.Fatalf("expected raw content persisted, got:\n%s", readPostfwdRaw())
	}
}

func TestAPIServeDomainLimitsPOSTResetAll(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	var capturedArgs []string
	runRatelimitScriptRun = func(skipReload bool, args ...string) (bool, string) {
		capturedArgs = args
		return true, "reset"
	}

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json", strings.NewReader(`{"action":"reset-all"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected body: %s", body)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "--all-users" {
		t.Fatalf("unexpected args: %v", capturedArgs)
	}
}

func TestAPIServeDomainLimitsPOSTMissingDomainReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json", strings.NewReader(`{"action":"reset-domain"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestAPIServeDomainLimitsPOSTInvalidActionReturns400(t *testing.T) {
	a := &APIEmails{}
	srv, client := newAPIEmailsTestServer(t, a)

	resp, err := client.Post(srv.URL+"/emails/domain-limits", "application/json", strings.NewReader(`{"action":"bogus"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Invalid action.") {
		t.Fatalf("unexpected body: %s", body)
	}
}
