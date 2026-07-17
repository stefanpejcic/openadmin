package paneldb

import "database/sql"

// RowMap is a generic column-name -> value row, used for "SELECT table.*"
// queries without needing to hardcode the full column list of tables this
// package doesn't own the schema for (plans, users, domains, ...).
// json.Marshal of a []RowMap serializes each row as a plain JSON object
// keyed by column name.
type RowMap map[string]interface{}

// scanRowsToMaps converts *sql.Rows into []RowMap, decoding []byte values
// (how database/sql returns most non-numeric MySQL column types by default)
// to string so JSON encoding produces readable values instead of base64.
func scanRowsToMaps(rows *sql.Rows) ([]RowMap, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []RowMap
	for rows.Next() {
		raw := make([]interface{}, len(columns))
		ptrs := make([]interface{}, len(columns))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make(RowMap, len(columns))
		for i, col := range columns {
			row[col] = normalizeValue(raw[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalizeValue(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
