package apidocs

import (
	"strings"
	"testing"
)

func TestTextListsEveryEndpoint(t *testing.T) {
	text, err := Text("https://server.example.com:2087/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Authentication\n",
		"Endpoint: GET /api/whoami\n",
		"curl -X GET 'https://server.example.com:2087/api/whoami' -H \"Authorization: Bearer TOKEN_HERE\"",
		"curl -X POST 'https://server.example.com:2087/api/' -H \"Content-Type: application/json\" -d '{\"username\":\"admin\",\"password\":\"secret\"}'",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(text, "Endpoint: "); n < 100 {
		t.Errorf("expected the whole catalogue, got %d endpoints", n)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got := shellQuote(`{"a":"it's"}`); got != `'{"a":"it'\''s"}'` {
		t.Errorf("got %s", got)
	}
}
