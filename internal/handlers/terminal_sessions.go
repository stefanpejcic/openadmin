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
	"openadmin/internal/auth"
)

// webTerminalSession is one open web terminal websocket, Target is "host" or "user/container"
type webTerminalSession struct {
	Admin, IP, Target string
	Since             time.Time
}

var (
	webTerminalMu       sync.Mutex
	webTerminalSessions = map[int]webTerminalSession{}
	webTerminalNextID   int
)

// trackWebTerminal records an open session and returns the func that removes it
func trackWebTerminal(r *http.Request, target string) func() {
	admin := ""
	if u := auth.CurrentUser(r); u != nil {
		admin = u.Username
	}
	webTerminalMu.Lock()
	id := webTerminalNextID
	webTerminalNextID++
	webTerminalSessions[id] = webTerminalSession{Admin: admin, IP: clientIP(r), Target: target, Since: time.Now()}
	webTerminalMu.Unlock()
	return func() {
		webTerminalMu.Lock()
		delete(webTerminalSessions, id)
		webTerminalMu.Unlock()
	}
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
