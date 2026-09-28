// Package paneldb holds the MySQL-backed queries the dashboard needs (see
// each function's doc comment).
package paneldb

import (
	"database/sql"
)

// Counts holds the dashboard's top-line user/plan/site/domain totals.
type Counts struct {
	UserCount   int
	PlanCount   int
	SiteCount   int
	DomainCount int
}

// GetCounts returns the dashboard's top-line user/plan/site/domain totals.
func GetCounts(db *sql.DB) (Counts, error) {
	var c Counts
	err := db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM users) AS user_count,
			(SELECT COUNT(*) FROM plans) AS plan_count,
			(SELECT COUNT(*) FROM sites) AS site_count,
			(SELECT COUNT(*) FROM domains) AS domain_count
	`).Scan(&c.UserCount, &c.PlanCount, &c.SiteCount, &c.DomainCount)
	return c, err
}

// GetUserAndPlanCount returns the pre-0.3.8 dashboard's simpler 2-tuple,
// still used to decide whether a not-yet-running core container is "not
// initialized yet" vs. "actually down".
func GetUserAndPlanCount(db *sql.DB) (userCount, planCount int, err error) {
	err = db.QueryRow(`SELECT (SELECT COUNT(*) FROM users) AS user_count, (SELECT COUNT(*) FROM plans) AS plan_count`).
		Scan(&userCount, &planCount)
	return userCount, planCount, err
}
