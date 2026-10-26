// This file implements the websocket-based PTY web terminal (a root
// shell on the host, or an interactive `podman exec` shell into a
// specific user's container).
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// Terminal bundles the /terminal and /ws/terminal handlers.
type Terminal struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

// terminalCommandTimeout is how long the websocket may sit idle (no input
// and no output) before it's closed. A var so tests can shrink it.
var terminalCommandTimeout = 300 * time.Second

var TerminalDisableFlagPath = "/root/disable_openadmin_terminal_ui"

// TerminalPTYCwd is the default working directory ("/root") used by
// both the host and per-user-container sessions. A var so tests can
// point it at a directory the test-running (non-root) user can actually
// chdir into.
var TerminalPTYCwd = "/root"

// terminalCSP locks the terminal page down to same-origin assets: everything
// (xterm included) is self-hosted, the websocket can only reach this origin,
// and the page can't be framed. Alpine and the inline setup script still need
// 'unsafe-inline'/'unsafe-eval', so this isn't a full XSS wall, but it keeps
// an injected tag from pulling script off another host or opening a socket to one.
const terminalCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'self'"

func terminalSetCSP(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", terminalCSP)
}

// ServeHostTerminalPage handles GET /terminal.
//
// Access control: auth.RequireAdmin (wired in main.go) already excludes
// the "reseller" role. Every other admindb account ("admin" or "user") is
// panel staff, not a hosting end-customer -- admindb's "user" role is a
// lower-privilege staff account, not a customer login -- so no further
// role/username restriction is applied here.
func (t *Terminal) ServeHostTerminalPage(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(TerminalDisableFlagPath); err == nil {
		http.Error(w, "Web Terminal access is disabled.", http.StatusForbidden)
		return
	}
	terminalSetCSP(w)
	webtemplates.Render(w, "settings_terminal.html", mergeChrome(map[string]interface{}{
		"TerminalType":    "root",
		"TerminalTimeout": int(terminalCommandTimeout.Seconds()),
		"InitialCommand":  terminalInitialCommand(r),
		"AutoRun":         sameOriginNavigation(r),
		"WebSessions":     openWebTerminals("host"),
		"SSHPeers":        openSSHPeers(sshSessionsRun()),
	}, r, "Web Terminal"))
}

// ServeUserTerminalPage handles GET /terminal/{username}/{container_name}.
// Access control: see ServeHostTerminalPage above -- auth.RequireAdmin's
// reseller exclusion is sufficient here too.
func (t *Terminal) ServeUserTerminalPage(w http.ResponseWriter, r *http.Request) {
	terminalSetCSP(w)
	webtemplates.Render(w, "settings_terminal.html", mergeChrome(map[string]interface{}{
		"TerminalType":    "users",
		"TerminalTimeout": int(terminalCommandTimeout.Seconds()),
		"InitialCommand":  terminalInitialCommand(r),
		"AutoRun":         sameOriginNavigation(r),
		"Username":        r.PathValue("username"),
		"ContainerName":   r.PathValue("container_name"),
		"WebSessions":     openWebTerminals(r.PathValue("username") + "/" + r.PathValue("container_name")),
	}, r, "Docker Terminal"))
}

// terminalInitialCommand is the ?cmd= the page types into the shell once it's up, one line only
func terminalInitialCommand(r *http.Request) string {
	cmd := strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return -1
		}
		return c
	}, r.URL.Query().Get("cmd"))
	if len(cmd) > 2000 {
		return ""
	}
	return strings.TrimSpace(cmd)
}

// sameOriginNavigation tells if the admin got here by a link inside OpenAdmin, a ?cmd= from anywhere else is only typed, not run
func sameOriginNavigation(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	ref, err := url.Parse(r.Referer())
	return err == nil && ref.Host != "" && ref.Host == r.Host
}

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Auth is already enforced by the session cookie (RequireAdmin +
	// the super-admin check below) before Upgrade() is ever called, so
	// there's no separate origin allowlist here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type terminalInitMessage struct {
	Type  string `json:"type"`
	Shell string `json:"shell"`
	Rows  int    `json:"rows"`
	Cols  int    `json:"cols"`
}

func terminalNormalizeShell(shell string) string {
	if shell == "bash" || shell == "sh" {
		return shell
	}
	return "sh"
}

// terminalReadInit reads the mandatory first message: the init JSON
// {"type":"init","shell":"bash","rows":24,"cols":80}, with a 10s deadline.
func terminalReadInit(conn *websocket.Conn) (terminalInitMessage, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return terminalInitMessage{}, false
	}
	var init terminalInitMessage
	if json.Unmarshal(data, &init) != nil {
		return terminalInitMessage{}, false
	}
	init.Shell = terminalNormalizeShell(init.Shell)
	if init.Rows <= 0 {
		init.Rows = 24
	}
	if init.Cols <= 0 {
		init.Cols = 80
	}
	return init, true
}

// runPTYSession starts argv attached to a PTY, pumps PTY output to the
// websocket as text frames, and pumps websocket messages (resize
// control JSON, or raw keystroke bytes) into the PTY.
func runPTYSession(conn *websocket.Conn, argv []string, rows, cols int, extraEnv []string, cwd string) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor"), extraEnv...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})

	// cap a single inbound frame so one giant message can't balloon memory
	conn.SetReadLimit(1 << 20)

	// extend the idle deadline on any activity, PTY output included, so a
	// long-running command that keeps printing isn't killed just because
	// nobody is typing
	bump := func() { _ = conn.SetReadDeadline(time.Now().Add(terminalCommandTimeout)) }
	bump()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				bump()
				// Replaces invalid UTF-8 sequences with the standard
				// replacement character rather than dropping or erroring
				// on them.
				text := strings.ToValidUTF8(string(buf[:n]), "�")
				if conn.WriteMessage(websocket.TextMessage, []byte(text)) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// close the websocket as soon as the shell exits, otherwise the session
	// (and its "already open" banner entry) lingers until the idle timeout
	// or the next keystroke
	go func() {
		<-readerDone
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"))
		_ = conn.Close()
	}()

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		bump()

		if messageType == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var msg terminalInitMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(msg.Rows), Cols: uint16(msg.Cols)})
				continue
			}
		}

		if _, werr := ptmx.Write(data); werr != nil {
			break
		}
	}

	ptmx.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	<-readerDone
}

// terminalRunPTYSessionRun is injectable so handler-level tests don't need
// to actually allocate a PTY / spawn a shell.
var terminalRunPTYSessionRun = runPTYSession

// ServeHostTerminalWS handles the websocket at /ws/terminal. Access
// control: see ServeHostTerminalPage above.
func (t *Terminal) ServeHostTerminalWS(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(TerminalDisableFlagPath); err == nil {
		http.Error(w, "Web Terminal access is disabled.", http.StatusForbidden)
		return
	}

	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	defer trackWebTerminal(r, "host")()

	init, ok := terminalReadInit(conn)
	if !ok {
		return
	}

	terminalRunPTYSessionRun(conn, []string{init.Shell}, init.Rows, init.Cols,
		[]string{"OPENPANEL_HIDE_WELCOME=true"}, TerminalPTYCwd)
}

// ServeUserTerminalWS handles the websocket at
// /ws/terminal/{username}/{container_name}. Access control: see
// ServeHostTerminalPage above.
func (t *Terminal) ServeUserTerminalWS(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	containerName := r.PathValue("container_name")

	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	defer trackWebTerminal(r, username+"/"+containerName)()

	init, ok := terminalReadInit(conn)
	if !ok {
		return
	}

	podmanContext, _ := queryContextByUsername(t.MySQL, username)
	argv := podman.Argv(podmanContext, "exec", "-it", "-e", "TERM=xterm-256color", containerName, init.Shell)
	extraEnv, _ := podman.Env(podmanContext, nil)

	terminalRunPTYSessionRun(conn, argv, init.Rows, init.Cols, extraEnv, TerminalPTYCwd)
}
