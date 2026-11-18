package handlers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withScratchSSHFiles(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	origCfg, origDropIn, origEff, origVal, origExec := SSHDConfigPath, SSHDDropInPath, sshEffectiveRun, sshValidateRun, sshExecuteActionRun
	t.Cleanup(func() {
		SSHDConfigPath, SSHDDropInPath, sshEffectiveRun, sshValidateRun, sshExecuteActionRun = origCfg, origDropIn, origEff, origVal, origExec
	})
	os.MkdirAll(filepath.Join(dir, "sshd_config.d"), 0755)
	SSHDConfigPath = filepath.Join(dir, "sshd_config")
	SSHDDropInPath = filepath.Join(dir, "sshd_config.d", "00-openadmin.conf")
	sshEffectiveRun = func() (sshSettings, bool) { return sshSettings{}, false }
	sshValidateRun = func() error { return nil }
	sshExecuteActionRun = func(string) {}
	os.WriteFile(SSHDConfigPath, []byte(strings.ReplaceAll(config, "DIR", filepath.Join(dir, "sshd_config.d"))), 0644)
	return dir
}

func TestSSHSettingsWriteTheDropInThatWinsOverCloudInit(t *testing.T) {
	withScratchSSHFiles(t, "Include DIR/*.conf\nPort 22\nPermitRootLogin yes\n")
	if err := sshUpdateSettingsRun(sshSettings{Port: "22", PasswordAuth: "no", PubkeyAuth: "yes", PermitRootLogin: "yes"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(SSHDDropInPath)
	if err != nil || !strings.Contains(string(b), "PasswordAuthentication no") {
		t.Fatalf("drop-in = %q, %v", b, err)
	}
}

func TestSSHSettingsNoDropInWithoutInclude(t *testing.T) {
	withScratchSSHFiles(t, "Port 22\nPasswordAuthentication yes\n")
	if err := sshUpdateSettingsRun(sshSettings{PasswordAuth: "no"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SSHDDropInPath); err == nil {
		t.Fatal("no Include, the drop-in would never be read")
	}
	if b, _ := os.ReadFile(SSHDConfigPath); !strings.Contains(string(b), "PasswordAuthentication no") {
		t.Fatalf("sshd_config = %q", b)
	}
}

func TestSSHSettingsRefusedConfigIsPutBack(t *testing.T) {
	withScratchSSHFiles(t, "Include DIR/*.conf\nPort 22\n")
	before, _ := os.ReadFile(SSHDConfigPath)
	restarted := false
	sshExecuteActionRun = func(string) { restarted = true }
	sshValidateRun = func() error { return errors.New("bad") }
	if err := sshUpdateSettingsRun(sshSettings{Port: "2222"}); err == nil {
		t.Fatal("expected an error")
	}
	if after, _ := os.ReadFile(SSHDConfigPath); string(after) != string(before) {
		t.Fatalf("sshd_config not restored: %q", after)
	}
	if _, err := os.Stat(SSHDDropInPath); err == nil {
		t.Fatal("drop-in should be gone again")
	}
	if restarted {
		t.Fatal("sshd must not restart on a refused config")
	}
}
