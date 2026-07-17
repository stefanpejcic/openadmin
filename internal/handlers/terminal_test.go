package handlers

import (
	"encoding/json"
	"io"
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
		for _, want := range []string{"Web Terminal", "id=\"terminal\"", "</html>"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("role %q: expected body to contain %q, got %s", role, want, truncate(string(body)))
			}
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
