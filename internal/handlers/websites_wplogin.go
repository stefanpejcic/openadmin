package handlers

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"openadmin/internal/activity"
	"openadmin/internal/auth"
)

// one-time wp-admin login, same mu-plugin, token format and 5 minute ttl as the user panel's /wp-cli/login so both keep working on the same site

// UserHomeDir is where user contexts live, a var so tests use a scratch dir
var UserHomeDir = "/home"

const wpLoginTTL = 300 * time.Second

// keep in sync with openpanelMuPluginPHP in the user panel's modules/wordpress/wpcli.go
const wpLoginMuPlugin = `<?php
/**
 * OpenPanel one-time admin login handler.
 * Managed by OpenPanel - do not edit, will be overwritten on next autologin from WP Manager.
 */
add_action('init', function () {
    if (!isset($_GET['op_login'])) {
        return;
    }

    $token = isset($_GET['op_login']) ? sanitize_text_field(wp_unslash($_GET['op_login'])) : '';
    if (!preg_match('/^[a-f0-9]{64}$/', $token)) {
        wp_die('Invalid login token.', 'OpenPanel Login', ['response' => 400]);
    }

    global $wpdb;
    $option_name = 'op_login_' . hash('sha256', $token);

    // Raw query on purpose: bypasses any persistent object cache
    $row = $wpdb->get_row(
        $wpdb->prepare("SELECT option_value FROM {$wpdb->options} WHERE option_name = %s LIMIT 1", $option_name)
    );

    if (!$row) {
        wp_die('This login link is invalid or has already been used.', 'OpenPanel Login', ['response' => 403]);
    }

    // One-time use: delete immediately
    $wpdb->delete($wpdb->options, ['option_name' => $option_name]);

    $data = maybe_unserialize($row->option_value);
    if (!is_array($data) || empty($data['user_id']) || empty($data['expires'])) {
        wp_die('This login link is invalid.', 'OpenPanel Login', ['response' => 403]);
    }

    if (time() > (int) $data['expires']) {
        wp_die('This login link has expired.', 'OpenPanel Login', ['response' => 403]);
    }

    $user = get_user_by('id', (int) $data['user_id']);
    if (!$user || !user_can($user, 'manage_options')) {
        wp_die('User not found or not an administrator.', 'OpenPanel Login', ['response' => 403]);
    }

    wp_set_current_user($user->ID);
    wp_set_auth_cookie($user->ID);
    do_action('wp_login', $user->user_login, $user);

    wp_safe_redirect(admin_url());
    exit;
});
`

var (
	wpDBNameRE      = regexp.MustCompile(`define\(\s*['"]DB_NAME['"]\s*,\s*['"]([^'"]+)['"]`)
	wpTablePrefixRE = regexp.MustCompile(`\$table_prefix\s*=\s*['"]([^'"]+)['"]`)
	wpSafeNameRE    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	wpSafePathRE    = regexp.MustCompile(`^[A-Za-z0-9_\-/.]+$`)
)

// parseWPConfig pulls the database name and table prefix out of wp-config.php
func parseWPConfig(content string) (dbName, prefix string, err error) {
	m := wpDBNameRE.FindStringSubmatch(content)
	if m == nil {
		return "", "", fmt.Errorf("DB_NAME not found in wp-config.php")
	}
	dbName, prefix = m[1], "wp_"
	if p := wpTablePrefixRE.FindStringSubmatch(content); p != nil {
		prefix = p[1]
	}
	// both end up inside backticks in the queries below
	if !wpSafeNameRE.MatchString(dbName) || !wpSafeNameRE.MatchString(prefix) {
		return "", "", fmt.Errorf("unexpected DB_NAME or table prefix in wp-config.php")
	}
	return dbName, prefix, nil
}

// userMySQLCredentials reads the [client] section of the user's my.cnf
func userMySQLCredentials(userContext string) (user, password string, err error) {
	f, err := os.Open(filepath.Join(UserHomeDir, userContext, "my.cnf"))
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && section == "client" {
			switch strings.TrimSpace(k) {
			case "user":
				user = strings.TrimSpace(v)
			case "password":
				password = strings.TrimSpace(v)
			}
		}
	}
	return user, password, scanner.Err()
}

// wpLoginOpenDB connects to the user's own mysqld over its socket, a var so tests can use sqlmock
var wpLoginOpenDB = func(userContext, dbName string) (*sql.DB, error) {
	user, password, err := userMySQLCredentials(userContext)
	if err != nil {
		return nil, fmt.Errorf("MySQL credentials not readable: %w", err)
	}
	socket := filepath.Join(UserHomeDir, userContext, "sockets", "mysqld", "mysqld.sock")
	if _, err := os.Stat(socket); err != nil {
		return nil, fmt.Errorf("the user's MySQL service isn't running")
	}
	return sql.Open("mysql", fmt.Sprintf("%s:%s@unix(%s)/%s?timeout=10s", user, password, socket, dbName))
}

// wpLoginLink creates the one-time login link for a site's first administrator
func wpLoginLink(userContext, siteName, docroot string) (string, error) {
	rel := strings.TrimPrefix(filepath.Clean(docroot), "/var/www/html")
	if !wpSafeNameRE.MatchString(userContext) || !wpSafePathRE.MatchString(rel) || strings.Contains(rel, "..") {
		return "", fmt.Errorf("invalid document root")
	}
	htmlData := filepath.Join(UserHomeDir, userContext, "docker-data", "volumes", userContext+"_html_data", "_data")
	realPath := filepath.Join(htmlData, rel)

	config, err := os.ReadFile(filepath.Join(realPath, "wp-config.php"))
	if err != nil {
		return "", fmt.Errorf("wp-config.php not found in %s", docroot)
	}
	dbName, prefix, err := parseWPConfig(string(config))
	if err != nil {
		return "", err
	}

	db, err := wpLoginOpenDB(userContext, dbName)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var adminID int
	err = db.QueryRow("SELECT u.ID FROM `"+prefix+"users` u JOIN `"+prefix+"usermeta` m ON m.user_id = u.ID"+
		" WHERE m.meta_key = ? AND m.meta_value LIKE '%administrator%' ORDER BY u.ID LIMIT 1", prefix+"capabilities").Scan(&adminID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no administrator users found")
	} else if err != nil {
		return "", fmt.Errorf("reading WordPress users failed: %w", err)
	}

	siteURL := "https://" + siteName
	var stored string
	if db.QueryRow("SELECT option_value FROM `"+prefix+"options` WHERE option_name = 'siteurl' LIMIT 1").Scan(&stored) == nil && stored != "" {
		siteURL = stored
	}
	siteURL = strings.TrimSuffix(siteURL, "/")

	if err := writeWPLoginPlugin(userContext, realPath); err != nil {
		return "", fmt.Errorf("failed deploying login handler: %w", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	optionName := fmt.Sprintf("op_login_%x", sha256.Sum256([]byte(token)))
	// php serialized array, the mu-plugin reads it with maybe_unserialize
	value := fmt.Sprintf(`a:2:{s:7:"user_id";i:%d;s:7:"expires";i:%d;}`, adminID, time.Now().Add(wpLoginTTL).Unix())
	if _, err := db.Exec("INSERT INTO `"+prefix+"options` (option_name, option_value, autoload) VALUES (?, ?, 'no')"+
		" ON DUPLICATE KEY UPDATE option_value = VALUES(option_value)", optionName, value); err != nil {
		return "", fmt.Errorf("saving the login token failed: %w", err)
	}
	return siteURL + "/?op_login=" + token, nil
}

// writeWPLoginPlugin drops the mu-plugin into the site, owned by the user like the rest of their files
func writeWPLoginPlugin(userContext, realPath string) error {
	dir := filepath.Join(realPath, "wp-content", "mu-plugins")
	file := filepath.Join(dir, "openpanel-login.php")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(wpLoginMuPlugin), 0o644); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(UserHomeDir, userContext)); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(dir, int(st.Uid), int(st.Uid))
			_ = os.Chown(file, int(st.Uid), int(st.Uid))
		}
	}
	return nil
}

// ServeLogin handles GET /websites/login/{id}, opens wp-admin as the site's administrator
func (ws *Websites) ServeLogin(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) {
		activity.Fail(r.Context())
		auth.AddFlash(w, r, ws.Sessions, msg, "error")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
	}
	if ws.MySQL == nil {
		fail("Database unavailable.")
		return
	}
	var siteName, typ, docroot, owner, userContext sql.NullString
	err := ws.MySQL.QueryRow(`
		SELECT s.site_name, s.type, d.docroot, u.username, u.server
		FROM sites s
		JOIN domains d ON d.domain_id = s.domain_id
		JOIN users u ON u.id = d.user_id
		WHERE s.id = ?`, r.PathValue("id")).Scan(&siteName, &typ, &docroot, &owner, &userContext)
	if err != nil {
		fail("Website not found.")
		return
	}
	if !strings.EqualFold(typ.String, "wordpress") {
		fail("Auto-login is only available for WordPress sites.")
		return
	}
	full := docroot.String
	if _, folder, ok := strings.Cut(siteName.String, "/"); ok && folder != "" {
		full = strings.TrimRight(full, "/") + "/" + folder
	}

	link, err := wpLoginLink(userContext.String, siteName.String, full)
	if err != nil {
		fail("Can't log in to " + siteName.String + ": " + err.Error())
		return
	}
	activity.Describe(r.Context(), "Logged in to wp-admin of "+siteName.String+" owned by "+owner.String)
	http.Redirect(w, r, link, http.StatusFound)
}
