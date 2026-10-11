package handlers

import (
	"net"
	"os/exec"
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

// openSSHPeers counts established connections owned by sshd, so it works whatever port SSH listens on
func openSSHPeers(ssOutput string) []sshPeer {
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, line := range strings.Split(ssOutput, "\n") {
		if !strings.Contains(line, `"sshd`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// privileged and child sshd share the socket, count each peer ip:port once
		peer := fields[3]
		if seen[peer] {
			continue
		}
		seen[peer] = true
		host, _, err := net.SplitHostPort(peer)
		if err != nil {
			continue
		}
		host = strings.TrimPrefix(host, "::ffff:")
		counts[host]++
	}
	out := make([]sshPeer, 0, len(counts))
	for ip, n := range counts {
		out = append(out, sshPeer{ip, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}
