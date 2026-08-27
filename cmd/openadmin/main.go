package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/bootstrap"
	"openadmin/internal/config"
	"openadmin/internal/handlers"
	"openadmin/internal/license"
	"openadmin/internal/mysqldb"
	"openadmin/internal/paneldb"
	"openadmin/internal/server"
	"openadmin/static"
)

func main() {
	handlers.EnsureErrorLogDirs()

	devMode := bootstrap.IsDevMode()

	appLog, err := bootstrap.SetupLogging(devMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up logging: %v\n", err)
		os.Exit(1)
	}

	bootstrap.RemoveRestartFlag(appLog)
	bootstrap.ExitIfDisabled(appLog)
	bootstrap.RunStartupHousekeeping(appLog)

	hostname := bootstrap.ReadHostnameBlock(appLog)
	host := hostname.Domain
	if host == "" {
		host = hostname.IP
	}

	disabled := server.Disable2087PortPresent()
	var certPaths bootstrap.CertPaths

	switch {
	case disabled:
		appLog.Println("Skipping SSL setup on port 2087 as /root/disable_2087_port exists.")
	case host != "":
		found := false
		certPaths, found = bootstrap.CheckSSLExists(host)
		switch {
		case found:
			appLog.Printf("HTTPS - %s certificate is configured.", certPaths.CertType)
		case hostname.Domain != "":
			appLog.Println("HTTP - Domain is set but no certificate exists, point domain A record to issue LetsEncrypt SSL or add custom cert: https://openpanel.com/docs/articles/server/how-to-set-custom-ssl-openpanel-webmail/")
		default:
			appLog.Println("HTTP - IP is set but no certificate exists.")
		}
	default:
		appLog.Println("HTTP - Using IP address for panel access, use 'opencli domain <DOMAIN_NAME>' to set a domain or  'opencli domain ip' to set IP.")
	}

	if hostname.Port != bootstrap.DefaultPort {
		appLog.Printf("Custom port will be used for OpenAdmin service: %d", hostname.Port)
	}

	useTLS := server.UseTLS(certPaths.CertFile, certPaths.KeyFile, disabled)

	secretKey, err := auth.LoadSecretKey()
	if err != nil {
		appLog.Printf("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	adminDB, err := admindb.Open()
	if err != nil {
		appLog.Printf("failed to open admin database: %v", err)
		fmt.Fprintf(os.Stderr, "failed to open admin database: %v\n", err)
		os.Exit(1)
	}

	// Loading the dovecot master pass is fatal at startup if the file is
	// missing, matching the same fatal-at-startup treatment already used
	// for auth.LoadSecretKey above.
	dovecotMasterPass, err := handlers.LoadDovecotMasterPass()
	if err != nil {
		appLog.Printf("%v", err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	// Runs once at startup, best-effort (failures are logged, never fatal).
	handlers.EnsureMasterUser(dovecotMasterPass)

	// MySQL connects lazily per query (see mysqldb.Open's doc comment), so a
	// down/not-yet-configured database at boot isn't fatal here.
	mysqlDB, err := mysqldb.Open()
	if err != nil {
		appLog.Printf("MySQL not available yet (%v) -- dashboard panel data will show its error fallback until it is.", err)
	}
	if err := paneldb.EnsurePlansSchema(mysqlDB); err != nil {
		appLog.Printf("could not ensure plans.upsell_plan_id/upsell_url columns exist: %v", err)
	}

	publicIP := detectPublicIP()
	licenseKey := config.Openpanel().Get("LICENSE", "key", "")
	licType := license.Type(licenseKey)

	// licenseChecker is nil for Community installs: RequireEnterprise
	// treats a nil checker as "not licensed", so Enterprise-only routes
	// (none exist in this Go build yet -- see the migration backlog) fail
	// closed by default rather than needing every call site to remember a
	// separate Community check.
	var licenseChecker *license.Checker
	if licType == "Enterprise" {
		licenseChecker = license.NewChecker(licenseKey, publicIP)
		licenseChecker.StartBackgroundRecheck()
		if !licenseChecker.Valid() {
			appLog.Println("Enterprise license key is set but could not be validated.")
		}
	}

	handler, err := newHandler(appDeps{
		AdminDB:           adminDB,
		MySQL:             mysqlDB,
		SecretKey:         secretKey,
		DovecotMasterPass: dovecotMasterPass,
		UseTLS:            useTLS,
		ForceDomainValue:  hostname.Domain,
		LoginBlockLimit:   atoiDefault(config.Admin().Get("PANEL", "login_blocklimit", "20"), 20),
		LoginRatePerMin:   atoiDefault(config.Admin().Get("PANEL", "login_ratelimit", "5"), 5),
		DemoMode:          config.Openpanel().Get("PANEL", "demo_mode", "off") == "on",
		ValidateSessionIP: config.Openpanel().Get("SECURITY", "validate_ip_address_cookie", "yes") == "yes",
		BasicAuthEnabled:  config.Admin().Get("SECURITY", "basic_auth", "no") == "yes",
		BasicAuthUsername: config.Admin().Get("SECURITY", "basic_auth_username", ""),
		BasicAuthPassword: config.Admin().Get("SECURITY", "basic_auth_password", ""),
		PublicIP:          publicIP,
		ServerHostname:    osHostname(),
		PanelVersion:      openpanelVersion(),
		LicenseType:       licType,
		LicenseChecker:    licenseChecker,
		DevMode:           devMode,
		AdminPortValue:    adminPortAtStartup(),
		SearchJSONPath:    handlers.ResolveSearchJSONFilePath(),
		Logger:            appLog,
	})
	if err != nil {
		appLog.Printf("failed to build request handler: %v", err)
		fmt.Fprintf(os.Stderr, "failed to build request handler: %v\n", err)
		os.Exit(1)
	}

	accessLog, err := server.NewAccessLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open access log: %v\n", err)
		os.Exit(1)
	}
	handler = server.AccessLogMiddleware(accessLog, handler)

	if err := server.Run(server.Options{
		Port:     hostname.Port,
		CertFile: certPaths.CertFile,
		KeyFile:  certPaths.KeyFile,
		Handler:  handler,
		Logger:   appLog,
	}); err != nil {
		appLog.Printf("server error: %v", err)
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// appDeps bundles everything newHandler needs to build the full router +
// middleware chain, factored out of main() so it can be exercised directly
// by an end-to-end test with scratch/mock dependencies instead of the real
// system paths (which need root).
type appDeps struct {
	AdminDB *admindb.DB
	MySQL   *sql.DB

	SecretKey         string
	DovecotMasterPass string
	UseTLS            bool

	ForceDomainValue string
	PublicIP         string
	ServerHostname   string
	PanelVersion     string
	LicenseType      string
	LicenseChecker   *license.Checker
	DevMode          bool
	AdminPortValue   string
	SearchJSONPath   string
	Logger           *log.Logger

	LoginBlockLimit   int
	LoginRatePerMin   int
	DemoMode          bool
	ValidateSessionIP bool

	BasicAuthEnabled  bool
	BasicAuthUsername string
	BasicAuthPassword string
}

func newHandler(d appDeps) (http.Handler, error) {
	handlers.InitChromeSiteInfo(d.PublicIP, d.ServerHostname, d.ForceDomainValue, d.PanelVersion, d.LicenseType, d.DevMode, handlers.ModulesConfigFilePath)

	sessions := auth.NewManager(d.SecretKey, d.UseTLS)
	loginRateLimiter := auth.NewPerIPLimiter(d.LoginRatePerMin, 5)

	login := handlers.NewLogin(d.AdminDB, sessions, loginRateLimiter, d.LoginBlockLimit)
	login.PublicIP = d.PublicIP
	login.ServerHostname = d.ServerHostname
	login.ForceDomainValue = d.ForceDomainValue
	login.PanelVersion = d.PanelVersion
	login.LicenseType = d.LicenseType

	autologin := &handlers.Autologin{MySQL: d.MySQL, Sessions: sessions, PublicIP: d.PublicIP, AdminPort: d.AdminPortValue}

	dash := &handlers.Dashboard{MySQL: d.MySQL, Sessions: sessions, AdminDB: d.AdminDB}
	admins := &handlers.Administrators{DB: d.AdminDB, Sessions: sessions, LicenseChecker: d.LicenseChecker}
	plans := &handlers.Plans{MySQL: d.MySQL, Sessions: sessions, LicenseChecker: d.LicenseChecker}
	twofa := &handlers.TwoFA{DB: d.AdminDB, Sessions: sessions}
	passkeys := &handlers.Passkeys{DB: d.AdminDB, Sessions: sessions}
	notifications := &handlers.Notifications{Sessions: sessions}
	notificationSettings := &handlers.NotificationSettings{Sessions: sessions}
	serverUtils := &handlers.ServerUtils{Sessions: sessions}
	cronjobs := &handlers.Cronjobs{Sessions: sessions}
	securityToggles := &handlers.SecurityToggles{Sessions: sessions}
	users := &handlers.Users{MySQL: d.MySQL, Sessions: sessions, AdminDB: d.AdminDB}
	domains := &handlers.Domains{MySQL: d.MySQL, Sessions: sessions}
	dnsZoneEditor := &handlers.DNSZoneEditor{MySQL: d.MySQL, Sessions: sessions}
	caddyFileEditor := &handlers.CaddyFileEditor{MySQL: d.MySQL, Sessions: sessions}
	vhostFileEditor := &handlers.VHostFileEditor{MySQL: d.MySQL, Sessions: sessions}
	sslPage := &handlers.SSLPage{Sessions: sessions}
	accessLogs := &handlers.AccessLogs{MySQL: d.MySQL, Sessions: sessions}
	goAccessStats := &handlers.GoAccessStats{Sessions: sessions}
	services := &handlers.Services{MySQL: d.MySQL, Sessions: sessions}
	ftp := &handlers.FTP{Sessions: sessions}
	limits := &handlers.Limits{Sessions: sessions}
	logs := &handlers.Logs{Sessions: sessions}
	reboot := &handlers.Reboot{Sessions: sessions}
	swap := &handlers.Swap{Sessions: sessions}
	caddySettings := &handlers.Caddy{Sessions: sessions}
	phpSettings := &handlers.PHP{Sessions: sessions, MySQL: d.MySQL}
	migrate := &handlers.Migrate{Sessions: sessions}
	customCode := &handlers.CustomCode{Sessions: sessions, LicenseChecker: d.LicenseChecker}
	general := &handlers.General{Sessions: sessions, DevMode: d.DevMode}
	locales := &handlers.Locales{Sessions: sessions}
	modules := &handlers.Modules{Sessions: sessions}
	updates := &handlers.Updates{Sessions: sessions, PanelVersion: d.PanelVersion}
	features := &handlers.Features{MySQL: d.MySQL, Sessions: sessions}
	resellers := &handlers.Resellers{DB: d.AdminDB, MySQL: d.MySQL, Sessions: sessions}
	openpanelSettings := &handlers.OpenpanelSettings{Sessions: sessions}
	defaultsSettings := &handlers.Defaults{MySQL: d.MySQL, Sessions: sessions}
	generalStatic := &handlers.GeneralStatic{Static: static.Files}
	licensePage := &handlers.LicensePage{Sessions: sessions}
	dnsTemplates := &handlers.DNSTemplates{Sessions: sessions}
	firewall := &handlers.Firewall{Sessions: sessions}
	domainTemplates := &handlers.DomainTemplates{Sessions: sessions}
	mailer := &handlers.Mailer{PublicIP: d.PublicIP, Logger: d.Logger}
	processManager := &handlers.ProcessManager{Sessions: sessions}
	slave := &handlers.Slave{Sessions: sessions}
	imunify := &handlers.Imunify{Sessions: sessions}
	waf := &handlers.WAF{Sessions: sessions}
	sshHandlers := &handlers.SSH{Sessions: sessions}
	podmanHandlers := &handlers.Podman{Sessions: sessions, MySQL: d.MySQL}
	backupsHandlers := &handlers.Backups{Sessions: sessions}
	search := &handlers.Search{MySQL: d.MySQL, Sessions: sessions, JSONFilePath: d.SearchJSONPath}
	dnsCluster := &handlers.DNSCluster{Sessions: sessions, PublicIP: d.PublicIP}
	emails := &handlers.Emails{Sessions: sessions, PublicIP: d.PublicIP, MasterPass: d.DovecotMasterPass}
	terminal := &handlers.Terminal{Sessions: sessions, MySQL: d.MySQL}
	importer := &handlers.Importer{Sessions: sessions, MySQL: d.MySQL}

	apiAuth := &handlers.APIAuth{DB: d.AdminDB, MySQL: d.MySQL, SecretKey: d.SecretKey}
	apiWelcome := &handlers.APIWelcome{DB: d.AdminDB, SecretKey: d.SecretKey, Limiter: loginRateLimiter}
	apiSettings := &handlers.APISettings{Sessions: sessions, SecretKey: d.SecretKey}
	apiUsers := &handlers.APIUsers{MySQL: d.MySQL, PublicIP: d.PublicIP}
	apiDomains := &handlers.APIDomains{MySQL: d.MySQL}
	apiPlans := &handlers.APIPlans{MySQL: d.MySQL, LicenseChecker: d.LicenseChecker}
	apiContainers := &handlers.APIContainers{MySQL: d.MySQL}
	apiDNS := &handlers.APIDNS{PublicIP: d.PublicIP}
	apiServices := &handlers.APIServices{}
	apiSystem := &handlers.APISystem{MySQL: d.MySQL}
	apiNotifications := &handlers.APINotifications{}
	apiDomainFiles := &handlers.APIDomainFiles{MySQL: d.MySQL}
	apiDomainStats := &handlers.APIDomainStats{}
	apiEmails := &handlers.APIEmails{PublicIP: d.PublicIP}
	apiSecurity := &handlers.APISecurity{}
	apiSecurity2FA := &handlers.APISecurity2FA{Auth: apiAuth}
	apiServerCrons := &handlers.APIServerCrons{}
	apiServerSSH := &handlers.APIServerSSH{}
	apiServerOps := &handlers.APIServerOps{}
	apiServerProcesses := &handlers.APIServerProcesses{}
	apiServerMigrate := &handlers.APIServerMigrate{}
	apiServerSwap := &handlers.APIServerSwap{}
	apiServicesPodman := &handlers.APIServicesPodman{MySQL: d.MySQL}
	apiBackups := &handlers.APIBackups{}
	apiUserExport := &handlers.APIUserExport{Users: users}
	apiSettingsAdministrators := &handlers.APISettingsAdministrators{DB: d.AdminDB, LicenseChecker: d.LicenseChecker}
	apiSettingsResellers := &handlers.APISettingsResellers{DB: d.AdminDB, Auth: apiAuth}
	apiSettingsGeneral := &handlers.APISettingsGeneral{DevMode: d.DevMode}
	apiSettingsDefaults := &handlers.APISettingsDefaults{MySQL: d.MySQL}
	apiSettingsFeatures := &handlers.APISettingsFeatures{MySQL: d.MySQL, Auth: apiAuth}
	apiSettingsLocales := &handlers.APISettingsLocales{}
	apiSettingsModules := &handlers.APISettingsModules{}
	apiSettingsCustomCode := &handlers.APISettingsCustomCode{}
	apiSettingsPHP := &handlers.APISettingsPHP{}
	apiSettingsCaddy := &handlers.APISettingsCaddy{}
	apiSettingsUpdates := &handlers.APISettingsUpdates{}
	apiSettingsNotifications := &handlers.APISettingsNotifications{}
	apiLicense := &handlers.APILicense{}
	apiSupport := &handlers.APISupport{}
	apiImport := &handlers.APIImport{}

	authOpts := auth.Options{
		DemoMode:          d.DemoMode,
		ValidateSessionIP: d.ValidateSessionIP,
	}

	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static.Files)))

	mux.HandleFunc("GET /login", login.ServeLoginPage)
	mux.HandleFunc("GET /login/", login.ServeLoginPage)
	mux.HandleFunc("POST /login", login.HandleLoginSubmit)
	mux.HandleFunc("POST /login/", login.HandleLoginSubmit)
	mux.HandleFunc("GET /login/2fa", login.ServeTwoFAPage)
	mux.HandleFunc("POST /login/2fa", login.HandleTwoFASubmit)
	mux.HandleFunc("POST /login/passkey/begin", login.HandlePasskeyBegin)
	mux.HandleFunc("POST /login/passkey/complete", login.HandlePasskeyComplete)
	mux.HandleFunc("GET /logout", auth.RequireLogin(sessions, authOpts, login.HandleLogout))
	mux.HandleFunc("POST /api/tour/complete", auth.RequireLogin(sessions, authOpts, login.HandleTourComplete))
	mux.HandleFunc("GET /api/", handlers.RequireAPIFeatureEnabled(apiWelcome.ServeWelcome))
	mux.HandleFunc("POST /api/", handlers.RequireAPIFeatureEnabled(apiWelcome.ServeWelcome))
	mux.HandleFunc("GET /api/whoami", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiWelcome.ServeWhoami)))

	mux.HandleFunc("GET /api/users", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("POST /api/users", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("GET /api/users/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("POST /api/users/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("PUT /api/users/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("PATCH /api/users/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("DELETE /api/users/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeUsers)))
	mux.HandleFunc("POST /api/users/{username}/autologin", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServeAutologin)))
	mux.HandleFunc("POST /api/users/{username}/permissions/reset", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUsers.ServePermissionsReset)))
	mux.HandleFunc("GET /api/users/{username}/export/status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUserExport.ServeStatus)))
	mux.HandleFunc("POST /api/users/{username}/export/create", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUserExport.ServeCreate)))
	mux.HandleFunc("GET /api/users/{username}/export/download/{filename...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUserExport.ServeDownload)))
	mux.HandleFunc("POST /api/users/{username}/export/delete", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiUserExport.ServeDelete)))

	mux.HandleFunc("GET /api/domains", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDomains.ServeDomains)))
	mux.HandleFunc("POST /api/domains/new", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDomains.HandleAddDomain)))
	// GET/POST /api/domains/docroot/{domain}, GET/POST /api/domains/{domain_name}/dns,
	// GET/POST /api/domains/{domain_name}/caddy, GET/POST /api/domains/{domain_name}/ssl,
	// GET /api/domains/{domain_name}/log, and POST /api/domains/{action}/{domain}
	// all share the same two-segment shape and genuinely overlap at single
	// URLs like /api/domains/docroot/dns -- Go's ServeMux refuses to register
	// genuinely overlapping patterns at all, so this is dispatched manually,
	// same as the equivalent HTML-page /domains/{seg2}/{seg3} routes above.
	mux.HandleFunc("GET /api/domains/{seg2}/{seg3}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(func(w http.ResponseWriter, r *http.Request) {
		seg2, seg3 := r.PathValue("seg2"), r.PathValue("seg3")
		switch {
		case seg2 == "docroot":
			r.SetPathValue("domain", seg3)
			apiDomains.ServeDomainDocroot(w, r)
		case seg3 == "dns":
			r.SetPathValue("domain_name", seg2)
			apiDNS.ServeDomainDNSZone(w, r)
		case seg3 == "caddy":
			r.SetPathValue("domain_name", seg2)
			apiDomainFiles.ServeDomainCaddyConfig(w, r)
		case seg3 == "ssl":
			r.SetPathValue("domain_name", seg2)
			apiDomainStats.ServeDomainSSL(w, r)
		case seg3 == "log":
			r.SetPathValue("domain_name", seg2)
			apiDomainStats.ServeDomainAccessLog(w, r)
		default:
			http.NotFound(w, r)
		}
	})))
	mux.HandleFunc("POST /api/domains/{seg2}/{seg3}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(func(w http.ResponseWriter, r *http.Request) {
		seg2, seg3 := r.PathValue("seg2"), r.PathValue("seg3")
		switch {
		case seg2 == "docroot":
			r.SetPathValue("domain", seg3)
			apiDomains.ServeDomainDocroot(w, r)
		case seg3 == "dns":
			r.SetPathValue("domain_name", seg2)
			apiDNS.ServeDomainDNSZone(w, r)
		case seg3 == "caddy":
			r.SetPathValue("domain_name", seg2)
			apiDomainFiles.ServeDomainCaddyConfig(w, r)
		case seg3 == "ssl":
			r.SetPathValue("domain_name", seg2)
			apiDomainStats.ServeDomainSSL(w, r)
		default:
			r.SetPathValue("action", seg2)
			r.SetPathValue("domain", seg3)
			apiDomains.HandleDomainAction(w, r)
		}
	})))
	mux.HandleFunc("GET /api/domains/{domain_name}/vhost/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiDomainFiles.ServeDomainVHostConfig)))
	mux.HandleFunc("POST /api/domains/{domain_name}/vhost/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiDomainFiles.ServeDomainVHostConfig)))
	mux.HandleFunc("GET /api/domains/{domain_name}/stats/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiDomainStats.ServeDomainStats)))
	mux.HandleFunc("GET /api/domains/file-templates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDomainFiles.ServeDomainFileTemplates)))
	mux.HandleFunc("POST /api/domains/file-templates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDomainFiles.ServeDomainFileTemplates)))

	mux.HandleFunc("GET /api/plans", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeList)))
	mux.HandleFunc("POST /api/plans", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeList)))
	mux.HandleFunc("GET /api/plans/{plan_id}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeDetail)))
	mux.HandleFunc("PUT /api/plans/{plan_id}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeDetail)))
	mux.HandleFunc("PATCH /api/plans/{plan_id}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeDetail)))
	mux.HandleFunc("DELETE /api/plans/{plan_id}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiPlans.ServeDetail)))

	mux.HandleFunc("GET /api/users/{username}/containers", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiContainers.ServeUserContainers)))
	mux.HandleFunc("POST /api/users/{username}/containers/{action}/{container_name}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiContainers.ServeManageContainer)))

	mux.HandleFunc("GET /api/dns/cluster", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDNS.ServeDNSCluster)))
	mux.HandleFunc("POST /api/dns/cluster", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDNS.ServeDNSCluster)))
	mux.HandleFunc("GET /api/dns/cluster/{ip}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDNS.ServeDNSClusterNodeInfo)))
	mux.HandleFunc("GET /api/dns/zone-templates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDNS.ServeDNSZoneTemplates)))
	mux.HandleFunc("POST /api/dns/zone-templates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiDNS.ServeDNSZoneTemplates)))

	mux.HandleFunc("GET /api/emails/settings", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeSettings)))
	mux.HandleFunc("POST /api/emails/settings", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeSettings)))
	mux.HandleFunc("GET /api/emails/accounts", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeAccounts)))
	mux.HandleFunc("POST /api/emails/accounts", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeAccounts)))
	mux.HandleFunc("DELETE /api/emails/accounts", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeAccounts)))
	mux.HandleFunc("GET /api/emails/queue", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeQueue)))
	mux.HandleFunc("POST /api/emails/queue", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeQueue)))
	mux.HandleFunc("GET /api/emails/domain-limits", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeDomainLimits)))
	mux.HandleFunc("POST /api/emails/domain-limits", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiEmails.ServeDomainLimits)))

	mux.HandleFunc("GET /api/security/basic-auth", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeBasicAuth)))
	mux.HandleFunc("POST /api/security/basic-auth", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeBasicAuth)))
	mux.HandleFunc("GET /api/security/blacklist-useragents", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeBlacklistUseragents)))
	mux.HandleFunc("POST /api/security/blacklist-useragents", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeBlacklistUseragents)))
	mux.HandleFunc("POST /api/security/disable-admin", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.HandleDisableAdmin)))
	mux.HandleFunc("GET /api/security/firewall", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeFirewall)))
	mux.HandleFunc("POST /api/security/firewall", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeFirewall)))
	mux.HandleFunc("GET /api/security/waf", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeWAFStatus)))
	mux.HandleFunc("POST /api/security/waf", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeWAFStatus)))
	mux.HandleFunc("GET /api/security/waf/rules", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeWAFRules)))
	mux.HandleFunc("POST /api/security/waf/rules", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSecurity.ServeWAFRules)))
	mux.HandleFunc("GET /api/security/2fa", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.ServeStatus)))
	mux.HandleFunc("POST /api/security/2fa/enable", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.HandleEnable)))
	mux.HandleFunc("POST /api/security/2fa/disable", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.HandleDisable)))
	mux.HandleFunc("GET /api/security/passkeys", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.ServePasskeys)))
	mux.HandleFunc("POST /api/security/passkeys", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.ServePasskeys)))
	mux.HandleFunc("DELETE /api/security/passkeys", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSecurity2FA.ServePasskeys)))

	mux.HandleFunc("GET /api/services", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServices.ServeServicesFile)))
	mux.HandleFunc("PUT /api/services", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServices.ServeServicesFile)))
	mux.HandleFunc("GET /api/services/status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServices.ServeServicesStatus)))
	mux.HandleFunc("POST /api/service/{action}/{service_name}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServices.HandleManageService)))

	mux.HandleFunc("GET /api/docker/info", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSystem.ServeDockerInfo)))
	mux.HandleFunc("GET /api/ips", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSystem.ServeIPs)))
	mux.HandleFunc("GET /api/system", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSystem.ServeSystemInfo)))
	mux.HandleFunc("GET /api/usage/cpu", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSystem.ServeCPUUsage)))
	mux.HandleFunc("GET /api/usage/memory", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSystem.ServeMemoryUsage)))
	mux.HandleFunc("GET /api/usage/server", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSystem.ServeDiskUsage)))

	mux.HandleFunc("GET /api/notifications", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiNotifications.ServeNotifications)))
	mux.HandleFunc("POST /api/notifications/{line_number}/read", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiNotifications.HandleMarkRead)))
	mux.HandleFunc("DELETE /api/notifications/{line_number}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiNotifications.HandleDelete)))
	mux.HandleFunc("GET /api/usage/disk", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiNotifications.ServeDiskUsage)))

	mux.HandleFunc("GET /api/server/crons", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerCrons.ServeCrons)))
	mux.HandleFunc("POST /api/server/crons", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerCrons.ServeCrons)))
	mux.HandleFunc("GET /api/server/ssh", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSSH.ServeSSH)))
	mux.HandleFunc("POST /api/server/ssh", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSSH.ServeSSH)))
	mux.HandleFunc("GET /api/server/ssh/config", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSSH.ServeSSHConfig)))
	mux.HandleFunc("POST /api/server/ssh/config", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSSH.ServeSSHConfig)))
	mux.HandleFunc("GET /api/server/timezone", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerOps.ServeTimezone)))
	mux.HandleFunc("POST /api/server/timezone", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerOps.ServeTimezone)))
	mux.HandleFunc("POST /api/server/memory/drop-cache", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(serverUtils.HandleDropMemoryCache)))
	mux.HandleFunc("POST /api/server/memory/drop-swap", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(serverUtils.HandleDropSwapCache)))
	mux.HandleFunc("GET /api/server/processes", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerProcesses.ServeProcesses)))
	mux.HandleFunc("POST /api/server/processes/{pid}/{action}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerProcesses.ServeProcessAction)))
	mux.HandleFunc("GET /api/server/node", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerOps.ServeNode)))
	mux.HandleFunc("POST /api/server/node", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerOps.ServeNode)))
	mux.HandleFunc("POST /api/server/root-password", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPISuperAdmin(apiServerOps.ServeRootPassword)))
	mux.HandleFunc("POST /api/server/reboot", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPISuperAdmin(apiServerOps.ServeReboot)))
	mux.HandleFunc("GET /api/server/reboot/status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(reboot.ServeRebootStatus)))
	mux.HandleFunc("GET /api/server/migrate", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerMigrate.ServeMigrate)))
	mux.HandleFunc("POST /api/server/migrate", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerMigrate.ServeMigrate)))

	mux.HandleFunc("GET /api/server/swap", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSwap.ServeSwap)))
	mux.HandleFunc("POST /api/server/swap/action/{action}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSwap.ServeSwapAction)))
	mux.HandleFunc("GET /api/server/swap/action-status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServerSwap.ServeSwapActionStatus)))

	mux.HandleFunc("GET /api/services/podman", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeInfo)))
	mux.HandleFunc("GET /api/services/podman/images", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImages)))
	mux.HandleFunc("GET /api/services/podman/volumes", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeVolumes)))
	mux.HandleFunc("GET /api/services/podman/networks", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeNetworks)))
	mux.HandleFunc("GET /api/services/podman/disk-usage", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeDiskUsage)))
	mux.HandleFunc("POST /api/services/podman/images/{action}/{id...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImageAction)))
	mux.HandleFunc("GET /api/services/podman/images/action-status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImageActionStatus)))
	mux.HandleFunc("GET /api/services/podman/images/check-update", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImageCheckUpdate)))
	mux.HandleFunc("POST /api/services/podman/images/bulk/{action}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImagesBulkAction)))
	mux.HandleFunc("GET /api/services/podman/images/bulk-status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImagesBulkStatus)))
	mux.HandleFunc("GET /api/services/podman/images/vulnerabilities", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiServicesPodman.ServeImageVulnerabilities)))

	mux.HandleFunc("GET /api/backups/system", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackups)))
	mux.HandleFunc("POST /api/backups/system/settings", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackupsSettings)))
	mux.HandleFunc("POST /api/backups/system/run", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackupsRun)))
	mux.HandleFunc("POST /api/backups/system/restore/{filename...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackupsRestore)))
	mux.HandleFunc("POST /api/backups/system/delete/{filename...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackupsDelete)))
	mux.HandleFunc("GET /api/backups/system/action-status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeSystemBackupsActionStatus)))
	mux.HandleFunc("GET /api/backups/user", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackups)))
	mux.HandleFunc("POST /api/backups/user/settings", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackupsSettings)))
	mux.HandleFunc("GET /api/backups/user/configuration", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackupsConfiguration)))
	mux.HandleFunc("POST /api/backups/user/configuration", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackupsConfiguration)))
	mux.HandleFunc("POST /api/backups/user/run", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackupsRun)))
	mux.HandleFunc("GET /api/backups/user/action-status", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiBackups.ServeUserBackupsActionStatus)))

	mux.HandleFunc("GET /api/settings/administrators", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsAdministrators.ServeSettingsAdministrators)))
	mux.HandleFunc("POST /api/settings/administrators", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsAdministrators.ServeSettingsAdministrators)))
	mux.HandleFunc("GET /api/settings/resellers", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsResellers.ServeSettingsResellers)))
	mux.HandleFunc("GET /api/settings/resellers/enabled", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsResellers.ServeSettingsResellersEnabled)))
	mux.HandleFunc("POST /api/settings/resellers/enabled", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsResellers.ServeSettingsResellersEnabled)))
	mux.HandleFunc("POST /api/settings/resellers", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsResellers.ServeSettingsResellers)))
	mux.HandleFunc("GET /api/settings/general", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsGeneral.ServeSettingsGeneral)))
	mux.HandleFunc("POST /api/settings/general", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsGeneral.ServeSettingsGeneral)))
	mux.HandleFunc("GET /api/settings/defaults", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsDefaults.ServeSettingsDefaults)))
	mux.HandleFunc("POST /api/settings/defaults", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsDefaults.ServeSettingsDefaults)))
	mux.HandleFunc("GET /api/settings/defaults/files", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsDefaults.ServeSettingsDefaultsFiles)))
	mux.HandleFunc("POST /api/settings/defaults/files", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsDefaults.ServeSettingsDefaultsFiles)))
	mux.HandleFunc("DELETE /api/settings/defaults/files", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsDefaults.ServeSettingsDefaultsFiles)))
	mux.HandleFunc("GET /api/settings/defaults/files/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiSettingsDefaults.ServeSettingsDefaultsFilesForUser)))
	mux.HandleFunc("POST /api/settings/defaults/files/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiSettingsDefaults.ServeSettingsDefaultsFilesForUser)))
	mux.HandleFunc("GET /api/settings/features", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsFeatures.ServeSettingsFeatures)))
	mux.HandleFunc("POST /api/settings/features", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsFeatures.ServeSettingsFeatures)))
	mux.HandleFunc("GET /api/settings/features/{plan}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsFeatures.ServeSettingsFeatures)))
	mux.HandleFunc("POST /api/settings/features/{plan}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIToken(apiSettingsFeatures.ServeSettingsFeatures)))

	mux.HandleFunc("GET /api/settings/locales", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsLocales.Serve)))
	mux.HandleFunc("POST /api/settings/locales", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsLocales.Serve)))
	mux.HandleFunc("GET /api/settings/modules", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsModules.Serve)))
	mux.HandleFunc("POST /api/settings/modules", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsModules.Serve)))
	mux.HandleFunc("GET /api/settings/custom-code", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsCustomCode.Serve)))
	mux.HandleFunc("POST /api/settings/custom-code", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsCustomCode.Serve)))
	mux.HandleFunc("GET /api/settings/php", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsPHP.Serve)))
	mux.HandleFunc("POST /api/settings/php", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsPHP.Serve)))
	mux.HandleFunc("GET /api/settings/caddy/metrics", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsCaddy.ServeMetrics)))
	mux.HandleFunc("GET /api/settings/updates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsUpdates.Serve)))
	mux.HandleFunc("POST /api/settings/updates", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsUpdates.Serve)))
	mux.HandleFunc("POST /api/settings/updates/now", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsUpdates.ServeUpdateNow)))
	mux.HandleFunc("GET /api/settings/updates/tags", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsUpdates.ServeTags)))
	mux.HandleFunc("POST /api/settings/updates/tags", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsUpdates.ServeTags)))
	mux.HandleFunc("GET /api/settings/notifications", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsNotifications.Serve)))
	mux.HandleFunc("POST /api/settings/notifications", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSettingsNotifications.Serve)))

	mux.HandleFunc("GET /api/license", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiLicense.ServeLicense)))
	mux.HandleFunc("POST /api/license", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiLicense.ServeLicense)))
	mux.HandleFunc("DELETE /api/license", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiLicense.ServeLicense)))
	mux.HandleFunc("GET /api/license/info", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(licensePage.ServeLicenseInfo)))
	mux.HandleFunc("POST /api/license/verify", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(licensePage.ServeLicenseVerify)))
	mux.HandleFunc("GET /api/support/report", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiSupport.ServeSupportReport)))
	mux.HandleFunc("GET /api/import/{panel_type}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeImportFromBackup)))
	mux.HandleFunc("POST /api/import/{panel_type}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeImportFromBackup)))
	mux.HandleFunc("GET /api/import/logs/account/{log_filename...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeAccountImportLog)))
	mux.HandleFunc("GET /api/import/backup-files", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeListBackupFiles)))
	mux.HandleFunc("GET /api/import/transfers", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeTransfers)))
	mux.HandleFunc("POST /api/import/transfers", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeTransfers)))
	mux.HandleFunc("GET /api/import/transfers/{username}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIOwnerOrAdmin("username", apiImport.ServeTransfersForUser)))
	mux.HandleFunc("GET /api/import/logs/transfer/{log_filename...}", handlers.RequireAPIFeatureEnabled(apiAuth.RequireAPIAdmin(apiImport.ServeTransferImportLog)))

	mux.HandleFunc("GET /{$}", auth.RequireLogin(sessions, authOpts, dash.ServeDashboard))
	mux.HandleFunc("GET /dashboard", auth.RequireLogin(sessions, authOpts, dash.ServeDashboard))
	mux.HandleFunc("POST /api/quickstart/dismiss", auth.RequireLogin(sessions, authOpts, dash.HandleQuickStartDismiss))
	mux.HandleFunc("GET /onboarding", auth.RequireLogin(sessions, authOpts, dash.ServeOnboardingPage))
	mux.HandleFunc("GET /json/system", auth.RequireLogin(sessions, authOpts, dash.ServeSystemInfo))
	mux.HandleFunc("GET /json/{resource}", auth.RequireLogin(sessions, authOpts, dash.ServeResourceUsage))
	mux.HandleFunc("GET /json/user_activity_status", auth.RequireAdmin(sessions, authOpts, dash.ServeUserActivityStatus))
	mux.HandleFunc("GET /json/combined_activity", auth.RequireLogin(sessions, authOpts, dash.ServeCombinedActivity))
	mux.HandleFunc("GET /server/resource-usage", auth.RequireLogin(sessions, authOpts, dash.ServeResourceUsagePage))
	mux.HandleFunc("GET /server/resource-usage/history", auth.RequireLogin(sessions, authOpts, dash.ServeResourceUsageHistory))
	mux.HandleFunc("GET /server/resource-usage/history/data", auth.RequireLogin(sessions, authOpts, dash.ServeResourceUsageHistoryData))

	mux.HandleFunc("GET /administrators", auth.RequireAdmin(sessions, authOpts, admins.ServeAdministrators))
	mux.HandleFunc("POST /administrators", auth.RequireAdmin(sessions, authOpts, admins.ServeAdministrators))
	mux.HandleFunc("GET /administrators/{action}/{username}", auth.RequireAdmin(sessions, authOpts, admins.ServeEditForm))

	mux.HandleFunc("GET /plans", auth.RequireLogin(sessions, authOpts, plans.ServeList))
	mux.HandleFunc("POST /plans", auth.RequireLogin(sessions, authOpts, plans.ServeList))
	mux.HandleFunc("GET /plans/new", auth.RequireLogin(sessions, authOpts, plans.ServeNewForm))
	mux.HandleFunc("POST /plans/new", auth.RequireLogin(sessions, authOpts, plans.HandleCreate))
	mux.HandleFunc("GET /plans/{plan_id}", auth.RequireLogin(sessions, authOpts, plans.ServeEdit))
	mux.HandleFunc("POST /plans/{plan_id}", auth.RequireLogin(sessions, authOpts, plans.ServeEdit))
	mux.HandleFunc("POST /plan/delete/{plan_name}", auth.RequireLogin(sessions, authOpts, plans.HandleDelete))
	mux.HandleFunc("GET /plan/apply/{filename}", auth.RequireLogin(sessions, authOpts, plans.ServeApplyLog))
	mux.HandleFunc("GET /system/ips/{username}", auth.RequireLogin(sessions, authOpts, plans.ServeIPAddresses))

	mux.HandleFunc("GET /security/2fa", auth.RequireLogin(sessions, authOpts, twofa.ServeSettings))
	mux.HandleFunc("POST /security/2fa/enable", auth.RequireLogin(sessions, authOpts, twofa.HandleEnable))
	mux.HandleFunc("POST /security/2fa/disable", auth.RequireLogin(sessions, authOpts, twofa.HandleDisable))

	mux.HandleFunc("GET /security/passkeys", auth.RequireLogin(sessions, authOpts, passkeys.ServeSettings))
	mux.HandleFunc("POST /security/passkeys/register/begin", auth.RequireLogin(sessions, authOpts, passkeys.HandleRegisterBegin))
	mux.HandleFunc("POST /security/passkeys/register/complete", auth.RequireLogin(sessions, authOpts, passkeys.HandleRegisterComplete))
	mux.HandleFunc("POST /security/passkeys/rename", auth.RequireLogin(sessions, authOpts, passkeys.HandleRename))
	mux.HandleFunc("POST /security/passkeys/delete", auth.RequireLogin(sessions, authOpts, passkeys.HandleDelete))

	mux.HandleFunc("GET /notifications", auth.RequireAdmin(sessions, authOpts, notifications.ServeView))
	mux.HandleFunc("POST /notifications/delete/{line_number}", auth.RequireAdmin(sessions, authOpts, notifications.HandleDelete))
	mux.HandleFunc("POST /notifications/mark_as_read/{line_number}", auth.RequireAdmin(sessions, authOpts, notifications.HandleMarkAsRead))

	mux.HandleFunc("GET /settings/notifications", auth.RequireAdmin(sessions, authOpts, notificationSettings.ServeSettings))
	mux.HandleFunc("POST /settings/notifications", auth.RequireAdmin(sessions, authOpts, notificationSettings.HandleUpdate))
	mux.HandleFunc("POST /settings/notifications/test-smtp", auth.RequireAdmin(sessions, authOpts, notificationSettings.TestSMTP))

	mux.HandleFunc("GET /server/timezone", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeTimezone))
	mux.HandleFunc("POST /server/timezone", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeTimezone))
	mux.HandleFunc("GET /server/root-password", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeRootPassword))
	mux.HandleFunc("POST /server/root-password", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeRootPassword))
	mux.HandleFunc("POST /server/memory_usage/drop", auth.RequireAdmin(sessions, authOpts, serverUtils.HandleDropMemoryCache))
	mux.HandleFunc("POST /server/memory_usage/drop-swap", auth.RequireAdmin(sessions, authOpts, serverUtils.HandleDropSwapCache))

	mux.HandleFunc("GET /server/crons", auth.RequireAdmin(sessions, authOpts, cronjobs.ServeCrons))
	mux.HandleFunc("POST /server/crons", auth.RequireAdmin(sessions, authOpts, cronjobs.ServeCrons))

	mux.HandleFunc("GET /security/disable-admin", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeDisableAdmin))
	mux.HandleFunc("POST /security/disable-admin", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeDisableAdmin))
	mux.HandleFunc("GET /security/basic_auth", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeBasicAuth))
	mux.HandleFunc("POST /security/basic_auth", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeBasicAuth))
	mux.HandleFunc("GET /security/blacklist-useragents", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeBlacklistUseragents))
	mux.HandleFunc("POST /security/blacklist-useragents", auth.RequireAdmin(sessions, authOpts, securityToggles.ServeBlacklistUseragents))
	mux.HandleFunc("GET /server/demo-mode", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeDemoMode))
	mux.HandleFunc("POST /server/demo-mode", auth.RequireAdmin(sessions, authOpts, serverUtils.ServeDemoMode))

	mux.HandleFunc("GET /users", auth.RequireLogin(sessions, authOpts, users.ServeList))
	mux.HandleFunc("GET /users/", auth.RequireLogin(sessions, authOpts, users.ServeList))
	mux.HandleFunc("GET /user/new", auth.RequireLogin(sessions, authOpts, users.ServeCreateUser))
	mux.HandleFunc("POST /user/new", auth.RequireLogin(sessions, authOpts, users.ServeCreateUser))
	mux.HandleFunc("GET /users/{username}", auth.RequireLogin(sessions, authOpts, users.ServeDetail))
	mux.HandleFunc("POST /user/{action}/{username}", auth.RequireLogin(sessions, authOpts, users.HandleManage))
	mux.HandleFunc("GET /get_user_notes/{username}", auth.RequireLogin(sessions, authOpts, users.HandleNotes))
	mux.HandleFunc("POST /get_user_notes/{username}", auth.RequireLogin(sessions, authOpts, users.HandleNotes))
	mux.HandleFunc("GET /json/ips", auth.RequireAdmin(sessions, authOpts, users.ServeIPs))
	mux.HandleFunc("GET /get_resource_usage_history/{username}", auth.RequireLogin(sessions, authOpts, users.ServeResourceUsageHistory))
	mux.HandleFunc("GET /client/disk/{username}", auth.RequireLogin(sessions, authOpts, users.ServeUserDiskInfo))
	// /json/{userLogType}/{username} -- see ServeUserLog's doc comment for
	// why this is registered with a whole wildcard segment rather than a
	// literal "user-" prefix fused onto <log_type>. This has one more path
	// segment than GET /json/{resource} above, so the two patterns don't
	// collide.
	mux.HandleFunc("GET /json/{userLogType}/{username}", auth.RequireLogin(sessions, authOpts, users.ServeUserLog))
	mux.HandleFunc("GET /get_custom_message_for_user/{username}", auth.RequireLogin(sessions, authOpts, users.HandleCustomMessage))
	mux.HandleFunc("POST /get_custom_message_for_user/{username}", auth.RequireLogin(sessions, authOpts, users.HandleCustomMessage))
	mux.HandleFunc("POST /containers/{username}/{action}/{container_name}", auth.RequireLogin(sessions, authOpts, users.ServeManageContainer))
	mux.HandleFunc("POST /users/{username}/account-setting/{field}", auth.RequireLogin(sessions, authOpts, users.ServeUserAccountSetting))
	mux.HandleFunc("GET /containers/stats/{username}", auth.RequireLogin(sessions, authOpts, users.ServeContainersStats))

	mux.HandleFunc("GET /domains", auth.RequireAdmin(sessions, authOpts, domains.ServeList))
	mux.HandleFunc("GET /domains/", auth.RequireAdmin(sessions, authOpts, domains.ServeList))
	mux.HandleFunc("POST /domains/add", auth.RequireAdmin(sessions, authOpts, domains.HandleAdd))
	mux.HandleFunc("GET /domains/dns", auth.RequireAdmin(sessions, authOpts, dnsZoneEditor.ServeEditDNSZone))
	mux.HandleFunc("GET /domains/dns/{domain_name}", auth.RequireAdmin(sessions, authOpts, dnsZoneEditor.ServeEditDNSZone))
	mux.HandleFunc("GET /domains/caddy", auth.RequireAdmin(sessions, authOpts, caddyFileEditor.ServeEditCaddyFile))
	mux.HandleFunc("GET /domains/caddy/{domain_name}", auth.RequireAdmin(sessions, authOpts, caddyFileEditor.ServeEditCaddyFile))
	mux.HandleFunc("GET /domains/vhost", auth.RequireAdmin(sessions, authOpts, vhostFileEditor.ServeEditVHostFile))
	mux.HandleFunc("GET /domains/vhost/{username}/{domain_name}", auth.RequireAdmin(sessions, authOpts, vhostFileEditor.ServeEditVHostFile))
	mux.HandleFunc("POST /domains/vhost/{username}/{domain_name}", auth.RequireAdmin(sessions, authOpts, vhostFileEditor.ServeEditVHostFile))
	mux.HandleFunc("GET /domains/ssl/{domain_name}", auth.RequireAdmin(sessions, authOpts, sslPage.ServeSSL))
	mux.HandleFunc("GET /domains/log", auth.RequireAdmin(sessions, authOpts, accessLogs.ServeAccessLog))
	mux.HandleFunc("GET /domains/log/", auth.RequireAdmin(sessions, authOpts, accessLogs.ServeAccessLog))
	mux.HandleFunc("GET /domains/log/{domain_name}", auth.RequireAdmin(sessions, authOpts, accessLogs.ServeAccessLog))
	mux.HandleFunc("GET /domains/stats/{current_username}/{domain_name}", auth.RequireAdmin(sessions, authOpts, goAccessStats.ServeStats))
	// POST /domains/{feature}/toggle, POST /domains/dns/{domain_name},
	// POST /domains/caddy/{domain_name}, and POST /domains/ssl/{domain_name}
	// all share the same two-segment shape and genuinely overlap at single
	// URLs like /domains/dns/toggle or /domains/ssl/toggle -- "dns"/
	// "caddy"/"ssl" are themselves valid {feature} values for the toggle
	// route, so the ambiguity is real, and Go's ServeMux refuses to
	// register genuinely overlapping patterns at all. Dispatched manually
	// here rather than registered as four separate conflicting patterns.
	mux.HandleFunc("POST /domains/{seg2}/{seg3}", auth.RequireAdmin(sessions, authOpts, func(w http.ResponseWriter, r *http.Request) {
		seg2, seg3 := r.PathValue("seg2"), r.PathValue("seg3")
		switch {
		case seg3 == "toggle":
			r.SetPathValue("feature", seg2)
			domains.HandleToggleFeature(w, r)
		case seg2 == "dns":
			r.SetPathValue("domain_name", seg3)
			dnsZoneEditor.ServeEditDNSZone(w, r)
		case seg2 == "caddy":
			r.SetPathValue("domain_name", seg3)
			caddyFileEditor.ServeEditCaddyFile(w, r)
		case seg2 == "ssl":
			r.SetPathValue("domain_name", seg3)
			sslPage.ServeSSL(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	mux.HandleFunc("GET /services", auth.RequireAdmin(sessions, authOpts, services.ServeStatus))
	mux.HandleFunc("POST /services", auth.RequireAdmin(sessions, authOpts, services.ServeStatus))
	mux.HandleFunc("GET /services/admin/status", auth.RequireAdmin(sessions, authOpts, services.ServeAdminStatus))
	mux.HandleFunc("GET /services/action-status", auth.RequireAdmin(sessions, authOpts, services.ServeActionStatus))
	mux.HandleFunc("GET /services/monitored", auth.RequireAdmin(sessions, authOpts, services.ServeMonitored))
	// SECURITY: /services/edit is guarded here just like every sibling
	// route in this group. An unauthenticated config-write endpoint would
	// be a real gap, so it's protected rather than left open.
	mux.HandleFunc("GET /services/edit", auth.RequireAdmin(sessions, authOpts, services.ServeEdit))
	mux.HandleFunc("POST /services/edit", auth.RequireAdmin(sessions, authOpts, services.ServeEdit))
	mux.HandleFunc("GET /service/{action}/{service_name}", auth.RequireAdmin(sessions, authOpts, services.HandleManageService))
	mux.HandleFunc("POST /service/{action}/{service_name}", auth.RequireAdmin(sessions, authOpts, services.HandleManageService))
	mux.HandleFunc("GET /services/ftp/refresh", auth.RequireAdmin(sessions, authOpts, ftp.ServeRefresh))
	mux.HandleFunc("POST /services/ftp/refresh", auth.RequireAdmin(sessions, authOpts, ftp.ServeRefresh))
	mux.HandleFunc("GET /services/ftp", auth.RequireAdmin(sessions, authOpts, ftp.ServeAccounts))
	mux.HandleFunc("POST /services/ftp", auth.RequireAdmin(sessions, authOpts, ftp.ServeAccounts))
	mux.HandleFunc("GET /services/ftp/settings", auth.RequireAdmin(sessions, authOpts, ftp.ServeSettings))
	mux.HandleFunc("POST /services/ftp/settings", auth.RequireAdmin(sessions, authOpts, ftp.ServeSettings))
	mux.HandleFunc("GET /services/podman", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodman))
	mux.HandleFunc("POST /services/podman/images/bulk/{action}", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImagesBulkAction))
	mux.HandleFunc("GET /services/podman/images/bulk-status", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImagesBulkStatus))
	mux.HandleFunc("POST /services/podman/images/{action}/{id...}", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImageAction))
	mux.HandleFunc("GET /services/podman/images/action-status", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImageActionStatus))
	mux.HandleFunc("GET /services/podman/images/check-update", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImageCheckUpdate))
	mux.HandleFunc("GET /services/podman/images/vulnerabilities", auth.RequireAdmin(sessions, authOpts, podmanHandlers.ServePodmanImageVulnerabilities))
	mux.HandleFunc("GET /backups/user", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeUserBackups))
	mux.HandleFunc("POST /backups/user/settings", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeUserBackupsSettings))
	mux.HandleFunc("POST /backups/user/configuration", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeUserBackupsConfiguration))
	mux.HandleFunc("POST /backups/user/run", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeUserBackupsRun))
	mux.HandleFunc("GET /backups/user/action-status", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeUserBackupsActionStatus))
	mux.HandleFunc("GET /backups/system", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackups))
	mux.HandleFunc("POST /backups/system", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackups))
	mux.HandleFunc("POST /backups/system/run", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackupsRun))
	mux.HandleFunc("POST /backups/system/restore/{filename...}", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackupsRestore))
	mux.HandleFunc("POST /backups/system/delete/{filename...}", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackupsDelete))
	mux.HandleFunc("GET /backups/system/action-status", auth.RequireAdmin(sessions, authOpts, backupsHandlers.ServeSystemBackupsActionStatus))
	mux.HandleFunc("GET /services/limits", auth.RequireAdmin(sessions, authOpts, limits.ServeLimits))
	mux.HandleFunc("POST /services/limits", auth.RequireAdmin(sessions, authOpts, limits.ServeLimits))
	mux.HandleFunc("GET /services/logs", auth.RequireAdmin(sessions, authOpts, logs.ServeIndex))
	mux.HandleFunc("GET /services/logs/edit", auth.RequireAdmin(sessions, authOpts, logs.ServeEditLogs))
	mux.HandleFunc("POST /services/logs/edit", auth.RequireAdmin(sessions, authOpts, logs.ServeEditLogs))
	mux.HandleFunc("GET /services/logs/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewLog))
	mux.HandleFunc("POST /services/logs/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewLog))
	mux.HandleFunc("DELETE /services/logs/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewLog))
	mux.HandleFunc("GET /settings/updates/log/", auth.RequireAdmin(sessions, authOpts, logs.ServeUpdateLogsSettings))
	mux.HandleFunc("POST /settings/updates/log/", auth.RequireAdmin(sessions, authOpts, logs.ServeUpdateLogsSettings))
	mux.HandleFunc("GET /services/updates/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewUpdateLog))
	mux.HandleFunc("POST /services/updates/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewUpdateLog))
	mux.HandleFunc("DELETE /services/updates/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewUpdateLog))
	mux.HandleFunc("GET /services/crashlogs/log/", auth.RequireAdmin(sessions, authOpts, logs.ServeCrashlogsSettings))
	mux.HandleFunc("POST /services/crashlogs/log/", auth.RequireAdmin(sessions, authOpts, logs.ServeCrashlogsSettings))
	mux.HandleFunc("GET /services/crashlogs/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewCrashlogsLog))
	mux.HandleFunc("POST /services/crashlogs/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewCrashlogsLog))
	mux.HandleFunc("DELETE /services/crashlogs/log/raw", auth.RequireAdmin(sessions, authOpts, logs.ServeViewCrashlogsLog))
	mux.HandleFunc("GET /server/reboot", auth.RequireAdmin(sessions, authOpts, reboot.ServeReboot))
	mux.HandleFunc("POST /server/reboot", auth.RequireAdmin(sessions, authOpts, reboot.ServeReboot))
	mux.HandleFunc("GET /server/reboot/status", auth.RequireAdmin(sessions, authOpts, reboot.ServeRebootStatus))

	mux.HandleFunc("GET /server/swap", auth.RequireAdmin(sessions, authOpts, swap.ServeSwap))
	mux.HandleFunc("POST /server/swap/action/{action}", auth.RequireAdmin(sessions, authOpts, swap.ServeSwapAction))
	mux.HandleFunc("GET /server/swap/action-status", auth.RequireAdmin(sessions, authOpts, swap.ServeSwapActionStatus))
	mux.HandleFunc("GET /settings/caddy", auth.RequireAdmin(sessions, authOpts, caddySettings.ServeSettings))
	mux.HandleFunc("POST /settings/caddy", auth.RequireAdmin(sessions, authOpts, caddySettings.ServeSettings))
	mux.HandleFunc("GET /settings/caddy/metrics", auth.RequireAdmin(sessions, authOpts, caddySettings.ServeMetrics))
	mux.HandleFunc("GET /settings/php", auth.RequireAdmin(sessions, authOpts, phpSettings.ServePHP))
	mux.HandleFunc("GET /json/php/default_version/{username}", auth.RequireLogin(sessions, authOpts, phpSettings.ServePHPDefaultVersion))
	mux.HandleFunc("POST /json/php/default_version/{username}", auth.RequireLogin(sessions, authOpts, phpSettings.ServePHPDefaultVersion))
	mux.HandleFunc("POST /settings/php", auth.RequireAdmin(sessions, authOpts, phpSettings.ServePHP))
	mux.HandleFunc("GET /server/migrate", auth.RequireAdmin(sessions, authOpts, migrate.ServeMigrate))
	mux.HandleFunc("POST /server/migrate", auth.RequireAdmin(sessions, authOpts, migrate.ServeMigrate))
	mux.HandleFunc("GET /server/migrate/status", auth.RequireAdmin(sessions, authOpts, migrate.ServeMigrateStatus))
	mux.HandleFunc("GET /settings/custom-code", auth.RequireAdmin(sessions, authOpts, customCode.ServeCustomCode))
	mux.HandleFunc("POST /settings/custom-code", auth.RequireAdmin(sessions, authOpts, customCode.ServeCustomCode))
	mux.HandleFunc("GET /settings/general", auth.RequireAdmin(sessions, authOpts, general.ServeGeneral))
	mux.HandleFunc("POST /settings/general", auth.RequireAdmin(sessions, authOpts, general.ServeGeneral))
	mux.HandleFunc("GET /settings/locales", auth.RequireAdmin(sessions, authOpts, locales.ServeLocales))
	mux.HandleFunc("POST /settings/locales", auth.RequireAdmin(sessions, authOpts, locales.ServeLocales))
	mux.HandleFunc("GET /settings/modules", auth.RequireAdmin(sessions, authOpts, modules.ServeModules))
	mux.HandleFunc("POST /settings/modules", auth.RequireAdmin(sessions, authOpts, modules.ServeModules))
	mux.HandleFunc("GET /api/docker-tags", auth.RequireAdmin(sessions, authOpts, updates.ServeDockerTags))
	mux.HandleFunc("POST /api/docker-tags", auth.RequireAdmin(sessions, authOpts, updates.ServeDockerTags))
	mux.HandleFunc("POST /settings/updates/update_now", auth.RequireAdmin(sessions, authOpts, updates.ServeUpdateNow))
	mux.HandleFunc("GET /settings/updates", auth.RequireAdmin(sessions, authOpts, updates.ServeUpdates))
	mux.HandleFunc("POST /settings/updates", auth.RequireAdmin(sessions, authOpts, updates.ServeUpdates))
	mux.HandleFunc("GET /features", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("POST /features", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("GET /features/", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("POST /features/", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("GET /features/{plan}", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("POST /features/{plan}", auth.RequireLogin(sessions, authOpts, features.ServeFeatures))
	mux.HandleFunc("GET /resellers", auth.RequireLogin(sessions, authOpts, resellers.ServeResellers))
	mux.HandleFunc("POST /resellers", auth.RequireLogin(sessions, authOpts, resellers.ServeResellers))
	mux.HandleFunc("GET /resellers/{action}/{username}", auth.RequireAdmin(sessions, authOpts, resellers.ServeEditForm))
	mux.HandleFunc("GET /account", auth.RequireLogin(sessions, authOpts, resellers.ServeAccount))
	mux.HandleFunc("GET /settings/open-panel", auth.RequireAdmin(sessions, authOpts, openpanelSettings.ServeOpenpanelSettings))
	mux.HandleFunc("POST /settings/open-panel", auth.RequireAdmin(sessions, authOpts, openpanelSettings.ServeOpenpanelSettings))
	mux.HandleFunc("GET /settings/defaults", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaults))
	mux.HandleFunc("POST /settings/defaults", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaults))
	mux.HandleFunc("GET /settings/defaults/files", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaultsFiles))
	mux.HandleFunc("POST /settings/defaults/files", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaultsFiles))
	mux.HandleFunc("PUT /settings/defaults/files", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaultsFiles))
	mux.HandleFunc("DELETE /settings/defaults/files", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeDefaultsFiles))
	mux.HandleFunc("GET /settings/defaults/files/{username}", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeUserFiles))
	mux.HandleFunc("POST /settings/defaults/files/{username}", auth.RequireAdmin(sessions, authOpts, defaultsSettings.ServeUserFiles))
	mux.HandleFunc("GET /settings/api/endpoints", auth.RequireAdmin(sessions, authOpts, apiSettings.ServeAPIEndpointsList))
	mux.HandleFunc("GET /settings/api", auth.RequireAdmin(sessions, authOpts, apiSettings.ServeAPISettings))
	mux.HandleFunc("POST /settings/api", auth.RequireAdmin(sessions, authOpts, apiSettings.ServeAPISettings))
	mux.HandleFunc("GET /settings/api/", auth.RequireAdmin(sessions, authOpts, apiSettings.ServeAPISettings))
	mux.HandleFunc("POST /settings/api/", auth.RequireAdmin(sessions, authOpts, apiSettings.ServeAPISettings))
	mux.HandleFunc("GET /license", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicense))
	mux.HandleFunc("GET /license/key", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicenseKey))
	mux.HandleFunc("POST /license/key", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicenseKey))
	mux.HandleFunc("GET /license/info", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicenseInfo))
	mux.HandleFunc("POST /license/verify", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicenseVerify))
	mux.HandleFunc("DELETE /license/delete", auth.RequireAdmin(sessions, authOpts, licensePage.ServeLicenseDelete))
	mux.HandleFunc("GET /support/report", auth.RequireAdmin(sessions, authOpts, licensePage.ServeSupportReport))
	mux.HandleFunc("GET /domains/zone-templates", auth.RequireAdmin(sessions, authOpts, dnsTemplates.ServeDNSZoneTemplates))
	mux.HandleFunc("POST /domains/zone-templates", auth.RequireAdmin(sessions, authOpts, dnsTemplates.ServeDNSZoneTemplates))
	mux.HandleFunc("GET /configservercsf/iframe/", auth.RequireAdmin(sessions, authOpts, firewall.ServeCSFIframe))
	mux.HandleFunc("POST /configservercsf/iframe/", auth.RequireAdmin(sessions, authOpts, firewall.ServeCSFIframe))
	mux.HandleFunc("GET /security/firewall", auth.RequireAdmin(sessions, authOpts, firewall.ServeFirewallSettings))
	// csf.pl's own UI hardcodes this exact image URL (see ServeCSFImages);
	// ServeMux matches it ahead of the general "/static/" pattern below
	// since it's more specific, regardless of registration order.
	mux.HandleFunc("GET /static/configservercsf/{filename...}", auth.RequireAdmin(sessions, authOpts, firewall.ServeCSFImages))
	mux.HandleFunc("GET /login/token/{username}", auth.RequireLogin(sessions, authOpts, autologin.ServeLoginToken))
	mux.HandleFunc("GET /domains/file-templates", auth.RequireAdmin(sessions, authOpts, domainTemplates.ServeDomainTemplates))
	mux.HandleFunc("POST /domains/file-templates", auth.RequireAdmin(sessions, authOpts, domainTemplates.ServeDomainTemplates))
	// No auth wrapper: authenticated instead by its own one-time HMAC code
	// check against openpanel.config, not a login session.
	mux.HandleFunc("POST /send_email", mailer.ServeSendEmail)
	mux.HandleFunc("GET /server/processes", auth.RequireAdmin(sessions, authOpts, processManager.ServeProcesses))
	mux.HandleFunc("GET /server/processes/{pid}/{action}", auth.RequireAdmin(sessions, authOpts, processManager.ServeProcessAction))
	mux.HandleFunc("GET /server/node", auth.RequireAdmin(sessions, authOpts, slave.ServeNode))
	mux.HandleFunc("POST /server/node", auth.RequireAdmin(sessions, authOpts, slave.ServeNode))
	mux.HandleFunc("GET /security/imunify/", auth.RequireAdmin(sessions, authOpts, imunify.ServeImunifyGUI))
	mux.HandleFunc("GET /security/imunify/assets/static/{filename...}", auth.RequireAdmin(sessions, authOpts, imunify.ServeImunifyStatic))
	// Exempt from CSRF checks: proxies to a PHP app with its own CSRF
	// handling, unrelated to gorilla/csrf's session-cookie-based checks.
	mux.HandleFunc("GET /imav/{path...}", auth.RequireAdmin(sessions, authOpts, imunify.ServeImunifyPHP))
	mux.HandleFunc("POST /imav/{path...}", auth.RequireAdmin(sessions, authOpts, imunify.ServeImunifyPHP))
	mux.HandleFunc("GET /security/waf", auth.RequireAdmin(sessions, authOpts, waf.ServeWAFStatus))
	mux.HandleFunc("POST /security/waf", auth.RequireAdmin(sessions, authOpts, waf.ServeWAFStatus))
	mux.HandleFunc("GET /security/waf/rules", auth.RequireAdmin(sessions, authOpts, waf.ServeWAFRules))
	mux.HandleFunc("POST /security/waf/rules", auth.RequireAdmin(sessions, authOpts, waf.ServeWAFRules))
	mux.HandleFunc("GET /security/waf/view-rules", auth.RequireAdmin(sessions, authOpts, waf.ServeWAFViewRules))
	mux.HandleFunc("GET /server/ssh", auth.RequireAdmin(sessions, authOpts, sshHandlers.ServeSSH))
	mux.HandleFunc("POST /server/ssh", auth.RequireAdmin(sessions, authOpts, sshHandlers.ServeSSH))
	mux.HandleFunc("GET /server/ssh/config", auth.RequireAdmin(sessions, authOpts, sshHandlers.ServeSSHFullConfig))
	mux.HandleFunc("POST /server/ssh/config", auth.RequireAdmin(sessions, authOpts, sshHandlers.ServeSSHFullConfig))
	mux.HandleFunc("GET /search/pages", auth.RequireAdmin(sessions, authOpts, search.ServeSearchFilter))
	mux.HandleFunc("GET /search/websites", auth.RequireAdmin(sessions, authOpts, search.ServeSearchWebsites))
	mux.HandleFunc("GET /search/websites/{site_name}", auth.RequireAdmin(sessions, authOpts, search.ServeSearchWebsites))
	mux.HandleFunc("GET /search/users", auth.RequireLogin(sessions, authOpts, search.ServeSearchUsers))
	mux.HandleFunc("GET /search/users/{username}", auth.RequireLogin(sessions, authOpts, search.ServeSearchUsers))
	// Wildcard fallback behind the more specific literal /domains routes
	// above. Go's net/http.ServeMux has no "one or more segments" wildcard
	// that doesn't also match the empty remainder, which would conflict
	// with (and panic against) the exact "/domains/" registration above.
	// A single-segment {domain_name} avoids that conflict at the cost of
	// not matching multi-segment paths like "/domains/example.com/extra"
	// (unreachable via the real UI, which never constructs such a URL).
	mux.HandleFunc("GET /domains/{domain_name}", auth.RequireAdmin(sessions, authOpts, search.ServeDomainOwner))
	mux.HandleFunc("GET /domains/dns-cluster", auth.RequireAdmin(sessions, authOpts, dnsCluster.ServeDNSCluster))
	mux.HandleFunc("POST /domains/dns-cluster", auth.RequireAdmin(sessions, authOpts, dnsCluster.ServeDNSCluster))
	mux.HandleFunc("GET /domains/dns-cluster/{ip}", auth.RequireAdmin(sessions, authOpts, dnsCluster.ServeDNSClusterInfo))
	mux.HandleFunc("GET /emails/settings", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsSettings))
	mux.HandleFunc("POST /emails/settings", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsSettings))
	// SECURITY: these 5 routes previously had no auth decorator at all
	// (see the comment above ServeUpdatePassword in emails.go) --
	// RequireLogin closes that gap.
	mux.HandleFunc("POST /emails/api/update-password", auth.RequireLogin(sessions, authOpts, emails.ServeUpdatePassword))
	mux.HandleFunc("POST /emails/api/quota-set", auth.RequireLogin(sessions, authOpts, emails.ServeQuotaSet))
	mux.HandleFunc("POST /emails/api/quota-del", auth.RequireLogin(sessions, authOpts, emails.ServeQuotaDel))
	mux.HandleFunc("POST /emails/api/restrict", auth.RequireLogin(sessions, authOpts, emails.ServeRestrict))
	mux.HandleFunc("POST /emails/api/delete", auth.RequireLogin(sessions, authOpts, emails.ServeDeleteEmails))
	mux.HandleFunc("GET /emails/accounts", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsAccounts))
	mux.HandleFunc("POST /emails/accounts", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsAccounts))
	mux.HandleFunc("GET /emails/queue", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsQueue))
	mux.HandleFunc("POST /emails/queue", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsQueue))
	mux.HandleFunc("POST /emails/queue/action", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsQueueAction))
	mux.HandleFunc("GET /emails/reports", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsReports))
	mux.HandleFunc("GET /emails/reports/view", auth.RequireLogin(sessions, authOpts, emails.ServeReportsIndex))
	mux.HandleFunc("GET /emails/data/{filename}", auth.RequireLogin(sessions, authOpts, emails.ServeShowReport))
	mux.HandleFunc("GET /emails/webmail/{email}", auth.RequireLogin(sessions, authOpts, emails.ServeEmailsWebmailLink))
	mux.HandleFunc("GET /emails/domain-limits", auth.RequireLogin(sessions, authOpts, emails.ServeDomainLimits))
	mux.HandleFunc("POST /emails/domain-limits/save-raw", auth.RequireLogin(sessions, authOpts, emails.ServeDomainLimitsSaveRaw))
	mux.HandleFunc("GET /emails/domain-limits/hits", auth.RequireLogin(sessions, authOpts, emails.ServeDomainLimitsHits))
	mux.HandleFunc("POST /emails/domain-limits/api", auth.RequireLogin(sessions, authOpts, emails.ServeDomainLimitsAPI))
	mux.HandleFunc("GET /terminal", auth.RequireAdmin(sessions, authOpts, terminal.ServeHostTerminalPage))
	mux.HandleFunc("GET /terminal/{username}/{container_name}", auth.RequireAdmin(sessions, authOpts, terminal.ServeUserTerminalPage))
	mux.HandleFunc("GET /ws/terminal", auth.RequireAdmin(sessions, authOpts, terminal.ServeHostTerminalWS))
	mux.HandleFunc("GET /ws/terminal/{username}/{container_name}", auth.RequireAdmin(sessions, authOpts, terminal.ServeUserTerminalWS))
	mux.HandleFunc("GET /user/import", auth.RequireAdmin(sessions, authOpts, importer.ServeImportUser))
	mux.HandleFunc("GET /import/{panel_type}", auth.RequireAdmin(sessions, authOpts, importer.ServeImportFromBackup))
	mux.HandleFunc("POST /import/{panel_type}", auth.RequireAdmin(sessions, authOpts, importer.ServeImportFromBackup))
	mux.HandleFunc("GET /import/user/log/{log_filename...}", auth.RequireAdmin(sessions, authOpts, importer.ServeViewTransferImportLog))
	mux.HandleFunc("GET /import/account/log/{log_filename...}", auth.RequireAdmin(sessions, authOpts, importer.ServeViewAccountImportLog))
	mux.HandleFunc("GET /json/backup-files", auth.RequireAdmin(sessions, authOpts, importer.ServeListBackupFiles))
	mux.HandleFunc("GET /json/transfers", auth.RequireAdmin(sessions, authOpts, importer.ServeListTransfers))
	mux.HandleFunc("GET /json/transfers/{username}", auth.RequireLogin(sessions, authOpts, importer.ServeListTransfersFor))

	mux.HandleFunc("GET /user/export/status/{username}", auth.RequireLogin(sessions, authOpts, users.ServeUserExportStatus))
	mux.HandleFunc("POST /user/export/create/{username}", auth.RequireLogin(sessions, authOpts, users.ServeUserExportCreate))
	mux.HandleFunc("GET /user/export/download/{username}/{filename...}", auth.RequireLogin(sessions, authOpts, users.ServeUserExportDownload))
	mux.HandleFunc("POST /user/export/delete/{username}", auth.RequireLogin(sessions, authOpts, users.ServeUserExportDelete))
	mux.HandleFunc("GET /import/transfer/", auth.RequireAdmin(sessions, authOpts, importer.ServeImportTransfer))
	mux.HandleFunc("POST /import/transfer/", auth.RequireAdmin(sessions, authOpts, importer.ServeImportTransfer))
	// No auth wrapper: these are public files, served without a
	// login/admin decorator.
	mux.HandleFunc("GET /{filename}", generalStatic.ServeFile)

	csrfMiddleware := csrf.Protect(deriveCSRFKey(d.SecretKey),
		csrf.FieldName("csrf_token"),
		csrf.RequestHeader("X-CSRFToken"),
		csrf.Secure(d.UseTLS),
		csrf.Path("/"),
		csrf.ErrorHandler(handlers.CSRFErrorHandler()),
	)

	var handler http.Handler = handlers.NotFoundHandler(mux)
	handler = handlers.RecoverMiddleware(handler)
	handler = auth.ValidateSessionIPMiddleware(sessions, authOpts)(handler)
	handler = auth.WithUserLoader(sessions, d.AdminDB)(handler)

	// /send_email is exempt from CSRF checks: it's authenticated by its own
	// one-time HMAC code, not a browser session, so it can't carry a CSRF
	// cookie/token pair. /imav/... is also exempt: it proxies to a PHP app
	// with its own CSRF handling. /api/... is exempt too: every route in the
	// api blueprint is bearer-token (JWT) authenticated, not session/cookie
	// based, so there's no CSRF token to check in the first place --
	// EXCEPT /api/tour/complete, /api/quickstart/dismiss, and
	// /api/docker-tags, which all live under that path but are actually
	// plain session-authenticated routes (registered directly in
	// login.py/updates.py, not the api blueprint) and were never marked
	// @csrf.exempt there, so they keep going through the normal CSRF check.
	// gorilla/csrf has no first-class per-route exemption, so this splits
	// the chain: everything else goes through csrfMiddleware, these bypass
	// it entirely.
	apiCSRFExemptExceptions := map[string]bool{
		"/api/tour/complete":      true,
		"/api/quickstart/dismiss": true,
		"/api/docker-tags":        true,
	}
	withoutCSRF := handler
	handler = csrfMiddleware(handler)
	handler = func(csrfProtected, exempt http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/send_email" || strings.HasPrefix(r.URL.Path, "/imav/") ||
				(strings.HasPrefix(r.URL.Path, "/api/") && !apiCSRFExemptExceptions[r.URL.Path]) {
				exempt.ServeHTTP(w, r)
				return
			}
			csrfProtected.ServeHTTP(w, r)
		})
	}(handler, withoutCSRF)
	if !d.UseTLS {
		// gorilla/csrf enforces a same-origin Referer/Origin check on every
		// unsafe-method request unless it's explicitly told the request came
		// in over plaintext HTTP (csrf.PlaintextHTTPRequest) -- otherwise a
		// plain-HTTP deployment (the default before a domain/cert is
		// configured, see bootstrap's cert-discovery logging above) would
		// reject every POST with "referer not supplied" the moment a client
		// doesn't send one, which not all HTTP clients do.
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, csrf.PlaintextHTTPRequest(r))
		})
	}

	// Outermost: an optional network-level Basic Auth gate in front of the
	// whole panel (see /security/basic_auth), checked before session
	// cookies, CSRF, or routing even come into play.
	handler = auth.BasicAuthMiddleware(d.BasicAuthEnabled, d.BasicAuthUsername, d.BasicAuthPassword)(handler)

	return handler, nil
}

// deriveCSRFKey keeps the CSRF signing key independently derived from the
// same on-disk secret as the session key (see auth.deriveKey's doc comment
// for why these shouldn't share key material), hashed down to exactly 32
// bytes as gorilla/csrf's underlying AES-256 usage requires.
func deriveCSRFKey(secret string) []byte {
	sum := sha256.Sum256([]byte("csrf:" + secret))
	return sum[:]
}

func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func osHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "OpenPanel"
	}
	return h
}

// detectPublicIP does not perform the external ip.openpanel.com/ifconfig.me
// lookups (and their 1h cache) -- see the backlog -- this only does the
// local fallback (open a UDP "connection" to a public IP to learn which
// local interface/address routing would use).
func detectPublicIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "Unknown"
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "Unknown"
	}
	return addr.IP.String()
}

func openpanelVersion() string {
	out, err := exec.Command("opencli", "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// adminPortAtStartup computes the admin port once at process startup, not
// re-queried per-request.
func adminPortAtStartup() string {
	out, err := exec.Command("opencli", "admin", "port").Output()
	if err != nil {
		return "2087"
	}
	port := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return "2087"
	}
	return port
}
