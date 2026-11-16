package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"openadmin/internal/sieveparser"
)

// postfix access maps opencli email restrict writes to, "user@domain REJECT" per line, same files the user panel reads
var (
	EmailsSendAccessFile    = "/usr/local/mail/openmail/docker-data/dms/config/postfix-send-access.cf"
	EmailsReceiveAccessFile = "/usr/local/mail/openmail/docker-data/dms/config/postfix-receive-access.cf"
)

// emailRestricted returns the lowercased addresses listed in a postfix access file
func emailRestricted(path string) map[string]bool {
	set := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) > 0 && !strings.HasPrefix(f[0], "#") {
			set[strings.ToLower(f[0])] = true
		}
	}
	return set
}

// emailMailDir returns where a domain's mailboxes live on this server, same layout the user panel reads its sieve files from
func emailMailDir(store string, contexts map[string]string, domain string) string {
	if strings.HasPrefix(store, "/") {
		return filepath.Join(store, domain)
	}
	if ctx := contexts[domain]; ctx != "" {
		return filepath.Join("/home", ctx, "mail", domain)
	}
	return ""
}

// emailDomainContexts maps each domain to its owner's context, only needed when mail is stored in the users' home dirs
func emailDomainContexts(db *sql.DB) map[string]string {
	out := map[string]string{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT d.domain_url, u.server FROM domains d JOIN users u ON d.user_id = u.id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var domain, ctx sql.NullString
		if rows.Scan(&domain, &ctx) == nil && domain.Valid && ctx.Valid {
			out[strings.ToLower(domain.String)] = ctx.String
		}
	}
	return out
}

var sieveRuleRe = regexp.MustCompile(`(?mi)^\s*(?:if|elsif|else)\b`)

// emailFilterCount counts the rules in an address's .dovecot.sieve, 0 when it has none
func emailFilterCount(mailDir, address string) int {
	user, domain, ok := strings.Cut(address, "@")
	if !ok || mailDir == "" || user == "" || strings.ContainsAny(user+domain, "/\\") || strings.Contains(address, "..") || strings.HasPrefix(user, ".") {
		return 0
	}
	raw, err := os.ReadFile(filepath.Join(mailDir, user, "home", ".dovecot.sieve"))
	if err != nil {
		return 0
	}
	return sieveRuleCount(raw)
}

// sieveRuleCount counts if/elsif/else blocks, a script with none still does something, like a plain redirect
func sieveRuleCount(raw []byte) int {
	if n := len(sieveRuleRe.FindAll(raw, -1)); n > 0 {
		return n
	}
	// only comments and require lines, like the "# No filters defined." the editors save, is no filter
	for _, line := range strings.Split(string(raw), "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "require") {
			return 1
		}
	}
	return 0
}

// emailFilterCounts is the number of sieve rules of each account on the accounts page
func (e *Emails) emailFilterCounts(accounts []emailQuota) map[string]int {
	filters := map[string]int{}
	store := strings.TrimSpace(getEmailStorageLocation())
	var contexts map[string]string
	if !strings.HasPrefix(store, "/") {
		contexts = emailDomainContexts(e.MySQL)
	}
	for _, a := range accounts {
		addr := strings.ToLower(a.Email)
		_, domain, _ := strings.Cut(addr, "@")
		filters[a.Email] = emailFilterCount(emailMailDir(store, contexts, domain), addr)
	}
	return filters
}

// emailAccountExists is true for an address in the mailserver's account file, so nothing gets written for a made up one
func emailAccountExists(address string) bool {
	for _, a := range emailAccountsFromFile() {
		if strings.EqualFold(a.Email, address) {
			return true
		}
	}
	return false
}

// emailSievePath is where an account's sieve script lives, same path the user panel edits
func (e *Emails) emailSievePath(address string) (string, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	user, domain, _ := strings.Cut(address, "@")
	if !emailsRegex.MatchString(address) || strings.HasPrefix(user, ".") || strings.HasSuffix(user, ".") || strings.Contains(address, "..") {
		return "", errors.New("Invalid email address.")
	}
	store := strings.TrimSpace(getEmailStorageLocation())
	var contexts map[string]string
	if !strings.HasPrefix(store, "/") {
		contexts = emailDomainContexts(e.MySQL)
	}
	dir := emailMailDir(store, contexts, domain)
	if dir == "" {
		return "", errors.New("Can't tell where this domain's mail is stored.")
	}
	return filepath.Join(dir, user, "home", ".dovecot.sieve"), nil
}

// chownLike gives path the same owner as ref, the mailserver reads mailboxes as the account's user and not root
func chownLike(path, ref string) {
	if fi, err := os.Stat(ref); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			os.Chown(path, int(st.Uid), int(st.Gid))
		}
	}
}

// writeEmailSieve saves the script, a new home dir and file get the mailbox's owner
func writeEmailSieve(path, content string) error {
	home := filepath.Dir(path)
	mailbox := filepath.Dir(home)
	ref := mailbox
	if _, err := os.Stat(mailbox); err != nil {
		ref = filepath.Dir(mailbox)
	}
	if _, err := os.Stat(home); err != nil {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return err
		}
		chownLike(mailbox, ref)
		chownLike(home, ref)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	chownLike(path, ref)
	return nil
}

const maxSieveSize = 64 << 10

// ServeFilter handles GET/POST /emails/api/filter, GET returns the script and the parsed rules for the editor, POST saves a new script
func (e *Emails) ServeFilter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email   string `json:"email"`
		Content string `json:"content"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, maxSieveSize*2)).Decode(&body); err != nil {
			writeJSONMessage(w, http.StatusBadRequest, "Invalid request.")
			return
		}
	} else {
		body.Email = r.URL.Query().Get("email")
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !scopeFor(r, e.MySQL).AllowsEmail(email) {
		writeJSONMessage(w, http.StatusForbidden, "Email is not on a domain of your users.")
		return
	}
	if !emailAccountExists(email) {
		writeJSONMessage(w, http.StatusNotFound, "No such email account.")
		return
	}
	path, err := e.emailSievePath(email)
	if err != nil {
		writeJSONMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPost {
		if len(body.Content) > maxSieveSize {
			writeJSONMessage(w, http.StatusBadRequest, "The filter script is too large.")
			return
		}
		if err := writeEmailSieve(path, body.Content); err != nil {
			writeJSONMessage(w, http.StatusInternalServerError, "Error saving filters: "+err.Error())
			return
		}
	}
	raw, _ := os.ReadFile(path)
	filters := sieveparser.Parse(string(raw))
	if filters == nil {
		filters = []sieveparser.Filter{}
	}
	writeJSON(w, map[string]interface{}{"email": email, "path": path, "raw": string(raw), "filters": filters, "count": sieveRuleCount(raw)})
}
