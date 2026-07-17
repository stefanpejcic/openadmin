package bootstrap

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveRestartFlag(t *testing.T) {
	dir := t.TempDir()
	orig := RestartFlagPath
	RestartFlagPath = filepath.Join(dir, "openadmin_restart_needed")
	defer func() { RestartFlagPath = orig }()

	logger := log.New(io.Discard, "", 0)

	// no-op when absent
	RemoveRestartFlag(logger)

	if err := os.WriteFile(RestartFlagPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	RemoveRestartFlag(logger)
	if _, err := os.Stat(RestartFlagPath); !os.IsNotExist(err) {
		t.Fatal("expected restart flag to be removed")
	}
}

func TestExitIfDisabledNoopWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	orig := DisabledFlagPath
	DisabledFlagPath = filepath.Join(dir, "openadmin_is_disabled")
	defer func() { DisabledFlagPath = orig }()

	// Must return (not exit) when the flag file doesn't exist. The exit(1)
	// path itself can't be safely exercised in-process (see manual
	// verification notes) since os.Exit would kill the test binary.
	ExitIfDisabled(log.New(io.Discard, "", 0))
}
