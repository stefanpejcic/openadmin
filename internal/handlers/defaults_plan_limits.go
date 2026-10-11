package handlers

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// planMinimums is the smallest CPU and RAM limit among plans that have users, unlimited (0) plans are skipped
type planMinimums struct {
	CPU, RAMGB       float64
	CPUPlan, RAMPlan string
}

// services whose limit should cover what a user's plan allows, PHP-FPM groups always count
var planLimitedServices = map[string]bool{
	"NGINX": true, "APACHE": true, "OPENRESTY": true, "OPENLITESPEED": true, "MYSQL": true, "MARIADB": true,
}

type planLimitRow struct {
	name, cpu, ram string
}

func minPlanLimits(plans []planLimitRow) planMinimums {
	var m planMinimums
	for _, p := range plans {
		if cpu := parseCPULimit(p.cpu); cpu > 0 && (m.CPU == 0 || cpu < m.CPU) {
			m.CPU, m.CPUPlan = cpu, p.name
		}
		if ram := parseRAMLimitGB(p.ram); ram > 0 && (m.RAMGB == 0 || ram < m.RAMGB) {
			m.RAMGB, m.RAMPlan = ram, p.name
		}
	}
	return m
}

func parseCPULimit(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return f
}

// plans store RAM like "2g", "512m" or "2G"
func parseRAMLimitGB(v string) float64 {
	v = strings.ToLower(strings.TrimSpace(v))
	div := 1.0
	switch {
	case strings.HasSuffix(v, "m"):
		v, div = strings.TrimSuffix(v, "m"), 1024
	case strings.HasSuffix(v, "g"):
		v = strings.TrimSuffix(v, "g")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f / div
}

func applyPlanMinimums(groups []defaultsGroupView, m planMinimums) []defaultsGroupView {
	for gi := range groups {
		g := &groups[gi]
		if !g.IsPHPFPM && !planLimitedServices[g.Name] {
			continue
		}
		for ri := range g.Rows {
			r := &g.Rows[ri]
			switch {
			case strings.HasSuffix(r.Label, "CPU") && m.CPU > 0:
				r.MinRec = m.CPU
				r.MinRecText = fmt.Sprintf("Users on the %s plan get %s CPU, below that this service can't use all of it. Recommended: at least %s.", m.CPUPlan, formatLimit(m.CPU), formatLimit(m.CPU))
			case strings.HasSuffix(r.Label, "RAM") && m.RAMGB > 0:
				r.MinRec = m.RAMGB
				r.MinRecText = fmt.Sprintf("Users on the %s plan get %s GB RAM, below that this service can't use all of it. Recommended: at least %s.", m.RAMPlan, formatLimit(m.RAMGB), formatLimit(m.RAMGB))
			}
		}
	}
	return groups
}

func formatLimit(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// defaultsPlanMinimumsRun is swapped out in tests, plans without users only count when no plan has any
var defaultsPlanMinimumsRun = func(db *sql.DB) planMinimums {
	if db == nil {
		return planMinimums{}
	}
	query := func(q string) []planLimitRow {
		rows, err := db.Query(q)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []planLimitRow
		for rows.Next() {
			var name string
			var cpu, ram sql.NullString
			if rows.Scan(&name, &cpu, &ram) == nil {
				out = append(out, planLimitRow{name, cpu.String, ram.String})
			}
		}
		return out
	}
	plans := query(`SELECT p.name, p.cpu, p.ram FROM plans p WHERE EXISTS (SELECT 1 FROM users u WHERE u.plan_id = p.id)`)
	if len(plans) == 0 {
		plans = query(`SELECT name, cpu, ram FROM plans`)
	}
	return minPlanLimits(plans)
}
