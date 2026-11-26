package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

const testThemeSettings = `{"groups":[
 {"title":"Accent colour","fields":[
  {"key":"accent_light","label":"Light mode","default":"#4F46E5","css":"html:not(.dark) { --color-accent: {{accent_light}}; --color-on-accent: {{accent_light.on}}; }"},
  {"key":"accent_dark","label":"Dark mode","default":"#47ebeb","css":"html.dark { --color-accent: {{ accent_dark }}; }"}
 ]},
 {"title":"Sidebar","fields":[
  {"key":"sidebar_light_bg","label":"Background","default":"#161a2b","css":".op-sb { background: {{sidebar_light_bg}}; color-scheme: {{sidebar_light_bg.tone}}; border-color: {{accent_light}}; }"}
 ]}
]}`

func withThemeSettings(t *testing.T, schema string) string {
	t.Helper()
	dir := withPanelThemes(t)
	orig, origFlag := PanelThemeSettingsDir, OpenpanelSettingsRestartFlagPath
	PanelThemeSettingsDir = filepath.Join(t.TempDir(), "themes")
	OpenpanelSettingsRestartFlagPath = filepath.Join(t.TempDir(), "restart")
	t.Cleanup(func() { PanelThemeSettingsDir, OpenpanelSettingsRestartFlagPath = orig, origFlag })
	_ = os.MkdirAll(filepath.Join(dir, "modern"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "modern", "readme.txt"), []byte("Theme Name: Modern\n"), 0o644)
	if schema != "" {
		_ = os.WriteFile(filepath.Join(dir, "modern", "settings.json"), []byte(schema), 0o644)
	}
	return dir
}

func TestThemeSettingsSchema(t *testing.T) {
	withThemeSettings(t, testThemeSettings)
	s, err := loadThemeSettingsSchema("modern")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Groups[0].Fields[0].Default; got != "#4f46e5" {
		t.Errorf("default not lowercased: %s", got)
	}
	if t2, _ := readPanelTheme("modern"); !t2.HasSettings {
		t.Error("HasSettings is false with a settings.json")
	}

	bad := map[string]string{
		"unknown placeholder": `{"groups":[{"fields":[{"key":"a","default":"#000000","css":"x{color:{{b}}}"}]}]}`,
		"bad default":         `{"groups":[{"fields":[{"key":"a","default":"red"}]}]}`,
		"bad key":             `{"groups":[{"fields":[{"key":"A-b","default":"#000000"}]}]}`,
		"duplicate key":       `{"groups":[{"fields":[{"key":"a","default":"#000000"},{"key":"a","default":"#000000"}]}]}`,
		"other type":          `{"groups":[{"fields":[{"key":"a","type":"text","default":"#000000"}]}]}`,
		"closing tag":         `{"groups":[{"fields":[{"key":"a","default":"#000000","css":"</style>"}]}]}`,
		"not json":            `{`,
	}
	for name, schema := range bad {
		_ = os.WriteFile(filepath.Join(PanelThemesDir, "modern", "settings.json"), []byte(schema), 0o644)
		if _, err := loadThemeSettingsSchema("modern"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestBuildThemeSettingsCSS(t *testing.T) {
	withThemeSettings(t, testThemeSettings)
	s, _ := loadThemeSettingsSchema("modern")

	if css := buildThemeSettingsCSS(s, map[string]string{}); strings.Contains(css, "--color-accent") || strings.Contains(css, "background") {
		t.Errorf("defaults should write no rules:\n%s", css)
	}

	// changing only the sidebar keeps the accent rules out, but its css still sees the default accent
	css := buildThemeSettingsCSS(s, map[string]string{"sidebar_light_bg": "#ffffff"})
	if strings.Contains(css, "--color-accent") {
		t.Errorf("unchanged accent written:\n%s", css)
	}
	for _, want := range []string{"background: #ffffff", "color-scheme: light", "border-color: #4f46e5"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in:\n%s", want, css)
		}
	}

	css = buildThemeSettingsCSS(s, map[string]string{"accent_light": "#ffd60a", "accent_dark": "#1d4ed8"})
	for _, want := range []string{"--color-accent: #ffd60a", "--color-on-accent: #14141f", "html.dark { --color-accent: #1d4ed8; }"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in:\n%s", want, css)
		}
	}
}

func TestThemeColourTone(t *testing.T) {
	for c, want := range map[string]string{"#ffffff": "light", "#000000": "dark", "#161a2b": "dark", "#4f46e5": "dark", "#47ebeb": "light", "#ffd60a": "light", "#d13e3f": "dark"} {
		if got := themeColourTone(c); got != want {
			t.Errorf("%s: %s, want %s", c, got, want)
		}
	}
}

func TestServeThemeSettings(t *testing.T) {
	dir := withThemeSettings(t, testThemeSettings)
	o := &OpenpanelSettings{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/open-panel/themes/{theme}/customize", o.ServeThemeSettings)
	mux.HandleFunc("POST /settings/open-panel/themes/{theme}/customize", o.ServeThemeSettings)
	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/settings/open-panel/themes/modern/customize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	cssPath := filepath.Join(dir, "modern", "static", "css", "settings.css")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/settings/open-panel/themes/modern/customize", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="accent_light"`) || !strings.Contains(w.Body.String(), "Customize Modern") {
		t.Fatalf("GET: %d\n%.400s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/settings/open-panel/themes/..%2fx/customize", nil))
	if w.Code != 404 {
		t.Errorf("bad folder: %d", w.Code)
	}

	// a bad colour saves nothing and says which field
	if w := post(url.Values{"accent_light": {"#12"}, "accent_dark": {"#000000"}}); w.Code != 400 || !strings.Contains(w.Body.String(), `aria-invalid="true"`) {
		t.Errorf("bad colour: %d", w.Code)
	}
	if _, err := os.Stat(cssPath); err == nil {
		t.Error("settings.css written for a rejected form")
	}

	// hex without # is fine, and a value equal to the default isn't stored
	if w := post(url.Values{"accent_light": {"FFD60A"}, "accent_dark": {"#47EBEB"}, "sidebar_light_bg": {""}}); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := loadThemeSettingsValues("modern"); len(got) != 1 || got["accent_light"] != "#ffd60a" {
		t.Errorf("saved values: %v", got)
	}
	if b, _ := os.ReadFile(cssPath); !strings.Contains(string(b), "--color-accent: #ffd60a") {
		t.Errorf("settings.css:\n%s", b)
	}
	if _, err := os.Stat(OpenpanelSettingsRestartFlagPath); err != nil {
		t.Error("restart flag not set")
	}

	// a reinstall brings the css back from the saved values
	_ = os.Remove(cssPath)
	reapplyThemeSettings("modern")
	if b, _ := os.ReadFile(cssPath); !strings.Contains(string(b), "#ffd60a") {
		t.Errorf("reapply:\n%s", b)
	}

	if w := post(url.Values{"reset": {"1"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", w.Code)
	}
	if got := loadThemeSettingsValues("modern"); len(got) != 0 {
		t.Errorf("after reset: %v", got)
	}
	if b, _ := os.ReadFile(cssPath); strings.Contains(string(b), "--color-accent") {
		t.Errorf("reset left rules:\n%s", b)
	}
}

const testThemeSettingsAuto = `{"groups":[{"title":"Sidebar","fields":[
 {"key":"bg","label":"Background","default":"#161a2b","css":".sb { background: {{bg}}; color: {{text}}; }"},
 {"key":"text","label":"Text","default":"#d3d7e6","readable_on":"bg","css":".sb { --text: {{text}}; }"},
 {"key":"active","label":"Selected item","default":"#8b85ff","readable_on":"bg","min_contrast":3,"css":".sb { --active: {{active}}; }"}
]}]}`

func TestThemeSettingsAuto(t *testing.T) {
	withThemeSettings(t, testThemeSettingsAuto)
	s, err := loadThemeSettingsSchema("modern")
	if err != nil {
		t.Fatal(err)
	}

	// the defaults already read well on the default background: nothing to write
	if css := buildThemeSettingsCSS(s, map[string]string{}); strings.Contains(css, ".sb") {
		t.Errorf("defaults wrote rules:\n%s", css)
	}

	// a white background turns the light default text dark enough to read
	all := themeSettingsResolve(s, map[string]string{"bg": "#ffffff"})
	if c := themeContrast(all["text"], "#ffffff"); c < 4.5 || c > 6 {
		t.Errorf("text %s on white: %.2f", all["text"], c)
	}
	// the selected colour already reads at 3:1 on white, so it stays and isn't written
	if all["active"] != "#8b85ff" {
		t.Errorf("active on white changed to %s", all["active"])
	}
	css := buildThemeSettingsCSS(s, map[string]string{"bg": "#ffffff"})
	for _, want := range []string{"color: " + all["text"], "--text: " + all["text"]} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in:\n%s", want, css)
		}
	}
	if strings.Contains(css, "--active") {
		t.Errorf("unchanged active written:\n%s", css)
	}
	// on a lavender background it has to darken
	all = themeSettingsResolve(s, map[string]string{"bg": "#c8c4ff"})
	if c := themeContrast(all["active"], "#c8c4ff"); all["active"] == "#8b85ff" || c < 3 || c > 3.6 {
		t.Errorf("active %s on lavender: %.2f", all["active"], c)
	}
	if css := buildThemeSettingsCSS(s, map[string]string{"bg": "#c8c4ff"}); !strings.Contains(css, "--active: "+all["active"]) {
		t.Errorf("adjusted active not written:\n%s", css)
	}

	// black needs no change
	if all := themeSettingsResolve(s, map[string]string{"bg": "#000000"}); all["text"] != "#d3d7e6" || all["active"] != "#8b85ff" {
		t.Errorf("on black: %v", all)
	}

	// a colour the admin typed is kept as it is, with a warning when it's hard to read
	all = themeSettingsResolve(s, map[string]string{"bg": "#ffffff", "text": "#eeeeee"})
	if all["text"] != "#eeeeee" {
		t.Errorf("typed text changed to %s", all["text"])
	}
	groups := fillThemeSettingsForm(s, map[string]string{"bg": "#ffffff", "text": "#eeeeee"})
	text, active := groups[0].Fields[1], groups[0].Fields[2]
	// Auto is what the field gets once it's cleared, not the typed colour
	if text.Auto != themeReadableOn("#d3d7e6", "#ffffff", 4.5) {
		t.Errorf("typed text Auto: %s", text.Auto)
	}
	if text.Value != "#eeeeee" || !strings.Contains(text.Warning, "Hard to read on the background") {
		t.Errorf("typed text: %+v", text)
	}
	if active.Value != "" || active.Auto != all["active"] || active.Warning != "" {
		t.Errorf("automatic active: %+v", active)
	}

	bad := map[string]string{
		"missing":  `{"groups":[{"fields":[{"key":"a","default":"#000000","readable_on":"b"}]}]}`,
		"self":     `{"groups":[{"fields":[{"key":"a","default":"#000000","readable_on":"a"}]}]}`,
		"chain":    `{"groups":[{"fields":[{"key":"a","default":"#000000"},{"key":"b","default":"#000000","readable_on":"a"},{"key":"c","default":"#000000","readable_on":"b"}]}]}`,
		"contrast": `{"groups":[{"fields":[{"key":"a","default":"#000000"},{"key":"b","default":"#000000","readable_on":"a","min_contrast":30}]}]}`,
	}
	for name, schema := range bad {
		_ = os.WriteFile(filepath.Join(PanelThemesDir, "modern", "settings.json"), []byte(schema), 0o644)
		if _, err := loadThemeSettingsSchema("modern"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestServeThemeSettingsAuto(t *testing.T) {
	withThemeSettings(t, testThemeSettingsAuto)
	o := &OpenpanelSettings{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/open-panel/themes/{theme}/customize", o.ServeThemeSettings)
	mux.HandleFunc("POST /settings/open-panel/themes/{theme}/customize", o.ServeThemeSettings)
	post := func(form url.Values) int {
		req := httptest.NewRequest("POST", "/settings/open-panel/themes/modern/customize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}

	// empty automatic fields stay unsaved, a typed one is saved even when it equals the default
	if code := post(url.Values{"bg": {"#ffffff"}, "text": {"#D3D7E6"}, "active": {""}}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	got := loadThemeSettingsValues("modern")
	if len(got) != 2 || got["bg"] != "#ffffff" || got["text"] != "#d3d7e6" {
		t.Errorf("saved: %v", got)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/settings/open-panel/themes/modern/customize", nil))
	body := w.Body.String()
	auto := themeSettingsResolve(func() *ThemeSettingsSchema { s, _ := loadThemeSettingsSchema("modern"); return s }(), got)["active"]
	for _, want := range []string{`placeholder="Automatic"`, "Automatic <span class=\"font-mono\">" + auto, "readable on the background", "Hard to read on the background"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET missing %q", want)
		}
	}
}

func TestServeThemeSettingsNoSchema(t *testing.T) {
	withThemeSettings(t, "")
	o := &OpenpanelSettings{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/open-panel/themes/{theme}/customize", o.ServeThemeSettings)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/settings/open-panel/themes/modern/customize", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "has no settings.json") {
		t.Errorf("%d\n%.300s", w.Code, w.Body.String())
	}
}
