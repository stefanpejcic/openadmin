// Package search embeds filter.json (the default command-palette search index) into the binary so /search/pages works even without the external files deployed
package search

import _ "embed"

//go:embed filter.json
var DefaultFilterJSON []byte
