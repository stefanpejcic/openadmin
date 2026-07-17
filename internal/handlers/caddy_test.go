package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newCaddyTestServer(t *testing.T, c *Caddy) (*httptest.Server, *http.Client) {
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
	c.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /settings/caddy", c.ServeSettings)
	mux.HandleFunc("POST /settings/caddy", c.ServeSettings)
	mux.HandleFunc("GET /settings/caddy/metrics", c.ServeMetrics)
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

func TestServeSettingsGetRenders(t *testing.T) {
	c := &Caddy{}
	srv, client := newCaddyTestServer(t, c)

	resp, err := client.Get(srv.URL + "/settings/caddy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	// The real templates/settings/caddy.html has no {% extends %} at all --
	// it's a single blank line, so the rendered page is intentionally empty.
}

func TestServeSettingsPostIsNoOpAndRenders(t *testing.T) {
	c := &Caddy{}
	srv, client := newCaddyTestServer(t, c)

	resp, err := client.PostForm(srv.URL+"/settings/caddy", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestServeMetricsSuccess(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "http_requests_total 42", 200, nil }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	c := &Caddy{}
	srv, client := newCaddyTestServer(t, c)

	resp, err := client.Get(srv.URL + "/settings/caddy/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "http_requests_total 42" {
		t.Fatalf("expected raw metrics passthrough, got %q", body)
	}
}

func TestServeMetricsFetchError(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "", 0, &ftpStubError{"connection refused"} }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	c := &Caddy{}
	srv, client := newCaddyTestServer(t, c)

	resp, err := client.Get(srv.URL + "/settings/caddy/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Error fetching metrics: connection refused") {
		t.Fatalf("expected error message, got %q", body)
	}
}

func TestServeMetricsUpstreamErrorStatus(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "not found", 404, nil }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	c := &Caddy{}
	srv, client := newCaddyTestServer(t, c)

	resp, err := client.Get(srv.URL + "/settings/caddy/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (matching raise_for_status() being caught), got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "404") {
		t.Fatalf("expected upstream status reflected in message, got %q", body)
	}
}
