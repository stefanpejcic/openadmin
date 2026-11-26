package handlers

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"openadmin/internal/auth"
	"openadmin/internal/config"
)

// PanelThemesDir holds one folder per user panel theme, template= in openpanel.config names one of them
var PanelThemesDir = "/etc/openpanel/openpanel/templates"

// PanelTheme is a theme folder with the headers from its readme.txt, read the same way WordPress reads style.css
type PanelTheme struct {
	Folder      string
	Name        string
	URI         string
	Author      string
	AuthorURI   string
	Version     string
	License     string
	LicenseURI  string
	Description string
	// HasSettings: the theme ships settings.json, so it gets a Customize page
	HasSettings bool
	// UpdateVersion is the newer version themes.yml lists, "" when it's up to date
	UpdateVersion string
}

var themeReadmeHeaders = map[string]func(*PanelTheme, string){
	"theme name":  func(t *PanelTheme, v string) { t.Name = v },
	"theme uri":   func(t *PanelTheme, v string) { t.URI = v },
	"author":      func(t *PanelTheme, v string) { t.Author = v },
	"author uri":  func(t *PanelTheme, v string) { t.AuthorURI = v },
	"version":     func(t *PanelTheme, v string) { t.Version = v },
	"license":     func(t *PanelTheme, v string) { t.License = v },
	"license uri": func(t *PanelTheme, v string) { t.LicenseURI = v },
	"description": func(t *PanelTheme, v string) { t.Description = v },
}

// readPanelTheme reads dir/readme.txt, ok is false when the folder has none so it isn't a theme
func readPanelTheme(folder string) (PanelTheme, bool) {
	f, err := os.Open(filepath.Join(PanelThemesDir, folder, "readme.txt"))
	if err != nil {
		return PanelTheme{}, false
	}
	defer func() { _ = f.Close() }()
	t := PanelTheme{Folder: folder}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// headers may sit inside a comment block like in style.css
		line := strings.TrimLeft(strings.TrimSpace(sc.Text()), "/*# ")
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if set, known := themeReadmeHeaders[strings.ToLower(strings.TrimSpace(key))]; known {
			set(&t, strings.TrimSpace(value))
		}
	}
	if t.Name == "" {
		t.Name = folder
	}
	t.HasSettings = themeHasSettings(folder)
	// links end up in href, only allow http(s)
	for _, u := range []*string{&t.URI, &t.AuthorURI, &t.LicenseURI} {
		if !strings.HasPrefix(*u, "https://") && !strings.HasPrefix(*u, "http://") {
			*u = ""
		}
	}
	return t, true
}

// listPanelThemes returns every folder in PanelThemesDir that has a readme.txt, sorted by name
func listPanelThemes() []PanelTheme {
	entries, err := os.ReadDir(PanelThemesDir)
	if err != nil {
		return nil
	}
	var themes []PanelTheme
	for _, e := range entries {
		// .update- and .old- are half done updates
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if t, ok := readPanelTheme(e.Name()); ok {
			themes = append(themes, t)
		}
	}
	sort.Slice(themes, func(i, j int) bool { return strings.ToLower(themes[i].Name) < strings.ToLower(themes[j].Name) })
	return themes
}

// validPanelThemeFolder reports whether name is a plain folder name of an existing theme
func validPanelThemeFolder(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	_, ok := readPanelTheme(name)
	return ok
}

// ServeThemeScreenshot handles GET /settings/open-panel/theme-screenshot/{theme}, a missing screenshot.png is a 404 so the picker shows a broken image
func ServeThemeScreenshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("theme")
	if !validPanelThemeFolder(name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(PanelThemesDir, name, "screenshot.png")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

// PanelThemesListURL is the maintained list of user panel themes, the copy in the /etc/openpanel checkout is used when github can't be reached
var (
	PanelThemesListURL   = "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/openpanel/templates/themes.yml"
	PanelThemesListLocal = "/etc/openpanel/openpanel/templates/themes.yml"
)

const (
	themesListTTL      = 24 * time.Hour
	themeDownloadMax   = 50 << 20
	themeUnpackedMax   = 200 << 20
	themeDownloadLimit = 2 * time.Minute
)

// RemoteTheme is one entry from themes.yml
type RemoteTheme struct {
	Folder      string `yaml:"folder"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Author      string `yaml:"author"`
	AuthorURI   string `yaml:"author_uri"`
	Version     string `yaml:"version"`
	License     string `yaml:"license"`
	LicenseURI  string `yaml:"license_uri"`
	URI         string `yaml:"uri"`
	Screenshot  string `yaml:"screenshot"`
	Download    string `yaml:"download"`
}

var themeFolderRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var (
	themesListMu      sync.Mutex
	themesListCache   []RemoteTheme
	themesListFetched time.Time
)

// parseThemesList reads themes.yml and drops entries that can't be installed
func parseThemesList(data []byte) ([]RemoteTheme, error) {
	var f struct {
		Themes []RemoteTheme `yaml:"themes"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	var out []RemoteTheme
	seen := map[string]bool{}
	for _, t := range f.Themes {
		if !themeFolderRE.MatchString(t.Folder) || seen[t.Folder] || !isHTTPSURL(t.Download) {
			continue
		}
		seen[t.Folder] = true
		if t.Name == "" {
			t.Name = t.Folder
		}
		for _, u := range []*string{&t.URI, &t.AuthorURI, &t.LicenseURI, &t.Screenshot} {
			if !strings.HasPrefix(*u, "https://") && !strings.HasPrefix(*u, "http://") {
				*u = ""
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func isHTTPSURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// loadThemesList returns the theme list, fetched from github at most once a day
func loadThemesList(ctx context.Context) []RemoteTheme {
	themesListMu.Lock()
	defer themesListMu.Unlock()
	if !themesListFetched.IsZero() && time.Since(themesListFetched) < themesListTTL {
		return themesListCache
	}
	list, err := fetchThemesList(ctx)
	if err != nil {
		if PanelThemesListURL != "" {
			log.Printf("themes: %s: %v, using %s", PanelThemesListURL, err, PanelThemesListLocal)
		}
		if raw, readErr := os.ReadFile(PanelThemesListLocal); readErr == nil {
			list, _ = parseThemesList(raw)
		}
		// try github again in 10 minutes rather than tomorrow
		themesListFetched = time.Now().Add(-themesListTTL + 10*time.Minute)
	} else {
		themesListFetched = time.Now()
	}
	themesListCache = list
	return list
}

func fetchThemesList(ctx context.Context) ([]RemoteTheme, error) {
	if PanelThemesListURL == "" {
		return nil, errors.New("no list url")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PanelThemesListURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseThemesList(data)
}

// availableThemes are listed themes that aren't installed yet
func availableThemes(ctx context.Context, installed []PanelTheme) []RemoteTheme {
	have := map[string]bool{}
	for _, t := range installed {
		have[t.Folder] = true
	}
	var out []RemoteTheme
	for _, t := range loadThemesList(ctx) {
		if !have[t.Folder] {
			out = append(out, t)
		}
	}
	return out
}

// ServeInstallTheme handles POST /settings/open-panel/themes/install, downloads a listed theme into PanelThemesDir
func (o *OpenpanelSettings) ServeInstallTheme(w http.ResponseWriter, r *http.Request) {
	folder := r.PostFormValue("install_theme")
	var theme *RemoteTheme
	for _, t := range loadThemesList(r.Context()) {
		if t.Folder == folder {
			theme = &t
			break
		}
	}
	switch {
	case theme == nil:
		_ = auth.AddFlash(w, r, o.Sessions, "Error: theme not found in the theme list.", "error")
	case validPanelThemeFolder(folder):
		_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Theme %s is already installed.", theme.Name), "error")
	default:
		if err := installTheme(r.Context(), *theme, PanelThemesDir); err != nil {
			log.Printf("themes: installing %s from %s: %v", theme.Folder, theme.Download, err)
			_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Error installing theme %s: %v", theme.Name, err), "error")
		} else {
			// colours saved before the theme was removed come back with it
			reapplyThemeSettings(theme.Folder)
			_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Theme %s installed, select it and save to use it.", theme.Name), "success")
		}
	}
	http.Redirect(w, r, "/settings/open-panel#display", http.StatusSeeOther)
}

// installTheme downloads t.Download and unpacks it into dir/t.Folder, nothing is left behind on failure
func installTheme(ctx context.Context, t RemoteTheme, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, themeDownloadLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Download, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("download failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, themeDownloadMax+1))
	if err != nil {
		return errors.New("download failed")
	}
	if len(data) > themeDownloadMax {
		return errors.New("archive is larger than 50 MB")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := unpackTheme(data, tmp); err != nil {
		return err
	}
	root, err := themeRoot(tmp)
	if err != nil {
		return err
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, t.Folder)
	if _, err := os.Stat(dest); err == nil {
		return errors.New("folder already exists")
	}
	return os.Rename(root, dest)
}

// themeRoot is where readme.txt sits: the archive root, or its only top folder
func themeRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "readme.txt")); err == nil {
		return dir, nil
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 1 && entries[0].IsDir() {
		sub := filepath.Join(dir, entries[0].Name())
		if _, err := os.Stat(filepath.Join(sub, "readme.txt")); err == nil {
			return sub, nil
		}
	}
	return "", errors.New("archive has no readme.txt")
}

// unpackTheme extracts a zip or tar.gz into dest, only plain files and folders, nothing outside dest
func unpackTheme(data []byte, dest string) error {
	var total int64
	write := func(name string, isDir bool, size int64, open func() (io.Reader, func(), error)) error {
		clean := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))[1:]
		if clean == "" || clean == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if isDir {
			return os.MkdirAll(target, 0o755)
		}
		total += size
		if total > themeUnpackedMax {
			return errors.New("theme is larger than 200 MB unpacked")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rd, done, err := open()
		if err != nil {
			return err
		}
		defer done()
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(rd, size+1))
		// a failed close can mean the data never hit the disk
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil && n > size {
			err = errors.New("archive entry is larger than it says")
		}
		return err
	}

	if bytes.HasPrefix(data, []byte("PK")) {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return errors.New("not a valid zip")
		}
		for _, f := range zr.File {
			mode := f.Mode()
			if !mode.IsRegular() && !mode.IsDir() {
				continue
			}
			f := f
			err := write(f.Name, mode.IsDir(), int64(f.UncompressedSize64), func() (io.Reader, func(), error) {
				rc, err := f.Open()
				if err != nil {
					return nil, nil, err
				}
				return rc, func() { _ = rc.Close() }, nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return errors.New("archive must be .tar.gz or .zip")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("not a valid tar.gz")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			continue
		}
		if err := write(h.Name, h.Typeflag == tar.TypeDir, h.Size, func() (io.Reader, func(), error) {
			return tr, func() {}, nil
		}); err != nil {
			return err
		}
	}
}

// PanelUserThemeGlob finds the theme files users saved when allow_user_theme=yes, /home/<context>/theme holds a folder name
var PanelUserThemeGlob = "/home/*/theme"

// themeUpdates maps an installed theme folder to the newer version themes.yml lists for it
func themeUpdates(ctx context.Context, installed []PanelTheme) map[string]string {
	out := map[string]string{}
	have := map[string]string{}
	for _, t := range installed {
		have[t.Folder] = t.Version
	}
	for _, t := range loadThemesList(ctx) {
		if v, ok := have[t.Folder]; ok && newerVersion(t.Version, v) {
			out[t.Folder] = t.Version
		}
	}
	return out
}

// newerVersion compares dotted numbers like 1.10.0 > 1.9.2, anything that isn't a number counts as 0
func newerVersion(remote, local string) bool {
	r, l := strings.Split(strings.TrimPrefix(strings.TrimSpace(remote), "v"), "."), strings.Split(strings.TrimPrefix(strings.TrimSpace(local), "v"), ".")
	for i := 0; i < len(r) || i < len(l); i++ {
		var a, b int
		if i < len(r) {
			a, _ = strconv.Atoi(r[i])
		}
		if i < len(l) {
			b, _ = strconv.Atoi(l[i])
		}
		if a != b {
			return a > b
		}
	}
	return false
}

// ServeUninstallTheme handles POST /settings/open-panel/themes/uninstall, deletes the theme folder,
// users who picked it go back to the server theme and if it was the server theme that goes back to Default
func (o *OpenpanelSettings) ServeUninstallTheme(w http.ResponseWriter, r *http.Request) {
	folder := r.PostFormValue("theme")
	t, ok := readPanelTheme(folder)
	if !ok || !validPanelThemeFolder(folder) {
		_ = auth.AddFlash(w, r, o.Sessions, "Error: theme not found.", "error")
		http.Redirect(w, r, "/settings/open-panel#display", http.StatusSeeOther)
		return
	}
	if err := os.RemoveAll(filepath.Join(PanelThemesDir, folder)); err != nil {
		_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Error removing theme %s: %v", t.Name, err), "error")
		http.Redirect(w, r, "/settings/open-panel#display", http.StatusSeeOther)
		return
	}

	users := 0
	files, _ := filepath.Glob(PanelUserThemeGlob)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(b)) == folder && os.Remove(f) == nil {
			users++
		}
	}

	msg := fmt.Sprintf("Theme %s uninstalled.", t.Name)
	cfg := config.Load(OpenpanelSettingsConfigPath)
	if cfg.Get("PANEL", "template", "") == folder {
		cfg.Set("PANEL", "template", "")
		if err := config.Save(OpenpanelSettingsConfigPath, cfg); err != nil {
			_ = auth.AddFlash(w, r, o.Sessions, "Error: the theme was removed but template= still points at it, select Default and save.", "error")
		} else {
			msg += " It was the server theme, OpenPanel now uses Default."
		}
	}
	if users > 0 {
		msg += fmt.Sprintf(" %d user(s) who picked it now use the server theme.", users)
	}
	_ = os.WriteFile(OpenpanelSettingsRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0o644)
	_ = auth.AddFlash(w, r, o.Sessions, msg, "success")
	http.Redirect(w, r, "/settings/open-panel#display", http.StatusSeeOther)
}

// ServeUpdateTheme handles POST /settings/open-panel/themes/update, swaps the folder for the version themes.yml lists
func (o *OpenpanelSettings) ServeUpdateTheme(w http.ResponseWriter, r *http.Request) {
	folder := r.PostFormValue("theme")
	defer http.Redirect(w, r, "/settings/open-panel#display", http.StatusSeeOther)
	installed, ok := readPanelTheme(folder)
	if !ok || !validPanelThemeFolder(folder) {
		_ = auth.AddFlash(w, r, o.Sessions, "Error: theme not found.", "error")
		return
	}
	var remote *RemoteTheme
	for _, t := range loadThemesList(r.Context()) {
		if t.Folder == folder {
			remote = &t
			break
		}
	}
	if remote == nil || !newerVersion(remote.Version, installed.Version) {
		_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Theme %s is up to date.", installed.Name), "info")
		return
	}
	if err := updateTheme(r.Context(), *remote, PanelThemesDir); err != nil {
		log.Printf("themes: updating %s from %s: %v", folder, remote.Download, err)
		_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Error updating theme %s: %v", installed.Name, err), "error")
		return
	}
	reapplyThemeSettings(folder)
	_ = os.WriteFile(OpenpanelSettingsRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0o644)
	_ = auth.AddFlash(w, r, o.Sessions, fmt.Sprintf("Theme %s updated to %s.", installed.Name, remote.Version), "success")
}

// updateTheme unpacks the new version next to the old one and swaps them, the old folder stays if anything fails
func updateTheme(ctx context.Context, t RemoteTheme, dir string) error {
	staged := t
	staged.Folder = ".update-" + t.Folder
	_ = os.RemoveAll(filepath.Join(dir, staged.Folder))
	if err := installTheme(ctx, staged, dir); err != nil {
		return err
	}
	cur, next := filepath.Join(dir, t.Folder), filepath.Join(dir, staged.Folder)
	old := filepath.Join(dir, ".old-"+t.Folder)
	_ = os.RemoveAll(old)
	if err := os.Rename(cur, old); err != nil {
		_ = os.RemoveAll(next)
		return err
	}
	if err := os.Rename(next, cur); err != nil {
		_ = os.Rename(old, cur)
		_ = os.RemoveAll(next)
		return err
	}
	return os.RemoveAll(old)
}
