// This file implements BIND DNS clustering (allow-transfer/also-notify
// management in named.conf.options, rndc/SSH reachability checks against
// slaves, and zone syncing to a new slave).
package handlers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// DNSCluster bundles the /domains/dns-cluster handlers.
type DNSCluster struct {
	Sessions *auth.Manager
	PublicIP string
}

// DNSClusterConfigPath / DNSClusterRNDCKeyFile / DNSClusterRNDCPort are the
// BIND config path, rndc key file, and rndc port used by the DNS cluster
// handlers.
var (
	DNSClusterConfigPath  = "/etc/bind/named.conf.options"
	DNSClusterRNDCKeyFile = "/etc/bind/rndc.key"
	DNSClusterRNDCPort    = "953"
)

type dnsClusterConfig struct {
	AllowTransfer []string
	AlsoNotify    []string
	Enabled       bool
	RawContent    string
}

var dnsDirectiveBlockRe = regexp.MustCompile(`(allow-transfer|also-notify)\s*\{\s*([^}]+)\s*\};`)

// dnsDirectiveUncommented reports whether the given directive appears, as
// a whole word, on any line that isn't commented out (doesn't start with
// "//" or "#" after leading whitespace). Go's RE2 engine has no lookahead
// support, so this walks lines directly rather than using a single regex.
func dnsDirectiveUncommented(content, directive string) bool {
	wordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(directive) + `\b`)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if wordRe.MatchString(line) {
			return true
		}
	}
	return false
}

var (
	dnsBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	dnsSlashCommentRe = regexp.MustCompile(`(?m)//.*$`)
	dnsHashCommentRe  = regexp.MustCompile(`(?m)#.*$`)
)

// dnsStripComments strips //, /* */, and # comments via three sequential
// regex passes (Go's RE2 can express each comment style individually, but
// not as cleanly combined into one alternation with mixed
// dot-matches-newline scoping). Produces the same result for any
// well-formed BIND config, which is the only realistic input here.
func dnsStripComments(content string) string {
	content = dnsBlockCommentRe.ReplaceAllString(content, "")
	content = dnsSlashCommentRe.ReplaceAllString(content, "")
	content = dnsHashCommentRe.ReplaceAllString(content, "")
	return content
}

func dnsParseIPs(blocks []string) []string {
	var ips []string
	for _, block := range blocks {
		for _, ip := range strings.Split(block, ";") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

// extractDNSClusterConfig reads path and extracts the allow-transfer/
// also-notify IP lists and enabled state.
func extractDNSClusterConfig(path string) (dnsClusterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return dnsClusterConfig{}, err
	}
	content := string(raw)

	isAllowTransferUncommented := dnsDirectiveUncommented(content, "allow-transfer")
	isAlsoNotifyUncommented := dnsDirectiveUncommented(content, "also-notify")

	noComments := dnsStripComments(content)

	var allowTransferBlocks, alsoNotifyBlocks []string
	for _, m := range dnsDirectiveBlockRe.FindAllStringSubmatch(noComments, -1) {
		switch m[1] {
		case "allow-transfer":
			allowTransferBlocks = append(allowTransferBlocks, m[2])
		case "also-notify":
			alsoNotifyBlocks = append(alsoNotifyBlocks, m[2])
		}
	}

	return dnsClusterConfig{
		AllowTransfer: dnsParseIPs(allowTransferBlocks),
		AlsoNotify:    dnsParseIPs(alsoNotifyBlocks),
		Enabled:       isAllowTransferUncommented && isAlsoNotifyUncommented,
		RawContent:    content,
	}, nil
}

var dnsInlineBlockRe = regexp.MustCompile(`(?m)^([ \t]*)(allow-transfer|also-notify)\s*\{([^}]*)\};`)

// dnsRewriteInlineBlocks is the shared core of add_ip_to_config() and
// remove_ip_from_config(): re-render each allow-transfer/also-notify
// block's entries (deduplicated, in order) through transform, erroring if
// either directive block is missing from the file entirely.
func dnsRewriteInlineBlocks(content string, transform func(entries []string) []string) (string, error) {
	found := map[string]bool{}

	newContent := dnsInlineBlockRe.ReplaceAllStringFunc(content, func(match string) string {
		groups := dnsInlineBlockRe.FindStringSubmatch(match)
		indent, name, inner := groups[1], groups[2], groups[3]
		found[name] = true

		var entries []string
		seen := map[string]bool{}
		for _, item := range strings.Split(inner, ";") {
			item = strings.TrimSpace(item)
			if item != "" && !seen[item] {
				entries = append(entries, item)
				seen[item] = true
			}
		}

		entries = transform(entries)

		var innerInline string
		if len(entries) > 0 {
			innerInline = strings.Join(entries, ";") + ";"
		}
		return indent + name + " {" + innerInline + "};"
	})

	var missing []string
	for _, name := range []string{"allow-transfer", "also-notify"} {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("Block(s) not found in config: %s", strings.Join(missing, ", "))
	}
	return newContent, nil
}

// addIPToConfig adds newIP to both the allow-transfer and also-notify
// blocks in path, if not already present.
func addIPToConfig(path, newIP string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	newContent, err := dnsRewriteInlineBlocks(string(raw), func(entries []string) []string {
		for _, e := range entries {
			if e == newIP {
				return entries
			}
		}
		return append(entries, newIP)
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(newContent), 0644)
}

// removeIPFromConfig removes ipToRemove from both the allow-transfer and
// also-notify blocks in path.
func removeIPFromConfig(path, ipToRemove string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	newContent, err := dnsRewriteInlineBlocks(string(raw), func(entries []string) []string {
		var out []string
		for _, e := range entries {
			if e != ipToRemove {
				out = append(out, e)
			}
		}
		return out
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(newContent), 0644)
}

// updateDNSClusterConfigFile comments/uncomments the allow-transfer/
// also-notify directive lines in place.
func updateDNSClusterConfigFile(path string, enable bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	hadTrailingNewline := strings.HasSuffix(string(raw), "\n")

	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if enable {
			switch {
			case strings.HasPrefix(trimmed, "// allow-transfer"):
				lines[i] = strings.Replace(line, "// allow-transfer", "allow-transfer", 1)
			case strings.HasPrefix(trimmed, "// also-notify"):
				lines[i] = strings.Replace(line, "// also-notify", "also-notify", 1)
			}
		} else {
			switch {
			case strings.HasPrefix(trimmed, "allow-transfer"):
				lines[i] = strings.Replace(line, "allow-transfer", "// allow-transfer", 1)
			case strings.HasPrefix(trimmed, "also-notify"):
				lines[i] = strings.Replace(line, "also-notify", "// also-notify", 1)
			}
		}
	}

	out := strings.Join(lines, "\n")
	if !hadTrailingNewline {
		out = strings.TrimSuffix(out, "\n")
	}
	return os.WriteFile(path, []byte(out), 0644)
}

// dnsRestartServiceRun issues a fire-and-forget podman restart: the caller
// doesn't wait for it to finish.
var dnsRestartServiceRun = func() {
	cmd, err := podman.Command("default", "restart", "openpanel_dns")
	if err != nil {
		return
	}
	_ = cmd.Start()
}

// dnsRNDCCommandRun runs an rndc command via podman exec into the DNS
// container, with a 10s timeout.
var dnsRNDCCommandRun = func(slaveIP string, args ...string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	argv := podman.Argv("default", append([]string{
		"exec", "openpanel_dns", "rndc",
		"-s", slaveIP, "-p", DNSClusterRNDCPort, "-k", DNSClusterRNDCKeyFile,
	}, args...)...)
	env, err := podman.Env("default", nil)
	if err != nil {
		return false, err.Error()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return false, string(out) + err.Error()
		}
	}
	return err == nil, string(out)
}

func dnsSlaveReachableViaRNDC(slaveIP string) bool {
	ok, output := dnsRNDCCommandRun(slaveIP, "status")
	return ok && strings.Contains(output, "number of zones")
}

// dnsSSHRun is injectable so tests never shell out to a real ssh binary.
// timedOut is reported separately from err so callers can distinguish a
// context-deadline timeout from any other failure -- ServeDNSClusterInfo
// needs a three-way status (timeout / error / success).
var dnsSSHRun = func(host string, timeout time.Duration, remoteCmd string) (code int, stdout, stderr string, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"root@"+host,
		remoteCmd,
	)
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return 0, so.String(), se.String(), true, ctx.Err()
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		return exitErr.ExitCode(), so.String(), se.String(), false, nil
	}
	return 0, so.String(), se.String(), false, runErr
}

// dnsSlaveReachableViaSSH is not currently called anywhere in this
// handler -- every real SSH reachability check here is a separate inline
// dnsSSHRun call, not this helper. Kept for completeness rather than
// removed.
func dnsSlaveReachableViaSSH(slaveIP string) bool {
	code, _, _, timedOut, err := dnsSSHRun(slaveIP, 5*time.Second, "uname -a")
	return !timedOut && err == nil && code == 0
}

// dnsDomainsAllRun is injectable; runs `opencli domains-all`.
var dnsDomainsAllRun = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "opencli", "domains-all").Output()
	return string(out), err
}

// syncExistingZonesToSlave syncs existing zones to a newly added slave.
// Called as a fire-and-forget background goroutine from the "create"
// action.
func syncExistingZonesToSlave(slaveIP, masterIP string) {
	out, err := dnsDomainsAllRun()
	if err != nil {
		return
	}
	var domains []string
	for _, d := range strings.Split(out, "\n") {
		d = strings.TrimSpace(d)
		if d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return
	}

	if dnsSlaveReachableViaRNDC(slaveIP) {
		for _, domain := range domains {
			zoneDef := fmt.Sprintf(
				`%s { type slave; masters { %s; }; file "/etc/bind/zones/%s.zone"; allow-notify { %s; }; };`,
				domain, masterIP, domain, masterIP,
			)
			ok, output := dnsRNDCCommandRun(slaveIP, "addzone", zoneDef)
			if !ok && !strings.Contains(output, "already exists") {
				// Best-effort background sync; failures are silently
				// ignored here since nothing awaits this goroutine.
				continue
			}
		}
		return
	}

	_, existingConf, _, _, _ := dnsSSHRun(slaveIP, 10*time.Second, "cat /etc/bind/named.conf.local")

	var newStanzas []string
	for _, domain := range domains {
		if strings.Contains(existingConf, `zone "`+domain+`"`) {
			continue
		}
		newStanzas = append(newStanzas, fmt.Sprintf(
			`zone "%s" { type slave; masters { %s; }; file "/etc/bind/zones/%s.zone"; };`,
			domain, masterIP, domain,
		))
	}
	if len(newStanzas) == 0 {
		return
	}

	appendBlock := strings.Join(newStanzas, "\n")
	appendCmd := fmt.Sprintf(
		"printf '\\n%s\\n' >> /etc/bind/named.conf.local && named-checkconf && service bind9 restart",
		appendBlock,
	)
	_, _, _, _, _ = dnsSSHRun(slaveIP, 30*time.Second, appendCmd)
}

func dnsUniqueStrings(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// ServeDNSCluster handles GET/POST /domains/dns-cluster.
func (d *DNSCluster) ServeDNSCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		action := r.PostFormValue("action")

		switch action {
		case "enable", "disable":
			if err := updateDNSClusterConfigFile(DNSClusterConfigPath, action == "enable"); err != nil {
				auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("Failed to %s DNS cluster: %s", action, err.Error()), "error")
			} else {
				dnsRestartServiceRun()
				auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("DNS cluster %sd successfully.", action), "success")
			}

		case "create":
			ip := r.PostFormValue("ip")
			if ip == "" {
				auth.AddFlash(w, r, d.Sessions, "IP address is required.", "error")
			} else if parsed := net.ParseIP(ip); parsed == nil {
				auth.AddFlash(w, r, d.Sessions, "Invalid IP address format.", "error")
			} else if parsed.To4() == nil {
				auth.AddFlash(w, r, d.Sessions, "Only IPv4 addresses are currently supported.", "error")
				// Falls through to the normal re-render below instead of
				// returning early here, matching every other validation
				// branch's flash+re-render behavior -- returning early
				// with no response would be inconsistent with the rest
				// of this handler.
			} else {
				extracted, _ := extractDNSClusterConfig(DNSClusterConfigPath)
				allIPs := map[string]bool{}
				for _, v := range append(append([]string{}, extracted.AllowTransfer...), extracted.AlsoNotify...) {
					allIPs[v] = true
				}
				if allIPs[ip] {
					auth.AddFlash(w, r, d.Sessions, "IP address already exists in configuration.", "error")
				} else if !dnsSlaveReachableViaRNDC(ip) {
					auth.AddFlash(w, r, d.Sessions, fmt.Sprintf(
						"Cannot reach %s via rndc on port %s. Ensure the slave has allow-new-zones yes, a matching rndc key, and controls block configured. See: https://openpanel.com/docs/articles/domains/how-to-setup-dns-cluster-in-openpanel/",
						ip, DNSClusterRNDCPort), "error")
				} else if err := addIPToConfig(DNSClusterConfigPath, ip); err != nil {
					auth.AddFlash(w, r, d.Sessions, "Failed to add IP to DNS cluster: "+err.Error(), "error")
				} else {
					dnsRestartServiceRun()
					auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("IP %s added to DNS cluster successfully.", ip), "success")
					go syncExistingZonesToSlave(ip, d.PublicIP)
				}
			}

		case "delete":
			ip := r.PostFormValue("ip")
			if ip == "" {
				auth.AddFlash(w, r, d.Sessions, "IP address is required.", "error")
			} else {
				extracted, _ := extractDNSClusterConfig(DNSClusterConfigPath)
				allIPs := map[string]bool{}
				for _, v := range append(append([]string{}, extracted.AllowTransfer...), extracted.AlsoNotify...) {
					allIPs[v] = true
				}
				if !allIPs[ip] {
					auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("IP %s not found in DNS cluster configuration.", ip), "error")
				} else if err := removeIPFromConfig(DNSClusterConfigPath, ip); err != nil {
					auth.AddFlash(w, r, d.Sessions, "Failed to remove IP from DNS cluster: "+err.Error(), "error")
				} else {
					dnsRestartServiceRun()
					auth.AddFlash(w, r, d.Sessions, fmt.Sprintf("IP %s removed from DNS cluster successfully.", ip), "success")
				}
			}
		}
	}

	extracted, err := extractDNSClusterConfig(DNSClusterConfigPath)
	if err != nil {
		extracted = dnsClusterConfig{}
	}

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]interface{}{
			"allow_transfer": nonNilStrings(extracted.AllowTransfer),
			"also_notify":    nonNilStrings(extracted.AlsoNotify),
			"enabled":        extracted.Enabled,
			"raw_content":    extracted.RawContent,
		})
		return
	}

	webtemplates.Render(w, "domains_cluster.html", mergeChrome(map[string]interface{}{
		"AllowTransfer":    extracted.AllowTransfer,
		"AlsoNotify":       extracted.AlsoNotify,
		"Enabled":          extracted.Enabled,
		"RawContent":       extracted.RawContent,
		"AllIPs":           dnsUniqueStrings(extracted.AllowTransfer, extracted.AlsoNotify),
		"AllowTransferSet": dnsStringSet(extracted.AllowTransfer),
		"AlsoNotifySet":    dnsStringSet(extracted.AlsoNotify),
		"CSRFToken":        csrf.Token(r),
		"Flashes":          auth.PopFlashes(w, r, d.Sessions),
	}, r, "DNS Cluster"))
}

func dnsStringSet(s []string) map[string]bool {
	set := make(map[string]bool, len(s))
	for _, v := range s {
		set[v] = true
	}
	return set
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ServeDNSClusterInfo handles GET /domains/dns-cluster/{ip}.
func (d *DNSCluster) ServeDNSClusterInfo(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	result := map[string]interface{}{"ip": ip, "status": "failed", "output": nil, "error": nil}

	ok, output := dnsRNDCCommandRun(ip, "status")
	if ok && strings.Contains(output, "number of zones") {
		result["status"] = "success"
		result["output"] = strings.TrimSpace(output)
		result["method"] = "rndc"
		writeJSON(w, result)
		return
	}

	code, stdout, stderr, timedOut, err := dnsSSHRun(ip, 5*time.Second, "uname -a")
	if timedOut {
		result["status"] = "timeout"
		result["error"] = "Connection timed out. Please check documentation: https://openpanel.com/docs/articles/domains/how-to-setup-dns-cluster-in-openpanel/"
		writeJSON(w, result)
		return
	}
	if err != nil {
		result["status"] = "error"
		result["error"] = err.Error()
		writeJSON(w, result)
		return
	}

	if code == 0 {
		result["status"] = "success"
		result["method"] = "ssh"
	} else {
		result["status"] = "error"
	}
	if strings.TrimSpace(stdout) != "" {
		result["output"] = strings.TrimSpace(stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		result["error"] = strings.TrimSpace(stderr)
	}
	writeJSON(w, result)
}
