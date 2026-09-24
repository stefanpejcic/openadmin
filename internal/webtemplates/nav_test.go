package webtemplates

import (
	"strings"
	"testing"
)

func labels(items []NavItem) string {
	var out []string
	for _, i := range items {
		out = append(out, i.Label)
	}
	return strings.Join(out, ",")
}

func tabLabels(tabs []NavLink) (string, string) {
	var out []string
	active := ""
	for _, t := range tabs {
		out = append(out, t.Label)
		if t.Active {
			active = t.Label
		}
	}
	return strings.Join(out, ","), active
}

func TestBuildModernNavAdmin(t *testing.T) {
	c := &Chrome{CurrentPath: "/domains/dns", IsAdmin: true, LicenseType: "Enterprise", EnabledModules: []string{"dns"}}
	items, tabs := BuildModernNav(c)

	if got := labels(items); got != "Accounts,Hosting Plans,Domains,Emails,Backups,Services,Security,Server,System,Settings,License & Support" {
		t.Errorf("unexpected sidebar: %s", got)
	}
	for _, i := range items {
		if i.Active != (i.Label == "Domains") {
			t.Errorf("%s active=%v on /domains/dns", i.Label, i.Active)
		}
		if i.Label == "Services" && i.Section != "Server" || i.Label == "Settings" && i.Section != "Settings" {
			t.Errorf("%s has section %q", i.Label, i.Section)
		}
	}
	got, active := tabLabels(tabs)
	if got != "Domains,DNS Zone Editor,DNS Cluster,Zone Templates,Domain Templates" || active != "DNS Zone Editor" {
		t.Errorf("unexpected domain tabs: %s (active %s)", got, active)
	}
}

func TestBuildModernNavGating(t *testing.T) {
	// community license, no dns module
	items, tabs := BuildModernNav(&Chrome{CurrentPath: "/domains", IsAdmin: true})
	if strings.Contains(labels(items), "Emails") {
		t.Error("did not expect Emails without Enterprise")
	}
	if got, _ := tabLabels(tabs); got != "Domains,Domain Templates" {
		t.Errorf("unexpected domain tabs without dns/Enterprise: %s", got)
	}
}

func TestBuildModernNavReseller(t *testing.T) {
	items, tabs := BuildModernNav(&Chrome{CurrentPath: "/security/2fa", IsReseller: true, LicenseType: "Enterprise"})
	if got := labels(items); got != "Accounts,Hosting Plans" {
		t.Errorf("unexpected reseller sidebar: %s", got)
	}
	got, active := tabLabels(tabs)
	if got != "Users,Reseller Account,Two-Factor Authentication,Passkeys" || active != "Two-Factor Authentication" {
		t.Errorf("unexpected reseller tabs: %s (active %s)", got, active)
	}
}

func TestBuildModernNavServerAndSystem(t *testing.T) {
	c := &Chrome{IsAdmin: true}
	for path, want := range map[string]string{
		"/server/resource-usage": "Server",
		"/terminal":              "Server",
		"/tasks":                 "Server",
		"/server/timezone":       "System",
		"/server/node":           "System",
		"/user/import":           "Backups",
		"/users/alice":           "Accounts",
		"/settings/updates/log":  "Settings",
	} {
		c.CurrentPath = path
		items, _ := BuildModernNav(c)
		var active []string
		for _, i := range items {
			if i.Active {
				active = append(active, i.Label)
			}
		}
		if len(active) != 1 || active[0] != want {
			t.Errorf("%s: expected only %s active, got %v", path, want, active)
		}
	}
}

func TestBuildModernNavSingleTabKeepsBarForDocs(t *testing.T) {
	_, tabs := BuildModernNav(&Chrome{CurrentPath: "/license", IsAdmin: true})
	if len(tabs) != 1 || func() bool { d, _ := HelpDocFor(tabs, "/license"); return d != "admin/license" }() {
		t.Errorf("expected License & Support to keep its tab bar for the docs button, got %+v", tabs)
	}
}

func TestBuildModernNavSystemStartsWithServerTime(t *testing.T) {
	items, tabs := BuildModernNav(&Chrome{CurrentPath: "/server/ssh", IsAdmin: true})
	got, _ := tabLabels(tabs)
	if !strings.HasPrefix(got, "Server Time,Root Password") {
		t.Errorf("expected Server Time as the first System tab, got %s", got)
	}
	for _, i := range items {
		if i.Label == "System" && i.Href != "/server/timezone" {
			t.Errorf("expected the System link to open Server Time, got %s", i.Href)
		}
	}
}

func TestHelpDocForUsersSplitsTheDoc(t *testing.T) {
	for path, want := range map[string]string{
		"/users":        "until:Single User",
		"/users/":       "until:Single User",
		"/user/new":     "until:Single User",
		"/users/stefan": "from:Single User",
	} {
		_, tabs := BuildModernNav(&Chrome{CurrentPath: path, IsAdmin: true})
		if doc, part := HelpDocFor(tabs, path); doc != "admin/accounts/users" || part != want {
			t.Errorf("%s: got %q %q, want admin/accounts/users %q", path, doc, part, want)
		}
	}
}
