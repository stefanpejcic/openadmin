// Package static embeds this application's static web assets
// (CSS/JS/images/robots.txt/security.txt) into the binary via go:embed,
// for the same single-static-binary deployment story as
// internal/webtemplates. Paths in Files are rooted at this directory, e.g.
// "dist/output.css", not "static/dist/output.css".
package static

import "embed"

//go:embed dist images pages src robots.txt security.txt
var Files embed.FS
