package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleBackupLog = `2026-11-10 03:00:00 : === Backup started at 2026-11-10 03:00:00 ===
2026-11-10 03:00:01 : Found 1 active users to process
2026-11-10 03:00:30 : Backup completed for user: old (context: old) | Time taken: 29s
2026-11-10 03:00:31 : === Backup finished at 2026-11-10 03:00:31 | Total time: 31s ===
2026-11-11 03:00:00 : === Backup started at 2026-11-11 03:00:00 ===
2026-11-11 03:00:00 : Fetching list of active users...
2026-11-11 03:00:01 : Skipping user: bob (/home/bob/backup.off exists)
2026-11-11 03:00:01 : Skipped 1 users with backup.off: bob
2026-11-11 03:00:01 : Skipped 2 users that manage their own backups: x y
2026-11-11 03:00:01 : Found 3 active users to process
2026-11-11 03:00:01 : Processing user: alice (1/3)
2026-11-11 03:00:01 : Processing user: alice
2026-11-11 03:00:02 : --- backup container output for user: alice ---
time=2026-11-11T02:00:03.000Z level=INFO msg="Created backup of ` + "`/backup`" + ` at ` + "`/tmp/alice-2026-11-11T03-00-02.tar.gz`" + `."
time=2026-11-11T02:00:09.000Z level=INFO msg="Finished running backup tasks."
2026-11-11 03:02:10 : --- end of backup container output for user: alice ---
2026-11-11 03:02:10 : Backup completed for user: alice (context: alicectx) | Time taken: 128s
2026-11-11 03:02:11 : Backups reindexed for user: alice
2026-11-11 03:02:11 : ------------------------------
2026-11-11 03:02:11 : Processing user: carol
2026-11-11 03:02:12 : --- backup container output for user: carol ---
time=2026-11-11T02:02:13.000Z level=ERROR msg="Fatal error running backup: error uploading: \"s3\" unreachable"
2026-11-11 03:02:14 : --- end of backup container output for user: carol ---
2026-11-11 03:02:14 : ERROR: Backup FAILED for user: carol (context: carol) | exit code: 1 | Time taken: 2s
2026-11-11 03:02:14 : Failed processing user: carol
2026-11-11 03:02:15 : Skipping user: dave (a backup of this account is already running)
2026-11-11 03:02:15 : Failed to process 1 users: carol
2026-11-12 03:00:00 : === Backup started at 2026-11-12 03:00:00 ===
`

func withBackupFiles(t *testing.T, cronLine, log string) string {
	t.Helper()
	dir := t.TempDir()
	origLog, origHome := DockerBackupLogPath, backupHomeDir
	t.Cleanup(func() { DockerBackupLogPath, backupHomeDir = origLog, origHome })
	DockerBackupLogPath = filepath.Join(dir, "docker-backup.log")
	backupHomeDir = dir + "/"
	withScratchCronFile(t, cronLine+"\n")
	// the schedule was saved long before the days the tests look at
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	os.Chtimes(CronFilePath, old, old)
	if log != "" {
		os.WriteFile(DockerBackupLogPath, []byte(log), 0644)
	}
	return dir
}

func TestSummaryBackupFor(t *testing.T) {
	dir := withBackupFiles(t, "0 3 * * * root opencli docker-backup", sampleBackupLog)
	os.MkdirAll(filepath.Join(dir, "alicectx"), 0755)
	os.WriteFile(filepath.Join(dir, "alicectx", "available_backups.json"), []byte(`[
		{"backup_file":"alice-2026-11-10T03-00-02.tar.gz","size":10},
		{"backup_file":"alice-2026-11-11T03-00-02.tar.gz.gpg","types":["html","mysql","mail"],"databases":["a","b"],"size":2147483648}
	]`), 0644)

	b := summaryBackupFor("2026-11-11")
	if b == nil || !b.Ran || !b.Finished {
		t.Fatalf("expected a finished run: %+v", b)
	}
	if b.Started != "03:00" || b.Accounts != 3 || b.Done != 1 || b.Excluded != 1 || b.SelfManaged != 2 {
		t.Errorf("counts: %+v", b)
	}
	if b.Duration != "2m 15s" {
		t.Errorf("duration %q", b.Duration)
	}
	if len(b.Failed) != 1 || b.Failed[0].Username != "carol" || b.Failed[0].Error != `Fatal error running backup: error uploading: "s3" unreachable` {
		t.Errorf("failed: %+v", b.Failed)
	}
	if len(b.Locked) != 1 || b.Locked[0] != "dave" {
		t.Errorf("locked: %+v", b.Locked)
	}
	if b.Size != "2.0 GB" || len(b.Largest) != 1 || b.Largest[0].Duration != "2m 8s" {
		t.Errorf("sizes: %q %+v", b.Size, b.Largest)
	}
	if b.Contents != "files, 2 databases, mail" {
		t.Errorf("contents %q", b.Contents)
	}
	if b.OK || b.Tone != "bad" || b.Status != "Failed for 1 of 3 accounts." {
		t.Errorf("status %v %q %q", b.OK, b.Tone, b.Status)
	}

	ok := summaryBackupFor("2026-11-10")
	if ok == nil || !ok.OK || ok.Tone != "ok" || ok.Duration != "31s" || ok.Status != "The 1 account was backed up." {
		t.Errorf("clean run: %+v", ok)
	}

	// the run that started but never wrote its end
	open := summaryBackupFor("2026-11-12")
	if open == nil || open.Finished || open.Tone != "bad" || !strings.Contains(open.Status, "never finished") {
		t.Errorf("unfinished run: %+v", open)
	}

	missed := summaryBackupFor("2026-11-09")
	if missed == nil || missed.Ran || missed.Status != "The scheduled backup didn't run." {
		t.Errorf("missed run: %+v", missed)
	}
}

func TestSummaryBackupForSchedule(t *testing.T) {
	withBackupFiles(t, "59 23 31 2 * root opencli docker-backup", sampleBackupLog)
	if b := summaryBackupFor("2026-11-11"); b != nil {
		t.Errorf("disabled schedule should show nothing: %+v", b)
	}

	// weekly on sunday, 2026-11-11 is a wednesday
	withBackupFiles(t, "0 3 * * 0 root opencli docker-backup", "")
	if b := summaryBackupFor("2026-11-11"); b != nil {
		t.Errorf("unscheduled day should show nothing: %+v", b)
	}
	if b := summaryBackupFor("2026-11-08"); b == nil || b.Ran || b.Schedule != "Weekly" {
		t.Errorf("scheduled sunday without a run: %+v", b)
	}
}
