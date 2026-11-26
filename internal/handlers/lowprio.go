package handlers

import "os/exec"

// lowPrio puts nice -n19 in front of name, and ionice -c3 too when idle, mq-deadline ignores -c2 levels so only the idle class does anything
func lowPrio(idle bool, name string, args ...string) (string, []string) {
	var p []string
	if path, err := exec.LookPath("nice"); err == nil {
		p = append(p, path, "-n19")
	}
	if path, err := exec.LookPath("ionice"); err == nil && idle {
		p = append(p, path, "-c3")
	}
	if len(p) == 0 {
		return name, args
	}
	return p[0], append(append(p[1:], name), args...)
}
