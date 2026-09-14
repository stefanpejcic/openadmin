// This file implements a fallback for the /domains/ssl/<domain_name> page
// (domains_ssl.go): domains that aren't an OpenPanel hosting user's domain
// -- most notably the panel's own hostname, configured directly as a site
// block in the main Caddyfile rather than a per-domain conf file under
// CaddyDomainsConfDir -- have no owner for `opencli domains-ssl` to resolve,
// so that command can't be used for them. When the per-domain conf file is
// missing or empty, ServeSSL looks for a matching site block in the main
// Caddyfile instead, and if found, reads/writes its `tls` directive
// directly rather than shelling out to opencli.
package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sslMainCaddyfilePath / sslCustomCaddySSLDir are vars so tests can point
// them at scratch files instead of the real system paths. Kept as separate
// copies rather than reusing autologin.go's autologinCaddyfilePath, per
// that file's own convention of not sharing path vars across call sites
// with different caching/lifecycle needs.
var (
	sslMainCaddyfilePath = "/etc/openpanel/caddy/Caddyfile"
	sslCustomCaddySSLDir = "/etc/openpanel/caddy/ssl/custom"
)

// domainConfMissingOrEmpty reports whether domainName has no (or an empty)
// per-domain Caddy conf file, i.e. it isn't a domain opencli manages.
func domainConfMissingOrEmpty(domainName string) bool {
	info, err := os.Stat(filepath.Join(CaddyDomainsConfDir, domainName+".conf"))
	if err != nil {
		return true
	}
	return info.Size() == 0
}

// caddyBlockMatch identifies one top-level site block in the Caddyfile
// whose address list includes the domain being looked up.
type caddyBlockMatch struct {
	headerLine, closeLine int
	scheme                string // "", "http", or "https" ("" = bare, both schemes)
}

// caddyAddressHost splits a single Caddyfile site address into its host
// and scheme, stripping any trailing ":<port>".
func caddyAddressHost(addr string) (host, scheme string) {
	addr = strings.TrimSpace(addr)
	switch {
	case strings.HasPrefix(addr, "https://"):
		scheme = "https"
		addr = addr[len("https://"):]
	case strings.HasPrefix(addr, "http://"):
		scheme = "http"
		addr = addr[len("http://"):]
	}
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		if _, err := strconv.Atoi(addr[idx+1:]); err == nil {
			addr = addr[:idx]
		}
	}
	return addr, scheme
}

// findCaddyfileDomainBlocks scans lines for every top-level site block
// whose address list contains domainName, tracking brace depth so nested
// directives (route {}, tls {}, coraza_waf {}, the global options block,
// ...) are never mistaken for a new top-level block.
func findCaddyfileDomainBlocks(lines []string, domainName string) []caddyBlockMatch {
	depth := 0
	type pending struct {
		headerLine int
		addrPart   string
	}
	var cur *pending
	var matches []caddyBlockMatch

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if depth == 0 && !strings.HasPrefix(line, "#") {
			if idx := strings.LastIndex(line, "{"); idx >= 0 {
				cur = &pending{headerLine: i, addrPart: strings.TrimSpace(line[:idx])}
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth == 0 && cur != nil {
			if cur.addrPart != "" {
				for _, tok := range strings.Split(cur.addrPart, ",") {
					host, scheme := caddyAddressHost(tok)
					if host == domainName {
						matches = append(matches, caddyBlockMatch{headerLine: cur.headerLine, closeLine: i, scheme: scheme})
						break
					}
				}
			}
			cur = nil
		}
	}
	return matches
}

// pickCaddyfileBlockForTLS prefers a block that can actually terminate TLS
// (bare or explicit https://) over an http://-only duplicate of the same
// domain, since that's where a `tls` directive belongs.
func pickCaddyfileBlockForTLS(matches []caddyBlockMatch) (caddyBlockMatch, bool) {
	for _, m := range matches {
		if m.scheme != "http" {
			return m, true
		}
	}
	if len(matches) > 0 {
		return matches[0], true
	}
	return caddyBlockMatch{}, false
}

// tlsDirective locates an existing `tls ...` line or `tls { ... }`
// sub-block directly inside a site block's body.
type tlsDirective struct {
	startLine, endLine int
	indent             string
}

func findTLSDirective(lines []string, bodyStart, bodyEnd int) (tlsDirective, bool) {
	depth := 0
	for i := bodyStart; i <= bodyEnd && i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if depth == 0 && (trimmed == "tls" || strings.HasPrefix(trimmed, "tls ") || strings.HasPrefix(trimmed, "tls{")) {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			if strings.HasSuffix(trimmed, "{") {
				d := 1
				j := i
				for j < bodyEnd && d > 0 {
					j++
					d += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
				}
				return tlsDirective{startLine: i, endLine: j, indent: indent}, true
			}
			return tlsDirective{startLine: i, endLine: i, indent: indent}, true
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
	}
	return tlsDirective{}, false
}

// inferCaddyBlockIndent guesses the body indentation of a site block from
// its first non-blank line, falling back to a reasonable default.
func inferCaddyBlockIndent(lines []string, bodyStart, bodyEnd int) string {
	for i := bodyStart; i <= bodyEnd && i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if trimmed == "" {
			continue
		}
		return lines[i][:len(lines[i])-len(trimmed)]
	}
	return "    "
}

// setCaddyfileCustomTLS replaces (or inserts) the `tls <cert> <key>` line
// inside block, returning the whole updated file as lines.
func setCaddyfileCustomTLS(lines []string, block caddyBlockMatch, certPath, keyPath string) []string {
	if dir, ok := findTLSDirective(lines, block.headerLine+1, block.closeLine-1); ok {
		newLine := dir.indent + "tls " + certPath + " " + keyPath
		result := make([]string, 0, len(lines))
		result = append(result, lines[:dir.startLine]...)
		result = append(result, newLine)
		result = append(result, lines[dir.endLine+1:]...)
		return result
	}

	indent := inferCaddyBlockIndent(lines, block.headerLine+1, block.closeLine-1)
	newLine := indent + "tls " + certPath + " " + keyPath
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:block.closeLine]...)
	result = append(result, newLine)
	result = append(result, lines[block.closeLine:]...)
	return result
}

// caddyfileDomainSSLInfo reports the current SSL setting for domainName as
// configured directly in the main Caddyfile, mirroring what
// `opencli domains-ssl <domain> status`/`info` reports for opencli-managed
// domains. found is false when domainName has no matching site block at
// all (i.e. it's neither opencli-managed nor hand-configured in Caddy).
func caddyfileDomainSSLInfo(domainName string) (currentSetting, keys string, found bool) {
	content, err := os.ReadFile(sslMainCaddyfilePath)
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(string(content), "\n")
	block, ok := pickCaddyfileBlockForTLS(findCaddyfileDomainBlocks(lines, domainName))
	if !ok {
		return "", "", false
	}

	dir, hasTLS := findTLSDirective(lines, block.headerLine+1, block.closeLine-1)
	if !hasTLS {
		return "autossl", "", true
	}

	if dir.startLine == dir.endLine {
		fields := strings.Fields(strings.TrimSpace(lines[dir.startLine]))
		if len(fields) == 3 {
			certBytes, certErr := os.ReadFile(fields[1])
			keyBytes, keyErr := os.ReadFile(fields[2])
			if certErr == nil && keyErr == nil {
				return "custom ssl", strings.TrimSpace(string(certBytes)) + "\n" + strings.TrimSpace(string(keyBytes)), true
			}
		}
	}
	return "autossl", "", true
}

// applyCaddyfileCustomSSL writes the pasted cert/key content under
// sslCustomCaddySSLDir and points domainName's site block at them directly
// in the main Caddyfile (the same host paths are reachable from inside the
// caddy container, since /etc/openpanel/caddy is bind-mounted through
// as-is), then validates and reloads Caddy. On validation failure the
// Caddyfile is reverted, matching domains_caddy_files.go's own
// write-then-validate-then-revert pattern for the same file.
func applyCaddyfileCustomSSL(domainName, certificate, privateKey string) (message string, err error) {
	original, readErr := os.ReadFile(sslMainCaddyfilePath)
	if readErr != nil {
		return "", readErr
	}
	lines := strings.Split(string(original), "\n")
	block, ok := pickCaddyfileBlockForTLS(findCaddyfileDomainBlocks(lines, domainName))
	if !ok {
		return "", fmt.Errorf("domain %q was not found in the main Caddyfile", domainName)
	}

	certDir := filepath.Join(sslCustomCaddySSLDir, domainName)
	if mkErr := os.MkdirAll(certDir, 0o755); mkErr != nil {
		return "", mkErr
	}
	certPath := filepath.Join(certDir, "fullchain.pem")
	keyPath := filepath.Join(certDir, "key.pem")
	if writeErr := os.WriteFile(certPath, []byte(certificate), 0o644); writeErr != nil {
		return "", writeErr
	}
	if writeErr := os.WriteFile(keyPath, []byte(privateKey), 0o644); writeErr != nil {
		return "", writeErr
	}

	newContent := strings.Join(setCaddyfileCustomTLS(lines, block, certPath, keyPath), "\n")
	if writeErr := os.WriteFile(sslMainCaddyfilePath, []byte(newContent), 0o644); writeErr != nil {
		return "", writeErr
	}

	_, stderr, exitCode, verr := caddyValidateRun()
	if verr != nil {
		os.WriteFile(sslMainCaddyfilePath, original, 0o644)
		return "", verr
	}
	if exitCode != 0 {
		os.WriteFile(sslMainCaddyfilePath, original, 0o644)
		return "", fmt.Errorf("Caddyfile validation failed, changes reverted: %s", strings.TrimSpace(stderr))
	}
	if rerr := caddyReloadRun(); rerr != nil {
		return "", rerr
	}
	return "Updated " + domainName + " to use custom SSL.", nil
}
