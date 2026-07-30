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
	"time"

	"github.com/pquerna/otp/totp"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newTestLogin(t *testing.T) (*Login, *admindb.DB) {
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

	sessions := auth.NewManager("test-secret", false)
	limiter := auth.NewPerIPLimiter(1000, 1000) // effectively unlimited unless a test overrides it
	login := NewLogin(db, sessions, limiter, 20)
	return login, db
}

func newTestServer(t *testing.T, login *Login) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			login.HandleLoginSubmit(w, r)
		} else {
			login.ServeLoginPage(w, r)
		}
	})
	mux.HandleFunc("/login/2fa", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			login.HandleTwoFASubmit(w, r)
		} else {
			login.ServeTwoFAPage(w, r)
		}
	})
	mux.HandleFunc("/logout", login.HandleLogout)
	mux.HandleFunc("/api/tour/complete", login.HandleTourComplete)
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if cu := auth.CurrentUser(r); cu != nil {
			io.WriteString(w, cu.Username)
		} else {
			io.WriteString(w, "anonymous")
		}
	})

	handler := auth.WithUserLoader(login.Sessions, login.DB)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // follow redirects so cookies/state carry over, like a browser
		},
	}
	return srv, client
}

func TestLoginPageRenders(t *testing.T) {
	login, _ := newTestLogin(t)
	srv, client := newTestServer(t, login)

	resp, err := client.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Sign in") {
		t.Fatalf("expected login page content, got %q", truncate(string(body)))
	}
}

func TestLoginSuccessWithoutTwoFA(t *testing.T) {
	login, db := newTestLogin(t)
	hash, err := auth.GeneratePasswordHash("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	srv, client := newTestServer(t, login)

	resp, err := client.PostForm(srv.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"s3cret-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// followed the redirect chain -- should have ended up authenticated
	who, _ := client.Get(srv.URL + "/whoami")
	body, _ := io.ReadAll(who.Body)
	who.Body.Close()
	if string(body) != "admin" {
		t.Fatalf("expected to be logged in as admin, got %q", body)
	}
}

// TestLoginRedirectsToOnboardingUntilDismissed guards the new default
// post-login landing page: a plain login (no "next") should land on
// /onboarding until it's been dismissed, then fall back to /dashboard.
func TestLoginRedirectsToOnboardingUntilDismissed(t *testing.T) {
	login, db := newTestLogin(t)
	hash, err := auth.GeneratePasswordHash("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("admin", hash, "admin"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	origPath := ChromeQuickStartSkipFilePath
	ChromeQuickStartSkipFilePath = filepath.Join(dir, "quick_start.dismissed")
	t.Cleanup(func() { ChromeQuickStartSkipFilePath = origPath })

	srv, client := newTestServer(t, login)
	noFollow := &http.Client{
		Jar: client.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noFollow.PostForm(srv.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"s3cret-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/onboarding" {
		t.Fatalf("expected redirect to /onboarding before dismissal, got %q", loc)
	}

	client.Get(srv.URL + "/logout")
	os.WriteFile(ChromeQuickStartSkipFilePath, nil, 0644)

	resp2, err := noFollow.PostForm(srv.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"s3cret-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if loc := resp2.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard after dismissal, got %q", loc)
	}
}

func TestHandleTourCompleteCreatesSkipFile(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("s3cret-password")
	db.CreateUser("admin", hash, "admin")

	dir := t.TempDir()
	origPath := ChromeTourSkipFilePath
	ChromeTourSkipFilePath = filepath.Join(dir, "tour.skip")
	t.Cleanup(func() { ChromeTourSkipFilePath = origPath })

	srv, client := newTestServer(t, login)
	client.PostForm(srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"s3cret-password"}})

	resp, err := client.Post(srv.URL+"/api/tour/complete", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("expected 200 {\"ok\":true}, got %d %q", resp.StatusCode, body)
	}
	if _, err := os.Stat(ChromeTourSkipFilePath); err != nil {
		t.Fatalf("expected tour skip file to be created, err=%v", err)
	}

	// A second call with the file already present should still succeed.
	resp2, err := client.Post(srv.URL+"/api/tour/complete", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on second call too, got %d", resp2.StatusCode)
	}
}

func TestLoginWrongPasswordShowsFlash(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("correct-password")
	db.CreateUser("admin", hash, "admin")

	srv, client := newTestServer(t, login)

	resp, err := client.PostForm(srv.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"wrong-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Login failed. Please check your credentials.") {
		t.Fatalf("expected failure flash in rendered page, got %q", truncate(string(body)))
	}

	who, _ := client.Get(srv.URL + "/whoami")
	whoBody, _ := io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "anonymous" {
		t.Fatal("expected to remain anonymous after a wrong password")
	}
}

func TestLoginRepeatedFailuresDontAccumulateFlashes(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("correct-password")
	db.CreateUser("admin", hash, "admin")

	srv, client := newTestServer(t, login)

	for i := 0; i < 3; i++ {
		resp, err := client.PostForm(srv.URL+"/login", url.Values{
			"username": {"admin"},
			"password": {"wrong-password"},
		})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		count := strings.Count(string(body), "Login failed. Please check your credentials.")
		if count != 1 {
			t.Fatalf("attempt %d: expected exactly 1 flash message, got %d in body:\n%s", i+1, count, truncate(string(body)))
		}
	}
}

func TestLoginInactiveUserBlocked(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("disabled", hash, "admin")
	if err := db.SetActive("disabled", false); err != nil {
		t.Fatal(err)
	}

	srv, client := newTestServer(t, login)
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {"disabled"}, "password": {"pw"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Login failed. User is not active.") {
		t.Fatalf("expected inactive-user flash, got %q", truncate(string(body)))
	}
}

func TestLoginWithTwoFAFlow(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("pw123456")
	db.CreateUser("2fauser", hash, "admin")

	const secret = "JBSWY3DPEHPK3PXP" // classic RFC4648 base32 test secret
	if err := db.SetTOTP("2fauser", secret, true); err != nil {
		t.Fatal(err)
	}

	srv, client := newTestServer(t, login)

	// step 1: password-only POST should land on the 2FA page, not log in yet
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {"2fauser"}, "password": {"pw123456"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Two-Factor Authentication") {
		t.Fatalf("expected to land on the 2FA page, got %q", truncate(string(body)))
	}

	who, _ := client.Get(srv.URL + "/whoami")
	whoBody, _ := io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "anonymous" {
		t.Fatal("expected not to be logged in until 2FA is verified")
	}

	// step 2: wrong code should not log in
	resp, err = client.PostForm(srv.URL+"/login/2fa", url.Values{"code": {"000000"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	who, _ = client.Get(srv.URL + "/whoami")
	whoBody, _ = io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "anonymous" {
		t.Fatal("expected wrong 2FA code to not log in")
	}

	// step 3: correct code logs in
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.PostForm(srv.URL+"/login/2fa", url.Values{"code": {code}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	who, _ = client.Get(srv.URL + "/whoami")
	whoBody, _ = io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "2fauser" {
		t.Fatalf("expected correct 2FA code to log in, got %q", whoBody)
	}
}

func TestLogout(t *testing.T) {
	login, db := newTestLogin(t)
	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("admin", hash, "admin")

	srv, client := newTestServer(t, login)
	client.PostForm(srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"pw"}})

	who, _ := client.Get(srv.URL + "/whoami")
	whoBody, _ := io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "admin" {
		t.Fatal("expected to be logged in before testing logout")
	}

	client.Get(srv.URL + "/logout")

	who, _ = client.Get(srv.URL + "/whoami")
	whoBody, _ = io.ReadAll(who.Body)
	who.Body.Close()
	if string(whoBody) != "anonymous" {
		t.Fatal("expected to be logged out")
	}
}

func TestIsWebauthnCapableHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":         true,
		"panel.example.com": true,
		"192.168.1.10":      false,
		"10.0.0.5":          false,
	}
	for host, want := range cases {
		if got := isWebauthnCapableHost(host); got != want {
			t.Errorf("isWebauthnCapableHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestWebauthnRPIDAndOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login/passkey/begin", nil)
	req.Host = "panel.example.com:443"

	if got := webauthnRPID(req); got != "panel.example.com" {
		t.Fatalf("expected rp id without port, got %q", got)
	}

	req.Header.Set("X-Forwarded-Proto", "https")
	if got := webauthnOrigin(req); got != "https://panel.example.com:443" {
		t.Fatalf("unexpected origin: %q", got)
	}
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "...(truncated)"
	}
	return s
}
