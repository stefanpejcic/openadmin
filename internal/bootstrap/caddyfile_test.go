package bootstrap

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestReadHostnameBlockMissingFile(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyfilePath
	defer func() { CaddyfilePath = orig }()

	CaddyfilePath = filepath.Join(dir, "missing")
	info := ReadHostnameBlock(log.New(io.Discard, "", 0))
	if info.Domain != "" || info.IP != "" || info.Port != DefaultPort {
		t.Fatalf("expected zero-value defaults, got %+v", info)
	}
}

func TestReadHostnameBlockParsesDomainAndPort(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyfilePath
	defer func() { CaddyfilePath = orig }()

	// matches the real Caddyfile format used in production
	fixture := `# general
{
    on_demand_tls {
        ask "http://localhost/check"
    }
}

# START HOSTNAME DOMAIN #
panel.example.com {
    reverse_proxy localhost:2087
}

http://panel.example.com {
    reverse_proxy localhost:2087
}
# END HOSTNAME DOMAIN #

# START WEBMAIL DOMAIN #
webmail.example.com {
    reverse_proxy localhost:8080
}
# END WEBMAIL DOMAIN #
`
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte(fixture), 0644); err != nil {
		t.Fatal(err)
	}
	CaddyfilePath = path

	info := ReadHostnameBlock(log.New(io.Discard, "", 0))
	if info.Domain != "panel.example.com" {
		t.Fatalf("expected domain panel.example.com, got %q", info.Domain)
	}
	if info.Port != 2087 {
		t.Fatalf("expected port 2087, got %d", info.Port)
	}
	if info.IP != "" {
		t.Fatalf("expected no IP block, got %q", info.IP)
	}
}

func TestReadHostnameBlockPlaceholderDomainIsUnset(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyfilePath
	defer func() { CaddyfilePath = orig }()

	fixture := `# START HOSTNAME DOMAIN #
example.net {
    reverse_proxy localhost:2087
}
# END HOSTNAME DOMAIN #
`
	path := filepath.Join(dir, "Caddyfile")
	os.WriteFile(path, []byte(fixture), 0644)
	CaddyfilePath = path

	info := ReadHostnameBlock(log.New(io.Discard, "", 0))
	if info.Domain != "" {
		t.Fatalf("expected placeholder domain to be treated as unset, got %q", info.Domain)
	}
}

func TestReadHostnameBlockIPBlockAndCustomPort(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyfilePath
	defer func() { CaddyfilePath = orig }()

	fixture := `# START HOSTNAME IP #
203.0.113.5 {
    reverse_proxy localhost:2091
}
# END HOSTNAME IP #
`
	path := filepath.Join(dir, "Caddyfile")
	os.WriteFile(path, []byte(fixture), 0644)
	CaddyfilePath = path

	info := ReadHostnameBlock(log.New(io.Discard, "", 0))
	if info.IP != "203.0.113.5" {
		t.Fatalf("expected IP 203.0.113.5, got %q", info.IP)
	}
	if info.Port != 2091 {
		t.Fatalf("expected custom port 2091, got %d", info.Port)
	}
}

func TestCheckSSLExists(t *testing.T) {
	dir := t.TempDir()
	orig := CaddyCertDirs
	defer func() { CaddyCertDirs = orig }()

	customDir := filepath.Join(dir, "custom") + "/"
	leDir := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory") + "/"
	CaddyCertDirs = []string{customDir, leDir}

	if _, ok := CheckSSLExists("panel.example.com"); ok {
		t.Fatal("expected no cert found")
	}

	leHostDir := filepath.Join(leDir, "panel.example.com")
	os.MkdirAll(leHostDir, 0755)
	os.WriteFile(filepath.Join(leHostDir, "panel.example.com.crt"), []byte("cert"), 0644)
	os.WriteFile(filepath.Join(leHostDir, "panel.example.com.key"), []byte("key"), 0644)

	paths, ok := CheckSSLExists("panel.example.com")
	if !ok {
		t.Fatal("expected cert to be found")
	}
	if paths.CertType != "letsencrypt" {
		t.Fatalf("expected letsencrypt cert type, got %s", paths.CertType)
	}

	customHostDir := filepath.Join(customDir, "panel.example.com")
	os.MkdirAll(customHostDir, 0755)
	os.WriteFile(filepath.Join(customHostDir, "panel.example.com.crt"), []byte("cert"), 0644)
	os.WriteFile(filepath.Join(customHostDir, "panel.example.com.key"), []byte("key"), 0644)

	paths, ok = CheckSSLExists("panel.example.com")
	if !ok || paths.CertType != "custom" {
		t.Fatalf("expected custom cert to win over letsencrypt, got %+v ok=%v", paths, ok)
	}
}
