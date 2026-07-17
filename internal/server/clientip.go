package server

import (
	"net"
	"net/http"
	"strings"
)

// GetClientIP trusts CF-Connecting-IP first, then the first value of
// X-Forwarded-For, then falls back to the raw TCP peer address. There is
// no source-IP allowlist validation here -- that trust boundary is
// established at the network layer (only Caddy/Cloudflare should be able to
// reach this process directly), not in application code.
func GetClientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.Split(xff, ",")[0]
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
