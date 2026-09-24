package webtemplates

import (
	"html/template"
	"strings"
)

// NavItem is one modern-menu sidebar link, opening its area's first tab
type NavItem struct {
	Label   string
	Icon    template.HTML
	MenuID  string
	Href    string
	Active  bool
	Section string // heading rendered above this item, set only on the first item of a section

	Alert      string // "error" or "warning" shows a colored dot next to the label
	AlertTitle string // tooltip for that dot
}

// NavLink is one tab in the modern menu's page tab bar
type NavLink struct {
	Href   string
	Label  string
	Active bool
}

func hasAnyPrefix(path string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func hasModule(modules []string, name string) bool {
	for _, m := range modules {
		if strings.TrimSpace(m) == name {
			return true
		}
	}
	return false
}

// navArea is one modern-menu area: a sidebar link plus the tabs across its pages
type navArea struct {
	label   string
	icon    template.HTML
	menuID  string
	section string
	show    func(c *Chrome) bool
	match   func(c *Chrome, path string) bool
	tabs    func(c *Chrome, path string) []NavLink
}

type tabSpec struct {
	show   bool
	href   string
	label  string
	active bool
}

func buildTabs(specs ...tabSpec) []NavLink {
	var tabs []NavLink
	for _, s := range specs {
		if s.show {
			tabs = append(tabs, NavLink{Href: s.href, Label: s.label, Active: s.active})
		}
	}
	return tabs
}

func isEnterprise(c *Chrome) bool { return c.LicenseType == "Enterprise" }

func adminOnly(c *Chrome) bool { return !c.IsReseller }

func isAccountsPath(c *Chrome, path string) bool {
	return (strings.HasPrefix(path, "/user") && !strings.HasPrefix(path, "/user/import")) ||
		hasAnyPrefix(path, "/resellers", "/administrators", "/account", "/containers/", "/php/") ||
		(c.IsReseller && hasAnyPrefix(path, "/security/2fa", "/security/passkeys"))
}

func isServerPath(path string) bool {
	return hasAnyPrefix(path, "/server/resource-usage", "/server/crons", "/terminal", "/server/processes", "/tasks")
}

// modernAreas is the modern sidebar in display order, with the same role/license/module gating as the classic menu
var modernAreas = []navArea{
	{label: "Accounts", icon: accountsIcon, menuID: "accounts-menu",
		show:  func(c *Chrome) bool { return true },
		match: isAccountsPath,
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/users", "Users", (strings.HasPrefix(path, "/user") && !strings.HasPrefix(path, "/user/import")) || hasAnyPrefix(path, "/containers/", "/php/")},
				tabSpec{isEnterprise(c) && !c.IsReseller, "/resellers", "Resellers", strings.HasPrefix(path, "/resellers")},
				tabSpec{!c.IsReseller, "/administrators", "Administrators", strings.HasPrefix(path, "/administrators")},
				tabSpec{c.IsReseller, "/account", "Reseller Account", strings.HasPrefix(path, "/account")},
				tabSpec{c.IsReseller, "/security/2fa", "Two-Factor Authentication", strings.HasPrefix(path, "/security/2fa")},
				tabSpec{c.IsReseller, "/security/passkeys", "Passkeys", strings.HasPrefix(path, "/security/passkeys")},
			)
		}},
	{label: "Hosting Plans", icon: plansIcon, menuID: "plans-menu",
		show:  func(c *Chrome) bool { return true },
		match: func(c *Chrome, path string) bool { return hasAnyPrefix(path, "/plans", "/plan/", "/features") },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/plans", "User Packages", hasAnyPrefix(path, "/plans", "/plan/")},
				tabSpec{true, "/features", "Feature Manager", strings.HasPrefix(path, "/features")},
			)
		}},
	{label: "Domains", icon: domainsIcon, menuID: "domains-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool { return hasAnyPrefix(path, "/domains", "/dns/") },
		tabs: func(c *Chrome, path string) []NavLink {
			dns := hasModule(c.EnabledModules, "dns")
			isDNS := strings.HasPrefix(path, "/domains/dns") && !strings.HasPrefix(path, "/domains/dns-cluster")
			isCluster := hasAnyPrefix(path, "/domains/dns-cluster", "/dns/cluster")
			isZone := hasAnyPrefix(path, "/domains/zone-templates", "/dns/zone-templates")
			isFile := strings.HasPrefix(path, "/domains/file-templates")
			return buildTabs(
				tabSpec{true, "/domains", "Domains", !isDNS && !isCluster && !isZone && !isFile},
				tabSpec{dns, "/domains/dns", "DNS Zone Editor", isDNS},
				tabSpec{dns && isEnterprise(c), "/domains/dns-cluster", "DNS Cluster", isCluster},
				tabSpec{dns, "/domains/zone-templates", "Zone Templates", isZone},
				tabSpec{true, "/domains/file-templates", "Domain Templates", isFile},
			)
		}},
	{label: "Emails", icon: emailsIcon, menuID: "emails-menu",
		show:  func(c *Chrome) bool { return !c.IsReseller && isEnterprise(c) },
		match: func(c *Chrome, path string) bool { return strings.HasPrefix(path, "/emails") },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/emails/accounts", "Accounts", strings.HasPrefix(path, "/emails/accounts")},
				tabSpec{true, "/emails/queue", "Queue", strings.HasPrefix(path, "/emails/queue")},
				tabSpec{true, "/emails/reports", "Summary Reports", hasAnyPrefix(path, "/emails/reports", "/emails/data")},
				tabSpec{true, "/emails/settings", "Settings", strings.HasPrefix(path, "/emails/settings")},
			)
		}},
	{label: "Backups", icon: backupsIcon, menuID: "backups-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool {
			return hasAnyPrefix(path, "/backups", "/user/import", "/import/")
		},
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/backups/user", "User Backups", strings.HasPrefix(path, "/backups/user")},
				tabSpec{true, "/backups/system", "System Backups", strings.HasPrefix(path, "/backups/system")},
				tabSpec{isEnterprise(c), "/user/import", "Import Account", hasAnyPrefix(path, "/user/import", "/import/")},
			)
		}},

	{label: "Services", icon: servicesIcon, menuID: "services-menu", section: "Server", show: adminOnly,
		match: func(c *Chrome, path string) bool { return strings.HasPrefix(path, "/services") },
		tabs: func(c *Chrome, path string) []NavLink {
			isLimits := strings.HasPrefix(path, "/services/limits")
			isLogs := strings.HasPrefix(path, "/services/logs")
			isPodman := strings.HasPrefix(path, "/services/podman")
			isFTP := strings.HasPrefix(path, "/services/ftp")
			return buildTabs(
				tabSpec{true, "/services", "Service Status", !isLimits && !isLogs && !isPodman && !isFTP},
				tabSpec{true, "/services/limits", "Service Limits", isLimits},
				tabSpec{true, "/services/logs", "Log Files", isLogs},
				tabSpec{true, "/services/podman", "Podman", isPodman},
				tabSpec{isEnterprise(c), "/services/ftp", "FTP", isFTP},
			)
		}},
	{label: "Security", icon: securityIcon, menuID: "security-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool { return strings.HasPrefix(path, "/security/") },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/security/firewall", "Firewall", strings.HasPrefix(path, "/security/firewall")},
				tabSpec{true, "/security/waf", "Web Firewall (Coraza)", strings.HasPrefix(path, "/security/waf")},
				tabSpec{true, "/security/imunify", "ImunifyAV", strings.HasPrefix(path, "/security/imunify")},
				tabSpec{true, "/security/basic_auth", "Basic Auth", strings.HasPrefix(path, "/security/basic_auth")},
				tabSpec{true, "/security/2fa", "Two-Factor Authentication", strings.HasPrefix(path, "/security/2fa")},
				tabSpec{true, "/security/passkeys", "Passkeys", strings.HasPrefix(path, "/security/passkeys")},
				tabSpec{true, "/security/blacklist-useragents", "Blocked User Agents", strings.HasPrefix(path, "/security/blacklist-useragents")},
				tabSpec{true, "/security/disable-admin", "Disable OpenAdmin", strings.HasPrefix(path, "/security/disable-admin")},
			)
		}},
	{label: "Server", icon: serverIcon, menuID: "server-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool { return isServerPath(path) },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/server/resource-usage", "Resource Usage", strings.HasPrefix(path, "/server/resource-usage")},
				tabSpec{true, "/server/crons", "Scheduled Actions", strings.HasPrefix(path, "/server/crons")},
				tabSpec{true, "/terminal", "Terminal", strings.HasPrefix(path, "/terminal")},
				tabSpec{true, "/server/processes", "Process Manager", strings.HasPrefix(path, "/server/processes")},
			)
		}},
	{label: "System", icon: systemIcon, menuID: "system-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool { return strings.HasPrefix(path, "/server/") && !isServerPath(path) },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/server/timezone", "Server Time", strings.HasPrefix(path, "/server/timezone")},
				tabSpec{true, "/server/root-password", "Root Password", strings.HasPrefix(path, "/server/root-password")},
				tabSpec{true, "/server/ssh", "SSH Access", strings.HasPrefix(path, "/server/ssh")},
				tabSpec{true, "/server/reboot", "Server Reboot", strings.HasPrefix(path, "/server/reboot")},
				tabSpec{true, "/server/swap", "Swap", strings.HasPrefix(path, "/server/swap")},
				tabSpec{true, "/server/migrate", "Migrate", strings.HasPrefix(path, "/server/migrate")},
				tabSpec{true, "/server/demo-mode", "Demo Mode", strings.HasPrefix(path, "/server/demo-mode")},
			)
		}},

	{label: "Settings", icon: settingsIcon, menuID: "settings-menu", section: "Settings", show: adminOnly,
		match: func(c *Chrome, path string) bool { return strings.HasPrefix(path, "/settings/") },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(
				tabSpec{true, "/settings/general", "General", strings.HasPrefix(path, "/settings/general")},
				tabSpec{true, "/settings/open-panel", "OpenPanel", strings.HasPrefix(path, "/settings/open-panel")},
				tabSpec{true, "/settings/defaults", "User Defaults", strings.HasPrefix(path, "/settings/defaults")},
				tabSpec{true, "/settings/modules", "Modules", strings.HasPrefix(path, "/settings/modules")},
				tabSpec{isEnterprise(c), "/settings/api", "API Access", strings.HasPrefix(path, "/settings/api")},
				tabSpec{true, "/settings/php", "PHP", strings.HasPrefix(path, "/settings/php")},
				tabSpec{true, "/settings/notifications", "Notifications", strings.HasPrefix(path, "/settings/notifications")},
				tabSpec{true, "/settings/updates", "Updates", strings.HasPrefix(path, "/settings/updates")},
				tabSpec{true, "/settings/locales", "Locales", strings.HasPrefix(path, "/settings/locales")},
				tabSpec{true, "/settings/custom-code", "Custom Code", strings.HasPrefix(path, "/settings/custom-code")},
			)
		}},
	{label: "License & Support", icon: licenseIcon, menuID: "license-menu", show: adminOnly,
		match: func(c *Chrome, path string) bool { return hasAnyPrefix(path, "/license", "/support/") },
		tabs: func(c *Chrome, path string) []NavLink {
			return buildTabs(tabSpec{true, "/license", "License & Support", true})
		}},
}

// BuildModernNav returns the modern sidebar items and the tab bar for the current page, tabs are nil when the page's area has fewer than two unless that one tab has docs
func BuildModernNav(c *Chrome) ([]NavItem, []NavLink) {
	path := c.CurrentPath
	var items []NavItem
	var tabs []NavLink
	pendingSection := ""
	for _, a := range modernAreas {
		if a.section != "" {
			pendingSection = a.section
		}
		if !a.show(c) {
			continue
		}
		areaTabs := a.tabs(c, path)
		if len(areaTabs) == 0 {
			continue
		}
		active := a.match(c, path)
		items = append(items, NavItem{Label: a.label, Icon: a.icon, MenuID: a.menuID, Href: areaTabs[0].Href, Active: active, Section: pendingSection})
		pendingSection = ""
		if active && tabs == nil && (len(areaTabs) > 1 || helpDocs[areaTabs[0].Href] != "") {
			tabs = areaTabs
		}
	}
	return items, tabs
}

// helpDocs maps a tab's href to its docs page (website/docs/<doc>.md), so every page under that tab gets the tab bar's Documentation button
var helpDocs = map[string]string{
	"/users":                         "admin/accounts/users",
	"/resellers":                     "admin/accounts/resellers",
	"/administrators":                "admin/accounts/administrators",
	"/plans":                         "admin/plans/hosting_plans",
	"/features":                      "admin/plans/feature-manager",
	"/domains":                       "admin/domains/domains",
	"/domains/dns":                   "admin/domains/dns",
	"/domains/dns-cluster":           "admin/domains/dns-cluster",
	"/domains/zone-templates":        "admin/domains/dns_templates",
	"/domains/file-templates":        "admin/domains/file_templates",
	"/emails/accounts":               "admin/emails/emails",
	"/emails/queue":                  "admin/emails/queue",
	"/emails/reports":                "admin/emails/summary",
	"/emails/settings":               "admin/emails/settings",
	"/backups/user":                  "admin/backups/user",
	"/backups/system":                "admin/backups/system",
	"/user/import":                   "admin/backups/cpanel",
	"/services":                      "admin/services/status",
	"/services/limits":               "admin/services/limits",
	"/services/logs":                 "admin/services/logs",
	"/services/podman":               "admin/services/podman",
	"/services/ftp":                  "admin/services/ftp",
	"/security/firewall":             "admin/security/firewall",
	"/security/waf":                  "admin/security/waf",
	"/security/imunify":              "admin/security/imunify",
	"/security/basic_auth":           "admin/security/basic_auth",
	"/security/2fa":                  "admin/security/2fa",
	"/security/passkeys":             "admin/security/passkeys",
	"/security/blacklist-useragents": "admin/security/blacklist-useragents",
	"/security/disable-admin":        "admin/security/disable-admin",
	"/server/resource-usage":         "admin/advanced/resource-usage",
	"/server/crons":                  "admin/advanced/crons",
	"/terminal":                      "admin/advanced/terminal",
	"/server/processes":              "admin/advanced/processes",
	"/server/timezone":               "admin/system/timezone",
	"/server/root-password":          "admin/system/root-password",
	"/server/ssh":                    "admin/system/ssh",
	"/server/reboot":                 "admin/system/reboot",
	"/server/swap":                   "admin/system/swap",
	"/server/migrate":                "admin/system/migrate",
	"/server/demo-mode":              "admin/system/demo-mode",
	"/settings/general":              "admin/settings/general",
	"/settings/open-panel":           "admin/settings/openpanel",
	"/settings/defaults":             "admin/settings/defaults",
	"/settings/modules":              "admin/settings/modules",
	"/settings/api":                  "admin/settings/api",
	"/settings/php":                  "admin/settings/php",
	"/settings/notifications":        "admin/settings/notifications",
	"/settings/updates":              "admin/settings/updates",
	"/settings/locales":              "admin/settings/locales",
	"/settings/custom-code":          "admin/settings/custom_code",
	"/license":                       "admin/license",
}

// helpSection picks the part of doc a page shows, "until:X" keeps what's before the X heading and "from:X" keeps that section
func helpSection(doc, path string) string {
	switch doc {
	case "admin/accounts/users":
		if strings.HasPrefix(path, "/users/") && strings.Trim(path, "/") != "users" {
			return "from:Single User"
		}
		return "until:Single User"
	}
	return ""
}

// HelpDocFor returns the docs page for the active tab plus the part of it to show, "" when there's none
func HelpDocFor(tabs []NavLink, path string) (doc, section string) {
	for _, t := range tabs {
		if t.Active && helpDocs[t.Href] != "" {
			return helpDocs[t.Href], helpSection(helpDocs[t.Href], path)
		}
	}
	return "", ""
}
