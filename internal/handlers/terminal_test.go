package handlers

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newTerminalTestServer(t *testing.T, term *Terminal, role string) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	origFlag := TerminalDisableFlagPath
	TerminalDisableFlagPath = filepath.Join(dir, "disable_openadmin_terminal_ui")
	t.Cleanup(func() { TerminalDisableFlagPath = origFlag })

	origCwd := TerminalPTYCwd
	TerminalPTYCwd = dir
	t.Cleanup(func() { TerminalPTYCwd = origCwd })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	term.Sessions = sessions
	authOpts := auth.Options{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /terminal", auth.RequireAdmin(sessions, authOpts, term.ServeHostTerminalPage))
	mux.HandleFunc("GET /terminal/{username}/{container_name}", auth.RequireAdmin(sessions, authOpts, term.ServeUserTerminalPage))
	mux.HandleFunc("GET /ws/terminal", auth.RequireAdmin(sessions, authOpts, term.ServeHostTerminalWS))
	mux.HandleFunc("GET /ws/terminal/{username}/{container_name}", auth.RequireAdmin(sessions, authOpts, term.ServeUserTerminalWS))
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

func TestTerminalNormalizeShell(t *testing.T) {
	cases := map[string]string{"bash": "bash", "sh": "sh", "zsh": "sh", "": "sh", "; rm -rf /": "sh"}
	for in, want := range cases {
		if got := terminalNormalizeShell(in); got != want {
			t.Errorf("terminalNormalizeShell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServeHostTerminalPageAllowsNonResellerRoles(t *testing.T) {
	// admindb's "user" role is a lower-privilege staff/admin account, not
	// a hosting end-customer, so it must have full terminal access too --
	// only "reseller" is excluded, and that's auth.RequireAdmin's job.
	for _, role := range []string{"admin", "user"} {
		term := &Terminal{}
		srv, client := newTerminalTestServer(t, term, role)

		resp, err := client.Get(srv.URL + "/terminal")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("role %q: expected 200, got %d: %s", role, resp.StatusCode, truncate(string(body)))
		}
		// "Server" is the breadcrumb group label for /terminal's sidebar
		// section (Server > Terminal) -- a past regression let this fall
		// through to a bare "Web Terminal" crumb with no group prefix.
		for _, want := range []string{"Web Terminal", "id=\"terminal\"", "</html>", ">Server<"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("role %q: expected body to contain %q, got %s", role, want, truncate(string(body)))
			}
		}
	}
}

func TestTerminalPageSelfHostsAssetsAndSetsCSP(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	resp, err := client.Get(srv.URL + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self'", "frame-ancestors 'self'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP header missing %q, got %q", want, csp)
		}
	}

	page := string(body)
	if strings.Contains(page, "cdn.jsdelivr.net") || strings.Contains(page, "cdnjs.cloudflare.com") {
		t.Fatalf("terminal page still references a CDN, it should load everything from /static")
	}
	for _, want := range []string{"/static/vendor/xterm/xterm.js", "/static/vendor/xterm/xterm.css", "/static/vendor/xterm/addon-fit.min.js"} {
		if !strings.Contains(page, want) {
			t.Fatalf("terminal page should load %q, got %s", want, truncate(page))
		}
	}
}

func TestServeHostTerminalPageBlocksReseller(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "reseller")

	resp, err := client.Get(srv.URL + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller account, got %d", resp.StatusCode)
	}
}

func TestServeHostTerminalPageDisabledFlag(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")
	writeDisableFlag(t)

	resp, err := client.Get(srv.URL + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when the disable flag is present, got %d", resp.StatusCode)
	}
}

func writeDisableFlag(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(TerminalDisableFlagPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestServeUserTerminalPageAllowsNonResellerRoles(t *testing.T) {
	for _, role := range []string{"admin", "user"} {
		term := &Terminal{}
		srv, client := newTerminalTestServer(t, term, role)

		resp, err := client.Get(srv.URL + "/terminal/bob/bobs_container")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("role %q: expected 200, got %d", role, resp.StatusCode)
		}
		for _, want := range []string{"bobs_container", "Docker Terminal", "</html>"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("role %q: expected body to contain %q, got %s", role, want, truncate(string(body)))
			}
		}
	}
}

func TestServeUserTerminalPageBlocksReseller(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "reseller")

	resp, err := client.Get(srv.URL + "/terminal/bob/bobs_container")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a reseller account, got %d", resp.StatusCode)
	}
}

func TestWSTerminalBlocksReseller(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "reseller")

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	dialer := websocket.Dialer{Jar: client.Jar}
	_, resp, err := dialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected the websocket handshake to be rejected for a reseller account")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestWSHostTerminalRealPTYSession(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	dialer := websocket.Dialer{Jar: client.Jar}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v (status %v)", err, resp)
	}
	defer conn.Close()

	init := terminalInitMessage{Type: "init", Shell: "sh", Rows: 24, Cols: 80}
	initBytes, _ := json.Marshal(init)
	if err := conn.WriteMessage(websocket.TextMessage, initBytes); err != nil {
		t.Fatal(err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("echo hello_from_pty\n")); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var collected strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		collected.Write(data)
		if strings.Contains(collected.String(), "hello_from_pty") {
			break
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	if !strings.Contains(collected.String(), "hello_from_pty") {
		t.Fatalf("expected the shell's echoed output over the real PTY session, got: %q", collected.String())
	}
}

func TestWSTerminalResizeMessageDoesNotReachShell(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	dialer := websocket.Dialer{Jar: client.Jar}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	init := terminalInitMessage{Type: "init", Shell: "sh", Rows: 24, Cols: 80}
	initBytes, _ := json.Marshal(init)
	conn.WriteMessage(websocket.TextMessage, initBytes)

	resize := terminalInitMessage{Type: "resize", Rows: 40, Cols: 100}
	resizeBytes, _ := json.Marshal(resize)
	conn.WriteMessage(websocket.TextMessage, resizeBytes)

	conn.WriteMessage(websocket.TextMessage, []byte("echo after_resize\n"))

	var collected strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		collected.Write(data)
		if strings.Contains(collected.String(), "after_resize") {
			break
		}
	}

	output := collected.String()
	if !strings.Contains(output, "after_resize") {
		t.Fatalf("expected the shell to still process input after a resize message, got: %q", output)
	}
	// The resize JSON payload itself must not have been echoed back by the
	// shell (i.e. it must not have been written to the pty as keystrokes).
	if strings.Contains(output, `"type":"resize"`) {
		t.Fatalf("expected the resize control message NOT to be forwarded into the pty, got: %q", output)
	}
}

// dialHostTerminal opens an authenticated /ws/terminal websocket and sends the init message
func dialHostTerminal(t *testing.T, srv *httptest.Server, client *http.Client, shell string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	conn, resp, err := (&websocket.Dialer{Jar: client.Jar}).Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v (status %v)", err, resp)
	}
	initBytes, _ := json.Marshal(terminalInitMessage{Type: "init", Shell: shell, Rows: 24, Cols: 200})
	if err := conn.WriteMessage(websocket.TextMessage, initBytes); err != nil {
		t.Fatal(err)
	}
	return conn
}

// readUntil reads frames until want shows up or the per-read deadline passes, returning everything read
func readUntil(t *testing.T, conn *websocket.Conn, want string, perRead time.Duration) (string, error) {
	t.Helper()
	var out strings.Builder
	for {
		conn.SetReadDeadline(time.Now().Add(perRead))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return out.String(), err
		}
		out.Write(data)
		if strings.Contains(out.String(), want) {
			return out.String(), nil
		}
	}
}

// a command that keeps printing must not be killed just because the admin isn't typing, the idle deadline has to count output too
func TestRunPTYSessionOutputKeepsSessionAlive(t *testing.T) {
	orig := terminalCommandTimeout
	terminalCommandTimeout = 2 * time.Second
	t.Cleanup(func() { terminalCommandTimeout = orig })

	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")
	conn := dialHostTerminal(t, srv, client, "sh")
	defer conn.Close()

	// prints for ~6s, well past the 2s idle window, with no keystrokes in between
	conn.WriteMessage(websocket.TextMessage, []byte("i=0; while [ $i -lt 6 ]; do i=$((i+1)); echo tick_$i; sleep 1; done; echo done_$((21*2))\n"))

	out, err := readUntil(t, conn, "done_42", 15*time.Second)
	if !strings.Contains(out, "done_42") {
		t.Fatalf("session was killed mid-command (idle timeout ignored output), err=%v, got: %q", err, out)
	}
}

// when the shell exits the server has to close the socket promptly, not leave it lingering until the idle timeout or the next keystroke
func TestRunPTYSessionClosesWebsocketOnShellExit(t *testing.T) {
	orig := terminalCommandTimeout
	terminalCommandTimeout = 30 * time.Second // keep idle timeout out of the way so the close is attributable to the exit
	t.Cleanup(func() { terminalCommandTimeout = orig })

	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")
	conn := dialHostTerminal(t, srv, client, "sh")
	defer conn.Close()

	conn.WriteMessage(websocket.TextMessage, []byte("exit\n"))

	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var err error
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server did not close the websocket after the shell exited, read timed out instead: %v", err)
	}
}

// going quiet (no input, no output) must still close the session after the idle window, the output-based deadline reset must not keep an idle session alive forever
func TestRunPTYSessionIdleTimeoutStillCloses(t *testing.T) {
	orig := terminalCommandTimeout
	terminalCommandTimeout = 1 * time.Second
	t.Cleanup(func() { terminalCommandTimeout = orig })

	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")
	conn := dialHostTerminal(t, srv, client, "sh")
	defer conn.Close()

	// one command so there's recent activity, then go quiet
	conn.WriteMessage(websocket.TextMessage, []byte("echo primed_$((20+1))\n"))
	if _, err := readUntil(t, conn, "primed_21", 5*time.Second); err != nil {
		t.Fatalf("never saw the primer output: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	start := time.Now()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("idle session was not closed promptly, still open after %s", elapsed.Round(time.Millisecond))
	}
}
