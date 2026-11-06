package handlers

import (
	"encoding/json"
	"strings"
	"time"
)

// acmeDiagnosis is the last certificate error caddy logged for a domain, in words an admin can act on
type acmeDiagnosis struct {
	Found  bool   `json:"found"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	Time   string `json:"time,omitempty"`
}

// acmeReasons maps pieces of caddy and let's encrypt errors to what the admin should check, first match wins
var acmeReasons = []struct{ match, reason string }{
	{"rateLimited", "Let's Encrypt rate limit reached for this domain. Caddy retries on its own once the limit resets."},
	{"too many certificates", "Let's Encrypt rate limit reached for this domain. Caddy retries on its own once the limit resets."},
	{"caa", "A CAA DNS record of the domain doesn't allow Let's Encrypt to issue certificates for it."},
	{"rejectedIdentifier", "Let's Encrypt refuses to issue certificates for this name, it's a reserved or blocked domain."},
	{"NXDOMAIN", "The domain doesn't resolve. Add an A (and AAAA) record pointing to this server."},
	{"no valid A records", "The domain has no A or AAAA record. Point it to this server's IP address."},
	{"DNS problem", "Let's Encrypt couldn't look up the domain. Check its DNS records and nameservers."},
	{"Timeout during connect", "Let's Encrypt couldn't connect to this server. Check that ports 80 and 443 are open in the firewall."},
	{"Connection refused", "Let's Encrypt couldn't connect to this server. Check that ports 80 and 443 are open in the firewall."},
	{"unauthorized", "The validation request didn't reach this server. Check that the domain's A/AAAA records point to this server's IP and not to a proxy or another host."},
	{"Invalid response", "The validation request didn't reach this server. Check that the domain's A/AAAA records point to this server's IP and not to a proxy or another host."},
	{"certificate not allowed", "Caddy wasn't allowed to request a certificate for this name, the domain isn't set up on this server."},
}

// diagnoseACME finds the newest certificate error for domain or its www name in caddy's json log lines
func diagnoseACME(logs, domain string) acmeDiagnosis {
	domain = strings.ToLower(domain)
	var last acmeDiagnosis
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry struct {
			Level      string  `json:"level"`
			Ts         float64 `json:"ts"`
			Logger     string  `json:"logger"`
			Msg        string  `json:"msg"`
			Identifier string  `json:"identifier"`
			Error      string  `json:"error"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Level != "error" || !strings.HasPrefix(entry.Logger, "tls") {
			continue
		}
		id := strings.ToLower(entry.Identifier)
		// "will retry" lines carry the name only in the error, as "[example.com] Obtain: ..."
		if id == "" && strings.HasPrefix(entry.Error, "[") {
			if end := strings.Index(entry.Error, "]"); end > 0 {
				id = strings.ToLower(entry.Error[1:end])
			}
		}
		if id != domain && id != "www."+domain {
			continue
		}
		text := entry.Error
		if text == "" {
			text = entry.Msg
		}
		last = acmeDiagnosis{Found: true, Error: text, Reason: "Let's Encrypt couldn't issue a certificate, the error below has the details."}
		if entry.Ts > 0 {
			last.Time = time.Unix(int64(entry.Ts), 0).UTC().Format("2006-01-02 15:04 UTC")
		}
		lower := strings.ToLower(text)
		for _, r := range acmeReasons {
			if strings.Contains(lower, strings.ToLower(r.match)) {
				last.Reason = r.reason
				break
			}
		}
	}
	return last
}
