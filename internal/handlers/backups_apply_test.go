package handlers

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestApplyBackupEnvToAccounts(t *testing.T) {
	root := t.TempDir()
	origHome, origCore, origEnv, origRestart := BackupsHomeRoot, BackupsCoreUsersRoot, BackupEnvPath, backupsRestartUserBackup
	BackupsHomeRoot = filepath.Join(root, "home")
	BackupsCoreUsersRoot = filepath.Join(root, "core", "users")
	BackupEnvPath = filepath.Join(root, "backup.env")
	var mu sync.Mutex
	var restarted []string
	done := make(chan struct{}, 4)
	backupsRestartUserBackup = func(c string) {
		mu.Lock()
		restarted = append(restarted, c)
		mu.Unlock()
		done <- struct{}{}
	}
	t.Cleanup(func() {
		BackupsHomeRoot, BackupsCoreUsersRoot, BackupEnvPath, backupsRestartUserBackup = origHome, origCore, origEnv, origRestart
	})
	os.WriteFile(BackupEnvPath, []byte("BACKUP_CRON_EXPRESSION=@daily\n"), 0644)
	os.MkdirAll(filepath.Join(BackupsHomeRoot, "alice"), 0755)
	os.MkdirAll(filepath.Join(BackupsHomeRoot, "bob"), 0755)
	os.WriteFile(filepath.Join(BackupsHomeRoot, "alice", "backup.env"), []byte("old own settings"), 0644)

	applied, failed := applyBackupEnvToAccounts([]string{"alice", "bob", "ghost", "../etc"})
	if applied != 2 || len(failed) != 0 {
		t.Fatalf("applied=%d failed=%v", applied, failed)
	}
	for _, u := range []string{"alice", "bob"} {
		link := filepath.Join(BackupsHomeRoot, u, "backup.env")
		if target, err := os.Readlink(link); err != nil || target != BackupEnvPath {
			t.Errorf("%s backup.env should link to the default, got %q %v", u, target, err)
		}
		if _, err := os.Stat(filepath.Join(BackupsCoreUsersRoot, u, "admin.backups")); err != nil {
			t.Errorf("%s missing admin.backups marker", u)
		}
	}
	if _, err := os.Stat(filepath.Join(BackupsCoreUsersRoot, "ghost")); err == nil {
		t.Error("an account without a home dir should be skipped")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("backup containers were not restarted")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(restarted) != 2 {
		t.Errorf("restarted %v", restarted)
	}
}
