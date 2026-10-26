package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// most tests swap emailsPodmanPsRun mid test and expect the next check to see it
func init() { mailserverStatusTTL = 0 }

func TestCheckMailserverStatusIsCached(t *testing.T) {
	dir := t.TempDir()
	origCompose, origPs, origTTL := EmailsMailComposeFile, emailsPodmanPsRun, mailserverStatusTTL
	t.Cleanup(func() {
		EmailsMailComposeFile, emailsPodmanPsRun, mailserverStatusTTL = origCompose, origPs, origTTL
		resetMailserverStatus()
	})
	EmailsMailComposeFile = filepath.Join(dir, "compose.yml")
	os.WriteFile(EmailsMailComposeFile, nil, 0o644)
	mailserverStatusTTL = time.Minute
	resetMailserverStatus()

	calls := 0
	emailsPodmanPsRun = func(args ...string) (string, error) { calls++; return emailsMailserverContainerName, nil }
	checkMailserverStatus()
	if checkMailserverStatus() != "running" || calls != 1 {
		t.Fatalf("second check should come from the cache, podman ran %d times", calls)
	}
	resetMailserverStatus()
	emailsPodmanPsRun = func(args ...string) (string, error) { calls++; return "", nil }
	if checkMailserverStatus() != "stopped" || calls != 2 {
		t.Fatal("a reset after starting or stopping must check again")
	}
}
