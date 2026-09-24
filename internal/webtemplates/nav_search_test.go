package webtemplates

import (
	"encoding/json"
	"testing"

	coresearch "openadmin/core/search"
)

// every page the modern menu links to should also be findable from the search box
func TestSearchIndexCoversModernMenu(t *testing.T) {
	var index []struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(coresearch.DefaultFilterJSON, &index); err != nil {
		t.Fatal(err)
	}
	indexed := map[string]bool{}
	for _, e := range index {
		indexed[e.Link] = true
	}

	c := &Chrome{IsAdmin: true, LicenseType: "Enterprise", EnabledModules: []string{"dns"}}
	for _, a := range modernAreas {
		for _, tab := range a.tabs(c, "") {
			if !indexed[tab.Href] && !indexed[tab.Href+"/"] {
				t.Errorf("%s > %s (%s) is missing from core/search/filter.json", a.label, tab.Label, tab.Href)
			}
		}
	}
}

// every modern tab should have a docs page behind its Documentation button
func TestModernTabsHaveHelpDocs(t *testing.T) {
	for _, c := range []*Chrome{
		{IsAdmin: true, LicenseType: "Enterprise", EnabledModules: []string{"dns"}},
		{IsReseller: true, LicenseType: "Enterprise", EnabledModules: []string{"dns"}},
	} {
		for _, a := range modernAreas {
			for _, tab := range a.tabs(c, "") {
				// the reseller's own account page has no docs yet
				if helpDocs[tab.Href] == "" && tab.Href != "/account" {
					t.Errorf("%s > %s (%s) has no help doc", a.label, tab.Label, tab.Href)
				}
			}
		}
	}
}
