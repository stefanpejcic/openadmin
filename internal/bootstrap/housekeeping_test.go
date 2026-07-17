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

	origTarget, origLink := csfSymlinkTarget, csfSymlinkName
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	csfSymlinkTarget, csfSymlinkName = target, link
	defer func() { csfSymlinkTarget, csfSymlinkName = origTarget, origLink }()

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

	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected symlink to be created: %v", err)
	}
	if linkTarget != target {
		t.Fatalf("expected symlink to point at %s, got %s", target, linkTarget)
	}

	if info, err := os.Stat(cache); err != nil || !info.IsDir() {
		t.Fatalf("expected cache dir to exist: %v", err)
	}
}

func TestSymlinkForceReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.Mkdir(target, 0755)
	link := filepath.Join(dir, "link")
	// pre-existing regular file at the link path, like a fresh install might have
	os.WriteFile(link, []byte("stale"), 0644)

	symlinkForce(log.New(io.Discard, "", 0), target, link)

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected link to become a symlink: %v", err)
	}
	if got != target {
		t.Fatalf("expected symlink to %s, got %s", target, got)
	}
}
