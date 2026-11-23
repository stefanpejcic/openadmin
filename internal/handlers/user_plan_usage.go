package handlers

import (
	"database/sql"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// planUsageItem is one "used / limit" counter on the user overview, 0 limit means unlimited
type planUsageItem struct {
	Icon  template.HTML
	Label string
	Href  string
	Used  int
	Limit int
}

func (p planUsageItem) Unlimited() bool { return p.Limit <= 0 }

func (p planUsageItem) Percent() int {
	if p.Limit <= 0 {
		return 0
	}
	pct := p.Used * 100 / p.Limit
	if pct > 100 {
		pct = 100
	}
	return pct
}

// RingColor matches the small rings on the openpanel dashboard
func (p planUsageItem) RingColor() string { return usageColorClass(float64(p.Percent())) }

func planLimit(plan map[string]interface{}, key string) int {
	var s string
	switch v := plan[key].(type) {
	case string:
		s = v
	case int64:
		return int(v)
	case int:
		return v
	case []byte:
		s = string(v)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// userDomainNames lists the user's domains from the panel db
func userDomainNames(db *sql.DB, userID int64) []string {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT domain_url FROM domains WHERE user_id = ?`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil && d != "" {
			out = append(out, strings.ToLower(d))
		}
	}
	return out
}

// countMainDomains skips subdomains of the user's own domains, same as the plan limit in openpanel
func countMainDomains(domains []string) int {
	n := 0
	for _, d := range domains {
		sub := false
		for _, other := range domains {
			if other != d && strings.HasSuffix(d, "."+other) {
				sub = true
				break
			}
		}
		if !sub {
			n++
		}
	}
	return n
}

func countUserWebsites(db *sql.DB, userID int64) int {
	if db == nil {
		return 0
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sites s JOIN domains d ON d.domain_id = s.domain_id WHERE d.user_id = ?`, userID).Scan(&n)
	return n
}

func countUserEmails(domains []string) int {
	set := make(map[string]bool, len(domains))
	for _, d := range domains {
		set[d] = true
	}
	n := 0
	for _, a := range emailAccountsFromFile() {
		if at := strings.LastIndex(a.Email, "@"); at > 0 && set[strings.ToLower(a.Email[at+1:])] {
			n++
		}
	}
	return n
}

func countUserFTP(userContext string) int {
	raw, err := os.ReadFile(filepath.Join(FTPUsersDir, userContext, "users.list"))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// buildPlanUsage counts what the user has against the plan limits, each linking to the admin list searched for them
func buildPlanUsage(db *sql.DB, user *paneldb.UserData, plan map[string]interface{}, reseller bool) []planUsageItem {
	name := url.QueryEscape(stripSuspendedPrefix(user.Username))
	domains := userDomainNames(db, user.ID)
	websitesHref := "/websites?q=" + name
	// /websites is admin only
	if reseller {
		websitesHref = ""
	}
	return []planUsageItem{
		{Icon: webtemplates.UsageIcon("domains"), Label: "Domains", Href: "/domains?q=" + name, Used: countMainDomains(domains), Limit: planLimit(plan, "domains_limit")},
		{Icon: webtemplates.UsageIcon("websites"), Label: "Websites", Href: websitesHref, Used: countUserWebsites(db, user.ID), Limit: planLimit(plan, "websites_limit")},
		{Icon: webtemplates.UsageIcon("databases"), Label: "Databases", Href: "/databases?q=" + name, Used: len(readUserDatabases(user.Context)), Limit: planLimit(plan, "db_limit")},
		{Icon: webtemplates.UsageIcon("emails"), Label: "Emails", Href: "/emails/accounts?q=" + name, Used: countUserEmails(domains), Limit: planLimit(plan, "email_limit")},
		{Icon: webtemplates.UsageIcon("ftp"), Label: "FTP", Href: "/services/ftp?q=" + name, Used: countUserFTP(user.Context), Limit: planLimit(plan, "ftp_limit")},
	}
}
