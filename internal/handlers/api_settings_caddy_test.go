package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAPISettingsCaddyTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	a := &APISettingsCaddy{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings/caddy/metrics", a.ServeMetrics)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestAPISettingsCaddyMetricsSuccess(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "http_requests_total 42", 200, nil }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	srv, client := newAPISettingsCaddyTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/caddy/metrics")
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

func TestAPISettingsCaddyMetricsFetchError(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "", 0, &ftpStubError{"connection refused"} }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	srv, client := newAPISettingsCaddyTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/caddy/metrics")
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

func TestAPISettingsCaddyMetricsUpstreamErrorStatus(t *testing.T) {
	orig := caddyFetchMetrics
	caddyFetchMetrics = func() (string, int, error) { return "not found", 404, nil }
	t.Cleanup(func() { caddyFetchMetrics = orig })

	srv, client := newAPISettingsCaddyTestServer(t)
	resp, err := client.Get(srv.URL + "/api/settings/caddy/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "404") {
		t.Fatalf("expected upstream status reflected in message, got %q", body)
	}
}
