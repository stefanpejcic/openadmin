package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUseTLS(t *testing.T) {
	cases := []struct {
		name      string
		cert, key string
		disabled  bool
		want      bool
	}{
		{"no cert", "", "", false, false},
		{"cert but disabled", "cert", "key", true, false},
		{"cert and enabled", "cert", "key", false, true},
		{"only cert, no key", "cert", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UseTLS(c.cert, c.key, c.disabled); got != c.want {
				t.Fatalf("expected %v, got %v", c.want, got)
			}
		})
	}
}

func TestDisable2087PortPresent(t *testing.T) {
	dir := t.TempDir()
	orig := Disable2087PortFlagPath
	defer func() { Disable2087PortFlagPath = orig }()

	Disable2087PortFlagPath = filepath.Join(dir, "disable_2087_port")
	if Disable2087PortPresent() {
		t.Fatal("expected false when flag absent")
	}

	os.WriteFile(Disable2087PortFlagPath, nil, 0644)
	if !Disable2087PortPresent() {
		t.Fatal("expected true when flag present")
	}
}

func TestRunPlainHTTPAndGracefulShutdown(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	mux := NewPlaceholderMux(PlaceholderStatus{Port: 18099})

	done := make(chan error, 1)
	go func() {
		done <- Run(Options{Port: 18099, Handler: mux, Logger: logger})
	}()

	waitForHTTP(t, "http://127.0.0.1:18099/")

	resp, err := http.Get("http://127.0.0.1:18099/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func TestRunTLS(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	logger := log.New(io.Discard, "", 0)
	mux := NewPlaceholderMux(PlaceholderStatus{Port: 18100, TLS: true})

	done := make(chan error, 1)
	go func() {
		done <- Run(Options{Port: 18100, CertFile: certFile, KeyFile: keyFile, Handler: mux, Logger: logger})
	}()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp := waitForHTTPS(t, client, "https://127.0.0.1:18100/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

// TestRunTLSDoesNotNegotiateHTTP2 guards against a real bug: TLS servers
// auto-negotiate HTTP/2 whenever TLSNextProto is nil, and any real browser
// (which offers "h2" via ALPN on every HTTPS connection, including for
// wss:// -- confirmed via Firefox's dev tools) would then get promoted to
// HTTP/2 on this port. gorilla/websocket's Upgrade() needs to hijack the
// underlying net.Conn, which HTTP/2's multiplexed ResponseWriter can't do
// (the http.Hijacker type assertion fails), so every /ws/* route 500'd with
// "response does not implement http.Hijacker" until Run() disabled HTTP/2.
//
// A gorilla/websocket client can't reproduce this directly -- its own
// Dialer never offers "h2" via ALPN in the first place, unlike a real
// browser -- so this asserts on the actual mechanism instead: a raw TLS
// client that does offer "h2"/"http/1.1" (like a browser) must not have
// "h2" selected.
func TestRunTLSDoesNotNegotiateHTTP2(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	mux := NewPlaceholderMux(PlaceholderStatus{Port: 18101, TLS: true})
	logger := log.New(io.Discard, "", 0)
	done := make(chan error, 1)
	go func() {
		done <- Run(Options{Port: 18101, CertFile: certFile, KeyFile: keyFile, Handler: mux, Logger: logger})
	}()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp := waitForHTTPS(t, client, "https://127.0.0.1:18101/")
	resp.Body.Close()

	conn, err := tls.Dial("tcp", "127.0.0.1:18101", &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatalf("raw TLS dial failed: %v", err)
	}
	negotiated := conn.ConnectionState().NegotiatedProtocol
	conn.Close()

	if negotiated == "h2" {
		t.Fatalf("expected the server to never negotiate h2 (breaks websocket hijacking), got %q", negotiated)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never became ready on %s", url)
}

func waitForHTTPS(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never became ready on %s: %v", url, lastErr)
	return nil
}

func generateSelfSignedCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	certOut, err := os.Create(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()

	keyOut, err := os.Create(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()

	return certFile, keyFile
}
