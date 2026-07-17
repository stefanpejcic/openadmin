package paneldb

import (
	"database/sql"
)

// CheckIfOwnerForUser checks whether actingUsername is allowed to manage
// username, given actingRole.
//
// Preserved quirk: for any actingRole other than "reseller", this always
// returns true unconditionally (even when the target user doesn't exist)
// -- so non-reseller roles are never blocked by this check at all; they
// rely on a later 404 if the username turns out not to exist. Call sites
// already handle the not-found case separately, so this is left as-is
// rather than "fixed".
func CheckIfOwnerForUser(db *sql.DB, username, actingUsername, actingRole string) bool {
	if actingRole != "reseller" {
		return true
	}
	var exists int
	err := db.QueryRow(`SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1`, username, actingUsername).Scan(&exists)
	return err == nil
}

// GetAllUsers returns every user column plus their plan's
// name/disk_limit/inodes_limit/cpu/ram, optionally restricted to a
// reseller's own accounts (pass "" for an unrestricted/admin query).
func GetAllUsers(db *sql.DB, resellerOwner string) ([]RowMap, error) {
	query := `
		SELECT users.*, plans.name, plans.disk_limit, plans.inodes_limit, plans.cpu, plans.ram
		FROM users
		INNER JOIN plans ON users.plan_id = plans.id
	`
	var args []interface{}
	if resellerOwner != "" {
		query += " WHERE users.owner = ?"
		args = append(args, resellerOwner)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}

// UserData is a single user's core account fields.
type UserData struct {
	Username       string         `json:"username"`
	ID             int64          `json:"id"`
	Email          string         `json:"email"`
	Owner          sql.NullString `json:"owner"`
	TwoFAEnabled   bool           `json:"twofa_enabled"`
	RegisteredDate sql.NullString `json:"registered_date"`
	PlanID         int64          `json:"plan_id"`
	Context        string         `json:"context"` // the `server` column, aliased "context"
}

// GetUserDataByUsername matches either the exact username or a
// "SUSPENDED_<id>_<username>" variant. The underscores in the LIKE pattern
// are deliberately NOT backslash-escaped -- in SQL LIKE, unescaped "_"
// matches any single character, so this is technically a slightly looser
// match than "literal underscore", but that's intentional here.
func GetUserDataByUsername(db *sql.DB, username string) (*UserData, error) {
	suspendedPattern := "SUSPENDED_%_" + username
	row := db.QueryRow(`
		SELECT u.username, u.id, u.email, u.owner, u.twofa_enabled, u.registered_date, u.plan_id, u.server
		FROM users u
		WHERE u.username = ? OR u.username LIKE ?
	`, username, suspendedPattern)

	var d UserData
	if err := row.Scan(&d.Username, &d.ID, &d.Email, &d.Owner, &d.TwoFAEnabled, &d.RegisteredDate, &d.PlanID, &d.Context); err != nil {
		return nil, err
	}
	return &d, nil
}
