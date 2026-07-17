// This file implements generating a one-time admin -> user-panel
// impersonation/autologin link.
package handlers

import (
	"bufio"
	"database/sql"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/config"
)

// Autologin bundles the /login/token/{username} handler.
type Autologin struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
	PublicIP string
	// AdminPort is evaluated ONCE at process startup, unlike force_domain/
	// port below, which are re-queried fresh on every request -- not
	// re-read per request.
	AdminPort string
}

// autologinCaddyfilePath / autologinCaddyCertDirs are the paths this
// handler's own local SSL-exists/domain-lookup checks use -- kept separate
// from internal/panelinfo and internal/bootstrap's own copies, which serve
// different, differently-cached call sites.
var (
	autologinCaddyfilePath = "/etc/openpanel/caddy/Caddyfile"
	autologinCaddyCertDirs = []string{
		"/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/",
		"/etc/openpanel/caddy/ssl/custom/",
	}
)

// AutologinTokenBaseDir is the /etc/openpanel/openpanel/core/users/ prefix
// -- a var so tests can point it at a scratch directory instead of the
// real system path.
var AutologinTokenBaseDir = "/etc/openpanel/openpanel/core/users"

var autologinUserpanelDomainLineRe = regexp.MustCompile(`^([\w.-]+)\s*\{`)

// autologinOpenpanelDomainRun / autologinOpenpanelPortRun / autologinCheckSSLExistsRun
// are injectable so tests never touch a real Caddyfile/opencli/filesystem.
// Always queried fresh (no caching), since the domain/port/SSL state here
// can change between requests and staleness would produce a broken
// autologin link.
var autologinOpenpanelDomainRun = func() string {
	if d := readUserpanelDomainFresh(); d != "" {
		return d
	}
	out, err := exec.Command("opencli", "domain").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readUserpanelDomainFresh() string {
	f, err := os.Open(autologinCaddyfilePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.Contains(line, "# START USERPANEL DOMAIN #"):
			inBlock = true
			continue
		case strings.Contains(line, "# END USERPANEL DOMAIN #"):
			return ""
		}
		if inBlock {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if m := autologinUserpanelDomainLineRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

var autologinOpenpanelPortRun = func() string {
	out, err := exec.Command("opencli", "port").Output()
	if err != nil {
		return "2083"
	}
	port := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return "2083"
	}
	return port
}

// autologinCheckSSLExistsRun uses a looser local check than
// bootstrap.CheckSSLExists: directory exists and is non-empty, without
// requiring specific .crt/.key filenames.
var autologinCheckSSLExistsRun = func(domain string) bool {
	for _, base := range autologinCaddyCertDirs {
		entries, err := os.ReadDir(filepath.Join(base, domain))
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

const autologinTokenCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generateRandomToken uses math/rand, which is not cryptographically
// secure. That's flagged here rather than silently upgraded to a CSPRNG,
// since hardening this token generation's security properties is out of
// scope for this change.
func generateRandomToken(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = autologinTokenCharset[rand.Intn(len(autologinTokenCharset))]
	}
	return string(b)
}

// checkIfOwnerForUser: an admin/user caller always "owns" any account;
// only a reseller caller is actually checked against the users table's
// owner column.
func checkIfOwnerForUser(db *sql.DB, username string, actingUser *admindb.User) bool {
	if actingUser.Role != "reseller" {
		return true
	}
	if db == nil {
		return false
	}
	var dummy int
	err := db.QueryRow("SELECT 1 FROM users WHERE username = ? AND owner = ? LIMIT 1", username, actingUser.Username).Scan(&dummy)
	return err == nil
}

// ServeLoginToken handles GET /login/token/{username}.
func (a *Autologin) ServeLoginToken(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if username == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	currentUser := auth.CurrentUser(r)
	if !checkIfOwnerForUser(a.MySQL, username, currentUser) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	tokenDir := filepath.Join(AutologinTokenBaseDir, username)
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	randomToken := generateRandomToken(30)
	if err := os.WriteFile(filepath.Join(tokenDir, "logintoken.txt"), []byte(randomToken), 0644); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	forceDomain := autologinOpenpanelDomainRun()
	port := autologinOpenpanelPortRun()

	var hostname, scheme string
	if forceDomain != "" && autologinCheckSSLExistsRun(forceDomain) {
		hostname = strings.TrimSpace(forceDomain)
		scheme = "https"
	} else {
		hostname = a.PublicIP
		scheme = "http"
	}

	openPanel := fmt.Sprintf("%s://%s:%s", scheme, hostname, port)

	impersonate := config.Load(config.AdminConfigPath).Get("USERS", "impersonate", "no")

	var link string
	if impersonate == "yes" {
		link = fmt.Sprintf("%s/login_autologin?username=%s&admin_port=%s&impersonate=yes&admin_token=%s", openPanel, username, a.AdminPort, randomToken)
	} else {
		link = fmt.Sprintf("%s/login_autologin?username=%s&admin_port=%s&admin_token=%s", openPanel, username, a.AdminPort, randomToken)
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]string{"link": link})
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}
