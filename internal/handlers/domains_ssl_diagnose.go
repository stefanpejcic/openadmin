// finds the newest failed certificate attempt for a domain in Caddy's log, for the SSL page
package handlers

import (
	"bufio"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// caddyLogTail is how many of Caddy's newest log lines are searched
const caddyLogTail = "5000"

// caddyLogRun returns Caddy's recent log; injectable so tests never call podman
var caddyLogRun = func() (string, error) {
	stdout, stderr, _, err := podmanExecCapture("default", "logs", "--tail", caddyLogTail, "caddy")
	return stdout + "\n" + stderr, err
}

// caddyCertError is the newest certificate failure Caddy logged for a domain
type caddyCertError struct {
	Found  bool   `json:"found"`
	Reason string `json:"reason,omitempty"`
	Time   string `json:"time,omitempty"`
}

var (
	// "[example.com] Obtain: [example.com] " in front of a retry error
	caddyObtainPrefix = regexp.MustCompile(`^(\[[^\]]+\]\s*)?Obtain:\s*(\[[^\]]+\]\s*)?`)
	// " (ca=https://...)" after it
	caddyCASuffix = regexp.MustCompile(`\s*\(ca=[^)]*\)\s*$`)
)

// lastCaddyCertError scans Caddy's JSON log lines for the newest tls error about domain
func lastCaddyCertError(logs, domain string) caddyCertError {
	var last caddyCertError
	sc := bufio.NewScanner(strings.NewReader(logs))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, domain) || !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		var e struct {
			Level      string  `json:"level"`
			Logger     string  `json:"logger"`
			TS         float64 `json:"ts"`
			Identifier string  `json:"identifier"`
			Error      string  `json:"error"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Level != "error" || !strings.HasPrefix(e.Logger, "tls") || e.Error == "" {
			continue
		}
		if e.Identifier != domain && !strings.Contains(e.Error, "["+domain+"]") {
			continue
		}
		reason := caddyCASuffix.ReplaceAllString(caddyObtainPrefix.ReplaceAllString(e.Error, ""), "")
		last = caddyCertError{Found: true, Reason: strings.TrimSpace(reason)}
		if e.TS > 0 {
			last.Time = time.Unix(int64(e.TS), 0).UTC().Format("2006-01-02 15:04:05")
		}
	}
	return last
}

// diagnoseCertificate returns the newest certificate error Caddy logged for domain
func diagnoseCertificate(domain string) caddyCertError {
	logs, err := caddyLogRun()
	if err != nil && logs == "" {
		return caddyCertError{}
	}
	return lastCaddyCertError(logs, domain)
}
