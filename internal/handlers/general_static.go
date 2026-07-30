// Serves a small allowlist of top-level static/config/admin-override files
// (robots.txt, security.txt, shortcuts.json, custom.css).
package handlers

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// GeneralStaticFiles are served from GeneralOverrideDir if the admin has
// dropped a replacement there, falling back to the bundled default in
// StaticDir otherwise. GeneralConfigFiles are served from GeneralConfigDir.
// GeneralAdminOnlyFiles have no bundled default -- they're served only when
// present in GeneralOverrideDir.
var (
	GeneralStaticFiles    = map[string]bool{"robots.txt": true, "security.txt": true}
	GeneralConfigFiles    = map[string]bool{"shortcuts.json": true}
	GeneralAdminOnlyFiles = map[string]bool{"custom.css": true}

	GeneralConfigDir   = "/etc/openpanel/openadmin/config"
	GeneralOverrideDir = "/usr/local/admin"
)

// GeneralStatic bundles the top-level "/{filename}" handler. No auth
// wrapper: these files are meant to be publicly readable.
type GeneralStatic struct {
	// Static holds the bundled defaults for GeneralStaticFiles (the
	// embedded staticassets.Files in production; an fstest.MapFS in tests).
	Static fs.FS
}

// ServeFile handles GET /{filename}.
func (g *GeneralStatic) ServeFile(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")

	switch {
	case GeneralStaticFiles[filename]:
		if overridePath := filepath.Join(GeneralOverrideDir, filename); isRegularFile(overridePath) {
			http.ServeFile(w, r, overridePath)
			return
		}
		http.ServeFileFS(w, r, g.Static, filename)
	case GeneralConfigFiles[filename]:
		http.ServeFile(w, r, filepath.Join(GeneralConfigDir, filename))
	case GeneralAdminOnlyFiles[filename]:
		path := filepath.Join(GeneralOverrideDir, filename)
		if !isRegularFile(path) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

// isRegularFile reports whether path exists and is a regular file, not a
// directory.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
