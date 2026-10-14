package handlers

import (
	"compress/gzip"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newWebsitesTestServer(t *testing.T, ws *Websites) (*httptest.Server, *http.Client) {
	t.Helper()
	return newAdminTestServer(t, func(mux *http.ServeMux, sessions *auth.Manager) {
		ws.Sessions = sessions
		mux.HandleFunc("GET /websites", ws.ServeList)
		mux.HandleFunc("GET /websites/login/{id}", ws.ServeLogin)
		mux.HandleFunc("GET /websites/vulnerabilities/{id}", ws.ServeVulnerabilities)
		mux.HandleFunc("POST /websites/scan", ws.HandleScan)
		mux.HandleFunc("POST /websites/detach", ws.HandleDetach)
		mux.HandleFunc("POST /websites/git/{action}", ws.HandleGit)
		mux.HandleFunc("POST /websites/{check}", ws.HandleCheck)
	})
}

// newAdminTestServer serves the routes setup registers with an admin already logged in, redirects aren't followed
func newAdminTestServer(t *testing.T, setup func(mux *http.ServeMux, sessions *auth.Manager)) (*httptest.Server, *http.Client) {
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
	mux := http.NewServeMux()
	setup(mux, sessions)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	srv := httptest.NewServer(auth.WithUserLoader(sessions, db)(mux))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

func withWebsiteReportDirs(t *testing.T) (speed, vuln string) {
	t.Helper()
	speed, vuln = t.TempDir(), t.TempDir()
	origS, origV := WebsitesPageSpeedDir, WebsitesVulnerabilityDir
	WebsitesPageSpeedDir, WebsitesVulnerabilityDir = speed, vuln
	t.Cleanup(func() { WebsitesPageSpeedDir, WebsitesVulnerabilityDir = origS, origV })
	return speed, vuln
}

func stubWebsitesStart(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := websitesStart
	websitesStart = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { websitesStart = orig })
	return &calls
}

var websitesCols = []string{"id", "site_name", "type", "version", "admin_email", "created_date", "domain_url", "docroot", "username", "server"}

func stubLatestWordPress(t *testing.T, version string) *int {
	t.Helper()
	calls := 0
	orig := wpLatestFetch
	wpLatestFetch = func() (string, error) {
		calls++
		return version, nil
	}
	wpLatestVersion, wpLatestUntil = "", time.Time{}
	t.Cleanup(func() {
		wpLatestFetch = orig
		wpLatestVersion, wpLatestUntil = "", time.Time{}
	})
	return &calls
}

func TestWebsitesListShowsReports(t *testing.T) {
	fetches := stubLatestWordPress(t, "6.8.1")
	speed, vuln := withWebsiteReportDirs(t)
	os.WriteFile(filepath.Join(speed, "a.com.json"), []byte(`{"timestamp":"2026-10-01 10:00:00","desktop_speed":{"performance_score":0.97},"mobile_speed":{"performance_score":0.42}}`), 0644)
	os.WriteFile(filepath.Join(vuln, "b.com_blog.json"), []byte(`{"timestamp":"2026-10-11T03:00:17Z",
		"plugin_foo":{"data":{"name":"Foo Slider","link":"https://wordpress.org/plugins/foo/","vulnerability":[{"name":"Foo < 1.2 XSS","operator":{"max_version":"1.2","max_operator":"lt"},"source":[{"name":"CVE-2026-1","link":"https://www.cve.org/x"}]}]}},
		"theme_bar":{"data":{"name":"Bar","vulnerability":[{"name":"Bar <= 2.0 CSRF","operator":{"max_version":"2.0","max_operator":"lte"},"source":[]}]}},
		"plugin_clean":{"error":0,"data":{"name":"Clean","vulnerability":[]}}}`), 0644)

	mock := func() *Websites {
		db, m, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols).
			AddRow(1, "a.com", "nodejs", "20", "", "2026-01-01T08:30:00Z", "a.com", "/var/www/html/a.com", "alice", "alice").
			AddRow(2, "b.com/blog", "WordPress", "6.6", "admin@b.com", "2026-02-01T10:00:00Z", "b.com", "/var/www/html/b.com", "bob", "bobctx"))
		return &Websites{MySQL: db}
	}

	srv, client := newWebsitesTestServer(t, mock())
	resp, err := client.Get(srv.URL + "/websites?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`"desktop":{"score":97`, `"mobile":{"score":42`, `"created_date":"2026-01-01 08:30:00"`, `"total":2`, `"docroot":"/var/www/html/b.com/blog"`, `"host_docroot":"/home/bobctx/files/b.com/blog"`, `"owner":"alice"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("json missing %s: %s", want, body)
		}
	}

	srv, client = newWebsitesTestServer(t, mock())
	resp, err = client.Get(srv.URL + "/websites")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	page := string(body)
	if !strings.Contains(page, `href="http://b.com/blog"`) || !strings.Contains(page, `href="http://a.com"`) {
		t.Error("expected every site listed")
	}
	if !strings.Contains(page, `src="https://www.google.com/s2/favicons?domain=b.com/blog"`) {
		t.Error("expected the site favicon")
	}
	if !strings.Contains(page, `title="2026-02-01 10:00:00"`) || !strings.Contains(page, " months ago</span>") {
		t.Error("expected a relative created date with the original on hover")
	}
	if !strings.Contains(page, `href="/websites/login/2"`) || strings.Contains(page, `href="/websites/login/1"`) {
		t.Error("expected the wp-admin login on the wordpress site only")
	}
	if *fetches != 1 {
		t.Errorf("latest wordpress fetched %d times, want once", *fetches)
	}
	if strings.Count(page, "6.8.1 available") != 1 {
		t.Error("expected the update badge on the wordpress site only")
	}
	if !strings.Contains(page, "2 in 2 components") || strings.Count(page, `<symbol id="vuln-src-`) != 4 {
		t.Error("expected the vulnerability count and the logo sprite once")
	}
	if strings.Contains(page, "Foo Slider") {
		t.Error("report details should load on open, not with the page")
	}

	db, m, _ := sqlmock.New()
	m.ExpectQuery("SELECT id, site_name, type FROM sites").WithArgs(2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "site_name", "type"}).AddRow(2, "b.com/blog", "WordPress"))
	srv, client = newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, err = client.Get(srv.URL + "/websites/vulnerabilities/2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	page = string(body)
	for _, want := range []string{"Foo Slider", "Fixed in 1.2", `href="https://www.cve.org/x" target="_blank" rel="noopener" title="CVE-2026-1"`, `<use href="#vuln-src-3">`} {
		if !strings.Contains(page, want) {
			t.Errorf("popup missing %q", want)
		}
	}
	if strings.Contains(page, "Fixed in 2.0") || strings.Contains(page, ">Clean<") {
		t.Error("lte versions aren't fixed and clean components shouldn't show")
	}
}

func TestWebsitesDetach(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("SELECT id, site_name, type FROM sites").WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"id", "site_name", "type"}).AddRow(7, "c.com", "wordpress"))
	m.ExpectExec("DELETE FROM sites WHERE id").WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 1))

	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, err := client.PostForm(srv.URL+"/websites/detach", url.Values{"id": {"7"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/websites" {
		t.Errorf("redirected to %q", loc)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestWebsitesChecksAndScan(t *testing.T) {
	calls := stubWebsitesStart(t)
	db, m, _ := sqlmock.New()
	defer db.Close()
	siteRows := func(typ string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "site_name", "type"}).AddRow(3, "d.com", typ)
	}
	m.ExpectQuery("SELECT id, site_name, type FROM sites").WillReturnRows(siteRows("wordpress"))
	m.ExpectQuery("SELECT id, site_name, type FROM sites").WillReturnRows(siteRows("nodejs"))
	m.ExpectQuery("SELECT 1 FROM users").WithArgs("alice").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	post := func(path string, form url.Values) {
		resp, err := client.PostForm(srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	post("/websites/vulnerability", url.Values{"id": {"3"}})
	post("/websites/vulnerability", url.Values{"id": {"3"}}) // not wordpress, nothing runs
	post("/websites/scan", url.Values{"username": {"alice"}})
	post("/websites/scan", nil)

	want := [][]string{{"websites-vulnerability", "d.com"}, {"websites-scan", "alice"}, {"websites-scan", "-all"}}
	if len(*calls) != len(want) {
		t.Fatalf("calls = %v", *calls)
	}
	for i := range want {
		if strings.Join((*calls)[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("call %d = %v, want %v", i, (*calls)[i], want[i])
		}
	}
}

func TestTimeSince(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	cases := map[string]string{
		"2026-10-12 11:59:30":  "just now",
		"2026-10-12 11:15:00":  "45 minutes ago",
		"2026-10-12 09:00:00":  "3 hours ago",
		"2026-10-12T09:00:00Z": "3 hours ago",
		"2026-10-11 11:00:00":  "1 day ago",
		"2026-10-02 12:00:00":  "10 days ago",
		"2026-01-01 12:00:00":  "9 months ago",
		"2023-01-01 12:00:00":  "3 years ago",
		"not a date":           "",
	}
	for in, want := range cases {
		if got := timeSince(in, now); got != want {
			t.Errorf("timeSince(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostDocroot(t *testing.T) {
	cases := [][3]string{
		{"bob", "/var/www/html/b.com", "/home/bob/files/b.com"},
		{"bob", "/var/www/html", "/home/bob/files"},
		{"bob", "/var/www/htmlx/b.com", "/var/www/htmlx/b.com"},
		{"", "/var/www/html/b.com", "/var/www/html/b.com"},
	}
	for _, c := range cases {
		if got := hostDocroot(c[0], c[1]); got != c[2] {
			t.Errorf("hostDocroot(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestWebsitesListGzip(t *testing.T) {
	stubLatestWordPress(t, "")
	withWebsiteReportDirs(t)
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols))
	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})

	client.Transport = &http.Transport{DisableCompression: true}
	req, _ := http.NewRequest("GET", srv.URL+"/websites", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("expected a gzipped page")
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if !strings.Contains(string(body), "No websites yet") {
		t.Error("unexpected body")
	}
}

func TestWebsitesGit(t *testing.T) {
	stubLatestWordPress(t, "6.8.1")
	withWebsiteReportDirs(t)
	home := t.TempDir()
	origHome, origCfg, origRun := WebsitesGitHome, ModulesConfigFilePath, websitesGitRun
	WebsitesGitHome = home
	ModulesConfigFilePath = filepath.Join(t.TempDir(), "openpanel.config")
	t.Cleanup(func() { WebsitesGitHome, ModulesConfigFilePath, websitesGitRun = origHome, origCfg, origRun })
	os.WriteFile(ModulesConfigFilePath, []byte(`enabled_modules="dns,git"`+"\n"), 0644)
	os.MkdirAll(filepath.Join(home, "bobctx", "git"), 0700)
	os.WriteFile(filepath.Join(home, "bobctx", "git", "repos.json"), []byte(`[
		{"name":"blog-theme","source":"local","branch":"main","target_dir":"/var/www/html/b.com/blog/wp-content/themes/t","deploy_mode":"automatic","last_deploy":"2026-10-14 10:00:00 UTC","last_deployed_commit":"abcdef1234","push_pass_hash":"secret-hash"},
		{"name":"blog-plugin","source":"remote","branch":"dev","target_dir":"/var/www/html/b.com/blog/wp-content/plugins/p","deploy_mode":"manual","last_error":"boom"},
		{"name":"other-site","source":"remote","branch":"main","target_dir":"/var/www/html/c.com","deploy_mode":"automatic"}
	]`), 0600)

	mock := func() *Websites {
		db, m, _ := sqlmock.New()
		t.Cleanup(func() { db.Close() })
		m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols).
			AddRow(1, "a.com", "nodejs", "20", "", "2026-01-01T08:30:00Z", "a.com", "/var/www/html/a.com", "alice", "alice").
			AddRow(2, "b.com/blog", "WordPress", "6.6", "admin@b.com", "2026-02-01T10:00:00Z", "b.com", "/var/www/html/b.com", "bob", "bobctx"))
		return &Websites{MySQL: db}
	}

	srv, client := newWebsitesTestServer(t, mock())
	resp, err := client.Get(srv.URL + "/websites")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, page)
	}
	for _, want := range []string{`x-show="columns.git"`, ">blog-theme<", "+1 more", "blog-plugin (dev)", `title="blog-plugin: boom"`, "deployed ", "<code>abcdef1</code>", `data-bulk-action="git_deploy"`} {
		if !strings.Contains(page, want) && !strings.Contains(page, html.EscapeString(want)) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "other-site") || strings.Contains(page, "secret-hash") {
		t.Error("only repos inside the site's docroot should show, and never secrets")
	}

	// the menu flags drive which Git items the row menu offers
	row := websiteRow{Git: []gitRepo{{Source: "local", Mode: "none"}}}
	if m := row.Menu(); m["gitPull"] != false || m["gitDeploy"] != false || m["git"] != 1 {
		t.Errorf("menu flags = %v", m)
	}
	if got := row.BulkSkip(); got != "vulnerability,git_pull,git_deploy" {
		t.Errorf("BulkSkip = %q", got)
	}

	var ran []string
	websitesGitRun = func(action, userContext, repo string) (string, error) {
		ran = append(ran, action+" "+userContext+" "+repo)
		return "ok", nil
	}
	db, m, _ := sqlmock.New()
	m.ExpectQuery("WHERE s.id = ").WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"id", "site_name", "docroot", "server"}).AddRow(2, "b.com/blog", "/var/www/html/b.com", "bobctx"))
	srv, client = newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, err = client.PostForm(srv.URL+"/websites/git/deploy", url.Values{"id": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Join(ran, "|") != "deploy bobctx blog-theme|deploy bobctx blog-plugin" {
		t.Errorf("ran %v", ran)
	}
}

func TestWebsitesImunifyNeverScanned(t *testing.T) {
	stubLatestWordPress(t, "6.8.1")
	withWebsiteReportDirs(t)
	origAvail := imunifyCommandAvailableRun
	imunifyCommandAvailableRun = func(string) bool { return true }
	setImunifyUsersCache(map[string]imunifyUser{
		"alice":  {User: "alice"},
		"bobctx": {User: "bobctx", ScanDate: time.Now().Add(-time.Hour).Unix(), ScanStatus: "done"},
	})
	t.Cleanup(func() { imunifyCommandAvailableRun = origAvail; setImunifyUsersCache(nil) })

	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols).
		AddRow(1, "a.com", "nodejs", "20", "", "2026-01-01T08:30:00Z", "a.com", "/var/www/html/a.com", "alice", "alice").
		AddRow(2, "b.com", "nodejs", "20", "", "2026-01-01T08:30:00Z", "b.com", "/var/www/html/b.com", "bob", "bobctx"))
	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, err := client.Get(srv.URL + "/websites")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if strings.Count(page, "</svg>None</span>") != 1 || !strings.Contains(page, "text-gray-400 dark:text-gray-500\" title=\"ImunifyAV hasn't scanned") {
		t.Error("a never scanned owner should show a gray None")
	}
	if strings.Count(page, "</svg>Clean</span>") != 1 {
		t.Error("only the scanned owner should show Clean")
	}
}
