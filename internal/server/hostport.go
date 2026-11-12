package server

import (
	"net"
	"net/http"
	"strconv"
)

// RequireAdminPort drops requests whose Host isn't on the admin port, so caddy proxying the hostname on 80/443 gets no response
func RequireAdminPort(port int, next http.Handler) http.Handler {
	want := strconv.Itoa(port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, p, err := net.SplitHostPort(r.Host); err != nil || p != want {
			// closes the connection without writing anything, net/http doesn't log this panic
			panic(http.ErrAbortHandler)
		}
		next.ServeHTTP(w, r)
	})
}
