package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/auth"
)

func TestDecodeMySQLDirName(t *testing.T) {
	cases := map[string]string{"plain_db": "plain_db", "my@002ddb": "my-db", "a@002eb": "a.b", "odd@0": "odd@0"}
	for in, want := range cases {
		if got := decodeMySQLDirName(in); got != want {
			t.Errorf("decodeMySQLDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadUserDatabases(t *testing.T) {
	home := t.TempDir()
	orig := UserHomeDir
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir = orig })

	data := filepath.Join(home, "ctx1", "docker-data", "volumes", "ctx1_mysql_data", "_data")
	write := func(rel string, size int) {
		p := filepath.Join(data, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wp@002dsite/wp_posts.frm", 100)
	write("wp@002dsite/wp_posts.ibd", 900)
	write("wp@002dsite/logs#p#p0.ibd", 50)
	write("wp@002dsite/logs#p#p1.ibd", 50)
	write("wp@002dsite/db.opt", 10)
	write("mysql/user.frm", 10)
	write("performance_schema/db.opt", 10)
	write("#innodb_redo/ib_redo1", 10)
	write("ibdata1", 1000)

	rows := readUserDatabases("ctx1")
	if len(rows) != 1 {
		t.Fatalf("want 1 database, got %+v", rows)
	}
	r := rows[0]
	if r.Name != "wp-site" || r.Tables != 2 || r.SizeBytes != 1110 || r.Context != "ctx1" {
		t.Errorf("unexpected row %+v", r)
	}
	if rows := readUserDatabases("missing"); rows != nil {
		t.Errorf("missing datadir should give nil, got %+v", rows)
	}
}

// fakeDBUser makes ctx1 with one database and a running mysqld socket, and points podmanExec at sh
func fakeDBUser(t *testing.T, script string) (*Databases, sqlmock.Sqlmock) {
	t.Helper()
	home := t.TempDir()
	orig, origExec := UserHomeDir, podmanExec
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir, podmanExec = orig, origExec })
	for _, dir := range []string{"docker-data/volumes/ctx1_mysql_data/_data/shop", "sockets/mysqld"} {
		if err := os.MkdirAll(filepath.Join(home, "ctx1", dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(home, "ctx1", "sockets", "mysqld", "mysqld.sock"), nil, 0o644)
	os.WriteFile(filepath.Join(home, "ctx1", ".env"), []byte(`MYSQL_TYPE="mariadb"`+"\n"), 0o644)
	podmanExec = func(ctx context.Context, userContext string, args ...string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "sh", "-c", script), nil
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mock.ExpectQuery(`SELECT server FROM users`).WithArgs("bob").WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("ctx1"))
	return &Databases{MySQL: db, Sessions: auth.NewManager("test-secret", false)}, mock
}

func TestDatabasesExportGzip(t *testing.T) {
	d, _ := fakeDBUser(t, `echo "CREATE TABLE t (id int);"`)
	form := url.Values{"owner": {"bob"}, "database": {"shop"}, "format": {"gzip"}}
	req := httptest.NewRequest("POST", "/databases/export", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	d.ServeExport(rec, req)

	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="bob_shop.sql.gz"` {
		t.Fatalf("unexpected disposition %q, status %d", got, rec.Code)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != "CREATE TABLE t (id int);\n" {
		t.Errorf("unexpected dump %q", body)
	}
}

func TestDatabasesExportFailureRedirects(t *testing.T) {
	d, _ := fakeDBUser(t, `echo "access denied" >&2; exit 2`)
	form := url.Values{"owner": {"bob"}, "database": {"shop"}}
	req := httptest.NewRequest("POST", "/databases/export", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	d.ServeExport(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("want a redirect without a download, got %d %v", rec.Code, rec.Header())
	}
}

func TestDatabasesImportStreamsGzip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "stdin.sql")
	d, _ := fakeDBUser(t, "cat > "+out)

	var gzBody bytes.Buffer
	zw := gzip.NewWriter(&gzBody)
	zw.Write([]byte("INSERT INTO t VALUES (1);\n"))
	zw.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("db_file", "dump.sql.gz")
	fw.Write(gzBody.Bytes())
	mw.Close()

	req := httptest.NewRequest("POST", "/databases/import?owner=bob&database=shop", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	d.ServeImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(out); string(got) != "INSERT INTO t VALUES (1);\n" {
		t.Errorf("mysql got %q on stdin", got)
	}
}

func TestDatabasesImportUnknownDatabase(t *testing.T) {
	d, _ := fakeDBUser(t, "true")
	req := httptest.NewRequest("POST", "/databases/import?owner=bob&database=nope", strings.NewReader(""))
	rec := httptest.NewRecorder()
	d.ServeImport(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestDatabaseTerminalCommand(t *testing.T) {
	if got := (databaseRow{Name: "it's", Type: "mariadb"}).TerminalCommand(); got != `mariadb 'it'\''s'` {
		t.Errorf("got %s", got)
	}
}

func TestTerminalInitialCommand(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/bob/mariadb?cmd="+url.QueryEscape("mariadb 'shop'\nrm -rf /"), nil)
	if got := terminalInitialCommand(req); got != "mariadb 'shop'rm -rf /" {
		t.Errorf("newlines should be stripped so it stays one line, got %q", got)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameOriginNavigation(req) {
		t.Error("cross-site link must not auto run")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOriginNavigation(req) {
		t.Error("link from OpenAdmin should auto run")
	}
}
