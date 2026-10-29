package handlers

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"openadmin/internal/podman"
)

// where applying the default backup.env to existing accounts writes, vars so tests use temp dirs
var (
	BackupsHomeRoot      = "/home"
	BackupsCoreUsersRoot = "/etc/openpanel/openpanel/core/users"
)

// backupsRestartUserBackup restarts one account's backup container so it picks up the new backup.env, a var so tests don't need podman
var backupsRestartUserBackup = func(userContext string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := podman.Command(userContext, "restart", "-t", "3", "backup")
	if err != nil {
		return
	}
	// same argv and socket env as podman.Command, with a deadline so one stuck account can't hang the rest
	timed := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	timed.Env = cmd.Env
	_ = timed.Run()
}

// secureBackupEnv makes the default backup.env 0600 root:root, it holds remote backup credentials and every
// admin-managed account's backup.env links to it, compose reads it as root so accounts don't need to
func secureBackupEnv() error {
	if err := os.Chmod(BackupEnvPath, 0600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return os.Chown(BackupEnvPath, 0, 0)
	}
	return nil
}

// writeBackupEnv saves the default backup.env, WriteFile keeps an existing file's mode so it's secured after
func writeBackupEnv(data []byte) error {
	if err := os.WriteFile(BackupEnvPath, data, 0600); err != nil {
		return err
	}
	return secureBackupEnv()
}

// backupAccountContexts lists every account's context, the folder under /home that holds its backup.env
func backupAccountContexts(db *sql.DB) ([]string, error) {
	if db == nil {
		return nil, errors.New("database unavailable")
	}
	rows, err := db.Query(`SELECT DISTINCT server FROM users WHERE server IS NOT NULL AND server <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// applyBackupEnvToAccounts does what the discussion #1146 script did: every account's backup.env becomes a link to the default,
// gets the admin.backups marker so the panel treats backups as admin-managed, and its backup container restarts in the background
func applyBackupEnvToAccounts(contexts []string) (applied int, failed []string) {
	if err := secureBackupEnv(); err != nil {
		return 0, []string{BackupEnvPath + ": " + err.Error()}
	}
	var restart []string
	for _, c := range contexts {
		if c == "" || c == "." || c == ".." || strings.ContainsAny(c, "/\\") {
			continue
		}
		home := filepath.Join(BackupsHomeRoot, c)
		if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
			continue
		}
		envPath := filepath.Join(home, "backup.env")
		if err := os.Remove(envPath); err != nil && !os.IsNotExist(err) {
			failed = append(failed, c+": "+err.Error())
			continue
		}
		if err := os.Symlink(BackupEnvPath, envPath); err != nil {
			failed = append(failed, c+": "+err.Error())
			continue
		}
		markerDir := filepath.Join(BackupsCoreUsersRoot, c)
		if err := os.MkdirAll(markerDir, 0755); err == nil {
			_ = os.WriteFile(filepath.Join(markerDir, "admin.backups"), nil, 0644)
		}
		applied++
		restart = append(restart, c)
	}
	go func() {
		for _, c := range restart {
			backupsRestartUserBackup(c)
		}
	}()
	return applied, failed
}
