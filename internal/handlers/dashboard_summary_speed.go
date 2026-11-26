package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"openadmin/internal/paneldb"
)

// summarySpeed is one website's PageSpeed check from that day
type summarySpeed struct {
	Name, Username, Link, Server string
	Desktop, Mobile              int
	// first contentful paint and time to interactive, like "2.3 s"
	MobileFCP, MobileInteractive, DesktopFCP string
	Checked                      string
}

// Favicon is the website's icon, same source as the websites table
func (s summarySpeed) Favicon() string { return faviconBase() + s.Name }

// RingColor is the desktop score ring, same as the websites table
func (s summarySpeed) RingColor() string { return speedRingColor(s.Desktop) }

// DashOffset draws the ring up to the desktop score
func (s summarySpeed) DashOffset() int { return 100 - s.Desktop }

// summarySpeedsFor is every PageSpeed check that ran on day, from the per-day copies websites-pagespeed keeps, or the current report while it's still from that day
func summarySpeedsFor(db *sql.DB, day string) []summarySpeed {
	files, _ := filepath.Glob(filepath.Join(WebsitesPageSpeedDir, "history", day, "*.json"))
	seen := map[string]bool{}
	for _, f := range files {
		seen[filepath.Base(f)] = true
	}
	current, _ := filepath.Glob(filepath.Join(WebsitesPageSpeedDir, "*.json"))
	for _, f := range current {
		if !seen[filepath.Base(f)] {
			files = append(files, f)
		}
	}
	owners := map[string]string{}
	if db != nil {
		if rows, err := paneldb.GetAllDomains(db); err == nil {
			for _, r := range rows {
				owners[fmt.Sprint(r["domain_url"])] = fmt.Sprint(r["username"])
			}
		}
	}
	var out []summarySpeed
	for _, f := range files {
		p := parsePageSpeed(f)
		// only checks made that day, the current report gets overwritten every morning
		if p == nil || !strings.HasPrefix(p.Checked, day) {
			continue
		}
		var meta struct {
			Website string `json:"website"`
		}
		raw, _ := os.ReadFile(f)
		if json.Unmarshal(raw, &meta) != nil || meta.Website == "" {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(meta.Website, "https://"), "http://")
		domain, _, _ := strings.Cut(name, "/")
		s := summarySpeed{Name: name, Username: owners[domain], Desktop: p.Desktop.Score, Mobile: p.Mobile.Score,
			MobileFCP: p.Mobile.FCP, MobileInteractive: p.Mobile.Interactive, DesktopFCP: p.Desktop.FCP, Checked: p.Checked}
		if s.Username != "" {
			s.Link = "/users/" + s.Username
		}
		out = append(out, s)
	}
	return out
}

// slowestSites is the worst PageSpeed scores of the day across the servers, slowest on mobile first since most visitors are on phones
func slowestSites(days []summaryServerDay, limit int) []summarySpeed {
	var all []summarySpeed
	for _, sd := range days {
		for _, s := range sd.Day.Speeds {
			if len(days) > 1 && s.Server == "" {
				s.Server = sd.Name
			}
			all = append(all, s)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Mobile != all[j].Mobile {
			return all[i].Mobile < all[j].Mobile
		}
		if all[i].Desktop != all[j].Desktop {
			return all[i].Desktop < all[j].Desktop
		}
		return all[i].Name < all[j].Name
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}
