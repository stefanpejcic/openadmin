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

// O que este teste executa:
// Valida o fallback para wp-config.php movido um diretório acima do web root (hardening de segurança WordPress).
func TestWPLoginLinkMovedWPConfigFallback(t *testing.T) {
	home := t.TempDir()
	orig := UserHomeDir
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir = orig })

	// Simula a estrutura com /var/www/html/wpxss.com/stats e wp-config.php em /var/www/html/wpxss.com/wp-config.php
	userContext := "wpxssuser"
	htmlData := filepath.Join(home, userContext, "docker-data", "volumes", userContext+"_html_data", "_data")
	domainDir := filepath.Join(htmlData, "wpxss.com")
	siteSubDir := filepath.Join(domainDir, "stats")
	if err := os.MkdirAll(siteSubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// wp-config.php no nível superior
	if err := os.WriteFile(filepath.Join(domainDir, "wp-config.php"), []byte("define('DB_NAME', 'hardened_db');\n$table_prefix = 'wp_';"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	origOpen := wpLoginOpenDB
	wpLoginOpenDB = func(ctx, dbName string) (*sql.DB, error) {
		if ctx != userContext || dbName != "hardened_db" {
			t.Errorf("opened unexpected db %s/%s", ctx, dbName)
		}
		return db, nil
	}
	t.Cleanup(func() { wpLoginOpenDB = origOpen })

	m.ExpectQuery("FROM `wp_users`").WithArgs("wp_capabilities").WillReturnRows(sqlmock.NewRows([]string{"ID"}).AddRow(7))
	m.ExpectQuery("FROM `wp_options` WHERE option_name = 'siteurl'").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("https://wpxss.com/stats/"))
	m.ExpectExec("INSERT INTO `wp_options`").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))

	link, err := wpLoginLink(userContext, "wpxss.com/stats", "/var/www/html/wpxss.com/stats")
	if err != nil {
		t.Fatalf("falha ao resolver wp-config.php no diretório pai: %v", err)
	}

	if !strings.HasPrefix(link, "https://wpxss.com/stats/?op_login=") {
		t.Errorf("link retornado inesperado: %s", link)
	}

	muFile := filepath.Join(siteSubDir, "wp-content", "mu-plugins", "openpanel-login.php")
	if _, err := os.Stat(muFile); err != nil {
		t.Errorf("esperava mu-plugin em %s: %v", muFile, err)
	}

	if err := m.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// O que este teste executa:
// Valida que erro explícito é retornado quando wp-config.php não existe nem no docroot nem no diretório pai.
func TestWPLoginLinkWPConfigNotFound(t *testing.T) {
	home := t.TempDir()
	orig := UserHomeDir
	UserHomeDir = home
	t.Cleanup(func() { UserHomeDir = orig })

	userContext := "emptyuser"
	htmlData := filepath.Join(home, userContext, "docker-data", "volumes", userContext+"_html_data", "_data")
	siteDir := filepath.Join(htmlData, "mysite.com")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := wpLoginLink(userContext, "mysite.com", "/var/www/html/mysite.com")
	if err == nil || !strings.Contains(err.Error(), "wp-config.php not found in /var/www/html/mysite.com") {
		t.Fatalf("esperava erro de wp-config.php não encontrado, obteve: %v", err)
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
