package handlers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
	"openadmin/internal/config"
)

// tests never fetch the real theme list from github
func init() {
	PanelThemesListURL, PanelThemesListLocal = "", ""
}

func withPanelThemes(t *testing.T) string {
	t.Helper()
	orig := PanelThemesDir
	PanelThemesDir = t.TempDir()
	t.Cleanup(func() { PanelThemesDir = orig })
	return PanelThemesDir
}

func TestReadPanelThemeReadme(t *testing.T) {
	dir := withPanelThemes(t)
	os.MkdirAll(filepath.Join(dir, "modern"), 0o755)
	os.WriteFile(filepath.Join(dir, "modern", "readme.txt"), []byte(`/*
Theme Name: Modern
Theme URI: https://openpanel.com/themes/modern
Author: Stefan
Author URI: javascript:alert(1)
Version: 1.2.0
License: GPLv2 or later
License URI: https://www.gnu.org/licenses/gpl-2.0.html
Description: The redesigned user panel.
*/`), 0o644)
	os.MkdirAll(filepath.Join(dir, "noreadme"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bare"), 0o755)
	os.WriteFile(filepath.Join(dir, "bare", "readme.txt"), []byte("just text\n"), 0o644)

	themes := listPanelThemes()
	if len(themes) != 2 {
		t.Fatalf("expected 2 themes, got %+v", themes)
	}
	m := themes[1]
	if themes[0].Folder != "bare" || themes[0].Name != "bare" {
		t.Fatalf("theme without headers should use the folder name: %+v", themes[0])
	}
	want := PanelTheme{Folder: "modern", Name: "Modern", URI: "https://openpanel.com/themes/modern", Author: "Stefan",
		Version: "1.2.0", License: "GPLv2 or later", LicenseURI: "https://www.gnu.org/licenses/gpl-2.0.html", Description: "The redesigned user panel."}
	if m != want {
		t.Fatalf("got %+v\nwant %+v", m, want)
	}
}

func TestValidateOpenpanelTemplate(t *testing.T) {
	dir := withPanelThemes(t)
	os.MkdirAll(filepath.Join(dir, "modern"), 0o755)
	os.WriteFile(filepath.Join(dir, "modern", "readme.txt"), []byte("Theme Name: Modern\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "noreadme"), 0o755)

	if v, ok, _ := validateOpenpanelValue("template", " "); !ok || v != "" {
		t.Fatalf("empty template should be allowed, got %q %v", v, ok)
	}
	if v, ok, _ := validateOpenpanelValue("template", "modern"); !ok || v != "modern" {
		t.Fatalf("modern should be allowed, got %q %v", v, ok)
	}
	for _, bad := range []string{"noreadme", "missing", "..", "../modern", dir + "/modern"} {
		if _, ok, _ := validateOpenpanelValue("template", bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestServeThemeScreenshot(t *testing.T) {
	dir := withPanelThemes(t)
	for _, name := range []string{"withshot", "noshot"} {
		os.MkdirAll(filepath.Join(dir, name), 0o755)
		os.WriteFile(filepath.Join(dir, name, "readme.txt"), []byte("Theme Name: x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "withshot", "screenshot.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /shot/{theme}", ServeThemeScreenshot)
	for path, want := range map[string]int{"/shot/withshot": 200, "/shot/noshot": 404, "/shot/missing": 404} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s: got %d, want %d", path, w.Code, want)
		}
	}
}

func TestParseThemesList(t *testing.T) {
	list, err := parseThemesList([]byte(`themes:
  - folder: modern
    name: Modern
    screenshot: javascript:alert(1)
    download: https://example.com/modern.tar.gz
  - folder: modern
    download: https://example.com/dupe.tar.gz
  - folder: ../evil
    download: https://example.com/evil.tar.gz
  - folder: plain
    download: http://example.com/plain.tar.gz
  - folder: noname
    download: https://example.com/noname.zip
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Folder != "modern" || list[0].Screenshot != "" || list[1].Name != "noname" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func themeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUnpackThemeStaysInside(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	data := themeTarGz(t, map[string]string{"../../escape.txt": "x", "/abs.txt": "y", "theme/readme.txt": "Theme Name: T"})
	if err := unpackTheme(data, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Fatal("entry escaped the destination")
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Fatal("symlink should be skipped")
	}
	if got, err := themeRoot(dest); err == nil {
		t.Fatalf("two top level entries plus readme in a subfolder shouldn't find a root, got %s", got)
	}

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("modern-1.0/readme.txt")
	w.Write([]byte("Theme Name: Modern"))
	zw.Close()
	zdest := t.TempDir()
	if err := unpackTheme(zbuf.Bytes(), zdest); err != nil {
		t.Fatal(err)
	}
	if got, err := themeRoot(zdest); err != nil || filepath.Base(got) != "modern-1.0" {
		t.Fatalf("zip root: %q %v", got, err)
	}
}

func TestInstallTheme(t *testing.T) {
	archive := themeTarGz(t, map[string]string{"modern/readme.txt": "Theme Name: Modern", "modern/base.html": "x"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad.tar.gz" {
			w.Write(themeTarGz(t, map[string]string{"base.html": "x"}))
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()
	orig := http.DefaultClient
	http.DefaultClient = srv.Client()
	t.Cleanup(func() { http.DefaultClient = orig })

	dir := withPanelThemes(t)
	if err := installTheme(t.Context(), RemoteTheme{Folder: "modern", Download: srv.URL + "/m.tar.gz"}, dir); err != nil {
		t.Fatal(err)
	}
	if !validPanelThemeFolder("modern") {
		t.Fatal("installed theme isn't valid")
	}
	if _, err := os.Stat(filepath.Join(dir, "modern", "base.html")); err != nil {
		t.Fatal(err)
	}
	if err := installTheme(t.Context(), RemoteTheme{Folder: "modern", Download: srv.URL + "/m.tar.gz"}, dir); err == nil {
		t.Fatal("installing over an existing theme should fail")
	}
	if err := installTheme(t.Context(), RemoteTheme{Folder: "bad", Download: srv.URL + "/bad.tar.gz"}, dir); err == nil {
		t.Fatal("archive without readme.txt should fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers after a failed install: %v", entries)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		remote, local string
		want          bool
	}{{"1.2.0", "1.0.0", true}, {"1.10.0", "1.9.2", true}, {"1.0", "1.0.0", false}, {"1.0.0", "1.2.0", false}, {"v2", "1.9", true}, {"", "1.0", false}} {
		if got := newerVersion(c.remote, c.local); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.remote, c.local, got)
		}
	}
}

func postThemeForm(t *testing.T, h http.HandlerFunc, folder string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/", strings.NewReader("theme="+folder))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(httptest.NewRecorder(), req)
}

func TestUninstallThemeResetsServerAndUsers(t *testing.T) {
	dir := withPanelThemes(t)
	withScratchOpenpanelSettingsPaths(t)
	home := t.TempDir()
	orig := PanelUserThemeGlob
	PanelUserThemeGlob = filepath.Join(home, "*", "theme")
	t.Cleanup(func() { PanelUserThemeGlob = orig })

	for _, f := range []string{"modern", "saturn"} {
		os.MkdirAll(filepath.Join(dir, f), 0o755)
		os.WriteFile(filepath.Join(dir, f, "readme.txt"), []byte("Theme Name: "+f), 0o644)
	}
	for user, theme := range map[string]string{"alice": "modern\n", "bob": "saturn\n"} {
		os.MkdirAll(filepath.Join(home, user), 0o755)
		os.WriteFile(filepath.Join(home, user, "theme"), []byte(theme), 0o644)
	}
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[PANEL]\ntemplate=modern\n"), 0o644)

	o := &OpenpanelSettings{Sessions: auth.NewManager("test-secret", false)}
	postThemeForm(t, o.ServeUninstallTheme, "modern")

	if _, err := os.Stat(filepath.Join(dir, "modern")); !os.IsNotExist(err) {
		t.Fatal("theme folder should be gone")
	}
	if _, err := os.Stat(filepath.Join(home, "alice", "theme")); !os.IsNotExist(err) {
		t.Fatal("alice picked modern, her choice should be cleared")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "bob", "theme")); string(b) != "saturn\n" {
		t.Fatalf("bob's choice shouldn't change, got %q", b)
	}
	if got := config.Load(OpenpanelSettingsConfigPath).Get("PANEL", "template", "x"); got != "" {
		t.Fatalf("template should go back to Default, got %q", got)
	}
	if _, err := os.Stat(OpenpanelSettingsRestartFlagPath); err != nil {
		t.Fatal("restart flag should be set")
	}

	postThemeForm(t, o.ServeUninstallTheme, "../etc")
	if _, err := os.Stat(filepath.Join(dir, "saturn")); err != nil {
		t.Fatal("a bad folder name must not delete anything")
	}
}

func TestUpdateThemeSwapsFolder(t *testing.T) {
	archive := themeTarGz(t, map[string]string{"modern/readme.txt": "Theme Name: Modern\nVersion: 1.2.0", "modern/base.html": "new"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer srv.Close()
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	t.Cleanup(func() { http.DefaultClient = origClient })

	dir := withPanelThemes(t)
	os.MkdirAll(filepath.Join(dir, "modern"), 0o755)
	os.WriteFile(filepath.Join(dir, "modern", "readme.txt"), []byte("Theme Name: Modern\nVersion: 1.0.0"), 0o644)
	os.WriteFile(filepath.Join(dir, "modern", "old.html"), []byte("old"), 0o644)

	if err := updateTheme(t.Context(), RemoteTheme{Folder: "modern", Download: srv.URL + "/m.tar.gz"}, dir); err != nil {
		t.Fatal(err)
	}
	if th, _ := readPanelTheme("modern"); th.Version != "1.2.0" {
		t.Fatalf("version after update = %q", th.Version)
	}
	if _, err := os.Stat(filepath.Join(dir, "modern", "old.html")); !os.IsNotExist(err) {
		t.Fatal("files from the old version should be gone")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers after update: %v", entries)
	}
}
