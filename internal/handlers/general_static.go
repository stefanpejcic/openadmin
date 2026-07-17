// Serves a small allowlist of top-level static/config files (robots.txt,
// security.txt, shortcuts.json).
package handlers

import (
	"net/http"
	"path/filepath"
)

// GeneralStaticFiles / GeneralConfigFiles / GeneralConfigDir are the
// allowlisted top-level filenames this handler will serve.
var (
	GeneralStaticFiles = map[string]bool{"robots.txt": true, "security.txt": true}
	GeneralConfigFiles = map[string]bool{"shortcuts.json": true}
	GeneralConfigDir   = "/etc/openpanel/openadmin/config"
)

// GeneralStatic bundles the top-level "/{filename}" handler. No auth
// wrapper: these files are meant to be publicly readable.
type GeneralStatic struct {
	StaticDir string
}

// ServeFile handles GET /{filename}.
func (g *GeneralStatic) ServeFile(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")

	switch {
	case GeneralStaticFiles[filename]:
		http.ServeFile(w, r, filepath.Join(g.StaticDir, filename))
	case GeneralConfigFiles[filename]:
		http.ServeFile(w, r, filepath.Join(GeneralConfigDir, filename))
	default:
		http.NotFound(w, r)
	}
}
