package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newSearchTestServer(t *testing.T, s *Search, role string) (*httptest.Server, *http.Client) {
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
	db.CreateUser("caller", hash, role)
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	s.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /search/pages", s.ServeSearchFilter)
	mux.HandleFunc("GET /search/websites", s.ServeSearchWebsites)
	mux.HandleFunc("GET /search/websites/{site_name}", s.ServeSearchWebsites)
	mux.HandleFunc("GET /search/users", s.ServeSearchUsers)
	mux.HandleFunc("GET /search/users/{username}", s.ServeSearchUsers)
	mux.HandleFunc("GET /domains/{domain_name...}", s.ServeDomainOwner)
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

func TestServeSearchFilterMissingFileReturns404EmptyArray(t *testing.T) {
	dir := t.TempDir()
	s := &Search{JSONFilePath: filepath.Join(dir, "does-not-exist.json")}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/search/pages")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected empty array body, got %s", truncate(string(body)))
	}
}

func TestServeSearchFilterReturnsCappedResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filter.json")
	var items []string
	for i := 0; i < 150; i++ {
		items = append(items, `{"title":"page"}`)
	}
	os.WriteFile(path, []byte("["+strings.Join(items, ",")+"]"), 0644)

	s := &Search{JSONFilePath: path}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/search/pages")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if strings.Count(string(body), `"title"`) != 100 {
		t.Fatalf("expected results capped at 100, got %d occurrences", strings.Count(string(body), `"title"`))
	}
}

func TestServeSearchFilterMalformedJSONReturns500(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filter.json")
	os.WriteFile(path, []byte("not json"), 0644)

	s := &Search{JSONFilePath: path}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/search/pages")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Error loading JSON data") {
		t.Fatalf("expected the loading-error message, got %s", truncate(string(body)))
	}
}

func TestServeDomainOwnerFound(t *testing.T) {
	origRun := searchDomainWhoOwnsRun
	searchDomainWhoOwnsRun = func(domain string) (string, error) {
		return "Domain: " + domain + " is owned by: alice\n", nil
	}
	t.Cleanup(func() { searchDomainWhoOwnsRun = origRun })

	s := &Search{}
	srv, client := newSearchTestServer(t, s, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/domains/example.com?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), `"username":"alice"`) {
		t.Fatalf("expected username alice, got %s", truncate(string(body)))
	}
}

func TestServeDomainOwnerTruncatesAtFirstSlash(t *testing.T) {
	origRun := searchDomainWhoOwnsRun
	var capturedDomain string
	searchDomainWhoOwnsRun = func(domain string) (string, error) {
		capturedDomain = domain
		return "owned by: bob\n", nil
	}
	t.Cleanup(func() { searchDomainWhoOwnsRun = origRun })

	s := &Search{}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/domains/example.com/extra/path?output=json")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if capturedDomain != "example.com" {
		t.Fatalf("expected domain truncated at first slash, got %q", capturedDomain)
	}
}

func TestServeDomainOwnerNotFound(t *testing.T) {
	origRun := searchDomainWhoOwnsRun
	searchDomainWhoOwnsRun = func(domain string) (string, error) {
		return "Domain " + domain + " not found in the database\n", nil
	}
	t.Cleanup(func() { searchDomainWhoOwnsRun = origRun })

	s := &Search{}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/domains/missing.com?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	if !strings.Contains(string(body), "Domain not found") {
		t.Fatalf("expected domain-not-found error, got %s", truncate(string(body)))
	}
}

func TestServeDomainOwnerNotFoundRendersHTML(t *testing.T) {
	origRun := searchDomainWhoOwnsRun
	searchDomainWhoOwnsRun = func(domain string) (string, error) {
		return "Domain " + domain + " not found in the database\n", nil
	}
	t.Cleanup(func() { searchDomainWhoOwnsRun = origRun })

	s := &Search{}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/domains/missing.com")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	for _, want := range []string{"Domain not found", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeDomainOwnerRedirectsWithoutJSON(t *testing.T) {
	origRun := searchDomainWhoOwnsRun
	searchDomainWhoOwnsRun = func(domain string) (string, error) {
		return "owned by: carol\n", nil
	}
	t.Cleanup(func() { searchDomainWhoOwnsRun = origRun })

	s := &Search{}
	srv, client := newSearchTestServer(t, s, "admin")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(srv.URL + "/domains/example.com")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/users/carol#nav-user-data") {
		t.Fatalf("expected redirect to carol's user page, got %q", loc)
	}
}

func TestServeSearchWebsitesReturnsNestedArrays(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT site_name FROM sites`)).
		WillReturnRows(sqlmock.NewRows([]string{"site_name"}).AddRow("a.com").AddRow("b.com"))

	s := &Search{MySQL: mysqlDB}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/search/websites")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != `[["a.com"],["b.com"]]` {
		t.Fatalf("expected nested-array JSON shape, got %s", truncate(string(body)))
	}
}

func TestServeSearchUsersReturnsFlatArray(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT username FROM users`)).
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("alice").AddRow("bob"))

	s := &Search{MySQL: mysqlDB}
	srv, client := newSearchTestServer(t, s, "admin")

	resp, err := client.Get(srv.URL + "/search/users")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != `["alice","bob"]` {
		t.Fatalf("expected flat-array JSON shape, got %s", truncate(string(body)))
	}
}

func TestServeSearchUsersRestrictsResellerScope(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT username FROM users WHERE owner = ?`)).
		WithArgs("caller").
		WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("client1"))

	s := &Search{MySQL: mysqlDB}
	srv, client := newSearchTestServer(t, s, "reseller")

	resp, err := client.Get(srv.URL + "/search/users")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != `["client1"]` {
		t.Fatalf("expected reseller-scoped result, got %s", truncate(string(body)))
	}
}
