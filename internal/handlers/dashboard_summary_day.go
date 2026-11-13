package handlers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"openadmin/internal/cluster"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// summaryDay is this server's own side of the daily summary: resource peaks, alerts and mail, for the admin only
type summaryDay struct {
	// this server's name, the sections only cover the server the admin is on
	Host     string
	Health   *summaryHealth
	Problems []summaryProblem
	// alerts left out of the list once it's cut
	MoreProblems int
	Mail         *summaryMail
	// accounts where ImunifyAV found malware that day, they go to the top of Needs attention
	Malware []summaryFinding
	// the admin configured account backup run that day, nil when backups aren't admin configured or nothing was scheduled
	Backup *summaryBackup
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
	// load is shown against the server's cores, 100% means every core was busy
	Cores   int
	LoadPct int
}

type summaryProblem struct {
	Title, Severity, First string
	Count                  int
	Resolved               bool
	// the addresses of alerts that only differ by IP, like an admin logging in from new IPs, merged into one row
	IPs []string
}

// trailingIPRe splits "Admin stefan accessed from new IP: 1.2.3.4" into the alert and its address
var trailingIPRe = regexp.MustCompile(`^(.+?):\s*([0-9a-fA-F.:]+)$`)

type summaryMail struct {
	Received, Delivered, Deferred, Bounced, Rejected int
	// the report's per-day Deferred column counts delivery retries, not messages, so no share of outgoing for it
	BouncePct int
	Link      string
}

// loadSummaryDay reads everything for one day ("2006-01-02"), nil when there's nothing at all
func loadSummaryDay(db *sql.DB, day string) *summaryDay {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil
	}
	out := &summaryDay{Health: summaryHealthFor(day), Mail: summaryMailFor(d), Malware: summaryMalwareFor(db, day), Backup: summaryBackupFor(day)}
	out.Problems, out.MoreProblems = summaryProblemsFor(day, 8)
	if out.Health == nil && out.Mail == nil && len(out.Problems) == 0 && len(out.Malware) == 0 && out.Backup == nil {
		return nil
	}
	return out
}

// summaryMalwareFor is every account an ImunifyAV scan found malware on that day, from the scan notifications
func summaryMalwareFor(db *sql.DB, day string) []summaryFinding {
	raw, err := os.ReadFile(NotificationsLogPath)
	if err != nil {
		return nil
	}
	type hit struct {
		malicious int
		files     []string
	}
	byContext := map[string]*hit{}
	var order []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "imunify_scan") || !strings.Contains(line, day) {
			continue
		}
		var n struct {
			Time, LastSeen string
			Details        struct {
				User           string   `json:"user"`
				Malicious      int      `json:"malicious"`
				MaliciousFiles []string `json:"malicious_files"`
			} `json:"details"`
		}
		var ls struct {
			LastSeen string `json:"last_seen"`
		}
		_ = json.Unmarshal([]byte(line), &ls)
		if json.Unmarshal([]byte(line), &n) != nil || n.Details.Malicious <= 0 || n.Details.User == "" {
			continue
		}
		if !strings.HasPrefix(n.Time, day) && !strings.HasPrefix(ls.LastSeen, day) {
			continue
		}
		h := byContext[n.Details.User]
		if h == nil {
			h = &hit{}
			byContext[n.Details.User] = h
			order = append(order, n.Details.User)
		}
		// the newest scan of the day is what's still there
		h.malicious, h.files = n.Details.Malicious, n.Details.MaliciousFiles
	}
	var out []summaryFinding
	for _, ctx := range order {
		h := byContext[ctx]
		username := ctx
		if db != nil {
			var u string
			if db.QueryRow("SELECT username FROM users WHERE server = ? LIMIT 1", ctx).Scan(&u) == nil && u != "" {
				username = u
			}
		}
		// the user knows the files by the path inside the panel, not the host volume path
		prefix := "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/"
		var shown []string
		for _, f := range h.files[:min(3, len(h.files))] {
			if rest, ok := strings.CutPrefix(f, prefix); ok {
				f = "/var/www/html/" + rest
			}
			shown = append(shown, f)
		}
		detail := strings.Join(shown, ", ")
		if h.malicious > len(shown) {
			detail += fmt.Sprintf(" and %d more", h.malicious-len(shown))
		}
		title := fmt.Sprintf("ImunifyAV found %d malicious file", h.malicious)
		if h.malicious > 1 {
			title += "s"
		}
		out = append(out, summaryFinding{Username: username, Title: title, Detail: detail, Link: "/security/imunify/" + username})
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
	h.Cores = runtime.NumCPU()
	if h.Cores > 0 {
		h.LoadPct = min(100, int(peakLoad*100/float64(h.Cores)))
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
		title, ip := n.Title, ""
		if m := trailingIPRe.FindStringSubmatch(n.Title); m != nil && net.ParseIP(m[2]) != nil {
			title, ip = m[1], m[2]
		}
		p := byTitle[title]
		if p == nil {
			p = &summaryProblem{Title: title, Severity: n.Severity, Resolved: true}
			byTitle[title] = p
		}
		if strings.HasPrefix(n.Time, day) && len(n.Time) >= 16 && (p.First == "" || n.Time[11:16] < p.First) {
			p.First = n.Time[11:16]
		}
		if ip != "" && !slices.Contains(p.IPs, ip) {
			p.IPs = append(p.IPs, ip)
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
		// one address reads like the original alert, several get listed under it
		if len(p.IPs) == 1 {
			p.Title += ": " + p.IPs[0]
			p.IPs = nil
		} else if strings.HasSuffix(p.Title, " IP") {
			p.Title += "s"
		}
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

// summaryServerDay is one server's health and mail on the summary page, a master shows one per member
type summaryServerDay struct {
	Name  string
	Color string
	Day   *summaryDay
	// the mail report link, through the cluster's open link for another server
	MailLink string
}

// ClusterSummaryHealth is this server's peaks, alerts, mail and malware for a master's summary
func ClusterSummaryHealth(db *sql.DB) func(day string) any {
	return func(day string) any {
		if d := loadSummaryDay(db, day); d != nil {
			return d
		}
		return struct{}{}
	}
}

// summaryServerDays is this server's day, plus every member's on a cluster master, servers without any data are left out
func summaryServerDays(db *sql.DB, userID int64, day, hostname string) []summaryServerDay {
	var out []summaryServerDay
	master := ClusterManager != nil && ClusterManager.IsMaster()
	if d := loadSummaryDay(db, day); d != nil {
		sd := summaryServerDay{Name: hostname, Day: d}
		if d.Mail != nil {
			sd.MailLink = d.Mail.Link
		}
		if master {
			sd.Color = cluster.ColorClass(ClusterManager.ColorOf(cluster.LocalNode))
		}
		out = append(out, sd)
	}
	if !master {
		return out
	}
	var remote []summaryServerDay
	var mu sync.Mutex
	ClusterManager.CallAllData("summary_health", day, func(id, name string, raw json.RawMessage) {
		var d summaryDay
		if json.Unmarshal(raw, &d) != nil || (d.Health == nil && d.Mail == nil && len(d.Problems) == 0 && len(d.Malware) == 0 && d.Backup == nil) {
			return
		}
		if d.Backup != nil {
			d.Backup.Link = ClusterManager.OpenLink(userID, id, d.Backup.Link)
		}
		for i := range d.Malware {
			d.Malware[i].Server = name
			d.Malware[i].Link = ClusterManager.OpenLink(userID, id, d.Malware[i].Link)
		}
		sd := summaryServerDay{Name: name, Day: &d, Color: cluster.ColorClass(ClusterManager.ColorOf(id))}
		if d.Mail != nil {
			sd.MailLink = ClusterManager.OpenLink(userID, id, d.Mail.Link)
		}
		mu.Lock()
		remote = append(remote, sd)
		mu.Unlock()
	})
	sort.Slice(remote, func(i, j int) bool { return remote[i].Name < remote[j].Name })
	return append(out, remote...)
}

// dailyReportSummary is yesterday's summary for the Daily Usage Report email: the accounts added up (every member on a master) and each server's health and mail
func dailyReportSummary(db *sql.DB, hostname string) (*serverSummary, []summaryServerDay) {
	if db == nil {
		return nil, nil
	}
	var s *serverSummary
	if summaryEnabled() {
		if ClusterManager != nil && ClusterManager.IsMaster() {
			s = cachedClusterSummary(db, 0)
		} else {
			s = cachedServerSummary(db, "")
		}
	}
	day := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if s != nil {
		day = s.Date
	}
	days := summaryServerDays(db, 0, day, hostname)
	var malware []summaryFinding
	for _, sd := range days {
		for _, f := range sd.Day.Malware {
			if len(days) > 1 && f.Server == "" {
				f.Server = sd.Name
			}
			malware = append(malware, f)
		}
	}
	if s != nil && len(malware) > 0 {
		withMalware := *s
		withMalware.Attention = append(malware, s.Attention...)
		withMalware.AttentionTotal = len(withMalware.Attention)
		withMalware.MoreAttn = max(0, withMalware.AttentionTotal-5)
		withMalware.AttentionGroups = groupAttention(withMalware.Attention)
		withMalware.MainProblem = mainAttentionProblem(withMalware.AttentionGroups)
		s = &withMalware
	} else if s != nil {
		cp := *s
		cp.MoreAttn = max(0, cp.AttentionTotal-5)
		s = &cp
	}
	return s, days
}
