package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

func newEmailsTestServer(t *testing.T, e *Emails) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origCompose := EmailsMailComposeFile
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	t.Cleanup(func() { EmailsMailComposeFile = origCompose })

	origEnv := EmailsMailserverEnvFile
	EmailsMailserverEnvFile = filepath.Join(dir, "mailserver.env")
	t.Cleanup(func() { EmailsMailserverEnvFile = origEnv })

	origRestart := emailsRestartMailserverRun
	emailsRestartMailserverRun = func() {}
	t.Cleanup(func() { emailsRestartMailserverRun = origRestart })

	origAccounts := emailsListAccountsRun
	emailsListAccountsRun = func() []emailQuota { return nil }
	t.Cleanup(func() { emailsListAccountsRun = origAccounts })

	origPs := emailsPodmanPsRun
	emailsPodmanPsRun = func(args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { emailsPodmanPsRun = origPs })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	e.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /emails/settings", e.ServeEmailsSettings)
	mux.HandleFunc("POST /emails/settings", e.ServeEmailsSettings)
	mux.HandleFunc("POST /emails/api/update-password", e.ServeUpdatePassword)
	mux.HandleFunc("POST /emails/api/quota-set", e.ServeQuotaSet)
	mux.HandleFunc("POST /emails/api/quota-del", e.ServeQuotaDel)
	mux.HandleFunc("POST /emails/api/restrict", e.ServeRestrict)
	mux.HandleFunc("POST /emails/api/delete", e.ServeDeleteEmails)
	mux.HandleFunc("GET /emails/accounts", e.ServeEmailsAccounts)
	mux.HandleFunc("GET /emails/queue", e.ServeEmailsQueue)
	mux.HandleFunc("POST /emails/queue/action", e.ServeEmailsQueueAction)
	mux.HandleFunc("GET /emails/reports", e.ServeEmailsReports)
	mux.HandleFunc("GET /emails/data/{filename}", e.ServeShowReport)
	mux.HandleFunc("GET /emails/webmail/{email}", e.ServeEmailsWebmailLink)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
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

func TestIsIPv4(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4": true, "255.255.255.255": true, "0.0.0.0": true,
		"256.1.1.1": false, "1.2.3": false, "example.com": false, "": false,
	}
	for in, want := range cases {
		if got := isIPv4(in); got != want {
			t.Errorf("isIPv4(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsValidEmailStorageLocation(t *testing.T) {
	cases := map[string]bool{
		"":               false,
		"user_dir":       true,
		"/var/mail/":     true,
		"relative/path":  false,
		"/has/../dotdot": false,
	}
	for in, want := range cases {
		if got := isValidEmailStorageLocation(in); got != want {
			t.Errorf("isValidEmailStorageLocation(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadDovecotMasterPassGeneratesAndPersistsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")

	orig := EmailsDovecotSecretKeyPath
	EmailsDovecotSecretKeyPath = path
	t.Cleanup(func() { EmailsDovecotSecretKeyPath = orig })

	got, err := LoadDovecotMasterPass()
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected a non-empty generated secret")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the generated secret persisted to disk: %v", err)
	}
	if strings.TrimSpace(string(raw)) != got {
		t.Fatalf("expected the persisted file to match the returned secret")
	}

	got2, err := LoadDovecotMasterPass()
	if err != nil {
		t.Fatal(err)
	}
	if got2 != got {
		t.Fatal("expected the second load to reuse the persisted secret")
	}
}

func TestParseAndUpdateMailserverEnvFile(t *testing.T) {
	dir := t.TempDir()
	origEnv := EmailsMailserverEnvFile
	EmailsMailserverEnvFile = filepath.Join(dir, "mailserver.env")
	t.Cleanup(func() { EmailsMailserverEnvFile = origEnv })

	os.WriteFile(EmailsMailserverEnvFile, []byte("# comment\nENABLE_CLAMAV=0\nENABLE_FAIL2BAN=1\n"), 0644)

	data, err := parseMailserverEnvFile(EmailsMailserverEnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if data["ENABLE_CLAMAV"] != "0" || data["ENABLE_FAIL2BAN"] != "1" {
		t.Fatalf("unexpected parsed data: %+v", data)
	}

	if err := updateEnvVariable("ENABLE_CLAMAV", "1"); err != nil {
		t.Fatal(err)
	}
	data, _ = parseMailserverEnvFile(EmailsMailserverEnvFile)
	if data["ENABLE_CLAMAV"] != "1" {
		t.Fatalf("expected ENABLE_CLAMAV updated to 1, got %+v", data)
	}

	if err := updateEnvVariable("NONEXISTENT_KEY", "1"); err == nil {
		t.Fatal("expected an error updating a key that doesn't exist in the file")
	}
}

func TestParseEnvFileDerivesEnablePostfwdFromFileExistence(t *testing.T) {
	dir := t.TempDir()
	origEnv := EmailsMailserverEnvFile
	EmailsMailserverEnvFile = filepath.Join(dir, "mailserver.env")
	t.Cleanup(func() { EmailsMailserverEnvFile = origEnv })
	origPostfwd := PostfwdConfigPath
	PostfwdConfigPath = filepath.Join(dir, "postfwd.cf")
	t.Cleanup(func() { PostfwdConfigPath = origPostfwd })

	os.WriteFile(EmailsMailserverEnvFile, []byte("ENABLE_CLAMAV=1\n"), 0644)

	data, err := parseEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if data["ENABLE_POSTFWD"] != "0" {
		t.Fatalf("expected ENABLE_POSTFWD=0 without postfwd.cf present, got %+v", data)
	}

	os.WriteFile(PostfwdConfigPath, []byte(""), 0644)
	data, _ = parseEnvFile()
	if data["ENABLE_POSTFWD"] != "1" {
		t.Fatalf("expected ENABLE_POSTFWD=1 with postfwd.cf present, got %+v", data)
	}
}

func TestCheckMailserverStatusNotInstalled(t *testing.T) {
	dir := t.TempDir()
	origCompose := EmailsMailComposeFile
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	t.Cleanup(func() { EmailsMailComposeFile = origCompose })

	if got := checkMailserverStatus(); got != "not_installed" {
		t.Fatalf("expected not_installed, got %q", got)
	}
}

func TestCheckMailserverStatusRunningAndStopped(t *testing.T) {
	dir := t.TempDir()
	origCompose := EmailsMailComposeFile
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	t.Cleanup(func() { EmailsMailComposeFile = origCompose })
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)

	origPs := emailsPodmanPsRun
	t.Cleanup(func() { emailsPodmanPsRun = origPs })

	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	if got := checkMailserverStatus(); got != "running" {
		t.Fatalf("expected running, got %q", got)
	}

	emailsPodmanPsRun = func(args ...string) (string, error) { return "", nil }
	if got := checkMailserverStatus(); got != "stopped" {
		t.Fatalf("expected stopped, got %q", got)
	}
}

func TestEmailsAPIRoutesRequireLogin(t *testing.T) {
	// Regression test: confirm these routes reject an unauthenticated
	// caller.
	e := &Emails{}
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	sessions := auth.NewManager("test-secret", false)
	e.Sessions = sessions
	authOpts := auth.Options{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /emails/api/update-password", auth.RequireLogin(sessions, authOpts, e.ServeUpdatePassword))
	mux.HandleFunc("POST /emails/api/delete", auth.RequireLogin(sessions, authOpts, e.ServeDeleteEmails))

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Post(srv.URL+"/emails/api/update-password", "application/json", strings.NewReader(`{"email":"a@b.com","password":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected an unauthenticated caller to be rejected, got 200")
	}

	resp, err = client.Post(srv.URL+"/emails/api/delete", "application/json", strings.NewReader(`{"emails":["a@b.com"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected an unauthenticated caller to be rejected, got 200")
	}
}

func TestServeUpdatePasswordSuccess(t *testing.T) {
	origRun := runEmailsCmd
	runEmailsCmd = func(args []string) (bool, string) { return true, "" }
	t.Cleanup(func() { runEmailsCmd = origRun })

	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/api/update-password", "application/json", strings.NewReader(`{"email":"a@b.com","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Password updated.") {
		t.Fatalf("expected success message, got %s", truncate(string(body)))
	}
}

func TestServeUpdatePasswordMissingFieldsReturns400WithMessageShape(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/api/update-password", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["message"]; !ok {
		t.Fatalf(`expected a "message" key (not "error"), got %s`, truncate(string(body)))
	}
}

func TestServeDeleteEmailsRequiresList(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	resp, err := client.Post(srv.URL+"/emails/api/delete", "application/json", strings.NewReader(`{"emails":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeEmailsAccountsNotRunning(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	// compose.yml doesn't exist in the scratch dir -> not_installed.

	resp, err := client.Get(srv.URL + "/emails/accounts?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"not_installed"`) {
		t.Fatalf("expected not_installed status, got %s", truncate(string(body)))
	}
}

func TestServeEmailsAccountsRunningListsAccounts(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	emailsListAccountsRun = func() []emailQuota { return []emailQuota{{Email: "a@b.com", Quota: "1024"}} }

	resp, err := client.Get(srv.URL + "/emails/accounts?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"email":"a@b.com"`) {
		t.Fatalf("expected the account listed, got %s", truncate(string(body)))
	}
}

func TestServeShowReportBlocksPathTraversal(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/emails/data/..%2f..%2fetc%2fpasswd", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected traversal attempt rejected, got 200: %s", truncate(string(body)))
	}
}

func TestServeShowReportServesRawFile(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	dir := t.TempDir()
	origDataDir := EmailsReportsDataDir
	EmailsReportsDataDir = dir
	t.Cleanup(func() { EmailsReportsDataDir = origDataDir })
	os.WriteFile(filepath.Join(dir, "2024-01-15.html"), []byte("<html>report</html>"), 0644)

	resp, err := client.Get(srv.URL + "/emails/data/2024-01-15.html")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "<html>report</html>" {
		t.Fatalf("expected raw report content, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeShowReportMissingFileReturns404(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	dir := t.TempDir()
	origDataDir := EmailsReportsDataDir
	EmailsReportsDataDir = dir
	t.Cleanup(func() { EmailsReportsDataDir = origDataDir })

	resp, err := client.Get(srv.URL + "/emails/data/nonexistent.html")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeEmailsWebmailLinkInvalidEmailFormat(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	resp, err := client.Get(srv.URL + "/emails/webmail/not-an-email")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for invalid email format, got %d", resp.StatusCode)
	}
}

func TestServeEmailsWebmailLinkNoMasterPassRedirects(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/emails/webmail/user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Fatalf("expected a redirect when MasterPass is empty, got %d", resp.StatusCode)
	}
}

func TestServeEmailsWebmailLinkTokenFailureFlashes(t *testing.T) {
	origToken := createWebmailToken
	createWebmailToken = func(email, masterPass string) (string, bool) { return "", false }
	t.Cleanup(func() { createWebmailToken = origToken })

	e := &Emails{MasterPass: "secretpass"}
	srv, client := newEmailsTestServer(t, e)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/emails/webmail/user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Fatalf("expected a redirect after token-creation failure, got %d", resp.StatusCode)
	}
}

func TestServeEmailsWebmailLinkTokenSuccessRedirectsWithToken(t *testing.T) {
	origToken := createWebmailToken
	createWebmailToken = func(email, masterPass string) (string, bool) { return "abc123", true }
	t.Cleanup(func() { createWebmailToken = origToken })

	e := &Emails{MasterPass: "secretpass"}
	srv, client := newEmailsTestServer(t, e)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/emails/webmail/user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "token=abc123") {
		t.Fatalf("expected the redirect to carry the token, got %q", loc)
	}
}

func TestServeEmailsSettingsPOSTStorageLocationRejectedWithExistingAccounts(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	emailsListAccountsRun = func() []emailQuota { return []emailQuota{{Email: "a@b.com"}} }

	resp, err := client.PostForm(srv.URL+"/emails/settings", url.Values{
		"storage_type": {"user_dir"}, "email_storage_location": {"user_dir"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "cannot be changed when email accounts already exist") {
		t.Fatalf("expected rejection flash, got %s", truncate(string(body)))
	}
}

func TestServeEmailsSettingsPOSTStorageLocationSuccess(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	dir := t.TempDir()
	origAdminConfig := config.AdminConfigPath
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	t.Cleanup(func() { config.AdminConfigPath = origAdminConfig })

	resp, err := client.PostForm(srv.URL+"/emails/settings", url.Values{
		"storage_type": {"custom"}, "email_storage_location": {"/mnt/mail/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "storage location updated successfully") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
}

func TestServeEmailsSettingsPOSTChecklistTogglesAndRestarts(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	os.WriteFile(EmailsMailserverEnvFile, []byte("ENABLE_CLAMAV=0\nENABLE_FAIL2BAN=0\n"), 0644)

	// newEmailsTestServer already stubs emailsRestartMailserverRun to a
	// no-op; override it again here (after the helper, so this wins) to
	// also track whether it was actually invoked.
	restarted := false
	emailsRestartMailserverRun = func() { restarted = true }

	resp, err := client.PostForm(srv.URL+"/emails/settings", url.Values{
		"ENABLE_CLAMAV": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !restarted {
		t.Fatal("expected the mailserver restart to be triggered")
	}
	if !strings.Contains(string(body), "ClamAV") {
		t.Fatalf("expected the ClamAV-specific info flash, got %s", truncate(string(body)))
	}

	data, _ := parseMailserverEnvFile(EmailsMailserverEnvFile)
	if data["ENABLE_CLAMAV"] != "1" {
		t.Fatalf("expected ENABLE_CLAMAV persisted as 1, got %+v", data)
	}
	if data["ENABLE_FAIL2BAN"] != "0" {
		t.Fatalf("expected an unchecked checkbox to be written as 0, got %+v", data)
	}
}

// --- Chrome-restyled page render tests -------------------------------
//
// These guard against a template silently truncating mid-render (the
// bug class this port hit earlier): each asserts 200 plus a few
// distinguishing content strings plus the literal "</html>".

func TestServeEmailsAccountsRendersHTMLNotInstalled(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	// compose.yml doesn't exist in the scratch dir -> not_installed.

	resp, err := client.Get(srv.URL + "/emails/accounts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Emails", "No emails yet.", "Not Configured", "opencli email-server install", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEmailsAccountsRendersHTMLRunning(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	emailsListAccountsRun = func() []emailQuota { return []emailQuota{{Email: "a@b.com", Quota: "512M [10%]"}} }

	resp, err := client.Get(srv.URL + "/emails/accounts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"emailsApp()", `"a@b.com","512M [10%]"`, "email hosted on this server", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEmailsQueueRendersHTMLNotInstalled(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	// compose.yml doesn't exist in the scratch dir -> not_installed.

	resp, err := client.Get(srv.URL + "/emails/queue")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Email Queue", "Not Configured", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEmailsQueueRendersHTMLRunningEmpty(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	// getEmailQueue() shells out to the real podman binary directly (not
	// injectable), which isn't present in the test sandbox, so it always
	// returns a nil/empty queue here -- this still exercises the
	// "running" branch's empty-queue table rendering.

	resp, err := client.Get(srv.URL + "/emails/queue")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"queueManager()", "Queue is empty.", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEmailsReportsRendersHTMLAllBranches(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)

	// not_installed: compose.yml missing (default scratch state).
	resp, err := client.Get(srv.URL + "/emails/reports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Email Reports", "email server is not installed", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("not_installed: expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}

	// stopped: compose.yml present, but no matching running container.
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "", nil }
	resp, err = client.Get(srv.URL + "/emails/reports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Not Running", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("stopped: expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}

	// running: mailserver container reports as running.
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	resp, err = client.Get(srv.URL + "/emails/reports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"No reports yet. Reports are generated every 24h.", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("running: expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeEmailsSettingsRendersHTMLRunning(t *testing.T) {
	e := &Emails{}
	srv, client := newEmailsTestServer(t, e)
	os.WriteFile(EmailsMailComposeFile, []byte(""), 0644)
	emailsPodmanPsRun = func(args ...string) (string, error) { return "openadmin_mailserver\n", nil }
	os.WriteFile(EmailsMailserverEnvFile, []byte("ENABLE_CLAMAV=1\nRELAY_HOST=relay.example.com\n"), 0644)

	resp, err := client.Get(srv.URL + "/emails/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Email Settings", "MailServer Status", "Relay Hosts", "relay.example.com", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}
