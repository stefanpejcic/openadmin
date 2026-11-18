package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DailySummaryDir is where openadmin daily-summary saves each day's admin summary, the page only reads these
var DailySummaryDir = "/etc/openpanel/openadmin/summaries"

const dailySummaryRetentionDays = 30

// storedDailySummary is everything the summary page shows for one day, made by openadmin daily-summary
type storedDailySummary struct {
	Date        string             `json:"date"`
	GeneratedAt time.Time          `json:"generated_at"`
	Summary     *serverSummary     `json:"summary"`
	Days        []summaryServerDay `json:"days"`
	Slowest     []summarySpeed     `json:"slowest"`
	WAF         *summaryWAF        `json:"waf"`
}

// GenerateDailySummary builds day's summary the way the page used to on open (every member on a master) and saves it, older files than the retention go away
func GenerateDailySummary(db *sql.DB, day, hostname string) (string, error) {
	if day == "" {
		day = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	if !summaryDayRe.MatchString(day) {
		return "", fmt.Errorf("invalid date %q, use YYYY-MM-DD", day)
	}
	// links to other servers are signed for user 0 here and signed again for whoever opens the page
	var s *serverSummary
	switch {
	case db == nil:
	case ClusterManager != nil && ClusterManager.IsMaster():
		s = cachedClusterSummaryDay(db, 0, day)
	default:
		s = cachedServerSummaryDay(db, "", day)
	}
	days := summaryServerDays(db, 0, day, hostname)
	out := storedDailySummary{Date: day, GeneratedAt: time.Now(), Summary: withMalware(s, days), Days: days, Slowest: slowestSites(days, 5), WAF: combinedWAF(days)}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(DailySummaryDir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(DailySummaryDir, day+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	pruneDailySummaries(time.Now())
	return path, nil
}

func pruneDailySummaries(now time.Time) {
	oldest := now.AddDate(0, 0, -dailySummaryRetentionDays).Format("2006-01-02")
	for _, day := range storedDailySummaryDates() {
		if day < oldest {
			os.Remove(filepath.Join(DailySummaryDir, day+".json"))
		}
	}
}

// storedDailySummaryDates lists the saved days, oldest first
func storedDailySummaryDates() []string {
	entries, err := os.ReadDir(DailySummaryDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if day, ok := strings.CutSuffix(e.Name(), ".json"); ok && summaryDayRe.MatchString(day) {
			out = append(out, day)
		}
	}
	sort.Strings(out)
	return out
}

var storedSummaryCache fileCache[[]byte]

// loadStoredDailySummary reads a saved day, the newest when day is "", with the cluster open links signed for userID
func loadStoredDailySummary(day string, userID int64) *storedDailySummary {
	if day == "" {
		dates := storedDailySummaryDates()
		if len(dates) == 0 {
			return nil
		}
		day = dates[len(dates)-1]
	}
	if !summaryDayRe.MatchString(day) {
		return nil
	}
	raw, _ := storedSummaryCache.get(filepath.Join(DailySummaryDir, day+".json"), func(path string) []byte {
		b, _ := os.ReadFile(path)
		return b
	})
	if len(raw) == 0 {
		return nil
	}
	if ClusterManager != nil {
		raw = ClusterManager.ResignOpenLinks(userID, raw)
	}
	var s storedDailySummary
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// withMalware puts the day's malware findings on top of Needs attention, on a copy since the summary can be cached
func withMalware(s *serverSummary, days []summaryServerDay) *serverSummary {
	var malware []summaryFinding
	for _, sd := range days {
		for _, f := range sd.Day.Malware {
			if len(days) > 1 && f.Server == "" {
				f.Server = sd.Name
			}
			malware = append(malware, f)
		}
	}
	if len(malware) == 0 || s == nil {
		return s
	}
	c := *s
	c.Attention = append(malware, s.Attention...)
	c.AttentionTotal = len(c.Attention)
	c.MoreAttn = max(0, c.AttentionTotal-8)
	c.AttentionGroups = groupAttention(c.Attention)
	c.MainProblem = mainAttentionProblem(c.AttentionGroups)
	return &c
}
