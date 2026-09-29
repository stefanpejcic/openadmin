package handlers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartDetachedFallbackWritesLogAndReaps(t *testing.T) {
	orig := systemdRunPath
	systemdRunPath = func() (string, error) { return "", errors.New("no systemd-run") }
	t.Cleanup(func() { systemdRunPath = orig })

	logPath := filepath.Join(t.TempDir(), "out.log")
	pid, err := startDetached("test-unit", logPath, []string{"echo", "hello world"})
	if err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("expected a pid, got %d", pid)
	}

	// a reaped child stops looking alive, a zombie would stay alive forever
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive after 5s, not reaped", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}

	out, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello world" {
		t.Fatalf("unexpected log content %q", out)
	}
}
