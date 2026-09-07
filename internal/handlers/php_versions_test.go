package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

var errPHPVersionsTestFailed = errors.New("simulated opencli failure")

func TestParseDefaultPHPVersionOutput(t *testing.T) {
	cases := []struct {
		name, username, output, want string
	}{
		{"success", "alice", "Default PHP version for user 'alice' is: 8.2", "8.2"},
		{"not found", "alice", "Default PHP version for user: 'alice' not found in the configuration file.", ""},
		{"missing config", "alice", "Configuration file for user 'alice' not found.", ""},
		{"wrong user prefix", "alice", "Default PHP version for user 'bob' is: 8.2", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDefaultPHPVersionOutput(c.username, c.output); got != c.want {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
		})
	}
}

func TestDefaultPHPVersionForReturnsEmptyOnError(t *testing.T) {
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) { return "", errPHPVersionsTestFailed }
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	if got := defaultPHPVersionFor("alice"); got != "" {
		t.Fatalf("expected empty string on error, got %q", got)
	}
}

func TestDefaultPHPVersionForReturnsVersion(t *testing.T) {
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) {
		return "Default PHP version for user 'alice' is: 8.3", nil
	}
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	if got := defaultPHPVersionFor("alice"); got != "8.3" {
		t.Fatalf("expected 8.3, got %q", got)
	}
}

func TestServePHPVersionUserGETSuccess(t *testing.T) {
	origGet := phpDefaultVersionGetRun
	phpDefaultVersionGetRun = func(username string) (string, error) {
		return "Default PHP version for user 'alice' is: 8.2", nil
	}
	t.Cleanup(func() { phpDefaultVersionGetRun = origGet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/php/alice")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["version"] != "8.2" || body["username"] != "alice" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestServePHPVersionUserPOSTSuccess(t *testing.T) {
	origSet := phpDefaultVersionSetRun
	var gotUsername, gotVersion string
	phpDefaultVersionSetRun = func(username, version string) error {
		gotUsername, gotVersion = username, version
		return nil
	}
	t.Cleanup(func() { phpDefaultVersionSetRun = origSet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/php/alice", url.Values{"version": {"8.4"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if gotUsername != "alice" || gotVersion != "8.4" {
		t.Fatalf("expected opencli called with alice/8.4, got %s/%s", gotUsername, gotVersion)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["version"] != "8.4" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestServePHPVersionUserPOSTMissingVersion(t *testing.T) {
	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/php/alice", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServePHPVersionDomainGETSuccess(t *testing.T) {
	origGet := phpDomainVersionGetRun
	phpDomainVersionGetRun = func(domain string) (string, error) {
		return "Domain 'example.com' (owned by user: alice) uses PHP version: 8.1", nil
	}
	t.Cleanup(func() { phpDomainVersionGetRun = origGet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/php/example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["version"] != "8.1" || body["owner"] != "alice" || body["domain"] != "example.com" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestServePHPVersionDomainPOSTSuccess(t *testing.T) {
	origSet := phpDomainVersionSetRun
	phpDomainVersionSetRun = func(domain, version string) (string, error) {
		return "Updated PHP version in the configuration file to " + version, nil
	}
	t.Cleanup(func() { phpDomainVersionSetRun = origSet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/php/example.com", url.Values{"version": {"8.3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["version"] != "8.3" {
		t.Fatalf("unexpected body: %v", body)
	}
}

// TestServePHPVersionDomainPOSTLiteSpeedRejectionExitsZeroButIsAnError
// covers opencli php-domain --update's real quirk: it prints an "ERROR:"
// message and exits 0 (not 1) when the domain's webserver is LiteSpeed, so
// the handler must treat that as a failure by inspecting the text, not the
// exit code.
func TestServePHPVersionDomainPOSTLiteSpeedRejectionExitsZeroButIsAnError(t *testing.T) {
	origSet := phpDomainVersionSetRun
	phpDomainVersionSetRun = func(domain, version string) (string, error) {
		return "ERROR: PHP version can not be changed on openlitespeed webserver. Instead you need to change the docker image tag for the user.\nAvailable tags: https://hub.docker.com/r/litespeedtech/openlitespeed/tags", nil
	}
	t.Cleanup(func() { phpDomainVersionSetRun = origSet })

	p := &PHP{}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.PostForm(srv.URL+"/php/example.com", url.Values{"version": {"8.3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 despite opencli's exit 0, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ERROR: PHP version can not be changed") {
		t.Fatalf("expected rejection message in body, got %s", string(body))
	}
}

func TestParsePHPFPMServiceVersionsSortsAndFilters(t *testing.T) {
	compose := `
services:
  php-fpm-8.2:
    image: openpanel/php:8.2
  php-fpm-8.1:
    image: openpanel/php:8.1
  nginx:
    image: openpanel/nginx
`
	versions, err := parsePHPFPMServiceVersions([]byte(compose))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != "8.1" || versions[1] != "8.2" {
		t.Fatalf("expected sorted [8.1 8.2], got %v", versions)
	}
}

func TestCachedAvailablePHPVersionsCachesResult(t *testing.T) {
	origRun := availablePHPVersionsRun
	origTTL := phpAvailableVersionsTTL
	callCount := 0
	availablePHPVersionsRun = func(context string) ([]string, error) {
		callCount++
		return []string{"8.2"}, nil
	}
	phpAvailableVersionsCacheMu.Lock()
	phpAvailableVersionsCache = map[string]phpAvailableVersionsCacheEntry{}
	phpAvailableVersionsCacheMu.Unlock()
	t.Cleanup(func() {
		availablePHPVersionsRun = origRun
		phpAvailableVersionsTTL = origTTL
		phpAvailableVersionsCacheMu.Lock()
		phpAvailableVersionsCache = map[string]phpAvailableVersionsCacheEntry{}
		phpAvailableVersionsCacheMu.Unlock()
	})

	cachedAvailablePHPVersions("alice-ctx")
	cachedAvailablePHPVersions("alice-ctx")
	if callCount != 1 {
		t.Fatalf("expected exactly one underlying call within the TTL, got %d", callCount)
	}
}

func TestServePHPAvailableVersionsHTTP(t *testing.T) {
	origRun := availablePHPVersionsRun
	availablePHPVersionsRun = func(context string) ([]string, error) {
		return []string{"8.1", "8.2"}, nil
	}
	phpAvailableVersionsCacheMu.Lock()
	phpAvailableVersionsCache = map[string]phpAvailableVersionsCacheEntry{}
	phpAvailableVersionsCacheMu.Unlock()
	t.Cleanup(func() { availablePHPVersionsRun = origRun })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("alice-ctx"))

	p := &PHP{MySQL: mysqlDB}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/php/alice/available")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Versions []string `json:"versions"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Versions) != 2 || body.Versions[0] != "8.1" {
		t.Fatalf("unexpected versions: %v", body.Versions)
	}
}

func TestServePHPAvailableVersionsUnknownUserReturns404(t *testing.T) {
	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT server FROM users WHERE username = \?`).
		WithArgs("nobody").
		WillReturnError(sqlErrConnRefused{})

	p := &PHP{MySQL: mysqlDB}
	srv, client := newPHPTestServer(t, p)

	resp, err := client.Get(srv.URL + "/php/nobody/available")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
