// Package mysqldb opens the panel's MySQL database using the [client]
// group of /etc/my.cnf, with no explicit host/user/password/database
// arguments -- all of it comes from the option file.
package mysqldb

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// OptionFilePath is a var (not const) so tests can point it at a scratch
// fixture instead of the real /etc file (which normally contains
// credentials and is root-only).
var OptionFilePath = "/etc/my.cnf"

// parseOptionFile reads the requested groups of a MySQL option file:
// "[section]" headers, "key=value" pairs (optionally quoted), and '#'/';'
// comments. This covers the subset of my.cnf syntax needed for the
// [client] group, including its nonstandard-but-supported "database" key.
func parseOptionFile(path string, groups ...string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	wanted := make(map[string]bool, len(groups))
	for _, g := range groups {
		wanted[g] = true
	}

	values := map[string]string{}
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if !wanted[section] {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return values, scanner.Err()
}

// Open reads OptionFilePath's [client] group and returns a ready *sql.DB.
//
// The returned *sql.DB is never nil, even when the option file can't be
// read: callers (see main.go) treat a non-nil error here as non-fatal and
// keep using the returned handle, relying on every subsequent query to fail
// gracefully instead of panicking on a nil *sql.DB.
func Open() (*sql.DB, error) {
	values, parseErr := parseOptionFile(OptionFilePath, "client")
	if parseErr != nil {
		values = map[string]string{}
	}

	cfg := mysql.NewConfig()
	cfg.User = values["user"]
	cfg.Passwd = values["password"]
	cfg.DBName = values["database"]
	cfg.ParseTime = true
	cfg.Params = map[string]string{"charset": "utf8mb4"}

	if socket, ok := values["socket"]; ok && socket != "" {
		cfg.Net = "unix"
		cfg.Addr = socket
	} else {
		host := values["host"]
		if host == "" {
			host = "127.0.0.1"
		}
		port := "3306"
		if p, ok := values["port"]; ok && p != "" {
			if _, err := strconv.Atoi(p); err == nil {
				port = p
			}
		}
		cfg.Net = "tcp"
		cfg.Addr = host + ":" + port
	}

	// sql.Open doesn't actually connect -- connections are made lazily per
	// query, so a MySQL outage at startup doesn't fail process startup.
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	if parseErr != nil {
		return db, fmt.Errorf("reading %s: %w", OptionFilePath, parseErr)
	}
	return db, nil
}
