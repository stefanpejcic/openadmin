package handlers

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// systemdRunPath is a var so tests can force the plain-child fallback
var systemdRunPath = func() (string, error) { return exec.LookPath("systemd-run") }

// startDetached runs args in its own transient systemd unit so restarting the admin service doesn't kill it, stdout/stderr go to logPath when set
func startDetached(unitPrefix, logPath string, args []string) (int, error) {
	// bash does the redirect so the log path doesn't depend on systemd's StandardOutput support
	bashArgs := append([]string{"-c", `log="$1"; shift; if [ -n "$log" ]; then exec "$@" > "$log" 2>&1; else exec "$@" > /dev/null 2>&1; fi`, "_", logPath}, args...)

	if systemdRun, err := systemdRunPath(); err == nil {
		unit := fmt.Sprintf("%s-%d", unitPrefix, time.Now().UnixNano())
		runArgs := append([]string{"--unit=" + unit, "--collect", "--quiet", "/bin/bash"}, bashArgs...)
		if out, runErr := exec.Command(systemdRun, runArgs...).CombinedOutput(); runErr == nil {
			pidOut, _ := exec.Command("systemctl", "show", "-p", "MainPID", "--value", unit).Output()
			pid, _ := strconv.Atoi(strings.TrimSpace(string(pidOut)))
			return pid, nil
		} else if len(out) > 0 {
			fmt.Printf("systemd-run failed for %s, starting it as a child instead: %s\n", unit, strings.TrimSpace(string(out)))
		}
	}

	cmd := exec.Command("/bin/bash", bashArgs...)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// reap it ourselves, otherwise it stays a zombie and looks alive to processAlive
	go cmd.Wait()
	return cmd.Process.Pid, nil
}
