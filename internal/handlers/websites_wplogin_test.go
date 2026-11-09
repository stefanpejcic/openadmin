package handlers

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestParseWPConfig(t *testing.T) {
	db, prefix, err := parseWPConfig("<?php\ndefine( 'DB_NAME', 'wp_shop' );\n$table_prefix = 'xy_';\n")
	if err != nil || db != "wp_shop" || prefix != "xy_" {
		t.Fatalf("got %q %q %v", db, prefix, err)
	}
	if _, prefix, _ := parseWPConfig(`define('DB_NAME', "a");`); prefix != "wp_" {
		t.Errorf("default prefix = %q", prefix)
	}
	if _, _, err := parseWPConfig("define('DB_NAME', 'x`; DROP');"); err == nil {
		t.Error("expected unsafe names to be rejected")
	}
}

func TestWPLoginLink(t *testing.T) {
	home := t.TempDir()
	orig := UserHomeDir
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir = orig })
	site := filepath.Join(home, "bob", "docker-data", "volumes", "bob_html_data", "_data", "b.com", "blog")
	os.MkdirAll(site, 0o755)
	os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("define('DB_NAME', 'bob_wp');\n$table_prefix = 'wp_';"), 0o644)

	db, m, _ := sqlmock.New()
	origOpen := wpLoginOpenDB
	wpLoginOpenDB = func(ctx, dbName string) (*sql.DB, error) {
		if ctx != "bob" || dbName != "bob_wp" {
			t.Errorf("opened %s/%s", ctx, dbName)
		}
		return db, nil
	}
	t.Cleanup(func() { wpLoginOpenDB = origOpen })

	m.ExpectQuery("FROM `wp_users`").WithArgs("wp_capabilities").WillReturnRows(sqlmock.NewRows([]string{"ID"}).AddRow(3))
	m.ExpectQuery("FROM `wp_options` WHERE option_name = 'siteurl'").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("https://b.com/blog/"))
	m.ExpectExec("INSERT INTO `wp_options`").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))

	link, err := wpLoginLink("bob", "b.com/blog", "/var/www/html/b.com/blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://b.com/blog/?op_login=") || len(link) != len("https://b.com/blog/?op_login=")+64 {
		t.Errorf("link = %s", link)
	}
	if b, err := os.ReadFile(filepath.Join(site, "wp-content", "mu-plugins", "openpanel-login.php")); err != nil || !strings.Contains(string(b), "op_login") {
		t.Error("expected the mu-plugin in the site")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}

	if _, err := wpLoginLink("bob", "x", "/var/www/html/../../etc"); err == nil {
		t.Error("expected path escapes to be rejected")
	}
}

// https://github.com/stefanpejcic/OpenPanel/issues/1298
func TestFindWPConfigOneLevelUp(t *testing.T) {
	htmlData := t.TempDir()
	domain := filepath.Join(htmlData, "b.com")
	site := filepath.Join(domain, "stats")
	os.MkdirAll(site, 0o755)
	write := func(p string) { os.WriteFile(p, []byte("<?php"), 0o644) }

	if got := findWPConfig(htmlData, site); got != filepath.Join(site, "wp-config.php") {
		t.Errorf("no config anywhere: got %s", got)
	}
	write(filepath.Join(domain, "wp-config.php"))
	if got := findWPConfig(htmlData, site); got != filepath.Join(domain, "wp-config.php") {
		t.Errorf("moved config: got %s", got)
	}
	write(filepath.Join(site, "wp-config.php"))
	if got := findWPConfig(htmlData, site); got != filepath.Join(site, "wp-config.php") {
		t.Errorf("docroot config should win: got %s", got)
	}
	os.Remove(filepath.Join(site, "wp-config.php"))
	write(filepath.Join(domain, "wp-settings.php"))
	if got := findWPConfig(htmlData, site); got != filepath.Join(site, "wp-config.php") {
		t.Errorf("parent is its own WordPress install, its config must not be used: got %s", got)
	}

	// docroot at the volume root, the folder above belongs to the host
	root := filepath.Join(t.TempDir(), "_data")
	os.MkdirAll(root, 0o755)
	write(filepath.Join(filepath.Dir(root), "wp-config.php"))
	if got := findWPConfig(root, root); got != filepath.Join(root, "wp-config.php") {
		t.Errorf("looked outside the html volume: got %s", got)
	}
}

func TestWPLoginLinkMovedConfig(t *testing.T) {
	home := t.TempDir()
	orig := UserHomeDir
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir = orig })
	domain := filepath.Join(home, "bob", "docker-data", "volumes", "bob_html_data", "_data", "b.com")
	site := filepath.Join(domain, "stats")
	os.MkdirAll(site, 0o755)
	os.WriteFile(filepath.Join(domain, "wp-config.php"), []byte("define('DB_NAME', 'bob_hard');\n$table_prefix = 'hd_';"), 0o644)

	db, m, _ := sqlmock.New()
	origOpen := wpLoginOpenDB
	wpLoginOpenDB = func(ctx, dbName string) (*sql.DB, error) {
		if dbName != "bob_hard" {
			t.Errorf("opened %s/%s", ctx, dbName)
		}
		return db, nil
	}
	t.Cleanup(func() { wpLoginOpenDB = origOpen })

	m.ExpectQuery("FROM `hd_users`").WithArgs("hd_capabilities").WillReturnRows(sqlmock.NewRows([]string{"ID"}).AddRow(1))
	m.ExpectQuery("FROM `hd_options` WHERE option_name = 'siteurl'").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("https://b.com/stats"))
	m.ExpectExec("INSERT INTO `hd_options`").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))

	link, err := wpLoginLink("bob", "b.com/stats", "/var/www/html/b.com/stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://b.com/stats/?op_login=") {
		t.Errorf("link = %s", link)
	}
	// the mu-plugin still goes into the WordPress folder, not next to the config
	if _, err := os.Stat(filepath.Join(site, "wp-content", "mu-plugins", "openpanel-login.php")); err != nil {
		t.Error("expected the mu-plugin in the site")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}

	os.WriteFile(filepath.Join(domain, "wp-settings.php"), []byte("<?php"), 0o644)
	if _, err := wpLoginLink("bob", "b.com/stats", "/var/www/html/b.com/stats"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found when the parent is its own install, got %v", err)
	}
}

func TestWebsitesLoginOnlyForWordPress(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("FROM sites s").WithArgs("4").WillReturnRows(sqlmock.NewRows([]string{"site_name", "type", "docroot", "username", "server"}).
		AddRow("n.com", "nodejs", "/var/www/html/n.com", "bob", "bob"))

	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	ws := srv.URL
	resp, err := client.Get(ws + "/websites/login/4")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/websites" {
		t.Errorf("got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
