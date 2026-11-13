package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// where docker-backup leaves each account's index of its archives, after the reindex at the end of a backup
var backupHomeDir = "/home/"

// summaryBackup is the admin configured account backup run of that day
type summaryBackup struct {
	Schedule string
	// false when the day was scheduled but no run started
	Ran      bool
	Finished bool
	Started  string
	Duration string
	// accounts the run picked up, backed up fine, failed or skipped because another backup of them was running
	Accounts, Done int
	Failed         []summaryBackupAccount
	Locked         []string
	// accounts left out with backup.off or because they run their own backups
	Excluded, SelfManaged int
	// a run that couldn't start at all, like the account list not loading
	Error string
	Size  string
	// what went into the archives, e.g. "files, 12 databases, mail"
	Contents string
	// largest archives first
	Largest []summaryBackupAccount
	OK      bool
	Link    string
	// one line on how it went and "ok", "warn" or "bad" for its color
	Status, Tone string
}

type summaryBackupAccount struct {
	Username, Duration, Size, Error string

	context string
	archive string
	seconds int
	bytes   int64
	done    bool
	failed  bool
}

var (
	backupLogLineRe   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}:\d{2}) : (.*)$`)
	backupDoneRe      = regexp.MustCompile(`^Backup completed for user: (\S+) \(context: (\S+)\) \| Time taken: (\d+)s`)
	backupFailedRe    = regexp.MustCompile(`^(?:ERROR: )?Backup FAILED for user: (\S+) \(context: (\S+)\)(?: \| exit code: (\d+) \| Time taken: (\d+)s)?(?: — (.+))?`)
	backupLockedRe    = regexp.MustCompile(`^Skipping user: (\S+) \(a backup of this account is already running\)`)
	backupSkippedRe   = regexp.MustCompile(`^Skipped (\d+) users (with backup.off|that manage their own backups)`)
	backupFoundRe     = regexp.MustCompile(`^Found (\d+) active users to process`)
	backupTotalRe     = regexp.MustCompile(`^=== Backup finished at .* \| Total time: (\d+)s ===`)
	backupBlockRe     = regexp.MustCompile(`^--- (end of )?backup container output for user: (\S+) ---$`)
	backupArchiveRe   = regexp.MustCompile("(?:Created|Encrypted) backup .*`([^`]+)`")
	backupContErrorRe = regexp.MustCompile(`level=ERROR msg="((?:[^"\\]|\\.)*)"`)
)

// backupScheduledOn is the cron schedule firing at some point of day, after the schedule was saved and not still ahead
func backupScheduledOn(schedule string, day time.Time, savedAt time.Time) bool {
	sched, err := cron.ParseStandard(schedule)
	if err != nil {
		return false
	}
	next := sched.Next(day.Add(-time.Second))
	return !next.IsZero() && next.Before(day.AddDate(0, 0, 1)) && next.After(savedAt) && next.Before(time.Now())
}

// summaryBackupFor reads the day's docker-backup run, nil when backups aren't admin configured or that day had nothing scheduled
func summaryBackupFor(day string) *summaryBackup {
	schedule := backupsUserSchedule()
	if schedule == backupScheduleChoices["disabled"] {
		return nil
	}
	d, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return nil
	}
	var savedAt time.Time
	if info, err := os.Stat(CronFilePath); err == nil {
		savedAt = info.ModTime()
	}
	out := parseBackupRun(DockerBackupLogPath, day)
	if out == nil {
		if !backupScheduledOn(schedule, d, savedAt) {
			return nil
		}
		out = &summaryBackup{}
	}
	out.Schedule = backupScheduleLabel(schedule)
	out.Link = "/backups/user#runs"
	if out.Ran {
		addBackupSizes(out)
	}
	out.Status, out.Tone = backupStatus(out)
	return out
}

func backupStatus(b *summaryBackup) (string, string) {
	switch {
	case !b.Ran:
		return "The scheduled backup didn't run.", "bad"
	case b.Error != "":
		return b.Error, "bad"
	case !b.Finished:
		return fmt.Sprintf("Started at %s but never finished, %d of %d accounts were backed up.", b.Started, b.Done, b.Accounts), "bad"
	case b.Accounts == 0:
		return "Ran at " + b.Started + ", no accounts to back up.", "warn"
	case len(b.Failed) > 0 && b.Done == 0:
		return "Failed for every account.", "bad"
	case len(b.Failed) > 0:
		return fmt.Sprintf("Failed for %d of %s.", len(b.Failed), countWord(b.Accounts, "account", "accounts")), "bad"
	case len(b.Locked) > 0:
		return fmt.Sprintf("Backed up %d of %s, %d skipped because another backup of them was running.", b.Done, countWord(b.Accounts, "account", "accounts"), len(b.Locked)), "warn"
	}
	if b.Accounts == 1 {
		return "The 1 account was backed up.", "ok"
	}
	return fmt.Sprintf("All %s accounts backed up.", commaNum(b.Accounts)), "ok"
}

// parseBackupRun finds the last full run started on day in the docker-backup log, single --account runs don't count
func parseBackupRun(path, day string) *summaryBackup {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var run *summaryBackup
	var accounts map[string]*summaryBackupAccount
	var order []string
	var startAt, lastAt time.Time
	inRun := false
	block := ""
	account := func(u string) *summaryBackupAccount {
		a := accounts[u]
		if a == nil {
			a = &summaryBackupAccount{Username: u}
			accounts[u] = a
			order = append(order, u)
		}
		return a
	}
	finish := func() {
		inRun = false
		if !lastAt.IsZero() && run.Duration == "" {
			run.Duration = backupDuration(int(lastAt.Sub(startAt).Seconds()))
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		m := backupLogLineRe.FindStringSubmatch(line)
		if m == nil {
			// raw backup container output between the block markers
			if inRun && block != "" {
				a := account(block)
				if am := backupArchiveRe.FindStringSubmatch(line); am != nil {
					a.archive = filepath.Base(am[1])
				}
				if em := backupContErrorRe.FindStringSubmatch(line); em != nil && a.Error == "" {
					a.Error, _ = strconv.Unquote(`"` + em[1] + `"`)
				}
			}
			continue
		}
		msg := m[3]
		if strings.HasPrefix(msg, "=== Backup started at ") {
			if m[1] != day {
				if run != nil && inRun {
					finish()
				}
				continue
			}
			run = &summaryBackup{Ran: true, Started: m[2][:5]}
			accounts = map[string]*summaryBackupAccount{}
			order = nil
			startAt, _ = time.Parse("2006-01-02 15:04:05", m[1]+" "+m[2])
			lastAt = time.Time{}
			inRun, block = true, ""
			continue
		}
		if !inRun {
			continue
		}
		lastAt, _ = time.Parse("2006-01-02 15:04:05", m[1]+" "+m[2])
		if bm := backupBlockRe.FindStringSubmatch(msg); bm != nil {
			block = ""
			if bm[1] == "" {
				block = bm[2]
			}
			continue
		}
		switch {
		case backupFoundRe.MatchString(msg):
			run.Accounts, _ = strconv.Atoi(backupFoundRe.FindStringSubmatch(msg)[1])
		case backupSkippedRe.MatchString(msg):
			sm := backupSkippedRe.FindStringSubmatch(msg)
			n, _ := strconv.Atoi(sm[1])
			if strings.Contains(sm[2], "backup.off") {
				run.Excluded = n
			} else {
				run.SelfManaged = n
			}
		case backupDoneRe.MatchString(msg):
			dm := backupDoneRe.FindStringSubmatch(msg)
			a := account(dm[1])
			a.context, a.done = dm[2], true
			a.seconds, _ = strconv.Atoi(dm[3])
		case backupFailedRe.MatchString(msg):
			fm := backupFailedRe.FindStringSubmatch(msg)
			a := account(fm[1])
			a.context, a.failed = fm[2], true
			a.seconds, _ = strconv.Atoi(fm[4])
			switch {
			case fm[5] != "":
				a.Error = fm[5]
			case a.Error == "" && fm[3] != "":
				a.Error = "backup container exited with code " + fm[3]
			}
		case backupLockedRe.MatchString(msg):
			run.Locked = append(run.Locked, backupLockedRe.FindStringSubmatch(msg)[1])
		case strings.HasPrefix(msg, "ERROR: Could not retrieve active users"):
			run.Error = "The list of accounts could not be loaded from the database, nothing was backed up."
			run.Finished = true
			finish()
		case strings.HasPrefix(msg, "No users to back up"):
			run.Finished = true
			finish()
		case strings.HasPrefix(msg, "Failed to process "):
			run.Finished = true
			finish()
		case backupTotalRe.MatchString(msg):
			secs, _ := strconv.Atoi(backupTotalRe.FindStringSubmatch(msg)[1])
			run.Duration = backupDuration(secs)
			run.Finished = true
			finish()
		}
	}
	if run == nil {
		return nil
	}
	if inRun {
		finish()
	}
	for _, u := range order {
		a := accounts[u]
		a.Duration = backupDuration(a.seconds)
		if a.failed {
			run.Failed = append(run.Failed, *a)
			continue
		}
		if !a.done {
			continue
		}
		run.Done++
		run.Largest = append(run.Largest, *a)
	}
	if run.Accounts == 0 {
		run.Accounts = run.Done + len(run.Failed) + len(run.Locked)
	}
	run.OK = run.Finished && run.Error == "" && len(run.Failed) == 0 && len(run.Locked) == 0
	return run
}

// addBackupSizes looks each new archive up in the account's available_backups.json, which docker-backup refreshes after every backup
func addBackupSizes(run *summaryBackup) {
	var total int64
	types := map[string]bool{}
	dbs := 0
	for i := range run.Largest {
		a := &run.Largest[i]
		ctx := a.context
		if ctx == "" {
			ctx = a.Username
		}
		raw, err := os.ReadFile(filepath.Join(backupHomeDir, ctx, "available_backups.json"))
		if err != nil {
			continue
		}
		var list []struct {
			BackupFile string   `json:"backup_file"`
			Types      []string `json:"types"`
			Databases  []string `json:"databases"`
			Size       int64    `json:"size"`
		}
		if json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, b := range list {
			name := filepath.Base(b.BackupFile)
			// encryption adds .gpg or .age after the name the backup was created with
			if a.archive == "" || (name != a.archive && !strings.HasPrefix(name, a.archive+".")) {
				continue
			}
			a.bytes = b.Size
			for _, t := range b.Types {
				types[t] = true
			}
			dbs += len(b.Databases)
			break
		}
		if a.bytes > 0 {
			a.Size = humanSize(a.bytes)
			total += a.bytes
		}
	}
	if total > 0 {
		run.Size = humanSize(total)
	}
	sort.SliceStable(run.Largest, func(i, j int) bool { return run.Largest[i].bytes > run.Largest[j].bytes })
	if len(run.Largest) > 5 {
		run.Largest = run.Largest[:5]
	}

	var parts []string
	if types["html"] {
		parts = append(parts, "files")
	}
	if dbs > 0 {
		parts = append(parts, countWord(dbs, "database", "databases"))
	}
	for _, t := range []string{"mail", "dns", "crons"} {
		if types[t] {
			parts = append(parts, map[string]string{"mail": "mail", "dns": "DNS zones", "crons": "cron jobs"}[t])
		}
	}
	if types["vhosts"] && !slices.Contains(parts, "files") {
		parts = append(parts, "web server configs")
	}
	run.Contents = strings.Join(parts, ", ")
}

// backupDuration writes 754 as 12m 34s
func backupDuration(secs int) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), secs%60)
	}
	return fmt.Sprintf("%ds", secs)
}
