package handlers

import "testing"

// the logo URL is written into every owned account's .env by a root script, see GHSA-q388-37qw-p96h
func TestValidResellerLogoURL(t *testing.T) {
	cases := map[string]bool{
		"":                                 true,
		"https://cdn.example.com/logo.png": true,
		"http://a.b/x?y=1&z=%20":           true,
		"x|;e touch /tmp/p #":              false,
		"https://a.com/$(id)":              false,
		`https://a.com/"x`:                 false,
		"https://a.com/a b":                false,
		"javascript:alert(1)":              false,
		"https://a.com/`id`":               false,
		"https://a.com/x'y":                false,
		"https://a.com/x\nB=1":             false,
	}
	for in, want := range cases {
		if got := validResellerLogoURL(in); got != want {
			t.Errorf("validResellerLogoURL(%q) = %v, want %v", in, got, want)
		}
	}
}
