package webtemplates

import (
	"errors"
	"html/template"
	"io"
	"strings"
	"testing"
)

// a broken attribute (quotes inside x-data) only shows up when html/template escapes the page, which serves an empty 200
func TestTemplatesEscapeCleanly(t *testing.T) {
	for _, tpl := range templates.Templates() {
		name := tpl.Name()
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		err := templates.ExecuteTemplate(io.Discard, name, map[string]interface{}{})
		var escErr *template.Error
		if errors.As(err, &escErr) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
