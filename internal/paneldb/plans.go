package paneldb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ResellerConfigDir is a var (not const) so tests can point it at a scratch
// fixture instead of the real /etc path.
var ResellerConfigDir = "/etc/openpanel/openadmin/resellers"

// AllowedPlansForReseller reads <ResellerConfigDir>/<reseller>.json's
// "allowed_plans" list. Returns (nil, false) if the reseller has no
// allowed_plans set, which callers should treat as "refuse to display any
// plans".
func AllowedPlansForReseller(reseller string) ([]int, bool) {
	path := fmt.Sprintf("%s/%s.json", ResellerConfigDir, reseller)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var parsed struct {
		AllowedPlans []int `json:"allowed_plans"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.AllowedPlans) == 0 {
		return nil, false
	}
	return parsed.AllowedPlans, true
}

// GetAllPlansAndUserCount returns every plan column plus a
// COUNT(users.id) user_count, optionally restricted to allowedPlanIDs
// (pass nil for an unrestricted/admin query).
func GetAllPlansAndUserCount(db *sql.DB, allowedPlanIDs []int) ([]RowMap, error) {
	query := `
		SELECT
			plans.*,
			COUNT(users.id) AS user_count
		FROM
			plans
		LEFT JOIN
			users ON users.plan_id = plans.id
	`
	var args []interface{}
	if allowedPlanIDs != nil {
		placeholders := make([]string, len(allowedPlanIDs))
		for i, id := range allowedPlanIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += " WHERE plans.id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " GROUP BY plans.id"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// GetAllPlans returns plain plan rows (no user_count join), optionally
// restricted to allowedPlanIDs.
func GetAllPlans(db *sql.DB, allowedPlanIDs []int) ([]RowMap, error) {
	query := "SELECT * FROM plans"
	var args []interface{}
	if allowedPlanIDs != nil {
		placeholders := make([]string, len(allowedPlanIDs))
		for i, id := range allowedPlanIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += " WHERE plans.id IN (" + strings.Join(placeholders, ",") + ")"
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// GetPlanByID returns a single plan row by ID.
func GetPlanByID(db *sql.DB, planID string) (RowMap, error) {
	rows, err := db.Query(`SELECT * FROM plans WHERE id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	maps, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(maps) == 0 {
		return nil, sql.ErrNoRows
	}
	return maps[0], nil
}
