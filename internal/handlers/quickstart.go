// This file computes the /onboarding page's 3-step content: step 1's
// toggleable services (real, live status), and steps 2/3's Done/not-done
// setup checklists. The various "default" values compared against below
// are hardcoded snapshots of
// https://github.com/stefanpejcic/openpanel-configuration (the shipped
// factory defaults for modules/plans/features/env files), not fetched at
// request time -- a login shouldn't depend on GitHub being reachable.
package handlers

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"

	"openadmin/internal/config"
	"openadmin/internal/paneldb"
)

// quickStartStep is one row of onboarding steps 2 ("Server configuration")
// and 3 ("Users and plans"): a link to the relevant settings page, its
// "what we offer" pitch, and whether it's already done.
type quickStartStep struct {
	Label       string
	Description string
	Offer       []string
	Href        string
	Done        bool
}

// onboardingServiceRef is one container/systemd unit backing an
// onboardingServiceItem -- some items (Email + Webmail) start more than
// one real service together.
type onboardingServiceRef struct {
	RealName    string
	ServiceType string // "docker" or "system" -- passed straight through to POST /services
}

// onboardingServiceItem is one row of onboarding step 1 ("Enable modules/
// services"): a toggleable service, pre-checked from its live running
// status.
type onboardingServiceItem struct {
	Label       string
	Description string
	Offer       []string
	Services    []onboardingServiceRef
	Active      bool
}

// quickStartDefaultModules is the factory-default enabled_modules list from
// openpanel-configuration's default openpanel.config.
var quickStartDefaultModules = []string{
	"services", "filemanager", "autoinstaller", "domains", "ssl", "php", "dns", "locale",
	"account", "sessions", "mysql", "mysql_import", "remote_mysql", "process_manager",
	"php_options", "favorites", "phpmyadmin", "crons", "domain_logs", "wordpress",
	"website_builder", "usage", "webserver_conf", "waf", "redis", "memcached",
	"login_history", "activity", "twofa", "goaccess", "docroot",
}

// quickStartDefaultBasicFeatures / quickStartDefaultDefaultFeatures are the
// factory-default feature lists for the "basic" and "default" feature sets
// (openpanel-configuration/openpanel/features/{basic,default}.txt) -- the
// two feature sets used by the two factory-default hosting plans.
var quickStartDefaultBasicFeatures = []string{
	"services", "account", "locale", "mysql", "remote_mysql", "mysql_import", "php",
	"php_options", "crons", "wordpress", "website_builder", "autoinstaller", "info", "waf",
	"filemanager", "fix_permissions", "dns", "domains", "docroot", "goaccess", "redis",
	"memcached", "temporary_links", "login_history", "twofa", "services",
}

var quickStartDefaultDefaultFeatures = []string{
	"services", "notifications", "account", "sessions", "locale", "favorites", "varnish",
	"docker", "ftp", "emails", "mysql", "remote_mysql", "mysql_import", "mysql_conf",
	"postgresql", "remote_postgresql", "postgresql_import", "postgresql_conf", "php",
	"php_options", "php_ini", "phpmyadmin", "pgadmin", "crons", "backups", "wordpress",
	"website_builder", "pm2", "autoinstaller", "disk_usage", "inodes", "usage", "info",
	"webserver_conf", "waf", "filemanager", "fix_permissions", "malware_scan", "dns",
	"redirects", "ssl", "docroot", "domains", "capitalize_domains", "edit_vhost",
	"domain_logs", "goaccess", "process_manager", "redis", "memcached", "elasticsearch",
	"opensearch", "temporary_links", "login_history", "twofa", "activity", "services",
}

// quickStartDefaultPlanFields are the factory-default field values for the
// two plans openpanel-configuration seeds on install (id 1 "Standard plan",
// id 2 "Developer Plus"), keyed by plan name.
var quickStartDefaultPlanFields = map[string]map[string]string{
	"Standard plan": {
		"domains_limit": "0", "websites_limit": "10", "email_limit": "0", "ftp_limit": "0",
		"disk_limit": "5 GB", "inodes_limit": "1000000", "db_limit": "0", "cpu": "2", "ram": "2g",
		"bandwidth": "100", "feature_set": "basic", "max_email_quota": "10G", "max_hourly_email": "100",
	},
	"Developer Plus": {
		"domains_limit": "0", "websites_limit": "10", "email_limit": "0", "ftp_limit": "0",
		"disk_limit": "10 GB", "inodes_limit": "1000000", "db_limit": "0", "cpu": "4", "ram": "6g",
		"bandwidth": "500", "feature_set": "default", "max_email_quota": "0", "max_hourly_email": "0",
	},
}

// quickStartParseEnv parses a .env-style file (KEY="value" per line) into a
// flat map. Returns nil if the file can't be read.
func quickStartParseEnv(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"`)
		value = strings.Trim(value, `'`)
		out[key] = value
	}
	return out
}

func quickStartStringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// quickStartModulesCustomized reports whether the enabled_modules list
// differs (in either direction) from the factory default set.
func quickStartModulesCustomized(modulesConfigPath string) bool {
	enabled := modulesEnabledList(modulesConfigPath)
	if enabled == nil {
		return false
	}
	current := append([]string{}, enabled...)
	sort.Strings(current)
	def := append([]string{}, quickStartDefaultModules...)
	sort.Strings(def)
	return !quickStartStringSliceEqual(current, def)
}

func quickStartFeatureListFromFile(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func quickStartFeatureSetCustomized(path string, defaults []string) bool {
	current := quickStartFeatureListFromFile(path)
	if current == nil {
		return false
	}
	sort.Strings(current)
	def := append([]string{}, defaults...)
	sort.Strings(def)
	return !quickStartStringSliceEqual(current, def)
}

// quickStartFeaturesCustomized reports whether either of the two
// factory-default feature sets ("basic", "default") has been edited from
// its shipped contents.
func quickStartFeaturesCustomized() bool {
	return quickStartFeatureSetCustomized(FeaturesDir+"basic.txt", quickStartDefaultBasicFeatures) ||
		quickStartFeatureSetCustomized(FeaturesDir+"default.txt", quickStartDefaultDefaultFeatures)
}

// quickStartPHPWebserverCustomized reports whether the default PHP version
// or webserver (used for new accounts) differ from the factory defaults.
func quickStartPHPWebserverCustomized() bool {
	current := quickStartParseEnv(DefaultsEnvPath)
	if current == nil {
		return false
	}
	if v, ok := current["WEB_SERVER"]; ok && v != "apache" {
		return true
	}
	if v, ok := current["DEFAULT_PHP_VERSION"]; ok && v != "8.5" {
		return true
	}
	return false
}

func quickStartRowString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// quickStartPlansCustomized reports whether the hosting plans differ from
// the two factory-default plans -- a different plan count, a renamed/
// replaced default plan, or any edited field on either.
func quickStartPlansCustomized(db *sql.DB) bool {
	if db == nil {
		return false
	}
	rows, err := paneldb.GetAllPlans(db, nil)
	if err != nil {
		return false
	}
	if len(rows) != len(quickStartDefaultPlanFields) {
		return true
	}
	seen := map[string]bool{}
	for _, row := range rows {
		name := quickStartRowString(row["name"])
		defaults, ok := quickStartDefaultPlanFields[name]
		if !ok {
			return true
		}
		seen[name] = true
		for field, want := range defaults {
			if quickStartRowString(row[field]) != want {
				return true
			}
		}
	}
	return len(seen) != len(quickStartDefaultPlanFields)
}

// quickStartSMTPConfigured reports whether an SMTP server has been set for
// outgoing notification emails.
func quickStartSMTPConfigured() bool {
	return config.Load(config.OpenpanelConfigPath).Get("SMTP", "mail_server", "") != ""
}

// quickStartPanelDomainConfigured reports whether a custom domain has been
// set for panel access. This is NOT read from openpanel.config's [DEFAULT]
// force_domain key -- installs commonly have no such key at all, since
// generalAdminDomain() (the same live check the /settings/general page
// itself uses) instead shells out via "opencli domain".
func quickStartPanelDomainConfigured() bool {
	return generalAdminDomain() != ""
}

// quickStartLicenseConfigured reports whether an Enterprise license key is
// configured (same test as apiFeatureEnabled's license check).
func quickStartLicenseConfigured() bool {
	return config.Load(config.OpenpanelConfigPath).Get("LICENSE", "key", "") != ""
}

// quickStartDNSConfigured reports whether the "dns" module is enabled AND
// at least one custom nameserver (ns1-ns4) has been set.
func quickStartDNSConfigured() bool {
	dnsEnabled := false
	for _, m := range modulesEnabledList(chromeSite.ModulesConfig) {
		if m == "dns" {
			dnsEnabled = true
			break
		}
	}
	if !dnsEnabled {
		return false
	}
	cfg := config.Load(config.OpenpanelConfigPath)
	for _, key := range []string{"ns1", "ns2", "ns3", "ns4"} {
		if cfg.Get("DEFAULT", key, "") != "" {
			return true
		}
	}
	return false
}

// quickStartPodmanImagesReady reports whether the Podman images a fresh
// account actually needs are already downloaded: redis, plus at least one
// of the three webserver images (nginx/apache/openlitespeed). Only
// already-downloaded images count -- podmanListImages(nil, nil) with no
// stack refs never synthesizes a NotDownloaded row, so every row here is
// real.
func quickStartPodmanImagesReady() bool {
	hasRedis, hasWebserver := false, false
	for _, img := range podmanListImages(nil, nil) {
		repo := strings.ToLower(img.Repository)
		if strings.Contains(repo, "redis") {
			hasRedis = true
		}
		if strings.Contains(repo, "nginx") || strings.Contains(repo, "apache") || strings.Contains(repo, "openlitespeed") {
			hasWebserver = true
		}
	}
	return hasRedis && hasWebserver
}

// quickStartUpdatePreferencesCustomized reports whether the update
// preference differs from the factory default ("minor_and_major": both
// autoupdate and autopatch "on").
func quickStartUpdatePreferencesCustomized() bool {
	cfg := config.Load(config.OpenpanelConfigPath)
	autoupdate := strings.ToLower(strings.TrimSpace(cfg.Get("PANEL", "autoupdate", "on")))
	autopatch := strings.ToLower(strings.TrimSpace(cfg.Get("PANEL", "autopatch", "on")))
	return autoupdate != "on" || autopatch != "on"
}

// onboardingServiceActive looks up a single service's live running status
// from the same docker/systemd caches services.go's /services page uses,
// treating "unknown" as not active (a safe default for a pre-checked
// checkbox -- we'd rather under- than over-claim something is running).
func onboardingServiceActive(realName, serviceType string, dockerCache, systemdCache map[string]bool, userCount int) bool {
	status := getServiceStatusFromCache(map[string]interface{}{"real_name": realName, "type": serviceType}, dockerCache, systemdCache, userCount)
	return status != nil && *status
}

// onboardingServiceItems builds onboarding step 1 ("Enable modules/
// services"): Community gets OpenPanel + the firewall; Enterprise also
// gets email/webmail, FTP, DNS, phpMyAdmin, and ImunifyAV. Each item's
// Active is its real, current running status -- these back checkboxes the
// user can toggle, not a Done/not-done link.
func onboardingServiceItems(licenseType string, userCount int) []onboardingServiceItem {
	isEnterprise := licenseType == "Enterprise"

	dockerCache := fetchAllDockerStatuses()
	systemdCache := fetchAllSystemdStatuses([]string{"csf", "imunify-antivirus"})
	active := func(realName, serviceType string) bool {
		return onboardingServiceActive(realName, serviceType, dockerCache, systemdCache, userCount)
	}

	openpanelThirdOffer := "Custom port"
	if isEnterprise {
		openpanelThirdOffer = "REST API"
	}

	items := []onboardingServiceItem{
		{
			Label:       "OpenPanel",
			Description: "The hosting control panel your users log into to manage their sites, databases, and email. It starts automatically the first time you create a user.",
			Offer:       []string{"Web hosting control panel", "Container-isolated accounts", openpanelThirdOffer},
			Services:    []onboardingServiceRef{{RealName: "openpanel", ServiceType: "docker"}},
			Active:      active("openpanel", "docker"),
		},
		{
			Label:       "Firewall (CSF)",
			Description: "Config Server Firewall protects this server from brute-force attacks and unwanted traffic.",
			Offer:       []string{"Login Failure Daemon", "IP blocklisting", "Port control"},
			Services:    []onboardingServiceRef{{RealName: "csf", ServiceType: "system"}},
			Active:      active("csf", "system"),
		},
	}

	if !isEnterprise {
		return items
	}

	return append(items,
		onboardingServiceItem{
			Label:       "Email + Webmail",
			Description: "Run a full mail server with Dovecot/Postfix and a Roundcube webmail client for your hosting users.",
			Offer:       []string{"Dovecot & Postfix", "Roundcube webmail", "Per-user mailboxes"},
			Services: []onboardingServiceRef{
				{RealName: "openadmin_mailserver", ServiceType: "docker"},
				{RealName: "openadmin_roundcube", ServiceType: "docker"},
			},
			Active: active("openadmin_mailserver", "docker") && active("openadmin_roundcube", "docker"),
		},
		onboardingServiceItem{
			Label:       "FTP",
			Description: "vsftpd gives hosting users FTP access to their account's files.",
			Offer:       []string{"vsftpd server", "Per-user FTP accounts", "Passive port range"},
			Services:    []onboardingServiceRef{{RealName: "openadmin_ftp", ServiceType: "docker"}},
			Active:      active("openadmin_ftp", "docker"),
		},
		onboardingServiceItem{
			Label:       "DNS",
			Description: "Run your own BIND9 nameservers so you can host DNS zones for your customers' domains.",
			Offer:       []string{"BIND9 nameserver", "DNS zone editor", "Zone templates"},
			Services:    []onboardingServiceRef{{RealName: "openpanel_dns", ServiceType: "docker"}},
			Active:      active("openpanel_dns", "docker"),
		},
		onboardingServiceItem{
			Label:       "phpMyAdmin",
			Description: "Give hosting users a web UI to manage their MySQL databases.",
			Offer:       []string{"Database browser", "Autologin", "Import/export"},
			Services:    []onboardingServiceRef{{RealName: "phpmyadmin", ServiceType: "docker"}},
			Active:      active("phpmyadmin", "docker"),
		},
		onboardingServiceItem{
			Label:       "ImunifyAV",
			Description: "Scan hosting accounts for malware and get alerted to infected files.",
			Offer:       []string{"Malware scanning", "Infected file alerts", "On-demand scans"},
			Services:    []onboardingServiceRef{{RealName: "imunify-antivirus", ServiceType: "system"}},
			Active:      active("imunify-antivirus", "system"),
		},
	)
}

// onboardingConfigSteps builds onboarding step 2 ("Server configuration").
// License verification and DNS/nameservers are Enterprise-only.
func onboardingConfigSteps(licenseType string) []quickStartStep {
	steps := []quickStartStep{
		{
			Label:       "Enable modules",
			Description: "Turn on or off individual OpenPanel features, from file manager to WAF.",
			Offer:       []string{"30+ toggleable modules", "Per-feature control", "No restart required"},
			Href:        "/settings/modules",
			Done:        quickStartModulesCustomized(chromeSite.ModulesConfig),
		},
		{
			Label:       "Set a custom domain for panel access",
			Description: "Access OpenAdmin and OpenPanel from your own domain instead of a bare IP address.",
			Offer:       []string{"Custom hostname", "Branded login URL", "SSL-ready"},
			Href:        "/settings/general",
			Done:        quickStartPanelDomainConfigured(),
		},
		{
			Label:       "Set up SMTP and notifications",
			Description: "Configure an SMTP server so OpenPanel can send account and admin notification emails.",
			Offer:       []string{"Outgoing SMTP", "Admin alerts", "User notifications"},
			Href:        "/settings/notifications",
			Done:        quickStartSMTPConfigured(),
		},
		{
			Label:       "Configure update preferences",
			Description: "Choose how OpenPanel keeps itself up to date -- automatic, patches only, or manual.",
			Offer:       []string{"Auto-updates", "Patch-only mode", "Manual control"},
			Href:        "/settings/updates",
			Done:        quickStartUpdatePreferencesCustomized(),
		},
		{
			Label:       "Secure your server with the firewall",
			Description: "Review and customize your CSF firewall rules -- ports, allow/deny lists, and rate limits.",
			Offer:       []string{"Port rules", "Allow/deny lists", "Rate limiting"},
			Href:        "/security/firewall",
			Done:        firewallCommandAvailableRun("csf"),
		},
		{
			Label:       "Download required Podman images",
			Description: "New accounts need Redis and a webserver image (Nginx, Apache, or OpenLiteSpeed) already downloaded so account creation doesn't stall on a first-time pull.",
			Offer:       []string{"Redis", "Nginx / Apache / OpenLiteSpeed", "Faster account creation"},
			Href:        "/services/podman#images",
			Done:        quickStartPodmanImagesReady(),
		},
	}

	if licenseType != "Enterprise" {
		return steps
	}

	return append(steps,
		quickStartStep{
			Label:       "Verify your Enterprise license",
			Description: "Confirm your Enterprise license is active to unlock unlimited users, domains, and email.",
			Offer:       []string{"License verification", "Unlimited accounts", "Priority support"},
			Href:        "/license",
			Done:        quickStartLicenseConfigured(),
		},
		quickStartStep{
			Label:       "Enable DNS and set custom nameservers",
			Description: "Turn on the DNS module and point ns1-ns4 at your own nameservers for full zone hosting.",
			Offer:       []string{"Custom nameservers", "DNS zone editor", "Zone templates"},
			Href:        "/domains/dns",
			Done:        quickStartDNSConfigured(),
		},
	)
}

// onboardingUserPlanSteps builds onboarding step 3 ("Users and plans").
func onboardingUserPlanSteps(licenseType string, data dashboardAdminData, mysqlDB *sql.DB) []quickStartStep {
	userStep := quickStartStep{
		Label:       "Add a user account",
		Description: "Create your first hosting account to start managing a real website.",
		Offer:       []string{"Isolated container per user", "Assign a plan", "Full panel access"},
		Href:        "/users",
		Done:        data.UserCount > 0,
	}
	if licenseType == "Enterprise" {
		userStep.Label = "Add a user account or import from backup"
		userStep.Description = "Create your first hosting account, or import existing accounts from a cPanel/backup archive."
		userStep.Offer = []string{"Isolated container per user", "cPanel/backup import", "Assign a plan"}
	}

	return []quickStartStep{
		{
			Label:       "Set default PHP version and webserver",
			Description: "Choose the default PHP version and web server (Apache, Nginx, or OpenResty) new accounts get.",
			Offer:       []string{"Default PHP version", "Apache / Nginx / OpenResty", "Per-account override"},
			Href:        "/settings/defaults",
			Done:        quickStartPHPWebserverCustomized(),
		},
		{
			Label:       "Create or edit a hosting plan",
			Description: "Define the resource limits and pricing tiers your hosting users get assigned to.",
			Offer:       []string{"Disk & bandwidth limits", "CPU & RAM caps", "Unlimited plans"},
			Href:        "/plans",
			Done:        quickStartPlansCustomized(mysqlDB),
		},
		{
			Label:       "Enable features on hosting plans",
			Description: "Control exactly which OpenPanel features each hosting plan's users can see and use.",
			Offer:       []string{"Per-plan feature sets", "Granular permissions", "Reseller overrides"},
			Href:        "/features",
			Done:        quickStartFeaturesCustomized(),
		},
		userStep,
		{
			Label:       "Point a domain to this server",
			Description: "Add your first domain and go live -- OpenPanel provisions the vhost automatically.",
			Offer:       []string{"Automatic vhost setup", "Free SSL", "DNS zone included"},
			Href:        "/domains",
			Done:        data.DomainCount > 0,
		},
	}
}
