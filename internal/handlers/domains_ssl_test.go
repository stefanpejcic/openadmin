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

// stubDomainWhoOwnsContext installs a domainWhoOwnsContextRun stub, so
// tests never shell out to a real opencli binary to resolve a domain's
// owner/context.
func stubDomainWhoOwnsContext(t *testing.T, out string, err error) {
	t.Helper()
	orig := domainWhoOwnsContextRun
	domainWhoOwnsContextRun = func(domain string) (string, error) { return out, err }
	t.Cleanup(func() { domainWhoOwnsContextRun = orig })
}

// withScratchSSLDataHomeRoot redirects installCustomSSL's "/home" base to
// a scratch directory, so tests never touch a real /home/<user>/... path.
func withScratchSSLDataHomeRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := sslCustomDataHomeRoot
	sslCustomDataHomeRoot = dir
	t.Cleanup(func() { sslCustomDataHomeRoot = orig })
	return dir
}

// stubOpencliSSL installs an opencliSSLRun stub keyed by the joined args
// string, so tests never shell out to a real opencli binary.
func stubOpencliSSL(t *testing.T, responses map[string]struct {
	stdout, stderr string
	exitCode       int
	err            error
}) {
	t.Helper()
	orig := opencliSSLRun
	opencliSSLRun = func(args ...string) (string, string, int, error) {
		key := strings.Join(args, " ")
		if resp, ok := responses[key]; ok {
			return resp.stdout, resp.stderr, resp.exitCode, resp.err
		}
		return "", "not stubbed: " + key, 1, nil
	}
	t.Cleanup(func() { opencliSSLRun = orig })
}

func newSSLPageTestServer(t *testing.T, h *SSLPage) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

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
	h.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /domains/ssl/{domain_name}", h.ServeSSL)
	mux.HandleFunc("POST /domains/ssl/{domain_name}", h.ServeSSL)
	// Stub for the redirect target of the invalid-domain flash
	// (url_for('domains')); not the real domains list page.
	mux.HandleFunc("GET /domains", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			w.Write([]byte(f.Message))
		}
	})
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	// RecoverMiddleware is what turns the deliberate panic() calls in
	// domains_ssl.go into the same 500 response production would return --
	// exercised below.
	handler := auth.WithUserLoader(sessions, db)(RecoverMiddleware(mux))
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

func TestSSLPageInvalidDomainRedirects(t *testing.T) {
	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/not-a-domain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains" {
		t.Fatalf("expected redirect to /domains, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Invalid domain name format.") {
		t.Fatalf("expected invalid-domain flash, got %s", truncate(string(body)))
	}
}

func TestSSLPageGetRendersWhenStatusSucceeds(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AutoSSL\n", exitCode: 0},
		"example.com info":   {stdout: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Auto SSL", "BEGIN CERTIFICATE", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

// TestSSLPageGetCrashesWhenStatusCheckFails locks in a genuine bug (see
// domains_ssl.go's file header): `keys` is only ever assigned when the
// status check succeeds, so rendering the page whenever it doesn't hits a
// referenced-before-assignment crash -- an unhandled exception. This is
// reproduced deliberately via panic()+RecoverMiddleware rather than
// silently rendering a graceful empty state.
func TestSSLPageGetCrashesWhenStatusCheckFails(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stderr: "no certificate configured", exitCode: 1},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.Get(srv.URL + "/domains/ssl/example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (reproducing the referenced-before-assignment crash), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

func TestSSLPagePostLogsActionReturnsJSON(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com logs 1000": {stdout: `{"msg":"obtained certificate"}` + "\n", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{"action": {"logs"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "obtained certificate") {
		t.Fatalf("expected logs JSON body, got %s", truncate(string(body)))
	}
}

// TestSSLPagePostAutosslCrashes locks in the other genuine bug: every POST
// with action=autossl ends by trying to redirect to a route name that
// doesn't exist anywhere in this app, so it always 500s.
func TestSSLPagePostAutosslCrashes(t *testing.T) {
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com auto": {stdout: "AutoSSL enabled", exitCode: 0},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{"action": {"autossl"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (reproducing the url-build crash), got %d: %s", resp.StatusCode, truncate(string(body)))
	}
}

// TestSSLPagePostCustomRejectsMissingFields covers the new pasted-cert
// validation, replacing the old public_path/private_path form.
func TestSSLPagePostCustomRejectsMissingFields(t *testing.T) {
	withScratchCaddyDomainsDir(t) // domain conf missing -> would try the Caddyfile fallback, but validation fails before that
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AutoSSL\n", exitCode: 0},
		"example.com info":   {exitCode: 1},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{"action": {"custom"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/domains/ssl/example.com" {
		t.Fatalf("expected redirect back to the SSL page, ended at %q", resp.Request.URL.Path)
	}
	if !strings.Contains(string(body), "Certificate and private key are required.") {
		t.Fatalf("expected validation flash, got %s", truncate(string(body)))
	}
}

func TestSSLPagePostCustomRejectsInvalidCertificate(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AutoSSL\n", exitCode: 0},
		"example.com info":   {exitCode: 1},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{
		"action":      {"custom"},
		"certificate": {"not a certificate"},
		"private_key": {"-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid certificate.") {
		t.Fatalf("expected invalid-certificate flash, got %s", truncate(string(body)))
	}
}

func TestSSLPagePostCustomRejectsInvalidPrivateKey(t *testing.T) {
	withScratchCaddyDomainsDir(t)
	stubOpencliSSL(t, map[string]struct {
		stdout, stderr string
		exitCode       int
		err            error
	}{
		"example.com status": {stdout: "AutoSSL\n", exitCode: 0},
		"example.com info":   {exitCode: 1},
	})

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{
		"action":      {"custom"},
		"certificate": {"-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"},
		"private_key": {"garbage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid private key.") {
		t.Fatalf("expected invalid-private-key flash, got %s", truncate(string(body)))
	}
}

// TestSSLPagePostCustomInstallsViaOpencliForOwnedDomain covers the
// opencli-managed path: a domain with a real (non-empty) per-domain conf
// file stages the pasted cert/key under the owner's html data volume and
// calls `opencli domains-ssl <domain> custom <cert> <key>`, exactly like
// before but with content instead of paths.
func TestSSLPagePostCustomInstallsViaOpencliForOwnedDomain(t *testing.T) {
	confDir := withScratchCaddyDomainsDir(t)
	if err := os.WriteFile(filepath.Join(confDir, "example.com.conf"), []byte("example.com {\n  reverse_proxy 127.0.0.1:1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dataRoot := withScratchSSLDataHomeRoot(t)
	stubDomainWhoOwnsContext(t, "testuser ctx1\n", nil)

	certPEM := "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"
	keyPEM := "-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----"
	expectedCertPath := filepath.Join(dataRoot, "ctx1", "docker-data/volumes", "ctx1_html_data", "_data", "example.com_tmp.crt")
	expectedKeyPath := filepath.Join(dataRoot, "ctx1", "docker-data/volumes", "ctx1_html_data", "_data", "example.com_tmp.key")

	var sawCertContent, sawKeyContent string
	orig := opencliSSLRun
	opencliSSLRun = func(args ...string) (string, string, int, error) {
		switch strings.Join(args, " ") {
		case "example.com custom /var/www/html/example.com_tmp.crt /var/www/html/example.com_tmp.key":
			b, _ := os.ReadFile(expectedCertPath)
			sawCertContent = string(b)
			b, _ = os.ReadFile(expectedKeyPath)
			sawKeyContent = string(b)
			return "Updated example.com to use custom SSL.", "", 0, nil
		case "example.com status":
			return "Custom SSL\n", "", 0, nil
		case "example.com info":
			return certPEM, "", 0, nil
		default:
			return "", "not stubbed: " + strings.Join(args, " "), 1, nil
		}
	}
	t.Cleanup(func() { opencliSSLRun = orig })

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/example.com", url.Values{
		"action":      {"custom"},
		"certificate": {certPEM},
		"private_key": {keyPEM},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Updated example.com to use custom SSL.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if sawCertContent != certPEM {
		t.Fatalf("expected opencli to see the pasted certificate content, got %q", sawCertContent)
	}
	if sawKeyContent != keyPEM {
		t.Fatalf("expected opencli to see the pasted private key content, got %q", sawKeyContent)
	}
	if _, err := os.Stat(expectedCertPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp cert file to be cleaned up, stat err = %v", err)
	}
	if _, err := os.Stat(expectedKeyPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp key file to be cleaned up, stat err = %v", err)
	}
}

// TestSSLPagePostCustomEditsCaddyfileForUnownedDomain covers the new
// fallback: a domain with no per-domain conf file (e.g. the panel's own
// hostname) never calls opencli at all -- it's staged straight into the
// main Caddyfile's own site block.
func TestSSLPagePostCustomEditsCaddyfileForUnownedDomain(t *testing.T) {
	withScratchCaddyDomainsDir(t) // left empty -> domainConfMissingOrEmpty("panel.example") is true

	caddyDir := t.TempDir()
	caddyfilePath := filepath.Join(caddyDir, "Caddyfile")
	original := "panel.example {\n  reverse_proxy localhost:2087\n}\n"
	if err := os.WriteFile(caddyfilePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	origCaddyfile := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = origCaddyfile })

	sslDir := filepath.Join(caddyDir, "ssl-custom")
	origSSLDir := sslCustomCaddySSLDir
	sslCustomCaddySSLDir = sslDir
	t.Cleanup(func() { sslCustomCaddySSLDir = origSSLDir })

	origValidate := caddyValidateRun
	caddyValidateRun = func() (string, string, int, error) { return "", "", 0, nil }
	t.Cleanup(func() { caddyValidateRun = origValidate })

	reloaded := false
	origReload := caddyReloadRun
	caddyReloadRun = func() error { reloaded = true; return nil }
	t.Cleanup(func() { caddyReloadRun = origReload })

	opencliCalled := false
	origOpencli := opencliSSLRun
	opencliSSLRun = func(args ...string) (string, string, int, error) {
		opencliCalled = true
		return "", "not stubbed: " + strings.Join(args, " "), 1, nil
	}
	t.Cleanup(func() { opencliSSLRun = origOpencli })

	certPEM := "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"
	keyPEM := "-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----"

	h := &SSLPage{}
	srv, client := newSSLPageTestServer(t, h)

	resp, err := client.PostForm(srv.URL+"/domains/ssl/panel.example", url.Values{
		"action":      {"custom"},
		"certificate": {certPEM},
		"private_key": {keyPEM},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Updated panel.example to use custom SSL.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	if opencliCalled {
		t.Fatal("expected opencli domains-ssl to never be called for a domain with no per-domain conf file")
	}
	if !reloaded {
		t.Fatal("expected caddy to be reloaded")
	}

	gotCert, err := os.ReadFile(filepath.Join(sslDir, "panel.example", "fullchain.pem"))
	if err != nil || string(gotCert) != certPEM {
		t.Fatalf("cert file mismatch: err=%v got=%q", err, gotCert)
	}

	newCaddyfile, err := os.ReadFile(caddyfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newCaddyfile), "tls "+filepath.Join(sslDir, "panel.example", "fullchain.pem")) {
		t.Fatalf("expected tls directive pointing at the new cert, got:\n%s", newCaddyfile)
	}

	// A follow-up GET (what the redirect above actually triggers) should
	// now report "Custom SSL" straight from the Caddyfile, still without
	// ever touching opencli.
	getResp, err := client.Get(srv.URL + "/domains/ssl/panel.example")
	if err != nil {
		t.Fatal(err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if !strings.Contains(string(getBody), "Custom SSL") {
		t.Fatalf("expected page to report Custom SSL, got %s", truncate(string(getBody)))
	}
}

func TestResolveSSLKeyPathAndContainmentCheck(t *testing.T) {
	inside := resolveSSLKeyPath("/var/www/html/site/cert.crt")
	if !isRelativeToSSLHomeDir(inside) {
		t.Fatalf("expected %q to be considered inside /var/www/html/", inside)
	}
	outsideLookalike := resolveSSLKeyPath("/var/www/html-evil/cert.crt")
	if isRelativeToSSLHomeDir(outsideLookalike) {
		t.Fatalf("expected %q to NOT be considered inside /var/www/html/ (prefix lookalike)", outsideLookalike)
	}
	traversal := resolveSSLKeyPath("/var/www/html/../../etc/passwd")
	if isRelativeToSSLHomeDir(traversal) {
		t.Fatalf("expected traversal path %q to be rejected", traversal)
	}
}
