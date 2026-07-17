package server

import (
	"net/http/httptest"
	"testing"
)

func TestGetClientIP(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(r *httptest.ResponseRecorder, remoteAddr string)
		cf         string
		xff        string
		remoteAddr string
		want       string
	}{
		{name: "cf header wins over xff and remote addr", cf: "1.1.1.1", xff: "2.2.2.2", remoteAddr: "3.3.3.3:1234", want: "1.1.1.1"},
		{name: "xff first value wins over remote addr", xff: "2.2.2.2, 4.4.4.4", remoteAddr: "3.3.3.3:1234", want: "2.2.2.2"},
		{name: "falls back to remote addr", remoteAddr: "3.3.3.3:1234", want: "3.3.3.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = c.remoteAddr
			if c.cf != "" {
				req.Header.Set("CF-Connecting-IP", c.cf)
			}
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := GetClientIP(req); got != c.want {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
		})
	}
}
