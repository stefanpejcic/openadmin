package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// AIUsageUsersDir is where the panel keeps each user's ai_usage.json
var AIUsageUsersDir = "/etc/openpanel/openpanel/core/users"

type aiUsageRow struct {
	Username     string
	Requests     int
	Actions      int
	InputTokens  int64
	OutputTokens int64
	LastUsed     string
}

type aiUsageReport struct {
	Month  string
	Rows   []aiUsageRow
	Totals aiUsageRow
}

// loadAIUsage reads this month's AI Assistant usage for every user, busiest first
func loadAIUsage() aiUsageReport {
	month := time.Now().UTC().Format("2006-01")
	report := aiUsageReport{Month: month}
	paths, _ := filepath.Glob(filepath.Join(AIUsageUsersDir, "*", "ai_usage.json"))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var f struct {
			Months map[string]struct {
				Requests     int   `json:"requests"`
				Actions      int   `json:"actions"`
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"months"`
			LastUsed string `json:"last_used"`
			// files written before per-month history
			Month    string `json:"month"`
			Requests int    `json:"requests"`
		}
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		row := aiUsageRow{Username: filepath.Base(filepath.Dir(p))}
		if m, ok := f.Months[month]; ok {
			row.Requests, row.Actions, row.InputTokens, row.OutputTokens = m.Requests, m.Actions, m.InputTokens, m.OutputTokens
		} else if f.Month == month {
			row.Requests = f.Requests
		}
		if row.Requests == 0 && row.Actions == 0 {
			continue
		}
		if t, err := time.Parse(time.RFC3339, f.LastUsed); err == nil {
			row.LastUsed = t.Local().Format("2006-01-02 15:04")
		}
		report.Rows = append(report.Rows, row)
		report.Totals.Requests += row.Requests
		report.Totals.Actions += row.Actions
		report.Totals.InputTokens += row.InputTokens
		report.Totals.OutputTokens += row.OutputTokens
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].Requests != report.Rows[j].Requests {
			return report.Rows[i].Requests > report.Rows[j].Requests
		}
		return report.Rows[i].Username < report.Rows[j].Username
	})
	return report
}
