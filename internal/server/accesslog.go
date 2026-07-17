package server

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"openadmin/internal/bootstrap"
)

// NewAccessLogger opens (or creates) bootstrap.AccessLogPath for appending.
// Unlike the app-level logger, this always writes: gunicorn's own accesslog
// isn't gated by dev_mode in the original config, so this middleware runs
// unconditionally in every mode.
func NewAccessLogger() (*log.Logger, error) {
	f, err := os.OpenFile(bootstrap.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return log.New(f, "", 0), nil
}

// AccessLogMiddleware logs one line per request: client IP, request line,
// status code, response size, and duration.
func AccessLogMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Printf("%s %q %s %d %d %s",
			GetClientIP(r), r.Method+" "+r.URL.RequestURI(), r.Proto,
			rec.status, rec.size, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.size += n
	return n, err
}

// Hijack forwards to the underlying ResponseWriter's Hijacker, if it has
// one. Embedding http.ResponseWriter as an interface field only promotes
// the methods declared by that interface (Write/WriteHeader/Header) -- it
// does NOT satisfy http.Hijacker, even though the real, unwrapped
// ResponseWriter from Go's HTTP/1.1 server does support it. Without this,
// every route behind this middleware that needs to hijack the connection
// (e.g. gorilla/websocket's Upgrade()) 500s with "response does not
// implement http.Hijacker", regardless of the HTTP/2-vs-1.1 ALPN outcome.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hj.Hijack()
}

// Flush forwards to the underlying ResponseWriter's Flusher, if it has one.
// Same embedding gap as Hijack above: without this, every route behind this
// middleware that streams a response (e.g. the SSE progress output from
// /user/new) 500s with "Streaming unsupported".
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
