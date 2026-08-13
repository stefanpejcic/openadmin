package auth

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
)

func withScratchAdminDB(t *testing.T) *admindb.DB {
	t.Helper()
	dir := t.TempDir()
	orig := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = orig })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func TestLoginLogoutRoundTripOverHTTP(t *testing.T) {
	db := withScratchAdminDB(t)
	if err := db.CreateUser("admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	u, err := db.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager("test-secret", false)

	mux := http.NewServeMux()
	mux.HandleFunc("/login-as-admin", func(w http.ResponseWriter, r *http.Request) {
		LoginUser(w, r, mgr, u, "203.0.113.1")
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if cu := CurrentUser(r); cu != nil {
			io.WriteString(w, cu.Username)
		} else {
			io.WriteString(w, "anonymous")
		}
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		LogoutUser(w, r, mgr)
	})

	srv := httptest.NewServer(WithUserLoader(mgr, db)(mux))
	defer srv.Close()

	client := newClient(t)

	resp, _ := client.Get(srv.URL + "/whoami")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "anonymous" {
		t.Fatalf("expected anonymous before login, got %q", body)
	}

	client.Get(srv.URL + "/login-as-admin")

	resp, _ = client.Get(srv.URL + "/whoami")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "admin" {
		t.Fatalf("expected admin after login, got %q", body)
	}

	client.Get(srv.URL + "/logout")

	resp, _ = client.Get(srv.URL + "/whoami")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "anonymous" {
		t.Fatalf("expected anonymous after logout, got %q", body)
	}
}

// TestLoginUserSucceedsWithUndecodableExistingCookie covers a browser
// arriving with a stale/corrupted OPENADMIN cookie (e.g. left over from
// before a secret rotation, or tampered with). gorilla/sessions' Get()
// still hands back a fresh, usable session in that case alongside a
// decode error; LoginUser must not treat that error as fatal, or every
// such login would 500 instead of just overwriting the bad cookie.
func TestLoginUserSucceedsWithUndecodableExistingCookie(t *testing.T) {
	db := withScratchAdminDB(t)
	if err := db.CreateUser("admin", "hash", "admin"); err != nil {
		t.Fatal(err)
	}
	u, err := db.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager("test-secret", false)

	mux := http.NewServeMux()
	mux.HandleFunc("/login-as-admin", func(w http.ResponseWriter, r *http.Request) {
		if err := LoginUser(w, r, mgr, u, "203.0.113.1"); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if cu := CurrentUser(r); cu != nil {
			io.WriteString(w, cu.Username)
		} else {
			io.WriteString(w, "anonymous")
		}
	})

	srv := httptest.NewServer(WithUserLoader(mgr, db)(mux))
	defer srv.Close()

	client := newClient(t)
	srvURL, _ := url.Parse(srv.URL)
	client.Jar.SetCookies(srvURL, []*http.Cookie{{Name: SessionCookieName, Value: "not-a-valid-encoded-session"}})

	resp, err := client.Get(srv.URL + "/login-as-admin")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from login with a bad existing cookie, got %d", resp.StatusCode)
	}

	resp, _ = client.Get(srv.URL + "/whoami")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "admin" {
		t.Fatalf("expected admin after login despite bad prior cookie, got %q", body)
	}
}

func TestFlashRoundTripAcrossRequests(t *testing.T) {
	mgr := NewManager("test-secret", false)

	mux := http.NewServeMux()
	mux.HandleFunc("/set-flash", func(w http.ResponseWriter, r *http.Request) {
		AddFlash(w, r, mgr, "Login failed. Please check your credentials.", "danger")
	})
	mux.HandleFunc("/render", func(w http.ResponseWriter, r *http.Request) {
		flashes := PopFlashes(w, r, mgr)
		for _, f := range flashes {
			io.WriteString(w, f.Category+":"+f.Message+"\n")
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newClient(t)

	client.Get(srv.URL + "/set-flash")

	resp, _ := client.Get(srv.URL + "/render")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "danger:Login failed. Please check your credentials.") {
		t.Fatalf("expected flash to render, got %q", body)
	}

	// flashes are consumed on read -- a second render should be empty
	resp, _ = client.Get(srv.URL + "/render")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "" {
		t.Fatalf("expected flash to be consumed after first read, got %q", body)
	}
}

func TestRequireLoginRedirectsAnonymousWithNext(t *testing.T) {
	mgr := NewManager("test-secret", false)
	handler := RequireLogin(mgr, Options{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "should not reach here")
	})

	req := httptest.NewRequest(http.MethodGet, "/dashboard/dismiss/usage_graphs", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("expected redirect to /login?next=..., got %q", loc)
	}
	if !strings.Contains(loc, url.QueryEscape("/dashboard/dismiss/usage_graphs")) {
		t.Fatalf("expected next to carry the original path, got %q", loc)
	}
}

func TestRequireAdminBlocksReseller(t *testing.T) {
	db := withScratchAdminDB(t)
	db.CreateUser("bob", "hash", "reseller")
	u, _ := db.UserByUsername("bob")

	mgr := NewManager("test-secret", false)
	admin := RequireAdmin(mgr, Options{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "admin content")
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/login-as-reseller", func(w http.ResponseWriter, r *http.Request) {
		LoginUser(w, r, mgr, u, "203.0.113.1")
	})
	mux.HandleFunc("/admin-only", admin)

	srv := httptest.NewServer(WithUserLoader(mgr, db)(mux))
	defer srv.Close()
	client := newClient(t)

	client.Get(srv.URL + "/login-as-reseller")
	resp, _ := client.Get(srv.URL + "/admin-only")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for reseller on admin-only route, got %d", resp.StatusCode)
	}
}

func TestValidateSessionIPMiddlewareLogsOutOnMismatch(t *testing.T) {
	db := withScratchAdminDB(t)
	db.CreateUser("admin", "hash", "admin")
	u, _ := db.UserByUsername("admin")

	mgr := NewManager("test-secret", false)
	opts := Options{ValidateSessionIP: true}

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cu := CurrentUser(r); cu != nil {
			io.WriteString(w, cu.Username)
		} else {
			io.WriteString(w, "anonymous")
		}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/login-as-admin", func(w http.ResponseWriter, r *http.Request) {
		// login from a different IP than the test client's loopback
		// RemoteAddr so the very next request already mismatches, mirroring
		// a session hijacking / IP-change scenario.
		LoginUser(w, r, mgr, u, "198.51.100.9")
	})
	mux.Handle("/protected", protected)

	handler := WithUserLoader(mgr, db)(ValidateSessionIPMiddleware(mgr, opts)(mux))
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := newClient(t)

	client.Get(srv.URL + "/login-as-admin")

	resp, _ := client.Get(srv.URL + "/protected")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// the loopback test client's real IP never matches the 198.51.100.9
	// the session was pinned to, so this request should have been logged
	// out before reaching the protected handler
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("expected redirect chain to end at /login, ended at %q (body %q)", resp.Request.URL.Path, body)
	}
}

func TestPerIPLimiter(t *testing.T) {
	lim := NewPerIPLimiter(5, 1) // 1 burst, refills slowly -- second immediate request must fail
	if !lim.Allow("1.2.3.4") {
		t.Fatal("expected first request to be allowed")
	}
	if lim.Allow("1.2.3.4") {
		t.Fatal("expected second immediate request from the same IP to be rate limited")
	}
	if !lim.Allow("5.6.7.8") {
		t.Fatal("expected a different IP to have its own independent bucket")
	}
}
