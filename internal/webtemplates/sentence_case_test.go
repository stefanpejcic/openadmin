package webtemplates

import "testing"

func TestSentenceCase(t *testing.T) {
	cases := map[string]string{
		"Settings (Email & Password Change)": "Settings (email & password change)",
		"Switch MySQL Type":                  "Switch MySQL type",
		"ClamAV (Malware Scanner)":           "ClamAV (malware scanner)",
		"Docker (Containers)":                "Docker (containers)",
		"Dynamic DNS":                        "Dynamic DNS",
		"Coraza WAF":                         "Coraza WAF",
		"PHP.INI Editor":                     "PHP.INI editor",
		"Edit VirtualHosts":                  "Edit VirtualHosts",
		"AI Assistant":                       "AI assistant",
		"Domains":                            "Domains",
	}
	for in, want := range cases {
		if got := sentenceCase(in); got != want {
			t.Errorf("sentenceCase(%q) = %q, want %q", in, got, want)
		}
	}
}
