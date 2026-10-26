package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
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
		// HTTP/2 loads a page's scripts and styles over one connection, websockets still work since Go only offers
		// websockets over HTTP/2 (RFC 8441) with GODEBUG=http2xconnect=1, so browsers open their own HTTP/1.1 connection for /ws/*
		srv.Protocols = new(http.Protocols)
		srv.Protocols.SetHTTP1(true)
		srv.Protocols.SetHTTP2(true)
	}

	errCh := make(chan error, 1)
	go func() {
		opts.Logger.Println("Server is ready. Spawning workers")
		var err error
		if useTLS {
			var ln net.Listener
			if ln, err = net.Listen("tcp", srv.Addr); err == nil {
				err = serveTLS(srv, ln, opts.CertFile, opts.KeyFile)
			}
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
