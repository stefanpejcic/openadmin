package bootstrap

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestRunStartupHousekeeping(t *testing.T) {
	dir := t.TempDir()

	origExec := executablePaths
	scriptPath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executablePaths = []string{scriptPath, filepath.Join(dir, "missing.sh")}
	defer func() { executablePaths = origExec }()

	origCache := cacheDir
	cache := filepath.Join(dir, "cache")
	cacheDir = cache
	defer func() { cacheDir = origCache }()

	logger := log.New(io.Discard, "", 0)
	RunStartupHousekeeping(logger)

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0111 != 0111 {
		t.Fatal("expected script to become executable")
	}

	if info, err := os.Stat(cache); err != nil || !info.IsDir() {
		t.Fatalf("expected cache dir to exist: %v", err)
	}
}
