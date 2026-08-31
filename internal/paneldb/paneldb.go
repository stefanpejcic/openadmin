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

// DockerContexts returns the number of distinct per-user podman contexts,
// plus 1 for root's own local/default stack.
func DockerContexts(db *sql.DB) (int, error) {
	contexts, err := UserContexts(db)
	if err != nil {
		return 0, err
	}
	return len(contexts) + 1, nil
}

// UserContexts returns the distinct per-user podman "server" contexts
// (without the "+1 for default" adjustment DockerContexts applies) -- used
// to enumerate contexts to query for container counts.
func UserContexts(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT server FROM users WHERE username NOT LIKE 'SUSPENDED\_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var server sql.NullString
		if err := rows.Scan(&server); err != nil {
			return nil, err
		}
		if server.Valid && server.String != "" {
			out = append(out, server.String)
		}
	}
	return out, rows.Err()
}
