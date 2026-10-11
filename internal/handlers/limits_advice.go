package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"

	"openadmin/internal/paneldb"
)

// limitsHost is what the Service Limits recommendations are based on
type limitsHost struct {
	Cores          int
	RAMGB          float64
	Modules        []string
	Users, Domains int
}

// limitsHostRun is swapped out in tests
var limitsHostRun = func(db *sql.DB) limitsHost {
	h := limitsHost{
		Cores:   runtime.NumCPU(),
		RAMGB:   float64(readMemInfo()["MemTotal"]) / (1024 * 1024 * 1024),
		Modules: modulesEnabledList(ModulesConfigFilePath),
	}
	if db != nil {
		if c, err := paneldb.GetCounts(db); err == nil {
			h.Users, h.Domains = c.UserCount, c.DomainCount
		}
	}
	return h
}

// limitAdvice is the range a field should sit in, 0 means no bound on that side
type limitAdvice struct {
	Low, High         float64
	LowText, HighText string
}

func ceil1(f float64) float64 { return math.Ceil(f*10) / 10 }

func fmtLimit(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// parseSizeGB reads php style sizes like "2048M" or "2G" as GB
func parseSizeGB(v string) float64 {
	v = strings.ToUpper(strings.TrimSpace(v))
	div := 1024.0
	if strings.HasSuffix(v, "G") {
		div = 1
	}
	f, err := strconv.ParseFloat(strings.TrimRight(v, "MGK"), 64)
	if err != nil {
		return 0
	}
	return f / div
}

// adviseLimits fills the recommended range for each CPU/RAM row (and ClamAV's startup delay) from the server, modules and panel size
func adviseLimits(groups []limitsGroup, raw map[string]map[string]string, h limitsHost) []limitsGroup {
	unit := func(isCPU bool) string {
		if isCPU {
			return "CPU"
		}
		return "GB RAM"
	}
	for gi := range groups {
		g := &groups[gi]
		for ri := range g.Rows {
			r := &g.Rows[ri]
			isCPU := strings.HasSuffix(r.Label, "CPU") || strings.HasSuffix(r.Label, "CPUS")
			isRAM := strings.HasSuffix(r.Label, "RAM")
			if g.Name == "CLAMD" && r.Label == "STARTUP_DELAY" && hasModule(h.Modules, "malware_scan") {
				r.Advice = limitAdvice{30, 600,
					"Recommended: at least 30 seconds, ClamAV downloads its malware signature database on startup.",
					"Recommended: no more than 600 seconds."}
				continue
			}
			if !isCPU && !isRAM {
				continue
			}
			server := h.RAMGB
			if isCPU {
				server = float64(h.Cores)
			}
			switch g.Name {
			case "OPENPANEL":
				if h.RAMGB > 4 {
					r.Advice = limitAdvice{Low: 2, LowText: "Recommended: at least 2 on servers with over 4 GB RAM (1 is the minimum)."}
				} else {
					r.Advice = limitAdvice{Low: 1, LowText: "Recommended: at least 1."}
				}
			case "BIND9":
				if hasModule(h.Modules, "dns") {
					r.Advice = limitAdvice{Low: 0.1, LowText: "Recommended: at least 0.1, the DNS module is enabled so BIND9 serves every domain's zone."}
				}
			case "CADDY":
				max := 8.0
				if isCPU {
					max = 6
				}
				rec := math.Ceil(float64(h.Domains) / 20)
				why := fmt.Sprintf("1 per 20 domains (%d domains) for SSL issuing and renewals", h.Domains)
				if h.RAMGB < 10 && ceil1(server/3) > rec {
					rec = ceil1(server / 3)
					why = "a third of the server's " + unit(isCPU) + " on servers under 10 GB RAM"
				}
				rec = math.Min(math.Max(rec, 1), max)
				r.Advice = limitAdvice{rec, max,
					fmt.Sprintf("Recommended: at least %s, %s.", fmtLimit(rec), why),
					fmt.Sprintf("Recommended: no more than %s, Caddy doesn't use more.", fmtLimit(max))}
			case "CLAMAV":
				if hasModule(h.Modules, "malware_scan") {
					r.Advice = limitAdvice{4, 6, "Recommended: 4 (2 is the minimum) so scans finish in reasonable time.", "Recommended: no more than 6."}
				}
			case "FTP":
				if hasModule(h.Modules, "ftp") {
					max := 3.0
					if isCPU {
						max = 2
					}
					rec := math.Min(ceil1(0.5+0.1*float64(h.Users/10)), max)
					r.Advice = limitAdvice{rec, max,
						fmt.Sprintf("Recommended: at least %s, 0.5 plus 0.1 per 10 users (%d users).", fmtLimit(rec), h.Users),
						fmt.Sprintf("Recommended: no more than %s.", fmtLimit(max))}
				}
			case "MYSQL":
				rec := ceil1(0.05 * float64(h.Users))
				key := "RAM"
				if isCPU {
					key = "CPUS"
				}
				max := 0.0
				if op := raw["OPENPANEL"][key]; op != "" {
					opv := parseRAMLimitGB(op)
					if isCPU {
						opv = parseCPULimit(op)
					}
					if opv > 0 {
						max = ceil1(opv / 3)
					}
				}
				if max > 0 && rec > max {
					rec = max
				}
				a := limitAdvice{High: max, HighText: fmt.Sprintf("Recommended: no more than %s, a third of the OpenPanel container's limit.", fmtLimit(max))}
				if rec > 0 {
					a.Low, a.LowText = rec, fmt.Sprintf("Recommended: at least %s, 0.5 per 10 users (%d users).", fmtLimit(rec), h.Users)
				}
				r.Advice = a
			case "PHPMYADMIN":
				if !hasModule(h.Modules, "phpmyadmin") {
					continue
				}
				if isCPU {
					r.Advice = limitAdvice{Low: 2, LowText: "Recommended: at least 2."}
					continue
				}
				rec, why := 1.0, "the minimum"
				if m := parseSizeGB(raw["PHPMYADMIN"]["MEMORY_LIMIT"]); m > rec {
					rec, why = m, "to fit PHPMYADMIN_MEMORY_LIMIT below"
				}
				if u := parseSizeGB(raw["PHPMYADMIN"]["UPLOAD_LIMIT"]); u > rec {
					rec, why = u, "to fit PHPMYADMIN_UPLOAD_LIMIT below"
				}
				rec = ceil1(rec)
				r.Advice = limitAdvice{Low: rec, LowText: fmt.Sprintf("Recommended: at least %s, %s.", fmtLimit(rec), why)}
			}
		}
	}
	return groups
}
