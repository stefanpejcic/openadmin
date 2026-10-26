package server

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serveTLSForTest answers "tls" or "plain" depending on how the request came in
func serveTLSForTest(t *testing.T) string {
	t.Helper()
	certFile, keyFile := generateSelfSignedCert(t, t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil {
				io.WriteString(w, "tls")
				return
			}
			io.WriteString(w, "plain")
		}),
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	go serveTLS(srv, ln, certFile, keyFile)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func get(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

var insecureClient = &http.Client{
	Timeout:   3 * time.Second,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

// this is what Caddy does with reverse_proxy localhost:2087 once the port runs TLS
func TestServeTLSStillServesPlainHTTPToLoopback(t *testing.T) {
	addr := serveTLSForTest(t)

	if code, body := get(t, insecureClient, "http://"+addr+"/"); code != http.StatusOK || body != "plain" {
		t.Fatalf("plain HTTP from loopback: got %d %q", code, body)
	}
	if code, body := get(t, insecureClient, "https://"+addr+"/"); code != http.StatusOK || body != "tls" {
		t.Fatalf("HTTPS from loopback: got %d %q", code, body)
	}
}

func TestServeTLSKeepsRejectingPlainHTTPFromRemote(t *testing.T) {
	orig := isLoopback
	isLoopback = func(net.Addr) bool { return false }
	defer func() { isLoopback = orig }()

	addr := serveTLSForTest(t)

	code, body := get(t, insecureClient, "http://"+addr+"/")
	if code != http.StatusBadRequest || !strings.Contains(body, "Client sent an HTTP request to an HTTPS server") {
		t.Fatalf("plain HTTP from a remote client should still get Go's 400, got %d %q", code, body)
	}
	if code, body := get(t, insecureClient, "https://"+addr+"/"); code != http.StatusOK || body != "tls" {
		t.Fatalf("HTTPS from a remote client: got %d %q", code, body)
	}
}

func TestServeTLSSilentLoopbackConnDoesNotBlockOthers(t *testing.T) {
	addr := serveTLSForTest(t)

	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()

	if code, _ := get(t, insecureClient, "https://"+addr+"/"); code != http.StatusOK {
		t.Fatalf("HTTPS behind a silent connection: got %d", code)
	}
	if code, _ := get(t, insecureClient, "http://"+addr+"/"); code != http.StatusOK {
		t.Fatalf("plain HTTP behind a silent connection: got %d", code)
	}
}

func TestServeTLSStillNegotiatesHTTP2(t *testing.T) {
	addr := serveTLSForTest(t)
	get(t, insecureClient, "https://"+addr+"/")

	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Fatalf("expected h2, got %q", got)
	}
}
