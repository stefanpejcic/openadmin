package handlers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCert writes a self-signed cert (leaf only, PEM-encoded) expiring
// at notAfter to path, creating parent directories as needed.
func writeTestCert(t *testing.T, path string, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		NotBefore:    notAfter.Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
}

func withScratchSSLBaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := sslBaseDir
	sslBaseDir = dir
	t.Cleanup(func() { sslBaseDir = orig })
	return dir
}

func TestCheckDomainSSLHealthNoneType(t *testing.T) {
	withScratchSSLBaseDir(t)
	health := checkDomainSSLHealth("example.com", "none", caddyTLSIndex{})
	if health.Status != "" || !health.NotAfter.IsZero() {
		t.Fatalf("expected zero-value health for ssl type none, got %+v", health)
	}
}

func TestCheckDomainSSLHealthMissingFile(t *testing.T) {
	withScratchSSLBaseDir(t)
	health := checkDomainSSLHealth("example.com", "automatic", caddyTLSIndex{})
	if health.Status != "missing" {
		t.Fatalf("expected missing, got %q", health.Status)
	}
}

func TestCheckDomainSSLHealthUnreadableFile(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	path := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory", "example.com", "example.com.crt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}

	health := checkDomainSSLHealth("example.com", "automatic", caddyTLSIndex{})
	if health.Status != "unreadable" {
		t.Fatalf("expected unreadable, got %q", health.Status)
	}
}

func TestCheckDomainSSLHealthExpired(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	path := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory", "example.com", "example.com.crt")
	writeTestCert(t, path, time.Now().Add(-24*time.Hour))

	health := checkDomainSSLHealth("example.com", "automatic", caddyTLSIndex{})
	if health.Status != "expired" {
		t.Fatalf("expected expired, got %q", health.Status)
	}
	if health.NotAfter.IsZero() {
		t.Fatal("expected NotAfter to be set")
	}
}

func TestCheckDomainSSLHealthExpiringSoon(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	path := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory", "example.com", "example.com.crt")
	writeTestCert(t, path, time.Now().Add(5*24*time.Hour))

	health := checkDomainSSLHealth("example.com", "automatic", caddyTLSIndex{})
	if health.Status != "expiring" {
		t.Fatalf("expected expiring, got %q", health.Status)
	}
}

func TestCheckDomainSSLHealthValid(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	path := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory", "example.com", "example.com.crt")
	writeTestCert(t, path, time.Now().Add(90*24*time.Hour))

	health := checkDomainSSLHealth("example.com", "automatic", caddyTLSIndex{})
	if health.Status != "" {
		t.Fatalf("expected valid (empty status), got %q", health.Status)
	}
	if health.NotAfter.IsZero() {
		t.Fatal("expected NotAfter to be set")
	}
}

func TestCheckDomainSSLHealthCustomUsesConventionPathWhenIndexUnavailable(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	path := filepath.Join(dir, "custom", "example.com", "fullchain.pem")
	writeTestCert(t, path, time.Now().Add(90*24*time.Hour))

	health := checkDomainSSLHealth("example.com", "custom", caddyTLSIndex{ok: false})
	if health.Status != "" {
		t.Fatalf("expected valid, got %q", health.Status)
	}
}

func TestCheckDomainSSLHealthCustomUsesLiveIndexPathWhenAvailable(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	// Deliberately not the naming-convention path, to prove the live index
	// path is what actually gets read.
	livePath := filepath.Join(dir, "custom", "example.com", "other-name.pem")
	writeTestCert(t, livePath, time.Now().Add(90*24*time.Hour))

	idx := caddyTLSIndex{
		ok:               true,
		customCertPaths: map[string]string{"example.com": livePath},
	}
	health := checkDomainSSLHealth("example.com", "custom", idx)
	if health.Status != "" {
		t.Fatalf("expected valid using live index path, got %q", health.Status)
	}
}

func TestTranslateCaddyCertPath(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	origPrefix := caddyCertContainerPrefix
	caddyCertContainerPrefix = "/data/caddy/certificates/"
	t.Cleanup(func() { caddyCertContainerPrefix = origPrefix })

	got := translateCaddyCertPath("/data/caddy/certificates/custom/unl.rs/fullchain.pem")
	want := filepath.Join(dir, "custom", "unl.rs", "fullchain.pem")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// A path that doesn't have the expected prefix is returned unchanged
	// rather than mangled.
	if got := translateCaddyCertPath("/some/other/path.pem"); got != "/some/other/path.pem" {
		t.Fatalf("expected unchanged path, got %q", got)
	}
}

const sampleCaddyAppsConfig = `{
	"http": {
		"servers": {
			"srv1": {
				"tls_connection_policies": [
					{
						"match": [{"sni": ["*.unl.rs", "unl.rs"]}],
						"certificate_selection": {"any_tag": ["cert0"]}
					},
					{}
				]
			}
		}
	},
	"tls": {
		"automation": {
			"policies": [
				{"on_demand": true, "subjects": ["example.com", "www.example.com"]},
				{"subjects": ["unl.rs", "server.pejcic.rs"]}
			]
		},
		"certificates": {
			"load_files": [
				{"certificate": "/data/caddy/certificates/custom/unl.rs/fullchain.pem", "key": "/data/caddy/certificates/custom/unl.rs/key.pem", "tags": ["cert0"]}
			]
		}
	}
}`

func TestFetchCaddyTLSIndexParsesCustomCertPaths(t *testing.T) {
	dir := withScratchSSLBaseDir(t)
	origPrefix := caddyCertContainerPrefix
	caddyCertContainerPrefix = "/data/caddy/certificates/"
	t.Cleanup(func() { caddyCertContainerPrefix = origPrefix })

	orig := caddyConfigFetch
	caddyConfigFetch = func() ([]byte, error) { return []byte(sampleCaddyAppsConfig), nil }
	t.Cleanup(func() { caddyConfigFetch = orig })

	idx := fetchCaddyTLSIndex()
	if !idx.ok {
		t.Fatal("expected ok=true on successful parse")
	}

	want := filepath.Join(dir, "custom", "unl.rs", "fullchain.pem")
	if got := idx.customCertPaths["unl.rs"]; got != want {
		t.Fatalf("customCertPaths[unl.rs] = %q, want %q", got, want)
	}
	if got := idx.customCertPaths["*.unl.rs"]; got != want {
		t.Fatalf("customCertPaths[*.unl.rs] = %q, want %q", got, want)
	}
}

func TestFetchCaddyTLSIndexFailsOpenOnTransportError(t *testing.T) {
	orig := caddyConfigFetch
	caddyConfigFetch = func() ([]byte, error) { return nil, os.ErrDeadlineExceeded }
	t.Cleanup(func() { caddyConfigFetch = orig })

	idx := fetchCaddyTLSIndex()
	if idx.ok {
		t.Fatal("expected ok=false on transport error")
	}
}

func TestFetchCaddyTLSIndexFailsOpenOnBadJSON(t *testing.T) {
	orig := caddyConfigFetch
	caddyConfigFetch = func() ([]byte, error) { return []byte("not json"), nil }
	t.Cleanup(func() { caddyConfigFetch = orig })

	idx := fetchCaddyTLSIndex()
	if idx.ok {
		t.Fatal("expected ok=false on unparsable body")
	}
}

func TestGetCaddyTLSIndexCachesResult(t *testing.T) {
	resetCaddyTLSIndexCache()
	t.Cleanup(resetCaddyTLSIndexCache)

	calls := 0
	orig := caddyConfigFetch
	caddyConfigFetch = func() ([]byte, error) {
		calls++
		return []byte(sampleCaddyAppsConfig), nil
	}
	t.Cleanup(func() { caddyConfigFetch = orig })

	getCaddyTLSIndex()
	getCaddyTLSIndex()
	getCaddyTLSIndex()

	if calls != 1 {
		t.Fatalf("expected 1 fetch across 3 calls within the cache TTL, got %d", calls)
	}
}

func TestDomainCertPathNoneReturnsEmpty(t *testing.T) {
	withScratchSSLBaseDir(t)
	if got := domainCertPath("example.com", "none", caddyTLSIndex{}); got != "" {
		t.Fatalf("expected empty path for ssl type none, got %q", got)
	}
}
