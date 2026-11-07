package webtemplates

import (
	"net/url"
	"strings"
	"testing"
)

func TestSortHeaderKeepsSearchAndTab(t *testing.T) {
	var b strings.Builder
	err := templates.ExecuteTemplate(&b, "sort_header", map[string]interface{}{
		"Label": "Date", "Key": "date", "Base": "/x", "SortCol": "", "SortDirection": "",
		"Extra": url.Values{"q": {"a b&c"}}, "Hash": "runs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `href="/x?sort=date&direction=asc&q=a%20b%26c#runs"`) {
		t.Fatalf("unexpected links: %s", b.String())
	}
}

func TestSortHeaderFlipsSortedColumn(t *testing.T) {
	var b strings.Builder
	err := templates.ExecuteTemplate(&b, "sort_header", map[string]interface{}{
		"Label": "Date", "Key": "date", "Base": "/x", "SortCol": "date", "SortDirection": "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `href="/x?sort=date&direction=desc"`) || !strings.Contains(out, "sorted ascending") {
		t.Fatalf("sorted column should link to desc and say so: %s", out)
	}
}
