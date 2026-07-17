// Package panelinfo answers questions about the OpenPanel (user-facing,
// port 2083) side of the install -- as opposed to OpenAdmin itself, which
// internal/bootstrap already covers. Used by the login page's "Switch to
// OpenPanel" link.
package panelinfo

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// CaddyfilePath matches bootstrap.CaddyfilePath; kept separate (var, not a
// shared reference) so tests can override it independently.
var CaddyfilePath = "/etc/openpanel/caddy/Caddyfile"

var (
	portOnce  sync.Once
	portValue string

	domainOnce  sync.Once
	domainValue string
)

// Port shells out to `opencli port`, defaulting to 2083 on any failure or
// out-of-range value. Cached for the process lifetime.
func Port() string {
	portOnce.Do(func() {
		portValue = queryPort()
	})
	return portValue
}

func queryPort() string {
	out, err := exec.Command("opencli", "port").Output()
	if err != nil {
		return "2083"
	}
	port := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return "2083"
	}
	return port
}

// Domain reads the "# START USERPANEL DOMAIN #" / "# END USERPANEL DOMAIN #"
// block of the Caddyfile for a configured domain, falling back to `opencli
// domain` (the admin's own domain command) when no such block/domain is
// set.
func Domain() string {
	domainOnce.Do(func() {
		domainValue = readUserpanelDomain()
		if domainValue == "" {
			domainValue = adminDomain()
		}
	})
	return domainValue
}

var userpanelDomainLineRe = regexp.MustCompile(`^([\w.-]+)\s*\{`)

func readUserpanelDomain() string {
	f, err := os.Open(CaddyfilePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.Contains(line, "# START USERPANEL DOMAIN #"):
			inBlock = true
			continue
		case strings.Contains(line, "# END USERPANEL DOMAIN #"):
			return ""
		}
		if inBlock {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if m := userpanelDomainLineRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func adminDomain() string {
	out, err := exec.Command("opencli", "domain").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
