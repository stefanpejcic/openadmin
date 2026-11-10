package handlers

import "testing"

func TestModuleAreaOf(t *testing.T) {
	cases := map[string]string{
		"/account/sessions":      "account",
		"":                       "account",
		"/domains/ssl":           "domains",
		"/wordpress":             "sites",
		"/joomla/install":        "sites",
		"/emails/aliases":        "email",
		"/webmail":               "email",
		"/mysql/phpmyadmin":      "db",
		"/postgresql/import":     "db",
		"/files.trash":           "files",
		"/backup-wizard":         "files",
		"/malware-scanner":       "security",
		"/imunifyav":             "security",
		"/php/options":           "runtimes",
		"/nodejs":                "runtimes",
		"/containers/terminal":   "server",
		"/cache/varnish":         "server",
		"/server/webserver_conf": "server",
		"/waf":                   "security",
		"/account/2fa":           "security",
		"/account/passkeys":      "security",
		"/account/login-history": "security",
		"/account/activity":      "account",
		"/api/login":             "account",
		"/services":              "server",
		"/process-manager":       "server",
		"/error-logs":            "server",
		"/sites":                 "sites",
	}
	for link, want := range cases {
		if got := moduleAreaOf(link); got != want {
			t.Errorf("moduleAreaOf(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestGroupModulesKeepsOrderAndSkipsEmptyAreas(t *testing.T) {
	groups := groupModules([]map[string]interface{}{
		{"name": "a", "link": "/waf"},
		{"name": "b", "link": "/account"},
		{"name": "c", "link": "/account/sessions"},
	})
	if len(groups) != 2 || groups[0].Key != "account" || groups[1].Key != "security" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if groups[0].Features[0]["name"] != "b" || groups[0].Features[1]["name"] != "c" {
		t.Fatalf("order not kept: %+v", groups[0].Features)
	}
}
