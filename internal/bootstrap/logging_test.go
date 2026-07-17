package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupLoggingProdModeDiscards(t *testing.T) {
	dir := t.TempDir()
	origErr, origAccess := ErrorLogPath, AccessLogPath
	ErrorLogPath = filepath.Join(dir, "admin", "error.log")
	AccessLogPath = filepath.Join(dir, "admin", "access.log")
	defer func() { ErrorLogPath, AccessLogPath = origErr, origAccess }()

	logger, err := SetupLogging(false)
	if err != nil {
		t.Fatal(err)
	}
	logger.Println("should be discarded")

	if _, err := os.Stat(filepath.Dir(ErrorLogPath)); err != nil {
		t.Fatalf("expected log dir to still be created in prod mode: %v", err)
	}
	if _, err := os.Stat(ErrorLogPath); err == nil {
		t.Fatal("expected no error.log file to be written in prod mode")
	}
}

func TestSetupLoggingDevModeWritesFile(t *testing.T) {
	dir := t.TempDir()
	origErr, origAccess := ErrorLogPath, AccessLogPath
	ErrorLogPath = filepath.Join(dir, "admin", "error.log")
	AccessLogPath = filepath.Join(dir, "admin", "access.log")
	defer func() { ErrorLogPath, AccessLogPath = origErr, origAccess }()

	logger, err := SetupLogging(true)
	if err != nil {
		t.Fatal(err)
	}
	logger.Println("hello from dev mode")

	data, err := os.ReadFile(ErrorLogPath)
	if err != nil {
		t.Fatalf("expected error.log to exist: %v", err)
	}
	if !strings.Contains(string(data), "hello from dev mode") {
		t.Fatalf("expected log content, got: %s", data)
	}
}
