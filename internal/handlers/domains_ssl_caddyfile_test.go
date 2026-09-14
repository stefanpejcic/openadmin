package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleCaddyfile is a trimmed-down copy of a real /etc/openpanel/caddy/Caddyfile
// (global options block, an IP block with an issuer sub-block, and the
// panel's own hostname block duplicated for http:// and bare/https), used
// to ground the block-matching/tls-editing logic in real syntax rather
// than a synthetic approximation.
const sampleCaddyfile = `# general
{
    #admin off

    on_demand_tls {
        ask      "http://localhost/check"
    }
}


# START HOSTNAME IP #
185.7.32.112 {
  tls {
    issuer acme {
      profile shortlived
    }
  }
  reverse_proxy localhost:2087
}
# END HOSTNAME IP #
# START HOSTNAME DOMAIN #
example.net {
    reverse_proxy localhost:2087
}

http://example.net {
    reverse_proxy localhost:2087
}
# END HOSTNAME DOMAIN #

# import all sites
import /etc/openpanel/caddy/domains/*
`

func TestFindCaddyfileDomainBlocksPicksNonHTTPBlock(t *testing.T) {
	lines := strings.Split(sampleCaddyfile, "\n")
	matches := findCaddyfileDomainBlocks(lines, "example.net")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (bare + http://), got %d", len(matches))
	}
	block, ok := pickCaddyfileBlockForTLS(matches)
	if !ok {
		t.Fatal("expected a block to be picked")
	}
	if block.scheme == "http" {
		t.Fatalf("expected the non-http:// block to be picked, got scheme %q", block.scheme)
	}
	if got := strings.TrimSpace(lines[block.headerLine]); got != "example.net {" {
		t.Fatalf("expected header line %q, got %q", "example.net {", got)
	}
}

func TestFindCaddyfileDomainBlocksNoMatch(t *testing.T) {
	lines := strings.Split(sampleCaddyfile, "\n")
	if matches := findCaddyfileDomainBlocks(lines, "notfound.example"); len(matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(matches))
	}
	// The IP block, the global options block, and nested sub-blocks
	// (on_demand_tls, tls, issuer acme) must never be mistaken for a site
	// block matching an unrelated domain lookup.
	if matches := findCaddyfileDomainBlocks(lines, "acme"); len(matches) != 0 {
		t.Fatalf("expected nested directive names to never match as domains, got %d", len(matches))
	}
}

func TestSetCaddyfileCustomTLSInsertsWhenAbsent(t *testing.T) {
	lines := strings.Split(sampleCaddyfile, "\n")
	block, ok := pickCaddyfileBlockForTLS(findCaddyfileDomainBlocks(lines, "example.net"))
	if !ok {
		t.Fatal("expected to find example.net block")
	}
	updated := setCaddyfileCustomTLS(lines, block, "/etc/openpanel/caddy/ssl/custom/example.net/fullchain.pem", "/etc/openpanel/caddy/ssl/custom/example.net/key.pem")

	newBlock, ok := pickCaddyfileBlockForTLS(findCaddyfileDomainBlocks(updated, "example.net"))
	if !ok {
		t.Fatal("expected to still find example.net block after edit")
	}
	found := false
	for i := newBlock.headerLine + 1; i < newBlock.closeLine; i++ {
		if strings.Contains(updated[i], "tls /etc/openpanel/caddy/ssl/custom/example.net/fullchain.pem /etc/openpanel/caddy/ssl/custom/example.net/key.pem") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tls line inside example.net's own (bare) block body, got:\n%s", strings.Join(updated, "\n"))
	}

	// It must land inside the bare block, not the http://-only duplicate.
	httpMatches := findCaddyfileDomainBlocks(updated, "example.net")
	for _, m := range httpMatches {
		if m.scheme != "http" {
			continue
		}
		for i := m.headerLine + 1; i < m.closeLine; i++ {
			if strings.Contains(updated[i], "tls ") {
				t.Fatalf("tls directive leaked into the http:// block body: %q", updated[i])
			}
		}
	}
}

func TestSetCaddyfileCustomTLSReplacesExistingSubBlock(t *testing.T) {
	content := "drupal.tests.openpanel.org {\n  reverse_proxy 127.0.0.1:1234\n\n  tls {\n    on_demand\n  }\n}\n"
	lines := strings.Split(content, "\n")
	block, ok := pickCaddyfileBlockForTLS(findCaddyfileDomainBlocks(lines, "drupal.tests.openpanel.org"))
	if !ok {
		t.Fatal("expected to find block")
	}
	updated := setCaddyfileCustomTLS(lines, block, "/certs/fullchain.pem", "/certs/key.pem")
	joined := strings.Join(updated, "\n")
	if strings.Contains(joined, "on_demand") {
		t.Fatalf("expected the on_demand tls sub-block to be replaced, got:\n%s", joined)
	}
	if !strings.Contains(joined, "tls /certs/fullchain.pem /certs/key.pem") {
		t.Fatalf("expected replaced tls line, got:\n%s", joined)
	}
	if strings.Count(joined, "tls ") != 1 {
		t.Fatalf("expected exactly one tls directive to survive, got:\n%s", joined)
	}
}

func TestCaddyfileDomainSSLInfoAutoWhenNoTLSDirective(t *testing.T) {
	dir := t.TempDir()
	caddyfilePath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(sampleCaddyfile), 0644); err != nil {
		t.Fatal(err)
	}
	orig := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = orig })

	setting, keys, found := caddyfileDomainSSLInfo("example.net")
	if !found {
		t.Fatal("expected domain to be found")
	}
	if setting != "autossl" {
		t.Fatalf("expected autossl, got %q", setting)
	}
	if keys != "" {
		t.Fatalf("expected no keys, got %q", keys)
	}
}

func TestCaddyfileDomainSSLInfoNotFound(t *testing.T) {
	dir := t.TempDir()
	caddyfilePath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(sampleCaddyfile), 0644); err != nil {
		t.Fatal(err)
	}
	orig := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = orig })

	if _, _, found := caddyfileDomainSSLInfo("nope.example"); found {
		t.Fatal("expected domain not to be found")
	}
}

func TestApplyCaddyfileCustomSSLWritesFilesAndReloads(t *testing.T) {
	dir := t.TempDir()
	caddyfilePath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(sampleCaddyfile), 0644); err != nil {
		t.Fatal(err)
	}
	origCaddyfile := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = origCaddyfile })

	sslDir := filepath.Join(dir, "ssl-custom")
	origSSLDir := sslCustomCaddySSLDir
	sslCustomCaddySSLDir = sslDir
	t.Cleanup(func() { sslCustomCaddySSLDir = origSSLDir })

	origValidate := caddyValidateRun
	caddyValidateRun = func() (string, string, int, error) { return "", "", 0, nil }
	t.Cleanup(func() { caddyValidateRun = origValidate })

	reloaded := false
	origReload := caddyReloadRun
	caddyReloadRun = func() error { reloaded = true; return nil }
	t.Cleanup(func() { caddyReloadRun = origReload })

	certPEM := "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"
	keyPEM := "-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----"

	msg, err := applyCaddyfileCustomSSL("example.net", certPEM, keyPEM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "example.net") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if !reloaded {
		t.Fatal("expected caddy to be reloaded")
	}

	gotCert, err := os.ReadFile(filepath.Join(sslDir, "example.net", "fullchain.pem"))
	if err != nil || string(gotCert) != certPEM {
		t.Fatalf("cert file mismatch: err=%v got=%q", err, gotCert)
	}
	gotKey, err := os.ReadFile(filepath.Join(sslDir, "example.net", "key.pem"))
	if err != nil || string(gotKey) != keyPEM {
		t.Fatalf("key file mismatch: err=%v got=%q", err, gotKey)
	}

	setting, keys, found := caddyfileDomainSSLInfo("example.net")
	if !found || setting != "custom ssl" {
		t.Fatalf("expected custom ssl after apply, got setting=%q found=%v", setting, found)
	}
	if !strings.Contains(keys, "BEGIN CERTIFICATE") || !strings.Contains(keys, "BEGIN PRIVATE KEY") {
		t.Fatalf("expected keys to contain both pem blocks, got %q", keys)
	}
}

func TestApplyCaddyfileCustomSSLRevertsOnValidationFailure(t *testing.T) {
	dir := t.TempDir()
	caddyfilePath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(sampleCaddyfile), 0644); err != nil {
		t.Fatal(err)
	}
	origCaddyfile := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = origCaddyfile })

	sslDir := filepath.Join(dir, "ssl-custom")
	origSSLDir := sslCustomCaddySSLDir
	sslCustomCaddySSLDir = sslDir
	t.Cleanup(func() { sslCustomCaddySSLDir = origSSLDir })

	origValidate := caddyValidateRun
	caddyValidateRun = func() (string, string, int, error) { return "", "boom", 1, nil }
	t.Cleanup(func() { caddyValidateRun = origValidate })

	reloadCalled := false
	origReload := caddyReloadRun
	caddyReloadRun = func() error { reloadCalled = true; return nil }
	t.Cleanup(func() { caddyReloadRun = origReload })

	_, err := applyCaddyfileCustomSSL("example.net",
		"-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----",
		"-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----")
	if err == nil {
		t.Fatal("expected an error from validation failure")
	}
	if reloadCalled {
		t.Fatal("expected caddy not to be reloaded after validation failure")
	}

	after, readErr := os.ReadFile(caddyfilePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != sampleCaddyfile {
		t.Fatal("expected Caddyfile to be reverted to its original content")
	}
}

func TestApplyCaddyfileCustomSSLDomainNotFound(t *testing.T) {
	dir := t.TempDir()
	caddyfilePath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(sampleCaddyfile), 0644); err != nil {
		t.Fatal(err)
	}
	orig := sslMainCaddyfilePath
	sslMainCaddyfilePath = caddyfilePath
	t.Cleanup(func() { sslMainCaddyfilePath = orig })

	if _, err := applyCaddyfileCustomSSL("nope.example", "cert", "key"); err == nil {
		t.Fatal("expected an error for a domain with no matching Caddyfile block")
	}
}
