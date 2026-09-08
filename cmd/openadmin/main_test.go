package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"html"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/websocket"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/bootstrap"
	"openadmin/internal/handlers"
	"openadmin/internal/server"
)

// TestNewHandlerEndToEnd exercises the real router+middleware chain from newHandler() against scratch deps, no root needed: login -> dashboard -> logout, CSRF on POST /login, and redirects for unauthenticated requests
func TestNewHandlerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	adminDB, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminDB.Close() })

	hash, err := auth.GeneratePasswordHash("integration-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := adminDB.CreateUser("admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(
		[]string{"user_count", "plan_count", "site_count", "domain_count"}).AddRow(1, 1, 1, 1))
	mock.ExpectQuery(`SELECT DISTINCT server`).WillReturnRows(sqlmock.NewRows([]string{"server"}))

	handler, err := newHandler(appDeps{
		AdminDB:           adminDB,
		MySQL:             mysqlDB,
		SecretKey:         "integration-test-secret",
		UseTLS:            false,
		LoginBlockLimit:   20,
		LoginRatePerMin:   1000,
		DemoMode:          false,
		ValidateSessionIP: false, // the test client's remote addr can vary across requests
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	// 1. unauthenticated request to the dashboard redirects to /login
	resp, err := client.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("expected unauthenticated /dashboard to redirect to /login, ended at %q", resp.Request.URL.Path)
	}
	resp.Body.Close()

	// 2. fetch the login page to obtain a real CSRF token
	resp, err = client.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	token := extractCSRFToken(t, string(body))

	// 3. POST without the CSRF token must be rejected (400, our own handle_csrf_error, not gorilla/csrf's default 403), proving the middleware is actually wired in
	resp, err = client.PostForm(srv.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"integration-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a POST /login missing its CSRF token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. POST with the token succeeds and logs in
	resp, err = client.PostForm(srv.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {"integration-test-password"},
		"csrf_token": {token},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	// a first-ever login lands on the onboarding wizard (see finalizeLogin in login.go), not the dashboard, since it hasn't been dismissed in this fresh test admindb
	if resp.Request.URL.Path != "/onboarding" {
		t.Fatalf("expected successful login to land on onboarding, ended at %q (body: %s)", resp.Request.URL.Path, truncateBody(string(body)))
	}
	if !strings.Contains(string(body), "Enable modules") {
		t.Fatalf("expected onboarding content, got %s", truncateBody(string(body)))
	}

	// 5. now authenticated, /dashboard resolves directly (no redirect)
	resp, err = client.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /dashboard, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. /json/system requires login too and returns real JSON
	resp, err = client.Get(srv.URL + "/json/system")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /json/system, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. logout, then dashboard redirects again
	client.Get(srv.URL + "/logout")
	resp, err = client.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("expected /dashboard to redirect to /login after logout, ended at %q", resp.Request.URL.Path)
	}
	resp.Body.Close()
}

// TestNewHandlerServesEmbeddedStaticAssets checks the real "/static/" and "/{filename}" routes in newHandler() actually reach the go:embed static package, not just GeneralStatic's own unit tests
func TestNewHandlerServesEmbeddedStaticAssets(t *testing.T) {
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origOverrideDir := handlers.GeneralOverrideDir
	handlers.GeneralOverrideDir = t.TempDir() // isolate from the real /usr/local/admin
	t.Cleanup(func() { handlers.GeneralOverrideDir = origOverrideDir })

	adminDB, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminDB.Close() })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	handler, err := newHandler(appDeps{
		AdminDB:           adminDB,
		MySQL:             mysqlDB,
		SecretKey:         "integration-test-secret",
		LoginBlockLimit:   20,
		LoginRatePerMin:   1000,
		ValidateSessionIP: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// a bundled asset under static/dist, served via the embedded FS behind "/static/"
	resp, err := http.Get(srv.URL + "/static/dist/output.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected /static/dist/output.css to be served from the embedded FS, got %d", resp.StatusCode)
	}

	// robots.txt has a bundled default and no admin override here, so it should come from the embedded FS via GeneralStatic
	resp, err = http.Get(srv.URL + "/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected /robots.txt to be served from the embedded default, got %d", resp.StatusCode)
	}

	// custom.css has no bundled default and nothing dropped in GeneralOverrideDir, so it must 404 not panic or 500
	resp, err = http.Get(srv.URL + "/custom.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /custom.css to 404 when absent, got %d", resp.StatusCode)
	}
}

func extractCSRFToken(t *testing.T, page string) string {
	t.Helper()
	const marker = `name="csrf_token" value="`
	idx := strings.Index(page, marker)
	if idx == -1 {
		t.Fatalf("could not find CSRF token field in login page HTML")
	}
	rest := page[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		t.Fatalf("malformed CSRF token field in login page HTML")
	}
	// html/template HTML-escapes attribute values (e.g. "+" -> "&#43;"), a real browser decodes that reading the DOM so the test must too, not submit the escaped literal back as the form value
	return html.UnescapeString(rest[:end])
}

// TestTerminalWebsocketOverRealTLSServer regression-tests a real prod bug: /ws/terminal 500'd with "response does not implement http.Hijacker" when a browser negotiated HTTP/2 over TLS, since gorilla/websocket needs to hijack the raw conn. server_test.go already checks Run() disables HTTP/2 at the ALPN level in isolation, but this exercises the real authenticated route through newHandler()+server.Run() with a real TLS client offering h2, which httptest.NewTLSServer wouldn't have caught
func TestTerminalWebsocketOverRealTLSServer(t *testing.T) {
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	adminDB, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminDB.Close() })

	hash, err := auth.GeneratePasswordHash("integration-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := adminDB.CreateUser("admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()

	handler, err := newHandler(appDeps{
		AdminDB:           adminDB,
		MySQL:             mysqlDB,
		SecretKey:         "integration-test-secret",
		UseTLS:            true,
		LoginBlockLimit:   20,
		LoginRatePerMin:   1000,
		ValidateSessionIP: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	origDisable := handlers.TerminalDisableFlagPath
	handlers.TerminalDisableFlagPath = filepath.Join(dir, "disable_openadmin_terminal_ui")
	t.Cleanup(func() { handlers.TerminalDisableFlagPath = origDisable })

	// wrap with the real AccessLogMiddleware like main() does, its statusRecorder must forward http.Hijacker or every websocket route 500s regardless of ALPN (a real bug this test caught)
	origAccessLog := bootstrap.AccessLogPath
	bootstrap.AccessLogPath = filepath.Join(dir, "access.log")
	t.Cleanup(func() { bootstrap.AccessLogPath = origAccessLog })
	accessLogger, err := server.NewAccessLogger()
	if err != nil {
		t.Fatal(err)
	}
	handler = server.AccessLogMiddleware(accessLogger, handler)

	certFile, keyFile := generateSelfSignedCertForTest(t, dir)

	const port = 18202
	logger := log.New(io.Discard, "", 0)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(server.Options{Port: port, CertFile: certFile, KeyFile: keyFile, Handler: handler, Logger: logger})
	}()

	baseURL := "https://127.0.0.1:18202"
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar

	// wait for the server to be listening, then fetch the login page for a CSRF token in the same request, a second ready-check GET would issue a different CSRF cookie and desync the token from what the client jar sends
	var body []byte
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get(baseURL + "/login")
		if err == nil {
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	token := extractCSRFToken(t, string(body))

	// gorilla/csrf also requires a matching Referer on state-changing HTTPS requests, a browser always sends one but bare http.Client PostForm doesn't, so set it manually or the check fails
	form := url.Values{
		"username":   {"admin"},
		"password":   {"integration-test-password"},
		"csrf_token": {token},
	}
	loginReq, err := http.NewRequest(http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Referer", baseURL+"/login")
	resp, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	loginBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path == "/login" {
		t.Fatalf("login failed, still on /login. status=%d body=%s", resp.StatusCode, truncateBody(string(loginBody)))
	}

	// the actual regression check: a real TLS WebSocket client offering h2/http1.1 via ALPN, hitting the real authenticated /ws/terminal route
	dialer := websocket.Dialer{
		Jar: jar,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2", "http/1.1"},
		},
	}
	conn, httpResp, err := dialer.Dial("wss://127.0.0.1:18202/ws/terminal", nil)
	if err != nil {
		status := ""
		if httpResp != nil {
			status = httpResp.Status
		}
		t.Fatalf("expected the authenticated websocket handshake to succeed, got err=%v status=%s", err, status)
	}
	conn.Close()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func generateSelfSignedCertForTest(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certOut, err := os.Create(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()
	keyOut, err := os.Create(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()
	return certFile, keyFile
}

func truncateBody(s string) string {
	if len(s) > 500 {
		return s[:500] + "...(truncated)"
	}
	return s
}
