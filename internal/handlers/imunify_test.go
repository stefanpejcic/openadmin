package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newImunifyTestServer(t *testing.T, im *Imunify) (*httptest.Server, *http.Client) {
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
	im.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /security/imunify/", im.ServeImunifyGUI)
	mux.HandleFunc("GET /security/imunify/assets/static/{filename...}", im.ServeImunifyStatic)
	mux.HandleFunc("GET /imav/{path...}", im.ServeImunifyPHP)
	mux.HandleFunc("POST /imav/{path...}", im.ServeImunifyPHP)
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

func withStubbedImunifyRunners(t *testing.T, available bool, portOpen bool, token string, tokenOK bool) {
	t.Helper()
	origAvailable, origPortOpen, origStart, origToken := imunifyCommandAvailableRun, imunifyIsPortOpenRun, imunifyStartDetachedRun, imunifyGetTokenRun
	imunifyCommandAvailableRun = func(string) bool { return available }
	imunifyIsPortOpenRun = func(string, int) bool { return portOpen }
	imunifyStartDetachedRun = func() {}
	imunifyGetTokenRun = func() (string, bool) { return token, tokenOK }
	t.Cleanup(func() {
		imunifyCommandAvailableRun = origAvailable
		imunifyIsPortOpenRun = origPortOpen
		imunifyStartDetachedRun = origStart
		imunifyGetTokenRun = origToken
	})
}

func TestServeImunifyGUINotInstalled(t *testing.T) {
	withStubbedImunifyRunners(t, false, false, "", false)
	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Not Configured", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImunifyGUINotRunningStartsDetached(t *testing.T) {
	started := false
	withStubbedImunifyRunners(t, true, false, "", false)
	imunifyStartDetachedRun = func() { started = true }
	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Not Running", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
	if !started {
		t.Fatal("expected start_imunify_detached to be invoked")
	}
}

func TestServeImunifyGUIAutostartsAndServesGUIOnceRunning(t *testing.T) {
	started := false
	portOpen := false
	origAvailable, origPortOpen, origStart, origToken := imunifyCommandAvailableRun, imunifyIsPortOpenRun, imunifyStartDetachedRun, imunifyGetTokenRun
	imunifyCommandAvailableRun = func(string) bool { return true }
	imunifyIsPortOpenRun = func(string, int) bool { return portOpen }
	imunifyStartDetachedRun = func() { started = true; portOpen = true }
	imunifyGetTokenRun = func() (string, bool) { return "abc123", true }
	t.Cleanup(func() {
		imunifyCommandAvailableRun = origAvailable
		imunifyIsPortOpenRun = origPortOpen
		imunifyStartDetachedRun = origStart
		imunifyGetTokenRun = origToken
	})

	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !started {
		t.Fatal("expected start_imunify_detached to be invoked")
	}
	if strings.Contains(got, "Not Running") {
		t.Fatalf("expected the real GUI once the port comes up after autostart, got %s", truncate(got))
	}
	for _, want := range []string{"token=abc123", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImunifyGUITokenSuccess(t *testing.T) {
	withStubbedImunifyRunners(t, true, true, "abc123", true)
	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"token=abc123", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImunifyGUITokenFailureFlashesWarning(t *testing.T) {
	withStubbedImunifyRunners(t, true, true, "", false)
	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Failed to generate token", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q, got %s", want, truncate(got))
		}
	}
}

func TestServeImunifyStaticServesAllowedFile(t *testing.T) {
	dir := t.TempDir()
	origDir := imunifyStaticDir
	imunifyStaticDir = dir
	t.Cleanup(func() { imunifyStaticDir = origDir })

	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0644); err != nil {
		t.Fatal(err)
	}

	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/assets/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if string(body) != "console.log(1)" {
		t.Fatalf("expected file contents served, got %s", truncate(string(body)))
	}
}

func TestServeImunifyStaticBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	origDir := imunifyStaticDir
	imunifyStaticDir = dir
	t.Cleanup(func() { imunifyStaticDir = origDir })

	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/security/imunify/assets/static/../../../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a path-traversal attempt, got %d", resp.StatusCode)
	}
}

func TestServeImunifyPHPProxiesRequest(t *testing.T) {
	withStubbedImunifyRunners(t, true, true, "", false)
	origProxy := imunifyProxyRun
	var capturedPath, capturedMethod string
	imunifyProxyRun = func(r *http.Request) (int, http.Header, []byte, error) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		h := http.Header{}
		h.Set("Content-Type", "text/html")
		return http.StatusOK, h, []byte("<html>proxied</html>"), nil
	}
	t.Cleanup(func() { imunifyProxyRun = origProxy })

	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/imav/some/page.php")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if string(body) != "<html>proxied</html>" {
		t.Fatalf("expected proxied body, got %s", truncate(string(body)))
	}
	if capturedMethod != "GET" || capturedPath != "/imav/some/page.php" {
		t.Fatalf("expected the proxy to see the original method/path, got %s %s", capturedMethod, capturedPath)
	}
}

func TestServeImunifyPHPAutostartsAndProxiesOnceRunning(t *testing.T) {
	started := false
	portOpen := false
	origAvailable, origPortOpen, origStart, origToken := imunifyCommandAvailableRun, imunifyIsPortOpenRun, imunifyStartDetachedRun, imunifyGetTokenRun
	imunifyCommandAvailableRun = func(string) bool { return true }
	imunifyIsPortOpenRun = func(string, int) bool { return portOpen }
	imunifyStartDetachedRun = func() { started = true; portOpen = true }
	imunifyGetTokenRun = func() (string, bool) { return "", false }
	t.Cleanup(func() {
		imunifyCommandAvailableRun = origAvailable
		imunifyIsPortOpenRun = origPortOpen
		imunifyStartDetachedRun = origStart
		imunifyGetTokenRun = origToken
	})

	origProxy := imunifyProxyRun
	proxyCalled := false
	imunifyProxyRun = func(r *http.Request) (int, http.Header, []byte, error) {
		proxyCalled = true
		return http.StatusOK, http.Header{}, []byte("<html>proxied</html>"), nil
	}
	t.Cleanup(func() { imunifyProxyRun = origProxy })

	im := &Imunify{}
	srv, client := newImunifyTestServer(t, im)

	resp, err := client.Get(srv.URL + "/imav/some/page.php")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !started {
		t.Fatal("expected start_imunify_detached to be invoked")
	}
	if !proxyCalled {
		t.Fatalf("expected the request to be proxied once the port comes up after autostart, got %s", truncate(string(body)))
	}
}

func TestServeImunifyPHPForbidsEscapingRoot(t *testing.T) {
	// net/http's ServeMux already 301-redirects/cleans ".." out of any
	// incoming URL path before a handler is ever reached, so a real HTTP
	// client can't actually exercise this branch end-to-end. It's called
	// directly here (bypassing the mux) to confirm the defense-in-depth
	// check in ServeImunifyPHP itself is still correct on its own terms.
	withStubbedImunifyRunners(t, true, true, "", false)
	called := false
	origProxy := imunifyProxyRun
	imunifyProxyRun = func(r *http.Request) (int, http.Header, []byte, error) {
		called = true
		return http.StatusOK, http.Header{}, nil, nil
	}
	t.Cleanup(func() { imunifyProxyRun = origProxy })

	im := &Imunify{}
	req := httptest.NewRequest(http.MethodGet, "/imav/../../../etc/passwd", nil)
	req.SetPathValue("path", "../../../etc/passwd")
	w := httptest.NewRecorder()
	im.ServeImunifyPHP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, truncate(w.Body.String()))
	}
	if called {
		t.Fatal("expected the proxy to never be reached for a forbidden path")
	}
}
