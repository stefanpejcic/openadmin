package paneldb

import "database/sql"

// ListSiteNames returns every site_name in the sites table. It always
// queries fresh (no caching), matching the "always fresh, never cached"
// convention applied throughout this package.
func ListSiteNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT site_name FROM sites`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

// SearchSiteNames does a LIKE '%query%' match, capped to 5 rows (the
// caller further caps the combined result to 10).
func SearchSiteNames(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(`SELECT site_name FROM sites WHERE site_name LIKE ? LIMIT 5`, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

// ListUsernames returns just usernames, optionally restricted to a
// reseller's own accounts. Distinct from GetAllUsers, which this package
// already exposes for the users list page and returns full rows.
func ListUsernames(db *sql.DB, resellerOwner string) ([]string, error) {
	query := `SELECT username FROM users`
	var args []interface{}
	if resellerOwner != "" {
		query += ` WHERE owner = ?`
		args = append(args, resellerOwner)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

// SearchUsernames does a LIKE '%query%' match, optionally further
// restricted to a reseller's own accounts, capped to 5 rows.
func SearchUsernames(db *sql.DB, query, resellerOwner string) ([]string, error) {
	sqlQuery := `SELECT username FROM users WHERE username LIKE ?`
	args := []interface{}{"%" + query + "%"}
	if resellerOwner != "" {
		sqlQuery += ` AND owner = ?`
		args = append(args, resellerOwner)
	}
	sqlQuery += ` LIMIT 5`

	rows, err := db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

func scanStringColumn(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
