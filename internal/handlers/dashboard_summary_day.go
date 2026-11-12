package handlers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// summaryDay is this server's own side of the daily summary: resource peaks, alerts and mail, for the admin only
type summaryDay struct {
	Health   *summaryHealth
	Problems []summaryProblem
	// alerts left out of the list once it's cut
	MoreProblems int
	Mail         *summaryMail
}

type summaryHealth struct {
	PeakLoad, PeakLoadAt string
	PeakCPU              int
	PeakCPUAt            string
	PeakRAM              int
	PeakRAMAt            string
	Disk                 int
	// 5 minute sentinel runs that had a failing check
	FailedRuns, Runs int
}

type summaryProblem struct {
	Title, Severity, First string
	Count                  int
	Resolved               bool
}

type summaryMail struct {
	Received, Delivered, Deferred, Bounced, Rejected int
	// the report's per-day Deferred column counts delivery retries, not messages, so no share of outgoing for it
	BouncePct int
	Link      string
}

// loadSummaryDay reads everything for one day ("2006-01-02"), nil when there's nothing at all
func loadSummaryDay(day string) *summaryDay {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil
	}
	out := &summaryDay{Health: summaryHealthFor(day), Mail: summaryMailFor(d)}
	out.Problems, out.MoreProblems = summaryProblemsFor(day, 8)
	if out.Health == nil && out.Mail == nil && len(out.Problems) == 0 {
		return nil
	}
	return out
}

// summaryHealthFor is the day's peaks from sentinel's 5 minute snapshots
func summaryHealthFor(day string) *summaryHealth {
	f, err := os.Open(ResourceUsageSnapshotFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	h := &summaryHealth{}
	var peakLoad float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line[:min(len(line), 20)]), day) {
			continue
		}
		var row struct {
			TS   string `json:"ts"`
			Load struct {
				M5 string `json:"5m"`
			} `json:"load"`
			CPU  int               `json:"cpu_pct"`
			Mem  struct{ Pct int } `json:"mem"`
			Disk int               `json:"disk_pct"`
			Fail int               `json:"fail"`
		}
		if json.Unmarshal(line, &row) != nil || !strings.HasPrefix(row.TS, day) {
			continue
		}
		at := row.TS[11:16]
		h.Runs++
		if row.Fail > 0 {
			h.FailedRuns++
		}
		if l, err := strconv.ParseFloat(row.Load.M5, 64); err == nil && l > peakLoad {
			peakLoad, h.PeakLoad, h.PeakLoadAt = l, row.Load.M5, at
		}
		if row.CPU > h.PeakCPU {
			h.PeakCPU, h.PeakCPUAt = row.CPU, at
		}
		if row.Mem.Pct > h.PeakRAM {
			h.PeakRAM, h.PeakRAMAt = row.Mem.Pct, at
		}
		h.Disk = row.Disk
	}
	if h.Runs == 0 {
		return nil
	}
	return h
}

// summaryProblemsFor groups the day's warning and critical alerts by title, most frequent first
func summaryProblemsFor(day string, limit int) ([]summaryProblem, int) {
	raw, err := os.ReadFile(NotificationsLogPath)
	if err != nil {
		return nil, 0
	}
	byTitle := map[string]*summaryProblem{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, day) {
			continue
		}
		var n struct {
			Time, Status, Severity, Title string
			LastSeen                      string `json:"last_seen"`
			Count                         int
		}
		if json.Unmarshal([]byte(line), &n) != nil || (n.Severity != "warning" && n.Severity != "critical") {
			continue
		}
		// an alert counts for the day it started or the day it was last seen again
		if !strings.HasPrefix(n.Time, day) && !strings.HasPrefix(n.LastSeen, day) {
			continue
		}
		p := byTitle[n.Title]
		if p == nil {
			p = &summaryProblem{Title: n.Title, Severity: n.Severity, Resolved: true}
			if strings.HasPrefix(n.Time, day) && len(n.Time) >= 16 {
				p.First = n.Time[11:16]
			}
			byTitle[n.Title] = p
		}
		p.Count += max(n.Count, 1)
		if n.Severity == "critical" {
			p.Severity = "critical"
		}
		if n.Status != "resolved" {
			p.Resolved = false
		}
	}
	list := make([]summaryProblem, 0, len(byTitle))
	for _, p := range byTitle {
		list = append(list, *p)
	}
	sort.Slice(list, func(i, j int) bool {
		if (list[i].Severity == "critical") != (list[j].Severity == "critical") {
			return list[i].Severity == "critical"
		}
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Title < list[j].Title
	})
	more := 0
	if len(list) > limit {
		more = len(list) - limit
		list = list[:limit]
	}
	return list, more
}

var mailDayRowRe = regexp.MustCompile(`<tr><td>([A-Z][a-z]{2} \d{1,2} \d{4})</td><td>(\d+)</td><td>(\d+)</td><td>(\d+)</td><td>(\d+)</td><td>(\d+)</td></tr>`)

// summaryMailFor is the day's row of the mail report's per-day table, from the report made the morning after since that one has the whole day
func summaryMailFor(d time.Time) *summaryMail {
	want := d.Format("Jan 2 2006")
	for _, day := range []time.Time{d.AddDate(0, 0, 1), d} {
		name := day.Format("2006-Jan-2") + ".html"
		raw, err := os.ReadFile(filepath.Join(EmailsReportsDataDir, name))
		if err != nil {
			continue
		}
		for _, m := range mailDayRowRe.FindAllStringSubmatch(string(raw), -1) {
			if m[1] != want {
				continue
			}
			n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
			out := &summaryMail{Received: n(2), Delivered: n(3), Deferred: n(4), Bounced: n(5), Rejected: n(6), Link: "/emails/data/" + name}
			if out.Delivered+out.Bounced > 0 {
				out.BouncePct = out.Bounced * 100 / (out.Delivered + out.Bounced)
			}
			return out
		}
	}
	return nil
}
