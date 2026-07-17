package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newTwoFATestServer(t *testing.T) (*httptest.Server, *http.Client, *admindb.DB) {
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

	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("alice", hash, "user")
	alice, err := db.UserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	tw := &TwoFA{DB: db, Sessions: sessions}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /security/2fa", tw.ServeSettings)
	mux.HandleFunc("POST /security/2fa/enable", tw.HandleEnable)
	mux.HandleFunc("POST /security/2fa/disable", tw.HandleDisable)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, alice, "203.0.113.1")
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
	return srv, client, db
}

var secretRe = regexp.MustCompile(`<code class="font-mono">([A-Z2-7]+)</code>`)

func TestTwoFASettingsGeneratesAndReusesPendingSecret(t *testing.T) {
	srv, client, _ := newTwoFATestServer(t)

	resp, err := client.Get(srv.URL + "/security/2fa")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m1 := secretRe.FindStringSubmatch(string(body1))
	if m1 == nil {
		t.Fatalf("expected a QR secret in the page, got %s", truncate(string(body1)))
	}

	// a second GET before enabling must reuse the same pending secret, not
	// mint a new one that would invalidate an in-progress authenticator-app
	// scan
	resp, err = client.Get(srv.URL + "/security/2fa")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m2 := secretRe.FindStringSubmatch(string(body2))
	if m2 == nil || m1[1] != m2[1] {
		t.Fatalf("expected the pending secret to be reused across requests, got %v vs %v", m1, m2)
	}

	if !strings.Contains(string(body1), "data:image/png;base64,") {
		t.Fatal("expected a QR code data URI to be rendered")
	}
}

func TestTwoFAEnableWithCorrectCode(t *testing.T) {
	srv, client, db := newTwoFATestServer(t)

	resp, _ := client.Get(srv.URL + "/security/2fa")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := secretRe.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("expected a secret in the page, got %s", truncate(string(body)))
	}
	secret := m[1]

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	resp, err = client.PostForm(srv.URL+"/security/2fa/enable", url.Values{"code": {code}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Two-factor authentication has been enabled.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	u, err := db.UserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !u.TOTPEnabled || u.TOTPSecret.String != secret {
		t.Fatalf("expected totp_enabled=true and matching secret, got %+v", u)
	}
}

func TestTwoFAEnableWithWrongCodeFails(t *testing.T) {
	srv, client, db := newTwoFATestServer(t)
	client.Get(srv.URL + "/security/2fa") // establish the pending secret

	resp, err := client.PostForm(srv.URL+"/security/2fa/enable", url.Values{"code": {"000000"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid authentication code") {
		t.Fatalf("expected failure flash, got %s", truncate(string(body)))
	}

	u, _ := db.UserByUsername("alice")
	if u.TOTPEnabled {
		t.Fatal("expected totp_enabled to remain false after a wrong code")
	}
}

func TestTwoFADisableRequiresCorrectPassword(t *testing.T) {
	srv, client, db := newTwoFATestServer(t)
	db.SetTOTP("alice", "JBSWY3DPEHPK3PXP", true)

	resp, err := client.PostForm(srv.URL+"/security/2fa/disable", url.Values{"password": {"wrong-password"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Incorrect password") {
		t.Fatalf("expected incorrect-password flash, got %s", truncate(string(body)))
	}
	u, _ := db.UserByUsername("alice")
	if !u.TOTPEnabled {
		t.Fatal("expected totp_enabled to remain true after a wrong password")
	}

	resp, err = client.PostForm(srv.URL+"/security/2fa/disable", url.Values{"password": {"pw123456"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Two-factor authentication has been disabled.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
	u, _ = db.UserByUsername("alice")
	if u.TOTPEnabled {
		t.Fatal("expected totp_enabled to be false after correct password")
	}
}
