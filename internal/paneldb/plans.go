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

// EnsurePlansSchema adds the plans.upsell_plan_id / plans.upsell_url
// columns if they don't already exist. The plans table itself is owned
// and created elsewhere (the opencli installer, outside this repo), so
// this is a best-effort ALTER TABLE guard -- called once at startup,
// non-fatal on failure -- rather than a full migration owner.
// https://github.com/stefanpejcic/OpenPanel/discussions/1079
func EnsurePlansSchema(db *sql.DB) error {
	existing, err := plansColumns(db)
	if err != nil {
		return err
	}
	if !existing["upsell_plan_id"] {
		if _, err := db.Exec(`ALTER TABLE plans ADD COLUMN upsell_plan_id INT NULL DEFAULT NULL`); err != nil {
			return err
		}
	}
	if !existing["upsell_url"] {
		if _, err := db.Exec(`ALTER TABLE plans ADD COLUMN upsell_url VARCHAR(255) NULL DEFAULT NULL`); err != nil {
			return err
		}
	}
	return nil
}

func plansColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`
		SELECT COLUMN_NAME FROM information_schema.columns
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'plans'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
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

// GetPlanIDByName looks up a plan's numeric ID by its (unique) name.
func GetPlanIDByName(db *sql.DB, name string) (string, error) {
	var id string
	err := db.QueryRow(`SELECT id FROM plans WHERE name = ?`, name).Scan(&id)
	return id, err
}

// SetPlanUpsell writes plans.upsell_plan_id/upsell_url for planID.
// upsellPlanID == "" clears the reference (NULL); upsellURL == "" clears
// the URL. These two columns are set outside of opencli's fixed
// insert/update column list (see EnsurePlansSchema), so callers apply
// this right after a successful opencli plan-create/plan-edit.
func SetPlanUpsell(db *sql.DB, planID, upsellPlanID, upsellURL string) error {
	var upsellIDArg interface{}
	if upsellPlanID != "" {
		upsellIDArg = upsellPlanID
	}
	var upsellURLArg interface{}
	if upsellURL != "" {
		upsellURLArg = upsellURL
	}
	_, err := db.Exec(`UPDATE plans SET upsell_plan_id = ?, upsell_url = ? WHERE id = ?`,
		upsellIDArg, upsellURLArg, planID)
	return err
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
