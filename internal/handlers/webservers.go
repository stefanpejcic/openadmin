package handlers

import (
	"html/template"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// knownWebservers is the display order for webservers we have labels and icons for, anything else found in a template goes after these
var knownWebservers = []string{"apache", "nginx", "openresty", "openlitespeed", "litespeed"}

// genericWebserverIcon is shown for webserver services we don't have a logo for
const genericWebserverIcon template.HTML = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="w-10 h-10" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><circle cx="7" cy="7.5" r="1" fill="currentColor"/><circle cx="7" cy="16.5" r="1" fill="currentColor"/></svg>`

// fallbackWebservers is used when the compose file can't be read or has no webserver services
var fallbackWebservers = []string{"apache", "nginx", "openresty", "openlitespeed"}

// cardWebservers have their own card on the defaults page, anything else gets a generic one
var cardWebservers = []string{"apache", "nginx", "openresty", "openlitespeed", "litespeed"}

// webserverServicesInComposeData returns services that mount webserver_data and publish ports, backup mounts the volume too but has no ports
func webserverServicesInComposeData(data []byte) ([]string, error) {
	var doc struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
			Ports   []any `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var names []string
	for name, svc := range doc.Services {
		if len(svc.Ports) > 0 && mountsVolume(svc.Volumes, "webserver_data") {
			names = append(names, name)
		}
	}
	return orderWebservers(names), nil
}

// mountsVolume handles both the short "name:/path" and long {source: name} volume syntax
func mountsVolume(volumes []any, volume string) bool {
	for _, v := range volumes {
		switch v := v.(type) {
		case string:
			if src, _, _ := strings.Cut(v, ":"); src == volume {
				return true
			}
		case map[string]any:
			if src, _ := v["source"].(string); src == volume {
				return true
			}
		}
	}
	return false
}

// orderWebservers puts known webservers first in their usual order, then the rest alphabetically
func orderWebservers(names []string) []string {
	rank := map[string]int{}
	for i, n := range knownWebservers {
		rank[n] = i
	}
	sort.Slice(names, func(i, j int) bool {
		ri, iKnown := rank[names[i]]
		rj, jKnown := rank[names[j]]
		switch {
		case iKnown && jKnown:
			return ri < rj
		case iKnown != jKnown:
			return iKnown
		}
		return names[i] < names[j]
	})
	return names
}

// availableWebservers lists the webservers defined in a compose file, or the built-in list if there are none
func availableWebservers(composePath string) []string {
	raw, err := os.ReadFile(composePath)
	if err == nil {
		if names, err := webserverServicesInComposeData(raw); err == nil && len(names) > 0 {
			return names
		}
	}
	return append([]string(nil), fallbackWebservers...)
}

func containsWebserver(list []string, name string) bool {
	for _, ws := range list {
		if ws == name {
			return true
		}
	}
	return false
}
