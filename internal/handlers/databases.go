package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"
	_ "github.com/lib/pq"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Databases lists every user's databases (a reseller only sees their own users), MySQL straight from the data volumes, Postgres and MongoDB live from running services only
type Databases struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// system schemas that live in the datadir but aren't the user's own
var systemDatabases = map[string]bool{
	"mysql": true, "sys": true, "performance_schema": true, "information_schema": true,
	"phpmyadmin": true, "mariadb.sys": true, "lost+found": true,
}

var systemMongoDatabases = map[string]bool{"admin": true, "local": true, "config": true}

// table data files, .frm covers every MariaDB table and .ibd every MySQL 8 InnoDB one
var tableFileExts = map[string]bool{".frm": true, ".ibd": true, ".MYD": true, ".MAD": true}

var mysqlEncodedCharRe = regexp.MustCompile(`@([0-9a-fA-F]{4})`)

const (
	liveDBWorkers  = 8
	liveDBTimeout  = 3 * time.Second
	liveDBCacheTTL = 2 * time.Minute
)

type databaseRow struct {
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Context   string `json:"context"`
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	Tables    int    `json:"tables"`
}

func (d databaseRow) SizeLabel() string { return humanFileSize(d.SizeBytes) }

// TypeIcon is the MySQL/MariaDB logo from the new user form, shrunk to fit a table cell
func (d databaseRow) TypeIcon() template.HTML {
	for _, o := range userCreateSQLOptions {
		if o.Value == d.Type {
			return template.HTML(strings.Replace(string(o.Icon), `class="w-16 h-16"`, `class="size-5 shrink-0"`, 1))
		}
	}
	return ""
}

// TerminalCommand opens the client straight in this database, typed into the container shell so the name is shell quoted
func (d databaseRow) TerminalCommand() string {
	return d.Type + " '" + strings.ReplaceAll(d.Name, "'", `'\''`) + "'"
}

func (d databaseRow) TypeLabel() string {
	for _, o := range userCreateSQLOptions {
		if o.Value == d.Type {
			return o.Label
		}
	}
	return d.Type
}

func humanFileSize(b int64) string {
	switch {
	case b < 1024:
		return strconv.FormatInt(b, 10) + " B"
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/1024/1024)
	default:
		return fmt.Sprintf("%.2f GB", float64(b)/1024/1024/1024)
	}
}

// databaseUser is a panel user whose service is installed but couldn't be listed
type databaseUser struct {
	Username string `json:"username"`
	Reason   string `json:"reason"`
}

type panelUser struct {
	Username string
	Context  string
}

// decodeMySQLDirName turns mysqld's on-disk name back into the database name, e.g. my@002ddb is my-db
func decodeMySQLDirName(name string) string {
	return mysqlEncodedCharRe.ReplaceAllStringFunc(name, func(m string) string {
		n, err := strconv.ParseUint(m[1:], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
}

func userVolumeDir(uc, volume string) string {
	return filepath.Join(UserHomeDir, uc, "docker-data", "volumes", uc+"_"+volume, "_data")
}

func userEnv(uc string) map[string]string {
	return quickStartParseEnv(filepath.Join(UserHomeDir, uc, ".env"))
}

func envOr(env map[string]string, key, def string) string {
	if v := env[key]; v != "" {
		return v
	}
	return def
}

// readUserDatabases lists the database dirs in one user's datadir with their size and table count
func readUserDatabases(uc string) []databaseRow {
	dataDir := userVolumeDir(uc, "mysql_data")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var rows []databaseRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "#") {
			continue
		}
		name := decodeMySQLDirName(e.Name())
		if systemDatabases[name] {
			continue
		}
		row := databaseRow{Name: name, Context: uc}
		files, _ := os.ReadDir(filepath.Join(dataDir, e.Name()))
		tables := map[string]bool{}
		for _, f := range files {
			if info, err := f.Info(); err == nil && info.Mode().IsRegular() {
				row.SizeBytes += info.Size()
			}
			ext := filepath.Ext(f.Name())
			if tableFileExts[ext] {
				// partitions are tbl#p#p0.ibd, count them once
				stem, _, _ := strings.Cut(strings.TrimSuffix(f.Name(), ext), "#")
				tables[stem] = true
			}
		}
		row.Tables = len(tables)
		rows = append(rows, row)
	}
	return rows
}

// postgresDatabases lists one user's databases over their postgres socket
func postgresDatabases(ctx context.Context, uc string) ([]databaseRow, error) {
	env := userEnv(uc)
	dsn := fmt.Sprintf("user='%s' password='%s' host='%s' dbname=postgres sslmode=disable connect_timeout=3",
		pqQuote(envOr(env, "POSTGRES_USER", "postgres")), pqQuote(envOr(env, "POSTGRES_PASSWORD", "postgrespassword")),
		pqQuote(filepath.Join(UserHomeDir, uc, "sockets", "postgres")))
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT datname, pg_get_userbyid(datdba), pg_database_size(oid) FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []databaseRow
	for rows.Next() {
		row := databaseRow{Context: uc}
		if err := rows.Scan(&row.Name, &row.Role, &row.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func pqQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// mongoDatabases lists one user's databases and their collection counts over their mongod socket
func mongoDatabases(ctx context.Context, uc string) ([]databaseRow, error) {
	env := userEnv(uc)
	socket := filepath.Join(UserHomeDir, uc, "sockets", "mongodb", "mongodb-27017.sock")
	uri := fmt.Sprintf("mongodb://%s:%s@%s/?authSource=admin",
		url.QueryEscape(envOr(env, "MONGODB_ROOT_USER", "admin")), url.QueryEscape(envOr(env, "MONGODB_ROOT_PASSWORD", "rootpassword")),
		url.QueryEscape(socket))
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(liveDBTimeout))
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(context.Background())
	res, err := client.ListDatabases(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	var out []databaseRow
	for _, spec := range res.Databases {
		if systemMongoDatabases[spec.Name] {
			continue
		}
		row := databaseRow{Name: spec.Name, Context: uc, SizeBytes: spec.SizeOnDisk}
		if names, err := client.Database(spec.Name).ListCollectionNames(ctx, bson.D{}); err == nil {
			row.Tables = len(names)
		}
		out = append(out, row)
	}
	return out, nil
}

type liveEngine struct {
	volume string
	socket []string // path under the user's home
	fetch  func(ctx context.Context, uc string) ([]databaseRow, error)
}

var liveEngines = map[string]liveEngine{
	"postgresql": {volume: "pg_data", socket: []string{"sockets", "postgres", ".s.PGSQL.5432"}, fetch: postgresDatabases},
	"mongodb":    {volume: "mongodb_data", socket: []string{"sockets", "mongodb", "mongodb-27017.sock"}, fetch: mongoDatabases},
}

type liveResult struct {
	at      time.Time
	rows    []databaseRow
	stopped []databaseUser
	failed  []databaseUser
}

var (
	liveCacheMu sync.Mutex
	liveCache   = map[string]liveResult{}
)

// collectLive queries every user with the service installed and running, a few at a time, skipping the rest
func collectLive(engine string, users []panelUser) liveResult {
	e := liveEngines[engine]
	res := liveResult{at: time.Now()}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, liveDBWorkers)

	for _, u := range users {
		if _, err := os.Stat(userVolumeDir(u.Context, e.volume)); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(append([]string{UserHomeDir, u.Context}, e.socket...)...)); err != nil {
			res.stopped = append(res.stopped, databaseUser{Username: u.Username, Reason: "service is not running"})
			continue
		}
		wg.Add(1)
		go func(u panelUser) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), liveDBTimeout)
			defer cancel()
			rows, err := e.fetch(ctx, u.Context)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.failed = append(res.failed, databaseUser{Username: u.Username, Reason: err.Error()})
				return
			}
			for i := range rows {
				rows[i].Owner, rows[i].Type = u.Username, engine
			}
			res.rows = append(res.rows, rows...)
		}(u)
	}
	wg.Wait()

	sort.Slice(res.failed, func(i, j int) bool { return res.failed[i].Username < res.failed[j].Username })
	sort.SliceStable(res.rows, func(i, j int) bool { return res.rows[i].Owner < res.rows[j].Owner })
	return res
}

func cachedLive(engine string, users []panelUser, refresh bool) liveResult {
	liveCacheMu.Lock()
	cached, ok := liveCache[engine]
	liveCacheMu.Unlock()
	if ok && !refresh && time.Since(cached.at) < liveDBCacheTTL {
		return cached
	}
	res := collectLive(engine, users)
	liveCacheMu.Lock()
	liveCache[engine] = res
	liveCacheMu.Unlock()
	return res
}

// panelUsers returns every user with a usable context, ok is false when the panel db can't be read
func (d *Databases) panelUsers() ([]panelUser, bool) {
	if d.MySQL == nil {
		return nil, false
	}
	rows, err := d.MySQL.Query(`SELECT username, server FROM users ORDER BY username`)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var users []panelUser
	for rows.Next() {
		var username string
		var ctx sql.NullString
		if rows.Scan(&username, &ctx) != nil || ctx.String == "" || strings.ContainsAny(ctx.String, "/.") {
			continue
		}
		users = append(users, panelUser{Username: username, Context: ctx.String})
	}
	return users, true
}

var databaseSortKeys = map[string]bool{"name": true, "owner": true, "type": true, "role": true, "size": true, "tables": true}

func sortDatabases(rows []databaseRow, col, direction string) {
	var less func(a, b databaseRow) bool
	switch col {
	case "name":
		less = func(a, b databaseRow) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	case "owner":
		less = func(a, b databaseRow) bool { return strings.ToLower(a.Owner) < strings.ToLower(b.Owner) }
	case "type":
		less = func(a, b databaseRow) bool { return a.Type < b.Type }
	case "role":
		less = func(a, b databaseRow) bool { return a.Role < b.Role }
	case "size":
		less = func(a, b databaseRow) bool { return a.SizeBytes < b.SizeBytes }
	case "tables":
		less = func(a, b databaseRow) bool { return a.Tables < b.Tables }
	default:
		return
	}
	sortBy(rows, direction, less)
}

type databasesPageData struct {
	webtemplates.Chrome
	Engine        string
	Base          string
	Databases     []databaseRow
	Stopped       []databaseUser
	Failed        []databaseUser
	CheckedAt     string
	PHPMyAdmin    bool // phpmyadmin module is on, shows the per-database link
	UserCount     int
	TotalSize     string
	MySQLIsDown   bool
	SortCol       string
	SortDirection string
	CSRFToken     string
	Flashes       []auth.Flash
}

// ServeMySQL handles GET /databases.
func (d *Databases) ServeMySQL(w http.ResponseWriter, r *http.Request) {
	users, ok := d.panelUsers()
	scope := scopeFor(r, d.MySQL)
	users = filterRows(scope, users, func(u panelUser) bool { return scope.AllowsUser(u.Username) })
	var rows []databaseRow
	for _, u := range users {
		env := userEnv(u.Context)
		for _, row := range readUserDatabases(u.Context) {
			row.Owner, row.Type = u.Username, envOr(env, "MYSQL_TYPE", "mysql")
			rows = append(rows, row)
		}
	}
	d.render(w, r, "mysql", "/databases", rows, liveResult{}, !ok)
}

// ServePostgreSQL handles GET /databases/postgresql.
func (d *Databases) ServePostgreSQL(w http.ResponseWriter, r *http.Request) {
	d.serveLive(w, r, "postgresql", "/databases/postgresql")
}

// ServeMongoDB handles GET /databases/mongodb.
func (d *Databases) ServeMongoDB(w http.ResponseWriter, r *http.Request) {
	d.serveLive(w, r, "mongodb", "/databases/mongodb")
}

func (d *Databases) serveLive(w http.ResponseWriter, r *http.Request, engine, base string) {
	users, ok := d.panelUsers()
	var res liveResult
	if ok {
		res = cachedLive(engine, users, r.URL.Query().Get("refresh") != "")
	}
	// the cache always holds every user so a reseller's refresh can't leave admins a partial list, scope after
	scope := scopeFor(r, d.MySQL)
	rows := filterRows(scope, res.rows, func(row databaseRow) bool { return scope.AllowsUser(row.Owner) })
	// copy so sorting never touches the cached slice
	rows = append([]databaseRow(nil), rows...)
	res.stopped = filterRows(scope, res.stopped, func(u databaseUser) bool { return scope.AllowsUser(u.Username) })
	res.failed = filterRows(scope, res.failed, func(u databaseUser) bool { return scope.AllowsUser(u.Username) })
	d.render(w, r, engine, base, rows, res, !ok)
}

func (d *Databases) render(w http.ResponseWriter, r *http.Request, engine, base string, rows []databaseRow, live liveResult, mysqlIsDown bool) {
	sortCol, sortDirection := readSort(r, databaseSortKeys)
	sortDatabases(rows, sortCol, sortDirection)

	if r.URL.Query().Get("output") == "json" {
		if rows == nil {
			rows = []databaseRow{}
		}
		writeJSON(w, map[string]interface{}{"databases": rows, "stopped": live.stopped, "failed": live.failed})
		return
	}

	owners := map[string]bool{}
	var total int64
	for _, row := range rows {
		owners[row.Owner] = true
		total += row.SizeBytes
	}
	checkedAt := ""
	if !live.at.IsZero() {
		checkedAt = live.at.Format("15:04:05")
	}

	chrome := buildChrome(r, "Databases")
	webtemplates.Render(w, "databases.html", databasesPageData{
		Chrome:        chrome,
		PHPMyAdmin:    engine == "mysql" && hasModule(chrome.EnabledModules, "phpmyadmin"),
		Engine:        engine,
		Base:          base,
		Databases:     rows,
		Stopped:       live.stopped,
		Failed:        live.failed,
		CheckedAt:     checkedAt,
		UserCount:     len(owners),
		TotalSize:     humanFileSize(total),
		MySQLIsDown:   mysqlIsDown,
		SortCol:       sortCol,
		SortDirection: sortDirection,
		CSRFToken:     csrf.Token(r),
		Flashes:       auth.PopFlashes(w, r, d.Sessions),
	})
}
