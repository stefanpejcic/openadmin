package handlers

import (
	"os"
	"path/filepath"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/config"
)

func TestQuickStartModulesCustomized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openpanel.config")

	os.WriteFile(path, []byte("enabled_modules=services,filemanager,autoinstaller,domains,ssl,php,dns,locale,account,sessions,mysql,mysql_import,remote_mysql,process_manager,php_options,favorites,phpmyadmin,crons,domain_logs,wordpress,website_builder,usage,webserver_conf,waf,redis,memcached,login_history,activity,twofa,goaccess,docroot\n"), 0644)
	if quickStartModulesCustomized(path) {
		t.Fatalf("expected default modules list to not be flagged as customized")
	}

	os.WriteFile(path, []byte("enabled_modules=services,filemanager,dns\n"), 0644)
	if !quickStartModulesCustomized(path) {
		t.Fatalf("expected a shrunk modules list to be flagged as customized")
	}

	os.WriteFile(path, []byte("enabled_modules=services,filemanager,autoinstaller,domains,ssl,php,dns,locale,account,sessions,mysql,mysql_import,remote_mysql,process_manager,php_options,favorites,phpmyadmin,crons,domain_logs,wordpress,website_builder,usage,webserver_conf,waf,redis,memcached,login_history,activity,twofa,goaccess,docroot,ftp\n"), 0644)
	if !quickStartModulesCustomized(path) {
		t.Fatalf("expected an extended modules list to be flagged as customized")
	}

	missing := filepath.Join(dir, "missing.config")
	if quickStartModulesCustomized(missing) {
		t.Fatalf("expected a missing config file to not be flagged as customized")
	}
}

func TestQuickStartDNSConfigured(t *testing.T) {
	dir := t.TempDir()

	modulesPath := filepath.Join(dir, "modules.config")
	origModulesConfig := chromeSite.ModulesConfig
	chromeSite.ModulesConfig = modulesPath
	t.Cleanup(func() { chromeSite.ModulesConfig = origModulesConfig })

	openpanelConfigPath := filepath.Join(dir, "openpanel.config")
	origOpenpanelConfigPath := config.OpenpanelConfigPath
	config.OpenpanelConfigPath = openpanelConfigPath
	t.Cleanup(func() { config.OpenpanelConfigPath = origOpenpanelConfigPath })

	// dns module disabled, no nameservers -- not configured.
	os.WriteFile(modulesPath, []byte("enabled_modules=services,filemanager\n"), 0644)
	os.WriteFile(openpanelConfigPath, []byte("[DEFAULT]\nns1=\n"), 0644)
	if quickStartDNSConfigured() {
		t.Fatalf("expected DNS to not be flagged as configured when the module is disabled")
	}

	// dns module enabled, but no nameservers set -- still not configured.
	os.WriteFile(modulesPath, []byte("enabled_modules=services,dns\n"), 0644)
	if quickStartDNSConfigured() {
		t.Fatalf("expected DNS to not be flagged as configured without any nameserver set")
	}

	// dns module enabled AND a nameserver set -- configured.
	os.WriteFile(openpanelConfigPath, []byte("[DEFAULT]\nns1=ns1.example.com\n"), 0644)
	if !quickStartDNSConfigured() {
		t.Fatalf("expected DNS to be flagged as configured once module is enabled and a nameserver is set")
	}
}

func TestQuickStartPHPWebserverCustomized(t *testing.T) {
	dir := t.TempDir()
	origPath := DefaultsEnvPath
	DefaultsEnvPath = filepath.Join(dir, ".env")
	t.Cleanup(func() { DefaultsEnvPath = origPath })

	os.WriteFile(DefaultsEnvPath, []byte("WEB_SERVER=\"apache\"\nDEFAULT_PHP_VERSION=8.5\n"), 0644)
	if quickStartPHPWebserverCustomized() {
		t.Fatalf("expected factory-default PHP/webserver values to not be flagged as customized")
	}

	os.WriteFile(DefaultsEnvPath, []byte("WEB_SERVER=\"nginx\"\nDEFAULT_PHP_VERSION=8.5\n"), 0644)
	if !quickStartPHPWebserverCustomized() {
		t.Fatalf("expected a changed webserver to be flagged as customized")
	}

	os.WriteFile(DefaultsEnvPath, []byte("WEB_SERVER=\"apache\"\nDEFAULT_PHP_VERSION=8.2\n"), 0644)
	if !quickStartPHPWebserverCustomized() {
		t.Fatalf("expected a changed default PHP version to be flagged as customized")
	}
}

func TestQuickStartFeaturesCustomized(t *testing.T) {
	dir := t.TempDir()
	origDir := FeaturesDir
	FeaturesDir = dir + "/"
	t.Cleanup(func() { FeaturesDir = origDir })

	writeLines := func(name string, lines []string) {
		content := ""
		for _, l := range lines {
			content += l + "\n"
		}
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}

	writeLines("basic.txt", quickStartDefaultBasicFeatures)
	writeLines("default.txt", quickStartDefaultDefaultFeatures)
	if quickStartFeaturesCustomized() {
		t.Fatalf("expected default feature sets to not be flagged as customized")
	}

	writeLines("basic.txt", append(append([]string{}, quickStartDefaultBasicFeatures...), "ftp"))
	if !quickStartFeaturesCustomized() {
		t.Fatalf("expected an added feature to be flagged as customized")
	}
}

func TestQuickStartPlansCustomized(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cols := []string{"id", "name", "domains_limit", "websites_limit", "email_limit", "ftp_limit",
		"disk_limit", "inodes_limit", "db_limit", "cpu", "ram", "bandwidth", "feature_set",
		"max_email_quota", "max_hourly_email"}

	mock.ExpectQuery(`SELECT \* FROM plans`).WillReturnRows(sqlmock.NewRows(cols).
		AddRow(1, "Standard plan", "0", "10", "0", "0", "5 GB", "1000000", "0", "2", "2g", "100", "basic", "10G", "100").
		AddRow(2, "Developer Plus", "0", "10", "0", "0", "10 GB", "1000000", "0", "4", "6g", "500", "default", "0", "0"))
	if quickStartPlansCustomized(db) {
		t.Fatalf("expected untouched factory-default plans to not be flagged as customized")
	}

	mock.ExpectQuery(`SELECT \* FROM plans`).WillReturnRows(sqlmock.NewRows(cols).
		AddRow(1, "Standard plan", "0", "10", "0", "0", "50 GB", "1000000", "0", "2", "2g", "100", "basic", "10G", "100").
		AddRow(2, "Developer Plus", "0", "10", "0", "0", "10 GB", "1000000", "0", "4", "6g", "500", "default", "0", "0"))
	if !quickStartPlansCustomized(db) {
		t.Fatalf("expected an edited plan field to be flagged as customized")
	}

	mock.ExpectQuery(`SELECT \* FROM plans`).WillReturnRows(sqlmock.NewRows(cols).
		AddRow(1, "Standard plan", "0", "10", "0", "0", "5 GB", "1000000", "0", "2", "2g", "100", "basic", "10G", "100"))
	if !quickStartPlansCustomized(db) {
		t.Fatalf("expected a deleted default plan to be flagged as customized")
	}

	if quickStartPlansCustomized(nil) {
		t.Fatalf("expected a nil db to not be flagged as customized")
	}
}

func TestOnboardingServiceItemsCommunityVsEnterprise(t *testing.T) {
	community := onboardingServiceItems("Community", 0)
	if len(community) != 2 {
		t.Fatalf("expected 2 Community services (OpenPanel, Firewall), got %d: %+v", len(community), community)
	}
	if community[0].Label != "OpenPanel" || community[1].Label != "Firewall (CSF)" {
		t.Fatalf("unexpected Community service labels: %+v", community)
	}

	enterprise := onboardingServiceItems("Enterprise", 0)
	wantLabels := []string{"OpenPanel", "Firewall (CSF)", "Email + Webmail", "FTP", "DNS", "phpMyAdmin", "ImunifyAV"}
	if len(enterprise) != len(wantLabels) {
		t.Fatalf("expected %d Enterprise services, got %d: %+v", len(wantLabels), len(enterprise), enterprise)
	}
	for i, want := range wantLabels {
		if enterprise[i].Label != want {
			t.Fatalf("expected service %d to be %q, got %q", i, want, enterprise[i].Label)
		}
	}

	emailItem := enterprise[2]
	if len(emailItem.Services) != 2 || emailItem.Services[0].RealName != "openadmin_mailserver" || emailItem.Services[1].RealName != "openadmin_roundcube" {
		t.Fatalf("expected Email + Webmail to bundle both mailserver and roundcube, got %+v", emailItem.Services)
	}
}

func TestOnboardingConfigStepsEnterpriseOnlyItems(t *testing.T) {
	community := onboardingConfigSteps("Community")
	for _, s := range community {
		if s.Label == "Verify your Enterprise license" || s.Label == "Enable DNS and set custom nameservers" {
			t.Fatalf("expected Community config steps to exclude Enterprise-only step %q", s.Label)
		}
	}

	enterprise := onboardingConfigSteps("Enterprise")
	found := map[string]bool{}
	for _, s := range enterprise {
		found[s.Label] = true
	}
	for _, want := range []string{"Verify your Enterprise license", "Enable DNS and set custom nameservers"} {
		if !found[want] {
			t.Fatalf("expected Enterprise config steps to include %q, got %+v", want, enterprise)
		}
	}
}

func TestOnboardingUserPlanStepsWording(t *testing.T) {
	data := dashboardAdminData{UserCount: 0, DomainCount: 0}

	community := onboardingUserPlanSteps("Community", data, nil)
	communityUserStep := community[3]
	if communityUserStep.Label != "Add a user account" {
		t.Fatalf("expected Community wording, got %q", communityUserStep.Label)
	}

	enterprise := onboardingUserPlanSteps("Enterprise", data, nil)
	enterpriseUserStep := enterprise[3]
	if enterpriseUserStep.Label != "Add a user account or import from backup" {
		t.Fatalf("expected Enterprise wording, got %q", enterpriseUserStep.Label)
	}
}

func TestQuickStartUpdatePreferencesCustomized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openpanel.config")
	origPath := config.OpenpanelConfigPath
	config.OpenpanelConfigPath = path
	t.Cleanup(func() { config.OpenpanelConfigPath = origPath })

	os.WriteFile(path, []byte("[PANEL]\nautoupdate=on\nautopatch=on\n"), 0644)
	if quickStartUpdatePreferencesCustomized() {
		t.Fatalf("expected default update preferences to not be flagged as customized")
	}

	os.WriteFile(path, []byte("[PANEL]\nautoupdate=off\nautopatch=on\n"), 0644)
	if !quickStartUpdatePreferencesCustomized() {
		t.Fatalf("expected a changed autoupdate preference to be flagged as customized")
	}
}
