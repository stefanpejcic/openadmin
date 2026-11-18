package handlers

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
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

// logrotate moves the day's run to .1 and older ones to .N.gz
func TestSummaryBackupForRotatedLog(t *testing.T) {
	withBackupFiles(t, "0 3 * * * root opencli docker-backup", "")
	os.WriteFile(DockerBackupLogPath, nil, 0644)
	os.WriteFile(DockerBackupLogPath+".1", []byte(strings.Split(sampleBackupLog, "2026-11-11 03:00:00 : === Backup started")[0]), 0644)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("2026-11-09 03:00:00 : === Backup started at 2026-11-09 03:00:00 ===\n2026-11-09 03:00:01 : Found 0 active users to process\n2026-11-09 03:00:02 : No users to back up\n"))
	zw.Close()
	os.WriteFile(DockerBackupLogPath+".2.gz", gz.Bytes(), 0644)

	if b := summaryBackupFor("2026-11-10"); b == nil || !b.OK || b.Status != "The 1 account was backed up." {
		t.Errorf("run in .1: %+v", b)
	}
	if b := summaryBackupFor("2026-11-09"); b == nil || !b.Ran || !b.Finished {
		t.Errorf("run in .2.gz: %+v", b)
	}
	if b := summaryBackupFor("2026-11-08"); b == nil || b.Ran {
		t.Errorf("day without a run in any log: %+v", b)
	}
}

func TestMergeAdminNewIPs(t *testing.T) {
	got := mergeAdminNewIPs([]summaryProblem{
		{Title: "Admin mirko accessed from new IP: 89.216.113.73", Severity: "warning", First: "16:40", Count: 1},
		{Title: "Disk usage high", Severity: "warning", Count: 2},
		{Title: "Admin jelena accessed from new IP: 89.216.113.73", Severity: "warning", First: "16:38", Count: 1, Resolved: true},
		{Title: "Admin stefan accessed from new IP: 1.2.3.4", Severity: "warning", First: "09:00", Count: 1},
	})
	if len(got) != 3 {
		t.Fatalf("rows: %+v", got)
	}
	if got[0].Title != "Admins jelena and mirko accessed from new IP: 89.216.113.73" || got[0].Count != 2 || got[0].First != "16:38" || got[0].Resolved {
		t.Errorf("merged row: %+v", got[0])
	}
	if got[2].Title != "Admin stefan accessed from new IP: 1.2.3.4" {
		t.Errorf("single admin row: %+v", got[2])
	}
}

func TestSummarySpeedsFor(t *testing.T) {
	dir := t.TempDir()
	orig := WebsitesPageSpeedDir
	t.Cleanup(func() { WebsitesPageSpeedDir = orig })
	WebsitesPageSpeedDir = dir
	report := func(path, ts, site string, desktop, mobile float64) {
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(`{"timestamp":"`+ts+`","website":"`+site+`","desktop_speed":{"performance_score":`+strconv.FormatFloat(desktop, 'f', 2, 64)+`,"first_contentful_paint":"0.5 s"},"mobile_speed":{"performance_score":`+strconv.FormatFloat(mobile, 'f', 2, 64)+`,"first_contentful_paint":"2.3 s","interactive":"8.9 s"}}`), 0644)
	}
	// the day's copy, and the current report already overwritten by the next morning's check
	report(filepath.Join(dir, "history", "2026-11-17", "a.com.json"), "2026-11-17 04:00:10", "a.com", 0.79, 0.41)
	report(filepath.Join(dir, "a.com.json"), "2026-11-18 04:00:10", "a.com", 0.95, 0.90)
	report(filepath.Join(dir, "b.com.json"), "2026-11-17 04:01:00", "b.com", 0.50, 0.20)

	got := summarySpeedsFor(nil, "2026-11-17")
	if len(got) != 2 {
		t.Fatalf("speeds: %+v", got)
	}
	slow := slowestSites([]summaryServerDay{{Name: "s1", Day: &summaryDay{Speeds: got}}}, 5)
	if slow[0].Name != "b.com" || slow[0].Mobile != 20 || slow[1].Name != "a.com" || slow[1].Mobile != 41 || slow[1].Desktop != 79 {
		t.Errorf("slowest: %+v", slow)
	}
	if n := len(summarySpeedsFor(nil, "2026-11-18")); n != 1 {
		t.Errorf("next day should only see a.com's new check, got %d", n)
	}
}
