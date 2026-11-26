package webtemplates

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// the access log viewer must never drop a logged value into a place the browser runs as code
func TestAccessLogXSSContained(t *testing.T) {
	payload := `/x';eval(atob('ZXZpbA=='));'`
	data := map[string]interface{}{
		"DomainName": "example.com", "TotalLines": 1, "TotalAllowedLinesForShowAll": 100, "ShowingAllLines": false, "LinesDisplayed": 1, "ItemsPerPage": 100, "CurrentPage": 1, "TotalPages": 1, "StartLine": 1, "EndLine": 1,
		"JSONLogs": []map[string]interface{}{{
			"ts": "1", "level": "info", "logger": "l", "msg": "m", "status": 403, "size": 0, "duration": 0.1,
			"request": map[string]interface{}{"client_ip": "1.2.3.4", "method": "GET", "uri": payload},
		}},
	}
	chrome := map[string]interface{}{
		"CurrentPath": "/domains/log", "CurrentUser": "admin", "Title": "Access Logs",
		"AreaHref": "", "AreaLabel": "", "HelpDoc": "", "HelpPart": "", "MenuStyle": "", "PanelVersion": "",
		"PublicIP": "", "ServerHostname": "", "LicenseType": "Community", "ForceDomainValue": "",
		"IsAdmin": true, "IsReseller": false, "IsUser": false, "DevMode": false, "TourShow": false,
		"CustomCSSEnabled": false, "ClusterSwitcher": false, "ClusterCount": 0, "UnreadNotifications": 0,
		"EnabledModules": []string{}, "NavItems": []interface{}{}, "PageTabs": []interface{}{},
		"RestartMessages": []interface{}{}, "CSRFToken": "",
	}
	for k, v := range chrome {
		if _, ok := data[k]; !ok {
			data[k] = v
		}
	}
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, "domains_logs.html", data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// the raw breakout sequence (decoded quote ending the JS string) must not be present anywhere
	if strings.Contains(out, `';eval(atob(`) {
		t.Errorf("raw payload breakout present in output")
	}
	// no x-show attribute may contain the eval payload
	for _, m := range regexp.MustCompile(`x-show="[^"]*"`).FindAllString(out, -1) {
		if strings.Contains(m, "eval(atob") {
			t.Errorf("payload landed inside an x-show expression: %.80s", m)
		}
	}
	// it should still be shown, HTML-escaped, in a table cell
	if !strings.Contains(out, "&#39;;eval(atob(") {
		t.Errorf("payload not rendered as escaped text; output missing escaped form")
	}
}
