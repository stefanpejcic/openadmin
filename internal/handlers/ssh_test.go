package handlers

import (
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
)

func newSSHTestServer(t *testing.T, s *SSH) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origSSHDConfig := SSHDConfigPath
	SSHDConfigPath = filepath.Join(dir, "sshd_config")
	t.Cleanup(func() { SSHDConfigPath = origSSHDConfig })

	origKeysPath := SSHAuthorizedKeysPath
	SSHAuthorizedKeysPath = filepath.Join(dir, "authorized_keys")
	t.Cleanup(func() { SSHAuthorizedKeysPath = origKeysPath })

	origStatusRun := sshStatusRun
	sshStatusRun = func() string { return "active" }
	t.Cleanup(func() { sshStatusRun = origStatusRun })

	origExecuteAction := sshExecuteActionRun
	sshExecuteActionRun = func(string) {}
	t.Cleanup(func() { sshExecuteActionRun = origExecuteAction })

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
	s.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/ssh", s.ServeSSH)
	mux.HandleFunc("POST /server/ssh", s.ServeSSH)
	mux.HandleFunc("GET /server/ssh/config", s.ServeSSHFullConfig)
	mux.HandleFunc("POST /server/ssh/config", s.ServeSSHFullConfig)
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

const testSSHDConfig = `Port 22
#PubkeyAuthentication yes
PasswordAuthentication yes
PermitRootLogin yes
X11Forwarding no
`

func TestIsValidSSHPort(t *testing.T) {
	cases := map[string]bool{"22": true, "10000": true, "21": false, "10001": false, "abc": false, "8080": true}
	for in, want := range cases {
		if got := isValidSSHPort(in); got != want {
			t.Errorf("isValidSSHPort(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSSHParseSettingsLastMatchWins(t *testing.T) {
	config := "Port 22\nPort 2222\n#PermitRootLogin no\nPermitRootLogin yes\n"
	settings := sshParseSettings(config)
	if settings.Port != "2222" {
		t.Fatalf("expected the LAST Port line to win, got %q", settings.Port)
	}
	if settings.PermitRootLogin != "yes" {
		t.Fatalf("expected the LAST PermitRootLogin line to win, got %q", settings.PermitRootLogin)
	}
	// PasswordAuthentication/PubkeyAuthentication not present at all -> defaults.
	if settings.PasswordAuth != "yes" || settings.PubkeyAuth != "no" {
		t.Fatalf("expected defaults for absent directives, got %+v", settings)
	}
}

func TestServeSSHGETJSON(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.Get(srv.URL + "/server/ssh?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"status":"active"`) || !strings.Contains(string(body), `"port":"22"`) {
		t.Fatalf("expected status/port in JSON, got %s", truncate(string(body)))
	}
}

func TestServeSSHRendersHTML(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.Get(srv.URL + "/server/ssh")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"SSH Access", `name="port"`, "function fetchFullConfig", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeSSHPOSTInvalidPortReturnsJSON400(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.PostForm(srv.URL+"/server/ssh", url.Values{"port": {"5"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Invalid SSH port") {
		t.Fatalf("expected the invalid-port error message, got %s", truncate(string(body)))
	}
}

func TestServeSSHPOSTInvalidAuthParamReturnsJSON400(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.PostForm(srv.URL+"/server/ssh", url.Values{"password_auth": {"maybe"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestServeSSHPOSTBasicSettingsUpdatesOnlySubmittedFields(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	// Only "port" submitted -- must NOT corrupt the other 3 directives
	// with a literal "None"/empty value (the fixed Python bug).
	resp, err := client.PostForm(srv.URL+"/server/ssh", url.Values{"port": {"2222"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "SSH settings updated") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	raw, _ := os.ReadFile(SSHDConfigPath)
	content := string(raw)
	if !strings.Contains(content, "Port 2222") {
		t.Fatalf("expected the port line updated, got:\n%s", content)
	}
	if strings.Contains(content, "None") {
		t.Fatalf("expected no directive corrupted with a literal None, got:\n%s", content)
	}
	if !strings.Contains(content, "PasswordAuthentication yes") {
		t.Fatalf("expected PasswordAuthentication left at its existing value, got:\n%s", content)
	}
	if !strings.Contains(content, "PermitRootLogin yes") {
		t.Fatalf("expected PermitRootLogin left at its existing value, got:\n%s", content)
	}
	if !strings.Contains(content, "X11Forwarding no") {
		t.Fatalf("expected unrelated lines preserved verbatim, got:\n%s", content)
	}
}

func TestServeSSHPOSTAddAndRemoveKey(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.PostForm(srv.URL+"/server/ssh", url.Values{"new_key": {"ssh-ed25519 AAAAtest me@host"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "New SSH key added") {
		t.Fatalf("expected key-added flash, got %s", truncate(string(body)))
	}

	raw, _ := os.ReadFile(SSHAuthorizedKeysPath)
	if !strings.Contains(string(raw), "ssh-ed25519 AAAAtest me@host") {
		t.Fatalf("expected the new key appended, got:\n%s", string(raw))
	}

	resp, err = client.PostForm(srv.URL+"/server/ssh", url.Values{"key_to_remove": {"ssh-ed25519 AAAAtest me@host"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "SSH key removed") {
		t.Fatalf("expected key-removed flash, got %s", truncate(string(body)))
	}

	raw, _ = os.ReadFile(SSHAuthorizedKeysPath)
	if strings.Contains(string(raw), "ssh-ed25519 AAAAtest me@host") {
		t.Fatalf("expected the key removed, got:\n%s", string(raw))
	}
}

func TestSSHGetAuthorizedKeysAssociatesComments(t *testing.T) {
	dir := t.TempDir()
	origKeysPath := SSHAuthorizedKeysPath
	SSHAuthorizedKeysPath = filepath.Join(dir, "authorized_keys")
	t.Cleanup(func() { SSHAuthorizedKeysPath = origKeysPath })

	content := "# laptop key\nssh-ed25519 AAAA1 a@b\nssh-rsa AAAA2 c@d\n# trailing orphan comment\n"
	os.WriteFile(SSHAuthorizedKeysPath, []byte(content), 0644)

	keys := sshGetAuthorizedKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %+v", len(keys), keys)
	}
	if keys[0].Comment != "laptop key" || keys[0].Key != "ssh-ed25519 AAAA1 a@b" {
		t.Fatalf("expected first key with its comment, got %+v", keys[0])
	}
	if keys[1].Comment != "" || keys[1].Key != "ssh-rsa AAAA2 c@d" {
		t.Fatalf("expected second key with no comment, got %+v", keys[1])
	}
}

func TestServeSSHFullConfigGETReturnsJSON(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.Get(srv.URL + "/server/ssh/config")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Port 22") {
		t.Fatalf("expected full config text in JSON, got %s", truncate(string(body)))
	}
}

func TestServeSSHFullConfigPOSTUpdatesAndRedirects(t *testing.T) {
	s := &SSH{}
	srv, client := newSSHTestServer(t, s)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }
	os.WriteFile(SSHDConfigPath, []byte(testSSHDConfig), 0644)

	resp, err := client.PostForm(srv.URL+"/server/ssh/config", url.Values{"config": {"Port 9999\n"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	raw, _ := os.ReadFile(SSHDConfigPath)
	if string(raw) != "Port 9999\n" {
		t.Fatalf("expected the config fully replaced, got:\n%s", string(raw))
	}
}
