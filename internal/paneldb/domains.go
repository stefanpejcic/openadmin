package paneldb

import "database/sql"

// GetAllDomains returns every domain along with its owning username.
func GetAllDomains(db *sql.DB) ([]RowMap, error) {
	rows, err := db.Query(`
		SELECT
			d.domain_id,
			d.docroot,
			d.domain_url,
			d.php_version,
			u.username
		FROM
			domains d
		JOIN
			users u
		ON
			d.user_id = u.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRowsToMaps(rows)
}
