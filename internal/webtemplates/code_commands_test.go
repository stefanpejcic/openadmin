package webtemplates

import (
	"strings"
	"testing"
)

func TestCodeCommands(t *testing.T) {
	cases := []struct{ in, cmd, after string }{
		{"Recommended: set PHP 8.3 as the default with opencli php-default demo --update 8.3.", "opencli php-default demo --update 8.3", "</code>."},
		{"Run opencli php-default demo --update 8.3, then stop php-fpm-8.5 to save resources.", "opencli php-default demo --update 8.3", "</code>, then"},
	}
	for _, c := range cases {
		got := string(codeCommands(c.in))
		if !strings.Contains(got, ">"+c.cmd+c.after) {
			t.Errorf("codeCommands(%q) = %q, want %q wrapped in code", c.in, got, c.cmd)
		}
	}
	if got := string(codeCommands("No command <b>here</b>.")); got != "No command &lt;b&gt;here&lt;/b&gt;." {
		t.Errorf("plain text not escaped: %q", got)
	}
}
