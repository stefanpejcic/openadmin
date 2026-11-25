package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// A theme can ship settings.json next to its readme.txt to get a Customize page under OpenPanel settings.
// Every field is a colour. The admin's values are kept in PanelThemeSettingsDir/<folder>.json, outside the
// theme folder so a reinstall keeps them, and written into the theme as static/css/settings.css, which the
// theme links like any of its static files. A field's css is only written when its value differs from the
// default, so an untouched theme keeps the exact colours it ships with.
//
//	{
//	  "groups": [{
//	    "title": "Accent colour", "description": "Buttons, links and toggles.",
//	    "fields": [{
//	      "key": "accent_light", "label": "Light mode", "default": "#4f46e5",
//	      "css": "html:not(.dark) { --color-accent: {{accent_light}}; --color-on-accent: {{accent_light.on}}; }"
//	    }]
//	  }]
//	}
//
// In css, {{key}} is that field's value, {{key.on}} is #ffffff or #14141f, whichever reads better on it,
// and {{key.tone}} is light or dark.

// PanelThemeSettingsDir holds the saved values, one <theme folder>.json each
var PanelThemeSettingsDir = "/etc/openpanel/openpanel/conf/themes"

const (
	themeSettingsFile = "settings.json"
	themeSettingsCSS  = "static/css/settings.css"
	themeSettingsMax  = 256 << 10
)

// ThemeSettingsSchema is a theme's settings.json
type ThemeSettingsSchema struct {
	Groups []ThemeSettingsGroup `json:"groups"`
}

type ThemeSettingsGroup struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Fields      []ThemeSettingsField `json:"fields"`
}

type ThemeSettingsField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	CSS         string `json:"css"`
	// set for the form
	Value string `json:"-"`
	Error string `json:"-"`
}

var (
	themeSettingKey  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	themeHexColour   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeCSSVariable = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)(?:\.(on|tone))?\s*\}\}`)
)

// themeHasSettings reports whether the theme folder ships a settings.json
func themeHasSettings(folder string) bool {
	info, err := os.Stat(filepath.Join(PanelThemesDir, folder, themeSettingsFile))
	return err == nil && !info.IsDir()
}

// loadThemeSettingsSchema reads and checks folder/settings.json
func loadThemeSettingsSchema(folder string) (*ThemeSettingsSchema, error) {
	b, err := os.ReadFile(filepath.Join(PanelThemesDir, folder, themeSettingsFile))
	if err != nil {
		return nil, errors.New("this theme has no settings.json")
	}
	if len(b) > themeSettingsMax {
		return nil, errors.New("settings.json is larger than 256 KB")
	}
	var s ThemeSettingsSchema
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON: %v", err)
	}
	seen := map[string]bool{}
	for gi := range s.Groups {
		for fi := range s.Groups[gi].Fields {
			f := &s.Groups[gi].Fields[fi]
			if !themeSettingKey.MatchString(f.Key) {
				return nil, fmt.Errorf("settings.json: %q is not a valid key, use lowercase letters, digits and _", f.Key)
			}
			if seen[f.Key] {
				return nil, fmt.Errorf("settings.json: key %s is used twice", f.Key)
			}
			seen[f.Key] = true
			if f.Type == "" {
				f.Type = "color"
			}
			if f.Type != "color" {
				return nil, fmt.Errorf("settings.json: %s has type %q, only color is supported", f.Key, f.Type)
			}
			if !themeHexColour.MatchString(f.Default) {
				return nil, fmt.Errorf("settings.json: the default for %s must be a #rrggbb colour", f.Key)
			}
			f.Default = strings.ToLower(f.Default)
			if f.Label == "" {
				f.Label = f.Key
			}
		}
	}
	// a placeholder for a key the theme doesn't define would end up as an empty css value
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			for _, m := range themeCSSVariable.FindAllStringSubmatch(f.CSS, -1) {
				if !seen[m[1]] {
					return nil, fmt.Errorf("settings.json: the css for %s uses {{%s}}, which isn't a field", f.Key, m[1])
				}
			}
			if strings.Contains(f.CSS, "</") {
				return nil, fmt.Errorf("settings.json: the css for %s contains </", f.Key)
			}
		}
	}
	return &s, nil
}

func themeSettingsValuesPath(folder string) string {
	return filepath.Join(PanelThemeSettingsDir, folder+".json")
}

// loadThemeSettingsValues returns the saved colours, an empty map when nothing was saved yet
func loadThemeSettingsValues(folder string) map[string]string {
	values := map[string]string{}
	b, err := os.ReadFile(themeSettingsValuesPath(folder))
	if err != nil {
		return values
	}
	var raw map[string]string
	if json.Unmarshal(b, &raw) != nil {
		return values
	}
	for k, v := range raw {
		if themeSettingKey.MatchString(k) && themeHexColour.MatchString(v) {
			values[k] = strings.ToLower(v)
		}
	}
	return values
}

// themeColourTone is dark when white text reads better on c than near-black text
func themeColourTone(c string) string {
	r, _ := strconv.ParseUint(c[1:3], 16, 8)
	g, _ := strconv.ParseUint(c[3:5], 16, 8)
	b, _ := strconv.ParseUint(c[5:7], 16, 8)
	lin := func(v uint64) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	l := 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	// contrast against white (1.0) vs against #14141f (~0.007)
	if (1.05)/(l+0.05) >= (l+0.05)/(0.007+0.05) {
		return "dark"
	}
	return "light"
}

// buildThemeSettingsCSS renders the css of every field whose value differs from its default
func buildThemeSettingsCSS(s *ThemeSettingsSchema, values map[string]string) string {
	all := map[string]string{}
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			all[f.Key] = f.Default
			if v, ok := values[f.Key]; ok {
				all[f.Key] = v
			}
		}
	}
	var out strings.Builder
	out.WriteString("/* written by OpenAdmin > Settings > OpenPanel > Customize colours, changes here are overwritten on the next save */\n")
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			if all[f.Key] == f.Default || strings.TrimSpace(f.CSS) == "" {
				continue
			}
			css := themeCSSVariable.ReplaceAllStringFunc(f.CSS, func(m string) string {
				p := themeCSSVariable.FindStringSubmatch(m)
				v := all[p[1]]
				switch p[2] {
				case "on":
					if themeColourTone(v) == "dark" {
						return "#ffffff"
					}
					return "#14141f"
				case "tone":
					return themeColourTone(v)
				}
				return v
			})
			out.WriteString("/* " + f.Key + " */\n" + strings.TrimSpace(css) + "\n")
		}
	}
	return out.String()
}

// writeFileAtomic writes data next to path and renames it over, so the panel never serves half a file
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// applyThemeSettings writes the saved values and the theme's settings.css
func applyThemeSettings(folder string, s *ThemeSettingsSchema, values map[string]string) error {
	b, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(themeSettingsValuesPath(folder), append(b, '\n')); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(PanelThemesDir, folder, filepath.FromSlash(themeSettingsCSS)), []byte(buildThemeSettingsCSS(s, values)))
}

// reapplyThemeSettings rewrites settings.css after a theme is installed again, a no-op when nothing was saved for it
func reapplyThemeSettings(folder string) {
	if _, err := os.Stat(themeSettingsValuesPath(folder)); err != nil || !themeHasSettings(folder) {
		return
	}
	s, err := loadThemeSettingsSchema(folder)
	if err != nil {
		log.Printf("themes: %s settings: %v", folder, err)
		return
	}
	if err := applyThemeSettings(folder, s, loadThemeSettingsValues(folder)); err != nil {
		log.Printf("themes: writing %s settings.css: %v", folder, err)
	}
}

type themeSettingsPageData struct {
	webtemplates.Chrome
	Theme     PanelTheme
	Groups    []ThemeSettingsGroup
	Error     string
	CSRFToken string
	Flashes   []auth.Flash
	Changed   int
}

// ServeThemeSettings handles GET/POST /settings/open-panel/themes/{theme}/customize
func (o *OpenpanelSettings) ServeThemeSettings(w http.ResponseWriter, r *http.Request) {
	folder := r.PathValue("theme")
	if !validPanelThemeFolder(folder) {
		http.NotFound(w, r)
		return
	}
	theme, _ := readPanelTheme(folder)
	data := themeSettingsPageData{
		Chrome:    buildChrome(r, "Customize "+theme.Name),
		Theme:     theme,
		CSRFToken: csrf.Token(r),
	}
	schema, err := loadThemeSettingsSchema(folder)
	if err != nil {
		data.Error = err.Error()
		data.Flashes = auth.PopFlashes(w, r, o.Sessions)
		o.renderThemeSettings(w, data, http.StatusOK)
		return
	}
	values := loadThemeSettingsValues(folder)

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		next := map[string]string{}
		invalid := false
		reset := r.PostFormValue("reset") == "1"
		for gi := range schema.Groups {
			for fi := range schema.Groups[gi].Fields {
				f := &schema.Groups[gi].Fields[fi]
				if reset {
					continue
				}
				v := strings.TrimSpace(r.PostFormValue(f.Key))
				if v == "" {
					v = f.Default
				}
				if !strings.HasPrefix(v, "#") {
					v = "#" + v
				}
				if !themeHexColour.MatchString(v) {
					f.Error = "Use a colour like " + f.Default + "."
					f.Value = strings.TrimSpace(r.PostFormValue(f.Key))
					invalid = true
					continue
				}
				v = strings.ToLower(v)
				f.Value = v
				if v != f.Default {
					next[f.Key] = v
				}
			}
		}
		if invalid {
			data.Groups = schema.Groups
			data.Error = "Some colours aren't valid, nothing was saved."
			o.renderThemeSettings(w, data, http.StatusBadRequest)
			return
		}
		if err := applyThemeSettings(folder, schema, next); err != nil {
			log.Printf("themes: saving %s settings: %v", folder, err)
			_ = auth.AddFlash(w, r, o.Sessions, "Error saving the theme colours: "+err.Error(), "error")
		} else {
			_ = os.WriteFile(OpenpanelSettingsRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
			msg := "Theme colours saved, they apply after OpenPanel restarts."
			if reset {
				msg = "Theme colours reset to the defaults, they apply after OpenPanel restarts."
			}
			_ = auth.AddFlash(w, r, o.Sessions, msg, "success")
		}
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}

	data.Groups = fillThemeSettingsForm(schema, values)
	for _, g := range data.Groups {
		for _, f := range g.Fields {
			if f.Value != f.Default {
				data.Changed++
			}
		}
	}
	data.Flashes = auth.PopFlashes(w, r, o.Sessions)
	o.renderThemeSettings(w, data, http.StatusOK)
}

// fillThemeSettingsForm sets each field's Value from the saved values
func fillThemeSettingsForm(s *ThemeSettingsSchema, values map[string]string) []ThemeSettingsGroup {
	for gi := range s.Groups {
		for fi := range s.Groups[gi].Fields {
			f := &s.Groups[gi].Fields[fi]
			f.Value = f.Default
			if v, ok := values[f.Key]; ok {
				f.Value = v
			}
		}
	}
	return s.Groups
}

func (o *OpenpanelSettings) renderThemeSettings(w http.ResponseWriter, data themeSettingsPageData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := webtemplates.Render(w, "settings_theme.html", data); err != nil {
		log.Printf("render settings_theme.html: %v", err)
	}
}
