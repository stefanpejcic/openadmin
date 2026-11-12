package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAdminPort(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	srv := httptest.NewServer(RequireAdminPort(2087, ok))
	defer srv.Close()

	cases := []struct {
		host string
		pass bool
	}{
		{"panel.example.com:2087", true},
		{"localhost:2087", true},
		{"[::1]:2087", true},
		{"panel.example.com", false},
		{"panel.example.com:443", false},
		{"panel.example.com:80", false},
		{"1.2.3.4", false},
	}

	for _, c := range cases {
		req, _ := http.NewRequest("GET", srv.URL+"/login", nil)
		req.Host = c.host
		resp, err := http.DefaultTransport.RoundTrip(req)
		if c.pass {
			if err != nil {
				t.Fatalf("%s: expected response, got %v", c.host, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("%s: expected ok, got %q", c.host, body)
			}
			continue
		}
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s: expected dropped connection, got status %d", c.host, resp.StatusCode)
		}
	}
}
