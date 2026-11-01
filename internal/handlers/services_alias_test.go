package handlers

import "testing"

func TestResolveUnitAlias(t *testing.T) {
	installed := func(units ...string) func(string) bool {
		set := map[string]bool{}
		for _, u := range units {
			set[u] = true
		}
		return func(u string) bool { return set[u] }
	}
	cases := []struct {
		name      string
		installed []string
		want      string
	}{
		{"cron", []string{"crond"}, "crond"},
		{"ssh", []string{"sshd"}, "sshd"},
		{"cron", []string{"cron"}, "cron"},
		{"crond", []string{"cron"}, "cron"},
		{"ssh", nil, "ssh"},
		{"nginx", nil, "nginx"},
	}
	for _, c := range cases {
		if got := resolveUnitAlias(c.name, installed(c.installed...)); got != c.want {
			t.Errorf("resolveUnitAlias(%q, %v) = %q, want %q", c.name, c.installed, got, c.want)
		}
	}
}
