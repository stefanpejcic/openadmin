package handlers

import (
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
