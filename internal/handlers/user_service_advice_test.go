package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdviseUserServices(t *testing.T) {
	join := func(a []string) string { return strings.Join(a, "\n") }

	// openlitespeed doesn't need php-fpm at all
	got := join(adviseUserServices("bob", userServiceState{
		WebServer: "openlitespeed", DefaultPHP: "8.3",
		DomainPHP: map[string]string{"a.com": ""},
		Services:  []string{"openlitespeed", "php-fpm-8.3", "backup"},
		Running:   map[string]bool{"openlitespeed": true, "php-fpm-8.3": true, "backup": true},
	}))
	if !strings.Contains(got, "php-fpm-8.3 isn't needed, OpenLiteSpeed uses its own LSPHP") {
		t.Fatalf("openlitespeed advice = %q", got)
	}

	// unused non-default version, default unused, webserver down, no backups
	got = join(adviseUserServices("bob", userServiceState{
		WebServer: "apache", DefaultPHP: "8.3",
		DomainPHP: map[string]string{"a.com": "8.4", "b.com": "8.4", "c.com": "8.2"},
		Services:  []string{"apache", "php-fpm-8.2", "php-fpm-8.3", "php-fpm-8.4", "php-fpm-8.1", "backup"},
		Running:   map[string]bool{"php-fpm-8.1": true, "php-fpm-8.3": true, "php-fpm-8.4": true},
	}))
	for _, want := range []string{
		"The apache webserver isn't running, so this user's 3 domain(s) are down",
		"php-fpm-8.1 is running but no domain uses it",
		"No domain uses the default PHP 8.3, 2 domain(s) use PHP 8.4. Recommended: set PHP 8.4 as the default with opencli php-default bob --update 8.4, then stop php-fpm-8.3",
		"Backups aren't configured for this user",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "php-fpm-8.3 is running but no domain uses it") || strings.Contains(got, "php-fpm-8.4 is running but") {
		t.Fatalf("the default and used versions shouldn't be flagged as unused:\n%s", got)
	}

	// healthy user: nothing to say
	if got := adviseUserServices("bob", userServiceState{
		WebServer: "nginx", DefaultPHP: "8.3",
		DomainPHP:    map[string]string{"a.com": "8.3"},
		Services:     []string{"nginx", "php-fpm-8.3", "backup"},
		Running:      map[string]bool{"nginx": true, "php-fpm-8.3": true},
		AdminBackups: true,
	}); len(got) != 0 {
		t.Fatalf("expected no advice, got %q", got)
	}

	// podman unreachable: skip the running based checks instead of guessing
	if got := adviseUserServices("bob", userServiceState{WebServer: "nginx", DomainPHP: map[string]string{"a.com": "8.3"}, Services: []string{"nginx", "backup"}}); len(got) != 0 {
		t.Fatalf("expected no advice without running state, got %q", got)
	}
}

func TestReadDomainPHPVersions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.com.conf"), []byte("proxy_pass fcgi://php-fpm-8.4:9000;"), 0644)
	os.WriteFile(filepath.Join(dir, "b.com.conf"), []byte("no php here"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("php-fpm-7.4"), 0644)
	got := readDomainPHPVersions(dir)
	if len(got) != 2 || got["a.com"] != "8.4" || got["b.com"] != "" {
		t.Fatalf("readDomainPHPVersions = %v", got)
	}
}

func TestDetailStatsWidgetOverLimit(t *testing.T) {
	w := detailStatsWidget{DiskUsageRounded: "95.5", InodesUsageRounded: "12", CPURatioRounded: "90", RAMPercentRounded: "89.99"}.withOverLimit()
	if strings.Join(w.OverLimit, ",") != "Disk,CPU" {
		t.Fatalf("OverLimit = %v", w.OverLimit)
	}
}

func TestAdviseServiceLimits(t *testing.T) {
	host := thresholdHost{Cores: 8, RAMGB: 16}
	services := []composeServiceView{
		{ContainerName: "apache", CPULimit: "4", MemoryLimitGB: "3.00"},
		{ContainerName: "mariadb", CPULimit: "12", MemoryLimitGB: "32.00"},
		{ContainerName: "redis", CPULimit: "0", MemoryLimitGB: ""},
		{ContainerName: "nginx", CPULimit: "1", MemoryLimitGB: "1.00"},
	}
	got := strings.Join(adviseServiceLimits(services, "Standard", "2", "2g", host), "\n")
	for _, want := range []string{
		"apache has a CPU limit of 4, above the Standard plan's 2 CPU. The plan limit is the ceiling",
		"apache has a memory limit of 3 GB, above the Standard plan's 2 GB",
		"mariadb can't be started with a CPU limit of 12, the server has 8 CPU cores. Set it under 8.",
		"mariadb can't be started with a memory limit of 32 GB, the server has 16.0 GB RAM. Set it under 16.0 GB.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "redis") || strings.Contains(got, "nginx") || strings.Contains(got, "mariadb has a CPU limit") {
		t.Fatalf("unexpected advice:\n%s", got)
	}
	// unlimited plan only checks the server
	if got := adviseServiceLimits(services[:1], "Unlimited", "0", "0g", host); len(got) != 0 {
		t.Fatalf("expected no plan advice on an unlimited plan, got %q", got)
	}
}
