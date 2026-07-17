package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchGlobalEnvPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := GlobalEnvPath
	GlobalEnvPath = filepath.Join(dir, ".env")
	t.Cleanup(func() { GlobalEnvPath = orig })
	return GlobalEnvPath
}

func newLimitsTestServer(t *testing.T, l *Limits) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	l.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/limits", l.ServeLimits)
	mux.HandleFunc("POST /services/limits", l.ServeLimits)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

func TestReadEnvGroupsMissingFile(t *testing.T) {
	withScratchGlobalEnvPath(t) // file not written -> missing

	if got := readEnvGroups(); got != nil {
		t.Fatalf("expected nil for missing file, got %+v", got)
	}
}

func TestReadEnvGroupsGroupsAndExcludes(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	content := `# a comment
VERSION="1.2.3"
PORT="8080"
PROXY_HTTP_PORT="2087"
CADDY_PORT="443"
MYSQL_PW="secret"
MYSQL_PASSWORD="secret"
MYSQL_USER="root"
NGINX_CPU="1.5"
NGINX_RAM="1G"
VARNISH="1"

not a valid line
`
	os.WriteFile(path, []byte(content), 0644)

	groups := readEnvGroups()
	if groups == nil {
		t.Fatal("expected non-nil groups")
	}
	if _, ok := groups["VERSION"]; ok {
		t.Fatal("VERSION should be excluded")
	}
	if _, ok := groups["PORT"]; ok {
		t.Fatal("PORT should be excluded")
	}
	if _, ok := groups["CADDY"]; ok {
		t.Fatal("CADDY_PORT should be excluded (not PROXY_HTTP_PORT)")
	}
	if v := groups["PROXY"]["HTTP_PORT"]; v != "2087" {
		t.Fatalf("PROXY_HTTP_PORT should NOT be excluded, got groups[PROXY]=%+v", groups["PROXY"])
	}
	if _, ok := groups["MYSQL"]; ok {
		t.Fatal("MYSQL_PW/PASSWORD/USER should all be excluded, leaving no MYSQL group")
	}
	if v := groups["NGINX"]["CPU"]; v != "1.5" {
		t.Fatalf("expected NGINX.CPU=1.5, got %+v", groups["NGINX"])
	}
	if v := groups["NGINX"]["RAM"]; v != "1G" {
		t.Fatalf("expected NGINX.RAM=1G, got %+v", groups["NGINX"])
	}
	if v, ok := groups["VARNISH"][""]; !ok || v != "1" {
		t.Fatalf("expected a no-underscore key to group under empty suffix, got %+v", groups["VARNISH"])
	}
}

func TestBuildLimitsViewFiltersSortsAndFieldNameQuirk(t *testing.T) {
	groups := map[string]map[string]string{
		"DEFAULTS": {"foo": "bar"},
		"PHP_FPM":  {"version": "8.2"},
		"NGINX":    {"RAM": "1G", "CPU": "1.5"},
		"VARNISH":  {"": "1"},
	}

	view := buildLimitsView(groups)

	for _, g := range view {
		if g.Name == "DEFAULTS" || g.Name == "PHP_FPM" {
			t.Fatalf("expected DEFAULTS/PHP_FPM to be filtered out, got group %q", g.Name)
		}
	}
	if len(view) != 2 {
		t.Fatalf("expected 2 groups (NGINX, VARNISH), got %d: %+v", len(view), view)
	}
	// sort.Strings orders "NGINX" before "VARNISH".
	if view[0].Name != "NGINX" || view[1].Name != "VARNISH" {
		t.Fatalf("expected sorted [NGINX, VARNISH], got [%s, %s]", view[0].Name, view[1].Name)
	}
	// Within NGINX, subkeys "CPU" < "RAM".
	if view[0].Rows[0].FieldName != "NGINX_CPU" || view[0].Rows[1].FieldName != "NGINX_RAM" {
		t.Fatalf("unexpected row order/names: %+v", view[0].Rows)
	}
	// VARNISH has empty suffix -> trailing-underscore quirk preserved.
	if view[1].Rows[0].FieldName != "VARNISH_" {
		t.Fatalf("expected trailing-underscore quirk field name VARNISH_, got %q", view[1].Rows[0].FieldName)
	}
}

func TestServeLimitsGetHTML(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	os.WriteFile(path, []byte("NGINX_CPU=\"1.5G\"\nNGINX_RAM=\"512M\"\nNGINX_ENABLED=\"true\"\n"), 0644)

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/limits")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{
		`name="NGINX_CPU"`, `value="1.5"`,
		`name="NGINX_RAM"`, `value="512M"`,
		`name="NGINX_ENABLED"`, `<select`,
		"Service Limits", "</html>",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeLimitsGetJSON(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	os.WriteFile(path, []byte("NGINX_CPU=\"1.5\"\n"), 0644)

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.Get(srv.URL + "/services/limits?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"CPU":"1.5"`) {
		t.Fatalf("expected raw JSON with NGINX.CPU, got %s", truncate(string(body)))
	}
}

func TestServeLimitsPostRewritesLines(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	original := "# header comment\nNGINX_CPU=\"1.0\"\nNGINX_RAM=\"512M\"\nUNTOUCHED_KEY=\"keep-me\"\n"
	os.WriteFile(path, []byte(original), 0644)

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.PostForm(srv.URL+"/services/limits", url.Values{
		"NGINX_CPU": {"2.5"},
		"NGINX_RAM": {"1024"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "New limits saved successfully!") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(saved)
	if !strings.Contains(got, `NGINX_CPU="2.5"`) {
		t.Fatalf("expected NGINX_CPU updated to quoted 2.5, got %q", got)
	}
	if !strings.Contains(got, `NGINX_RAM="1024G"`) {
		t.Fatalf("expected _RAM suffix to append G, got %q", got)
	}
	if !strings.Contains(got, `UNTOUCHED_KEY="keep-me"`) {
		t.Fatalf("expected untouched line preserved verbatim, got %q", got)
	}
	if !strings.Contains(got, "# header comment\n") {
		t.Fatalf("expected comment line preserved verbatim, got %q", got)
	}
}

func TestServeLimitsPostRAMZeroNotSuffixed(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	os.WriteFile(path, []byte("NGINX_RAM=\"512M\"\n"), 0644)

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.PostForm(srv.URL+"/services/limits", url.Values{"NGINX_RAM": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), `NGINX_RAM="0"`) {
		t.Fatalf(`expected "0" to be left unsuffixed (no trailing G), got %q`, saved)
	}
}

func TestServeLimitsPostMissingFile(t *testing.T) {
	withScratchGlobalEnvPath(t) // no file written

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.PostForm(srv.URL+"/services/limits", url.Values{"NGINX_CPU": {"2.5"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Environment file not found.") {
		t.Fatalf("expected missing-file flash, got %s", truncate(string(body)))
	}
}

func TestServeLimitsPostWriteFailure(t *testing.T) {
	dir := t.TempDir()
	orig := GlobalEnvPath
	GlobalEnvPath = filepath.Join(dir, ".env")
	t.Cleanup(func() { GlobalEnvPath = orig })
	os.WriteFile(GlobalEnvPath, []byte("NGINX_CPU=\"1.0\"\n"), 0644)

	// Directory permissions don't block rewriting an existing file the
	// owner can already write to -- the file itself must be read-only.
	if err := os.Chmod(GlobalEnvPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(GlobalEnvPath, 0644) })

	l := &Limits{}
	srv, client := newLimitsTestServer(t, l)

	resp, err := client.PostForm(srv.URL+"/services/limits", url.Values{"NGINX_CPU": {"2.5"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Failed to update limits:") {
		t.Fatalf("expected write-failure flash, got %s", truncate(string(body)))
	}
}
