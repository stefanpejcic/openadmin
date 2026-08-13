// Package search embeds this directory's filter.json (the default
// command-palette search index) into the binary via go:embed, so
// /search/pages works out of the box even on a server where the external
// /usr/local/admin/core/search/ files were never deployed alongside the
// binary.
package search

import _ "embed"

//go:embed filter.json
var DefaultFilterJSON []byte
