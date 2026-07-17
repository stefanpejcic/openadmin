package server

import (
	"encoding/json"
	"net/http"
)

// PlaceholderStatus is reported by the temporary root handler until real
// application routes are ported module-by-module.
type PlaceholderStatus struct {
	DevMode bool   `json:"dev_mode"`
	Domain  string `json:"domain,omitempty"`
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port"`
	TLS     bool   `json:"tls"`
}

// NewPlaceholderMux returns a mux serving a single "/" handler that reports
// the resolved bootstrap state -- enough to manually verify config read,
// cert discovery, and bind mode without any ported application routes.
func NewPlaceholderMux(status PlaceholderStatus) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})
	return mux
}
