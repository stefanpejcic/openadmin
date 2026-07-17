package bootstrap

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CaddyfilePath is a var (not const) so tests can point it at a scratch
// fixture instead of the real /etc path.
var CaddyfilePath = "/etc/openpanel/caddy/Caddyfile"

const DefaultPort = 2087

// CaddyCertDirs is checked in order: custom certs win over Let's Encrypt.
var CaddyCertDirs = []string{
	"/etc/openpanel/caddy/ssl/custom/",
	"/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/",
}

var (
	domainLineRe = regexp.MustCompile(`^([\w.-]+) \{`)
	ipLineRe     = regexp.MustCompile(`^(\d{1,3}(?:\.\d{1,3}){3}) \{`)
	portRe       = regexp.MustCompile(`reverse_proxy\s+[\w.-]+:(\d+)`)
)

// HostnameInfo is the result of parsing the Caddyfile's HOSTNAME DOMAIN/IP
// blocks.
type HostnameInfo struct {
	Domain string
	IP     string
	Port   int
}

// ReadHostnameBlock scans the delimited "# START/END HOSTNAME DOMAIN #" and
// "# START/END HOSTNAME IP #" blocks for the configured domain/IP and an
// optional reverse_proxy port override. A missing Caddyfile is not fatal --
// it just means no domain/IP/cert will be used.
func ReadHostnameBlock(logger *log.Logger) HostnameInfo {
	info := HostnameInfo{Port: DefaultPort}

	f, err := os.Open(CaddyfilePath)
	if err != nil {
		logger.Printf("Caddyfile does not exist at %s. No SSL will be used.", CaddyfilePath)
		return info
	}
	defer f.Close()

	inDomainBlock := false
	inIPBlock := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case strings.Contains(line, "# START HOSTNAME DOMAIN #"):
			inDomainBlock = true
			continue
		case strings.Contains(line, "# END HOSTNAME DOMAIN #"):
			inDomainBlock = false
			continue
		case strings.Contains(line, "# START HOSTNAME IP #"):
			inIPBlock = true
			continue
		case strings.Contains(line, "# END HOSTNAME IP #"):
			inIPBlock = false
			continue
		}

		if inDomainBlock {
			if m := domainLineRe.FindStringSubmatch(line); m != nil {
				info.Domain = m[1]
				continue
			}
			if m := portRe.FindStringSubmatch(line); m != nil {
				if port, err := strconv.Atoi(m[1]); err == nil {
					info.Port = port
				}
				continue
			}
		}

		if inIPBlock {
			if m := ipLineRe.FindStringSubmatch(line); m != nil {
				info.IP = m[1]
				continue
			}
			if m := portRe.FindStringSubmatch(line); m != nil {
				if port, err := strconv.Atoi(m[1]); err == nil {
					info.Port = port
				}
				continue
			}
		}
	}

	if info.Domain == "example.net" {
		info.Domain = ""
	}

	return info
}

// CertPaths is the result of CheckSSLExists.
type CertPaths struct {
	CertFile string
	KeyFile  string
	CertType string // "letsencrypt" or "custom"
}

// CheckSSLExists looks for <host>.crt/<host>.key under each of
// CaddyCertDirs in order.
func CheckSSLExists(host string) (CertPaths, bool) {
	for _, baseDir := range CaddyCertDirs {
		certDir := filepath.Join(baseDir, host)
		certFile := filepath.Join(certDir, host+".crt")
		keyFile := filepath.Join(certDir, host+".key")

		if fileExists(certFile) && fileExists(keyFile) {
			certType := "custom"
			if strings.Contains(baseDir, "letsencrypt") {
				certType = "letsencrypt"
			}
			return CertPaths{CertFile: certFile, KeyFile: keyFile, CertType: certType}, true
		}
	}
	return CertPaths{}, false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
