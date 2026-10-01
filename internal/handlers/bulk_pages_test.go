package handlers

import (
	"strings"
	"testing"

	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

func TestValidateBulkValue(t *testing.T) {
	num := &webtemplates.BulkInput{Type: "number", Min: "0", Max: "8", Step: "1"}
	sel := &webtemplates.BulkInput{Type: "select", Options: []webtemplates.BulkOption{{Value: "8.3"}}}
	quota := &webtemplates.BulkInput{Type: "text", Pattern: "[0-9]+[KMGT]?"}
	email := &webtemplates.BulkInput{Type: "email"}
	cases := []struct {
		in    *webtemplates.BulkInput
		value string
		ok    bool
	}{
		{num, "4", true}, {num, "-1", false}, {num, "9", false}, {num, "1.5", false}, {num, "x", false},
		{sel, "8.3", true}, {sel, "9.9", false},
		{quota, "2G", true}, {quota, "lots", false}, {quota, "2G; rm", false},
		{email, "a@example.com", true}, {email, "John <a@example.com>", false}, {email, "nope", false},
	}
	for _, c := range cases {
		if got := validateBulkValue(c.in, c.value) == ""; got != c.ok {
			t.Errorf("%s %q: valid=%v, want %v", c.in.Type, c.value, got, c.ok)
		}
	}
}

func TestPlanEditFormKeepsCurrentValues(t *testing.T) {
	form := planEditForm(paneldb.RowMap{"name": "Starter", "disk_limit": "5 GB", "ram": "2g", "cpu": "1", "inodes_limit": 1000000, "upsell_url": nil})
	for k, want := range map[string]string{"name": "Starter", "disk_limit": "5", "ram": "2", "cpu": "1", "inodes_limit": "1000000", "upsell_url": ""} {
		if got := form.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCronsBulkRouteKeepsTheOtherSetting(t *testing.T) {
	jobs := func() []CronJob {
		return []CronJob{{LineNumber: 20, Schedule: "0 * * * *", Command: "opencli x", Log: true}}
	}
	route := cronsBulkRoute(jobs)

	call, _ := route("schedule", "0 6 * * 1", "20")
	if call.Form.Get("20_schedule_4") != "1" || call.Form.Get("20_logging") != "on" {
		t.Fatalf("schedule change lost logging or the new schedule: %v", call.Form)
	}
	call, _ = route("log_off", "", "20")
	if call.Form.Get("20_schedule_0") != "0" || call.Form.Has("20_logging") {
		t.Fatalf("logging off changed the schedule or kept logging: %v", call.Form)
	}
	call, _ = route("disable", "", "20")
	if call.Form.Get("20_schedule_2") != "31" || call.Form.Get("20_schedule_3") != "2" {
		t.Fatalf("disable didn't set Feb 31st: %v", call.Form)
	}
	if call, _ := route("disable", "", "99"); call != nil {
		t.Fatal("expected a missing line to be skipped")
	}
}

func TestDomainsBulkRouteOnOff(t *testing.T) {
	call, _ := domainsBulkRoute("hsts", "on", "example.com")
	if call.Path != "/domains/hsts/toggle" || call.Form.Get("hsts_action") != "On" || call.Form.Get("domain_name") != "example.com" {
		t.Fatalf("hsts on: %s %v", call.Path, call.Form)
	}
	call, _ = domainsBulkRoute("waf", "off", "example.com")
	if call.Path != "/domains/waf/toggle" || call.Form.Get("modsec_action") != "Off" {
		t.Fatalf("waf off: %s %v", call.Path, call.Form)
	}
}

func TestDomainsBulkBatchCloudflareRunsOnce(t *testing.T) {
	var calls [][]string
	orig := cloudflareRun
	cloudflareRun = func(args ...string) (string, error) {
		calls = append(calls, args)
		return "Enabled: /etc/openpanel/caddy/domains/a.com.conf\n" +
			"ERROR: Domain config not found: /etc/openpanel/caddy/domains/b.com.conf\n" +
			"Enabled: /etc/openpanel/caddy/domains/c.com.conf\n" +
			"Reloading Caddy to apply the setting...\nSUCCESS: Caddy reloaded successfully.\n", nil
	}
	t.Cleanup(func() { cloudflareRun = orig })

	results, ok := domainsBulkBatch("cloudflare", "on", []string{"a.com", "b.com", "c.com", "bad domain"})
	if !ok {
		t.Fatal("expected cloudflare to be batched")
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "enable a.com b.com c.com" {
		t.Fatalf("expected one opencli call with the valid domains, got %v", calls)
	}
	want := map[string]bool{"a.com": true, "b.com": false, "c.com": true, "bad domain": false}
	for _, res := range results {
		if res.OK != want[res.Item] {
			t.Errorf("%s: ok=%v (%s)", res.Item, res.OK, res.Message)
		}
	}

	if _, ok := domainsBulkBatch("waf", "on", []string{"a.com"}); ok {
		t.Fatal("expected other actions to go through the per-item route")
	}
}

func TestCloudflareBulkReloadFailureFailsAll(t *testing.T) {
	orig := cloudflareRun
	cloudflareRun = func(args ...string) (string, error) {
		return "Disabled: /etc/openpanel/caddy/domains/a.com.conf\nReloading Caddy to apply the setting...\nWARNING: Failed to reload Caddy.\n", nil
	}
	t.Cleanup(func() { cloudflareRun = orig })

	if res := cloudflareBulk("disable", []string{"a.com"}); res[0].OK {
		t.Fatal("expected a failed reload to fail the domain")
	}
}

func TestBulkActionIconsExist(t *testing.T) {
	check := func(page string, actions []webtemplates.BulkAction) {
		for _, a := range actions {
			if a.Icon != "" && !webtemplates.HasBulkIcon(a.Icon) {
				t.Errorf("%s: action %q uses unknown icon %q", page, a.Key, a.Icon)
			}
		}
	}
	check("users", UsersBulkActions(nil, nil))
	check("containers", UserContainersBulkActions())
	check("locales", LocalesBulkActions())
	check("waf", WAFRulesBulkActions())
	check("backups", SystemBackupsBulkActions())
	check("notifications", NotificationsBulkActions())
	check("processes", ProcessesBulkActions())
	check("services", ServicesBulkActions())
	check("crons", CronsBulkActions())
	check("emails", EmailAccountsBulkActions())
	check("plans", PlansBulkActions(nil))
	check("resellers", AccountsBulkActions("resellers"))
	check("domains", DomainsBulkActions(nil))
}
