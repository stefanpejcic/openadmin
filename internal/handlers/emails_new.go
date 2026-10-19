package handlers

import (
	"context"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

// emailDomainOption is a domain new mailboxes can be created on, with the user that owns it
type emailDomainOption struct {
	Domain string `json:"domain"`
	Owner  string `json:"owner"`
}

// same rule the user panel uses for the part before the @
var emailLocalPartRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}$`)

var emailQuotaUnits = map[string]bool{"k": true, "M": true, "G": true, "T": true}

// emailDomainOptions lists every domain the acting admin or reseller can add mailboxes on
func (e *Emails) emailDomainOptions(r *http.Request) []emailDomainOption {
	if e.MySQL == nil {
		return nil
	}
	rows, err := paneldb.GetAllDomains(e.MySQL)
	if err != nil {
		return nil
	}
	scope := scopeFor(r, e.MySQL)
	var out []emailDomainOption
	for _, row := range rows {
		domain, _ := row["domain_url"].(string)
		owner, _ := row["username"].(string)
		if domain != "" && scope.AllowsDomain(domain) {
			out = append(out, emailDomainOption{Domain: strings.ToLower(domain), Owner: owner})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// emailAddRun runs opencli email-setup, it prints nothing on success so any stdout means it failed
var emailAddRun = func(args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencli", args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), strings.TrimSpace(errBuf.String()), err
}

// ServeEmailsNew handles POST /emails/new: creates a mailbox the same way the user panel's /emails/new does
func (e *Emails) ServeEmailsNew(w http.ResponseWriter, r *http.Request) {
	back := func(msg, category string) {
		auth.AddFlash(w, r, e.Sessions, msg, category)
		http.Redirect(w, r, "/emails/accounts", http.StatusSeeOther)
	}
	if checkMailserverStatus() != "running" {
		back("The mailserver is not running, start it before adding email accounts.", "error")
		return
	}

	domain := strings.ToLower(strings.TrimSpace(r.FormValue("domain")))
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	switch {
	case domain == "" || username == "" || password == "":
		back("Domain, username and password are required.", "error")
		return
	case !emailLocalPartRe.MatchString(username):
		back("Username can only contain letters, numbers, and . _ % + - (no @).", "error")
		return
	case len(password) < 8 || len(password) > 128 || strings.ContainsAny(password, " \t\r\n"):
		back("Password must be 8 to 128 characters with no spaces.", "error")
		return
	}

	owner := ""
	for _, d := range e.emailDomainOptions(r) {
		if d.Domain == domain {
			owner = d.Owner
		}
	}
	if owner == "" {
		back("Domain "+domain+" was not found.", "error")
		return
	}

	quota := ""
	if n, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("quota")), 64); err == nil && n > 0 {
		unit := r.FormValue("unit")
		if !emailQuotaUnits[unit] {
			back("Invalid quota unit.", "error")
			return
		}
		quota = strconv.FormatFloat(n, 'f', -1, 64) + unit
	}

	email := username + "@" + domain
	args := []string{"email-setup", "email", "add", email, password}
	// the quota can only be set once the mailbox exists
	if quota != "" {
		args = append(args, "--wait")
	}
	if r.FormValue("send_welcome") != "" {
		args = append(args, "--send-welcome")
	}
	stdout, stderr, err := emailAddRun(args...)
	defer resetEmailAccountsCache()
	if err != nil || stdout != "" {
		msg := stderr
		if msg == "" {
			msg = stdout
		}
		if msg == "" {
			msg = err.Error()
		}
		back("Failed to add email "+email+": "+msg, "error")
		return
	}

	if quota != "" {
		if _, qErr, err := emailAddRun("email-setup", "quota", "set", email, quota); err != nil {
			back("Email "+email+" was added for user "+owner+", but setting the "+quota+" quota failed: "+qErr, "warning")
			return
		}
	}
	back("Email "+email+" added for user "+owner+".", "success")
}
