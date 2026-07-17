package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDevMode(t *testing.T) {
	dir := t.TempDir()
	orig := OpenpanelConfigPath
	defer func() { OpenpanelConfigPath = orig }()

	OpenpanelConfigPath = filepath.Join(dir, "missing.config")
	if IsDevMode() {
		t.Fatal("expected false when config file is missing")
	}

	OpenpanelConfigPath = filepath.Join(dir, "openpanel.config")
	os.WriteFile(OpenpanelConfigPath, []byte("[PANEL]\ndev_mode=off\n"), 0644)
	if IsDevMode() {
		t.Fatal("expected false for dev_mode=off")
	}

	os.WriteFile(OpenpanelConfigPath, []byte("[PANEL]\ndev_mode=on\n"), 0644)
	if !IsDevMode() {
		t.Fatal("expected true for dev_mode=on")
	}
}
