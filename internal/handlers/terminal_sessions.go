package handlers

import (
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"net/http"
	"openadmin/internal/activity"
	"openadmin/internal/auth"
)

// webTerminalSession is one open web terminal websocket, Target is "host" or "user/container"
type webTerminalSession struct {
	Admin, IP, Target string
	Since             time.Time
	closeConn         func()
}

var (
	webTerminalMu       sync.Mutex
	webTerminalSessions = map[int]webTerminalSession{}
	webTerminalNextID   int
)

// trackWebTerminal records an open session (so it shows in the banner and can be torn down), logs it to the admin's activity log, and returns the func that removes and logs the close. closeFn force-closes the underlying websocket.
func trackWebTerminal(r *http.Request, target string, closeFn func()) func() {
	admin := ""
	if u := auth.CurrentUser(r); u != nil {
		admin = u.Username
	}
	ip := clientIP(r)
	webTerminalMu.Lock()
	id := webTerminalNextID
	webTerminalNextID++
	webTerminalSessions[id] = webTerminalSession{Admin: admin, IP: ip, Target: target, Since: time.Now(), closeConn: closeFn}
	webTerminalMu.Unlock()
	_ = activity.Record(admin, ip, terminalActivityAction("Opened", target))
	return func() {
		webTerminalMu.Lock()
		delete(webTerminalSessions, id)
		webTerminalMu.Unlock()
		_ = activity.Record(admin, ip, terminalActivityAction("Closed", target))
	}
}

// terminalActivityAction is the activity-log line for a terminal session, "host" is root's own shell
func terminalActivityAction(verb, target string) string {
	if target == "host" {
		return verb + " the root web terminal"
	}
	return verb + " the web terminal for " + target
}

// closeWebTerminalsForAdmin force-closes every live session opened by username and returns how many it closed, for logout and when the account is deleted or suspended
func closeWebTerminalsForAdmin(username string) int {
	webTerminalMu.Lock()
	var closers []func()
	for _, s := range webTerminalSessions {
		if s.closeConn != nil && s.Admin == username {
			closers = append(closers, s.closeConn)
		}
	}
	webTerminalMu.Unlock()
	// closing the websocket unblocks the handler, which runs the remover above
	for _, c := range closers {
		c()
	}
	return len(closers)
}

func openWebTerminals(target string) []webTerminalSession {
	webTerminalMu.Lock()
	defer webTerminalMu.Unlock()
	var out []webTerminalSession
	for _, s := range webTerminalSessions {
		if s.Target == target {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

// sshPeer is a remote IP with how many SSH connections it has open
type sshPeer struct {
	IP    string
	Count int
}

// sshSessionsRun is swapped out in tests
var sshSessionsRun = func() string {
	out, _ := exec.Command("ss", "-tnpH", "state", "established").Output()
	return string(out)
}

// sshProcTitle is swapped out in tests, sshd rewrites its title as the login goes along
var sshProcTitle = func(pid string) string {
	b, _ := os.ReadFile("/proc/" + pid + "/cmdline")
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}

var ssPIDRe = regexp.MustCompile(`pid=(\d+)`)

// openSSHPeers counts logged-in SSH connections per IP from ss output, so it works whatever port SSH listens on
func openSSHPeers(ssOutput string) []sshPeer {
	// privileged and child sshd share the socket, collect every pid holding each peer ip:port
	holders := map[string][]string{}
	for _, line := range strings.Split(ssOutput, "\n") {
		if !strings.Contains(line, `"sshd`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		for _, m := range ssPIDRe.FindAllStringSubmatch(line, -1) {
			holders[fields[3]] = append(holders[fields[3]], m[1])
		}
	}
	counts := map[string]int{}
	for peer, pids := range holders {
		if !sshLoggedIn(pids) {
			continue
		}
		host, _, err := net.SplitHostPort(peer)
		if err != nil {
			continue
		}
		counts[strings.TrimPrefix(host, "::ffff:")]++
	}
	out := make([]sshPeer, 0, len(counts))
	for ip, n := range counts {
		out = append(out, sshPeer{ip, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

// sshLoggedIn wants the [priv] monitor plus its session process, logins still in progress and port forwards only have one of them
func sshLoggedIn(pids []string) bool {
	monitor, session := false, false
	for _, pid := range pids {
		name, state, ok := strings.Cut(sshProcTitle(pid), ": ")
		if !ok || !strings.HasPrefix(name, "sshd") {
			continue
		}
		// a session is "root@pts/0", "root@notty" or "root", anything mid-login is tagged like "[accepted]" or "unknown [net]"
		if strings.HasSuffix(state, " [priv]") {
			monitor = true
		} else if !strings.Contains(state, "[") {
			session = true
		}
	}
	return monitor && session
}
