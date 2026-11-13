package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WPProfilesDir holds the named plugin/theme sets users can pick when installing WordPress
var WPProfilesDir = "/etc/openpanel/wordpress/sets/profiles"

type wpProfile struct {
	Slug    string   `json:"-"`
	Name    string   `json:"name"`
	Plugins []string `json:"plugins"`
	Themes  []string `json:"themes"`
}

func (p wpProfile) PluginsText() string { return strings.Join(p.Plugins, "\n") }
func (p wpProfile) ThemesText() string  { return strings.Join(p.Themes, "\n") }

// wpSetLines turns a textarea into a clean list, one slug or zip url per line
func wpSetLines(s string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

func listWPProfiles() []wpProfile {
	entries, _ := os.ReadDir(WPProfilesDir)
	var out []wpProfile
	for _, e := range entries {
		slug, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok || !validSuspensionSlug(slug) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(WPProfilesDir, e.Name()))
		if err != nil {
			continue
		}
		var p wpProfile
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		p.Slug = slug
		if p.Name == "" {
			p.Name = slug
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func wpProfileExists(slug string) bool {
	if !validSuspensionSlug(slug) {
		return false
	}
	_, err := os.Stat(filepath.Join(WPProfilesDir, slug+".json"))
	return err == nil
}

func saveWPProfile(slug, name, plugins, themes string) error {
	if err := os.MkdirAll(WPProfilesDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(wpProfile{Name: cleanSuspensionName(name), Plugins: wpSetLines(plugins), Themes: wpSetLines(themes)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(WPProfilesDir, slug+".json"), append(data, '\n'), 0644)
}

// saveWPProfilesForm handles the profile rows on /settings/custom-code, returns a flash only when something needs saying
func saveWPProfilesForm(r *http.Request) (string, string) {
	for _, p := range listWPProfiles() {
		name, hasName := r.PostForm["wp_profile_name__"+p.Slug]
		plugins, hasPlugins := r.PostForm["wp_profile_plugins__"+p.Slug]
		themes, hasThemes := r.PostForm["wp_profile_themes__"+p.Slug]
		if !hasName || !hasPlugins || !hasThemes {
			continue
		}
		if cleanSuspensionName(name[0]) == "" {
			name[0] = p.Name
		}
		if err := saveWPProfile(p.Slug, name[0], plugins[0], themes[0]); err != nil {
			return "Failed to save WordPress profile: " + err.Error(), "error"
		}
	}

	if del := r.PostFormValue("delete_wp_profile"); del != "" {
		if !wpProfileExists(del) {
			return "WordPress profile not found.", "error"
		}
		if err := os.Remove(filepath.Join(WPProfilesDir, del+".json")); err != nil {
			return "Failed to delete WordPress profile: " + err.Error(), "error"
		}
		return "WordPress profile deleted.", "success"
	}

	if name := cleanSuspensionName(r.PostFormValue("new_wp_profile_name")); name != "" {
		slug := suspensionSlugFromName(name)
		if slug == "" {
			return "Profile name needs at least one letter or number.", "error"
		}
		if wpProfileExists(slug) {
			return "A WordPress profile with that name already exists.", "error"
		}
		if err := saveWPProfile(slug, name, r.PostFormValue("new_wp_profile_plugins"), r.PostFormValue("new_wp_profile_themes")); err != nil {
			return "Failed to save WordPress profile: " + err.Error(), "error"
		}
		return "WordPress profile '" + name + "' added.", "success"
	}
	return "", ""
}
