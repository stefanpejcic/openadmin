package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/websocket"
)

func secDialWS(t *testing.T, srv *httptest.Server, client *http.Client, path string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	return (&websocket.Dialer{Jar: client.Jar}).Dial(url, hdr)
}

// demo mode only blocks non-GET requests in the middleware, the websocket handshake is a GET, so the terminal handler has to refuse it itself
func TestTerminalDemoModeBlocksWebsocket(t *testing.T) {
	term := &Terminal{DemoMode: true}
	srv, client := newTerminalTestServer(t, term, "admin")

	conn, resp, err := secDialWS(t, srv, client, "/ws/terminal", nil)
	if err == nil {
		conn.Close()
		t.Fatal("demo mode must refuse the terminal websocket")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 in demo mode, got %v", resp)
	}
}

// the disable flag used to guard only the host route, the per-container route has to honor it too
func TestTerminalDisableFlagBlocksContainerWS(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")
	if err := os.WriteFile(TerminalDisableFlagPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	conn, resp, err := secDialWS(t, srv, client, "/ws/terminal/alice/php-fpm-8.4", nil)
	if err == nil {
		conn.Close()
		t.Fatal("disable flag must refuse the container terminal websocket")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 with the disable flag set, got %v", resp)
	}
}

// an unknown username must fail, not fall through to root's own local podman on an empty context
func TestUserTerminalWSRejectsUnknownUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT server FROM users`).WithArgs("nosuch").WillReturnError(sql.ErrNoRows)

	term := &Terminal{MySQL: db}
	srv, client := newTerminalTestServer(t, term, "admin")

	started := make(chan []string, 1)
	orig := terminalRunPTYSessionRun
	terminalRunPTYSessionRun = func(conn *websocket.Conn, argv []string, rows, cols int, env []string, cwd string) {
		started <- argv
	}
	defer func() { terminalRunPTYSessionRun = orig }()

	conn, resp, err := secDialWS(t, srv, client, "/ws/terminal/nosuch/openpanel", nil)
	if err == nil {
		conn.Close()
		t.Fatal("an unknown user must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown user, got %v", resp)
	}
	select {
	case argv := <-started:
		t.Fatalf("no session should start for an unknown user, but it ran %q", argv)
	default:
	}
}

// a container or username that isn't a plain name must be refused before it reaches podman exec
func TestUserTerminalWSRejectsBadNames(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	for _, path := range []string{"/ws/terminal/alice/-rf", "/ws/terminal/-root/openpanel"} {
		conn, resp, err := secDialWS(t, srv, client, path, nil)
		if err == nil {
			conn.Close()
			t.Fatalf("%s should be refused", path)
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %v", path, resp)
		}
	}
}

// the host terminal websocket accepts any Origin no more, a same-site page on another port used to be able to open the root shell over this socket
func TestTerminalWebsocketOriginCheck(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	conn, _, err := secDialWS(t, srv, client, "/ws/terminal", http.Header{"Origin": {srv.URL}})
	if err != nil {
		t.Fatalf("a same-origin handshake should still succeed: %v", err)
	}
	conn.Close()

	conn, resp, err := secDialWS(t, srv, client, "/ws/terminal", http.Header{"Origin": {"http://evil.example.com"}})
	if err == nil {
		conn.Close()
		t.Fatal("a foreign Origin must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a foreign Origin, got %v", resp)
	}
}

// a ?cmd= carried across the login redirect must not auto-run, a genuine link from inside OpenAdmin still should
func TestSameOriginNavigationBlocksLoginReferer(t *testing.T) {
	mk := func(ref, secFetch string) *http.Request {
		r := httptest.NewRequest("GET", "https://panel.example.com/terminal?cmd=whoami", nil)
		r.Host = "panel.example.com"
		if ref != "" {
			r.Header.Set("Referer", ref)
		}
		if secFetch != "" {
			r.Header.Set("Sec-Fetch-Site", secFetch)
		}
		return r
	}

	if sameOriginNavigation(mk("https://panel.example.com/login?next=/terminal%3Fcmd%3Dwhoami", "same-origin")) {
		t.Fatal("a cmd carried across the login redirect must not auto-run")
	}
	if !sameOriginNavigation(mk("https://panel.example.com/databases", "same-origin")) {
		t.Fatal("a genuine same-origin link from inside OpenAdmin should still auto-run")
	}
}
