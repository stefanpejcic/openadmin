package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLastLogin(t *testing.T, dir, user string, lines ...string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, user), 0755)
	os.WriteFile(filepath.Join(dir, user, ".lastlogin"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func TestUserLoginCountries(t *testing.T) {
	dir := t.TempDir()
	writeLastLogin(t, dir, "ana", "IP: 109.245.161.6 - Country: RS - Login Time: 2026-10-10 18:15:57")
	// newest login is the server's own autologin, so the one before it counts
	writeLastLogin(t, dir, "bob",
		"IP: 82.64.1.2 - Country: FR - Login Time: 2026-10-09 10:00:00",
		"IP: 161.97.157.83 - Country: FR - Login Time: 2026-10-10 10:00:00",
		"IP: 109.245.165.249 - Country: rs - Login Time: 2026-10-11 10:00:00",
		"IP: 161.97.157.83 - Country: FR - Login Time: 2026-10-11 11:00:00",
	)
	writeLastLogin(t, dir, "cid", "IP: 161.97.157.83 - Country: FR - Login Time: 2026-10-11 11:00:00")
	writeLastLogin(t, dir, "dan", "IP: 192.168.1.5 - Country: DE - Login Time: 2026-10-11 11:00:00")
	writeLastLogin(t, dir, "eve", "garbage")
	os.MkdirAll(filepath.Join(dir, "fay"), 0755)

	got := userLoginCountries(dir, "161.97.157.83")
	if len(got) != 2 || got["ana"] != "RS" || got["bob"] != "RS" {
		t.Fatalf("userLoginCountries = %v", got)
	}
}

func TestSuggestLocales(t *testing.T) {
	rows := []localeRow{
		{Locale: "sr-rs"},
		{Locale: "fr-fr"},
		{Locale: "de-de", Installed: true},
		{Locale: "en-us", Installed: true},
		{Locale: "pt-br"},
	}
	got := suggestLocales(rows, map[string]string{"ana": "RS", "bob": "RS", "cid": "FR", "dan": "DE", "eve": "AT"})
	if len(got) != 2 {
		t.Fatalf("suggestLocales = %+v", got)
	}
	if got[0].Locale != "sr-rs" || strings.Join(got[0].Users, ",") != "ana,bob" || got[1].Locale != "fr-fr" || got[1].Country != "FR" {
		t.Fatalf("expected sr-rs (2 users) then fr-fr, got %+v", got)
	}
}

func TestServeLocalesShowsSuggestions(t *testing.T) {
	withScratchLocalesPaths(t)
	withScratchLocalesFetch(t, []githubContentItem{{Name: "sr-rs", Type: "dir"}, {Name: "fr-fr", Type: "dir"}}, http.StatusOK, nil)
	orig := LocalesUsersDir
	LocalesUsersDir = t.TempDir()
	t.Cleanup(func() { LocalesUsersDir = orig })
	writeLastLogin(t, LocalesUsersDir, "ana", "IP: 109.245.161.6 - Country: RS - Login Time: 2026-10-10 18:15:57")
	writeLastLogin(t, LocalesUsersDir, "bob", "IP: 109.245.165.249 - Country: RS - Login Time: 2026-10-10 18:15:57")
	writeLastLogin(t, LocalesUsersDir, "cid", "IP: 82.64.1.2 - Country: FR - Login Time: 2026-10-10 18:15:57")

	srv, client := newLocalesTestServer(t, &Locales{})
	resp, err := client.Get(srv.URL + "/settings/locales")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{
		"<b>2 users</b> last logged in from RS. Recommended: install <b>sr-rs</b>",
		"(ana, bob)",
		"User <b>cid</b> last logged in from FR",
		`name="locale" value="fr-fr"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in the page", want)
		}
	}
}
