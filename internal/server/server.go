package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Disable2087PortFlagPath, when present, means the panel's direct port
// should never terminate TLS itself (another proxy handles it instead).
// Var (not const) so tests can point it at a scratch fixture.
var Disable2087PortFlagPath = "/root/disable_2087_port"

// Disable2087PortPresent reports whether Disable2087PortFlagPath exists.
func Disable2087PortPresent() bool {
	_, err := os.Stat(Disable2087PortFlagPath)
	return err == nil
}

// UseTLS reports whether TLS should be terminated directly by this process.
func UseTLS(certFile, keyFile string, disabled bool) bool {
	return !disabled && certFile != "" && keyFile != ""
}

// Options configures Run.
type Options struct {
	Port     int
	CertFile string
	KeyFile  string
	Handler  http.Handler
	Logger   *log.Logger
}

// Run starts the HTTP(S) server and blocks until SIGINT/SIGTERM triggers a
// graceful shutdown (10s grace period). Timeouts: 30s (read/write), 2s
// (idle keepalive). There is no worker/pidfile/fork-hook equivalent here:
// net/http serves concurrent requests via goroutines in this single process,
// and systemd tracks its PID directly.
func Run(opts Options) error {
	srv := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", opts.Port),
		Handler:      opts.Handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  2 * time.Second,
	}

	useTLS := UseTLS(opts.CertFile, opts.KeyFile, false)
	if useTLS {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		// ListenAndServeTLS auto-configures HTTP/2 whenever TLSNextProto is
		// nil. gorilla/websocket's Upgrade() needs to hijack the
		// underlying net.Conn, which HTTP/2's multiplexed ResponseWriter
		// doesn't support (http.Hijacker type-assertion fails), so any
		// browser that negotiates h2 over TLS gets a 500 "response does
		// not implement http.Hijacker" on every /ws/* route. Setting a
		// non-nil (even empty) TLSNextProto disables that auto-upgrade,
		// forcing HTTP/1.1 so websocket hijacking keeps working.
		srv.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	}

	errCh := make(chan error, 1)
	go func() {
		opts.Logger.Println("Server is ready. Spawning workers")
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS(opts.CertFile, opts.KeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		opts.Logger.Printf("worker received %s signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}
