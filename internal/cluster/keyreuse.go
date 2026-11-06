package cluster

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

// caddyfilePath is a var so tests can point it at a temp file
var caddyfilePath = "/etc/openpanel/caddy/Caddyfile"

const reuseKeysLine = "reuse_private_keys"

// addKeyReuse puts reuse_private_keys in the tls block of the HOSTNAME IP site, the cluster pins that key and shortlived certs renew every few days
func addKeyReuse(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	inIP, changed := false, false
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.Contains(t, "# START HOSTNAME IP #"):
			inIP = true
		case strings.Contains(t, "# END HOSTNAME IP #"):
			inIP = false
		case inIP && t == reuseKeysLine:
			return src, false
		case inIP && strings.HasPrefix(t, "tls") && strings.HasSuffix(t, "{"):
			out = append(out, line)
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			out = append(out, indent+"  "+reuseKeysLine)
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return src, false
	}
	return strings.Join(out, "\n"), true
}

// ensureKeyReuse keeps the 2087 key stable across renewals so the pin other servers saved stays valid
func ensureKeyReuse() {
	raw, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return
	}
	updated, changed := addKeyReuse(string(raw))
	if !changed {
		return
	}
	info, err := os.Stat(caddyfilePath)
	if err != nil {
		return
	}
	// written in place, caddy has the file bind mounted and a rename would leave it on the old inode
	if err := os.WriteFile(caddyfilePath, []byte(updated), info.Mode().Perm()); err != nil {
		return
	}
	if out, err := exec.Command("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput(); err != nil {
		// put it back if caddy doesn't take it, a pin that breaks later beats a dead proxy now
		os.WriteFile(caddyfilePath, raw, info.Mode().Perm())
		exec.Command("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Run()
		log.Printf("cluster: caddy rejected reuse_private_keys: %s", strings.TrimSpace(string(out)))
	}
}
