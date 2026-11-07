package handlers

import (
	"regexp"

	"openadmin/internal/webtemplates"
)

// moduleGroup is one area on the Modules page with its modules in features.json order
type moduleGroup struct {
	webtemplates.ModuleArea
	Features []map[string]interface{}
}

var moduleAreaRules = []struct {
	key string
	re  *regexp.Regexp
}{
	{"security", regexp.MustCompile(`^/(waf|security)\b|^/account/(2fa|passkeys|login-history)\b`)},
	{"domains", regexp.MustCompile(`^/domains`)},
	{"sites", regexp.MustCompile(`^/(wordpress|auto-installer|website-builder|git|sites)\b|/install$`)},
	{"email", regexp.MustCompile(`^/(emails|webmail)\b`)},
	{"db", regexp.MustCompile(`^/(mysql|postgresql|mongodb)\b`)},
	{"files", regexp.MustCompile(`^/(files|ftp|disk-usage|inodes|fix-permissions|backup|cronjobs|malware)`)},
	{"runtimes", regexp.MustCompile(`^/(php|python|nodejs|ruby|java|n8n)\b`)},
	{"server", regexp.MustCompile(`^/(containers|cache|server|services|process-manager|error-logs)\b`)},
}

// moduleAreaOf picks the area from the module's panel path, anything unmatched lands in Account
func moduleAreaOf(link string) string {
	for _, r := range moduleAreaRules {
		if r.re.MatchString(link) {
			return r.key
		}
	}
	return "account"
}

// groupModules splits features.json into the page's areas, empty areas are left out
func groupModules(features []map[string]interface{}) []moduleGroup {
	byKey := map[string][]map[string]interface{}{}
	for _, f := range features {
		link, _ := f["link"].(string)
		k := moduleAreaOf(link)
		byKey[k] = append(byKey[k], f)
	}
	var out []moduleGroup
	for _, a := range webtemplates.ModuleAreas {
		if len(byKey[a.Key]) > 0 {
			out = append(out, moduleGroup{ModuleArea: a, Features: byKey[a.Key]})
		}
	}
	return out
}
