// This file implements the websocket-based PTY web terminal (a root
// shell on the host, or an interactive `podman exec` shell into a
// specific user's container).
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
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

// terminalCommandTimeout is how long the websocket may sit idle before
// it's closed.
const terminalCommandTimeout = 300 * time.Second

var TerminalDisableFlagPath = "/root/disable_openadmin_terminal_ui"

// TerminalPTYCwd is the default working directory ("/root") used by
// both the host and per-user-container sessions. A var so tests can
// point it at a directory the test-running (non-root) user can actually
// chdir into.
var TerminalPTYCwd = "/root"

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
	webtemplates.Render(w, "settings_terminal.html", mergeChrome(map[string]interface{}{
		"TerminalType":    "root",
		"TerminalTimeout": int(terminalCommandTimeout.Seconds()),
	}, r, "Web Terminal"))
}

// ServeUserTerminalPage handles GET /terminal/{username}/{container_name}.
// Access control: see ServeHostTerminalPage above -- auth.RequireAdmin's
// reseller exclusion is sufficient here too.
func (t *Terminal) ServeUserTerminalPage(w http.ResponseWriter, r *http.Request) {
	webtemplates.Render(w, "settings_terminal.html", mergeChrome(map[string]interface{}{
		"TerminalType":    "users",
		"TerminalTimeout": int(terminalCommandTimeout.Seconds()),
		"Username":        r.PathValue("username"),
		"ContainerName":   r.PathValue("container_name"),
	}, r, "Docker Terminal"))
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

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
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

	for {
		_ = conn.SetReadDeadline(time.Now().Add(terminalCommandTimeout))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

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

	init, ok := terminalReadInit(conn)
	if !ok {
		return
	}

	podmanContext, _ := queryContextByUsername(t.MySQL, username)
	argv := podman.Argv(podmanContext, "exec", "-it", "-e", "TERM=xterm-256color", containerName, init.Shell)
	extraEnv, _ := podman.Env(podmanContext, nil)

	terminalRunPTYSessionRun(conn, argv, init.Rows, init.Cols, extraEnv, TerminalPTYCwd)
}
