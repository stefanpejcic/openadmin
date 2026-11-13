// This file implements /security/bot-control: per-site AI crawler control and the "Who's hitting me" traffic breakdown.
package handlers

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// BotControl bundles the /security/bot-control handlers.
type BotControl struct {
	Sessions *auth.Manager
	MySQL    *sql.DB
}

// CaddyDomlogsDir is where Caddy writes per-domain JSON access logs.
var CaddyDomlogsDir = "/var/log/caddy/domlogs"

// BotASNPrefixesPath is an optional TSV (cidr\tasn\torg) for local IP-to-ASN mapping.
var BotASNPrefixesPath = "/etc/openpanel/asn_prefixes.tsv"

// botReloadCaddy returns caddy's own error so the admin sees why a config was rejected
var botReloadCaddy = func(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput()
	if err != nil {
		if msg := botCaddyErrorLine(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// botCaddyErrorLine pulls caddy's reason out of its output, older caddy prints "Error: ...", newer logs it as a JSON error line
func botCaddyErrorLine(out string) string {
	msg := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error: ") {
			return strings.TrimPrefix(line, "Error: ")
		}
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Level == "error" && entry.Msg != "" {
			msg = entry.Msg
		}
	}
	if msg != "" {
		return msg
	}
	return strings.TrimSpace(out)
}

var errBotNoAnchor = errors.New("no place for the bot control rule in the domain's Caddy config")

var botRestartOpenPanel = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "podman", "restart", "openpanel").Run()
}

const botModuleName = "bot_control"

func botModuleEnabled() bool {
	return slices.Contains(modulesEnabledList(ModulesConfigFilePath), botModuleName)
}

// botSetModuleEnabled toggles bot_control in openpanel.config's enabled_modules list and syncs OpenPanel.
func botSetModuleEnabled(ctx context.Context, enable bool) (restarted bool, err error) {
	raw, err := os.ReadFile(ModulesConfigFilePath)
	if err != nil {
		return false, err
	}

	current := parseEnabledModules(ModulesConfigFilePath)
	updated := make([]string, 0, len(current)+1)
	seen := false
	for _, mod := range current {
		mod = strings.TrimSpace(strings.Trim(mod, `"'`))
		if mod == "" {
			continue
		}
		if mod == botModuleName {
			if !enable || seen {
				continue
			}
			seen = true
		}
		updated = append(updated, mod)
	}
	if enable && !seen {
		updated = append(updated, botModuleName)
	}

	newLine := `enabled_modules="` + strings.Join(updated, ",") + `"`
	lines := strings.SplitAfter(string(raw), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "enabled_modules=") {
			lines[i] = newLine + "\n"
			replaced = true
			break
		}
	}
	out := strings.Join(lines, "")
	if !replaced {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += newLine + "\n"
	}
	if err := os.WriteFile(ModulesConfigFilePath, []byte(out), 0o644); err != nil {
		return false, err
	}

	enabledModulesCache.mu.Lock()
	if enabledModulesCache.m != nil {
		delete(enabledModulesCache.m, ModulesConfigFilePath)
	}
	enabledModulesCache.mu.Unlock()

	// Keep /root/openpanel_restart_needed present (truncated, never deleted) because
	// it is bind-mounted into the openpanel container and conmon fails restart if missing.
	_ = os.WriteFile(ModulesOpenpanelRestartFlagPath, []byte(""), 0o644)
	if botRestartOpenPanel(ctx) == nil {
		return true, nil
	}
	_ = os.WriteFile(ModulesOpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0o644)
	return false, nil
}

type botPolicy string

const (
	botPolicyAllow     botPolicy = "allow"
	botPolicyRateLimit botPolicy = "ratelimit"
	botPolicyBlock     botPolicy = "block"
)

type botCategory string

const (
	botCatAITraining   botCategory = "AI Training"
	botCatAISearch     botCategory = "AI Search"
	botCatScraper      botCategory = "Scraper"
	botCatSearchEngine botCategory = "Search Engine"
	botCatHuman        botCategory = "Browser / Other"
)

type botSignature struct {
	ID       string
	Name     string
	Operator string
	Category botCategory
	Token    string
}

var knownBots = []botSignature{
	{ID: "gptbot", Name: "GPTBot", Operator: "OpenAI", Category: botCatAITraining, Token: "gptbot"},
	{ID: "chatgpt-user", Name: "ChatGPT-User", Operator: "OpenAI", Category: botCatAISearch, Token: "chatgpt-user"},
	{ID: "oai-searchbot", Name: "OAI-SearchBot", Operator: "OpenAI", Category: botCatAISearch, Token: "oai-searchbot"},
	{ID: "claudebot", Name: "ClaudeBot", Operator: "Anthropic", Category: botCatAITraining, Token: "claudebot"},
	{ID: "claude-web", Name: "Claude-Web", Operator: "Anthropic", Category: botCatAISearch, Token: "claude-web"},
	{ID: "anthropic-ai", Name: "anthropic-ai", Operator: "Anthropic", Category: botCatAITraining, Token: "anthropic-ai"},
	{ID: "bytespider", Name: "Bytespider", Operator: "ByteDance", Category: botCatAITraining, Token: "bytespider"},
	{ID: "ccbot", Name: "CCBot", Operator: "Common Crawl", Category: botCatAITraining, Token: "ccbot"},
	{ID: "perplexitybot", Name: "PerplexityBot", Operator: "Perplexity", Category: botCatAISearch, Token: "perplexitybot"},
	{ID: "perplexity-user", Name: "Perplexity-User", Operator: "Perplexity", Category: botCatAISearch, Token: "perplexity-user"},
	{ID: "amazonbot", Name: "Amazonbot", Operator: "Amazon", Category: botCatAITraining, Token: "amazonbot"},
	{ID: "meta-externalagent", Name: "Meta-ExternalAgent", Operator: "Meta", Category: botCatAITraining, Token: "meta-externalagent"},
	{ID: "meta-externalfetcher", Name: "Meta-ExternalFetcher", Operator: "Meta", Category: botCatAISearch, Token: "meta-externalfetcher"},
	{ID: "facebookbot", Name: "FacebookBot", Operator: "Meta", Category: botCatAITraining, Token: "facebookbot"},
	{ID: "diffbot", Name: "Diffbot", Operator: "Diffbot", Category: botCatScraper, Token: "diffbot"},
	{ID: "omgilibot", Name: "Omgilibot", Operator: "Webz.io", Category: botCatScraper, Token: "omgilibot"},
	{ID: "cohere-ai", Name: "cohere-ai", Operator: "Cohere", Category: botCatAITraining, Token: "cohere-ai"},
	{ID: "youbot", Name: "YouBot", Operator: "You.com", Category: botCatAISearch, Token: "youbot"},
	{ID: "duckassistbot", Name: "DuckAssistBot", Operator: "DuckDuckGo", Category: botCatAISearch, Token: "duckassistbot"},
	{ID: "timpibot", Name: "Timpibot", Operator: "Timpi", Category: botCatAISearch, Token: "timpibot"},
	{ID: "scrapy", Name: "Scrapy", Operator: "Scraper", Category: botCatScraper, Token: "scrapy"},
	{ID: "googlebot", Name: "Googlebot", Operator: "Google", Category: botCatSearchEngine, Token: "googlebot"},
	{ID: "bingbot", Name: "Bingbot", Operator: "Microsoft", Category: botCatSearchEngine, Token: "bingbot"},
	{ID: "duckduckbot", Name: "DuckDuckBot", Operator: "DuckDuckGo", Category: botCatSearchEngine, Token: "duckduckbot"},
	{ID: "yandexbot", Name: "YandexBot", Operator: "Yandex", Category: botCatSearchEngine, Token: "yandexbot"},
	{ID: "baiduspider", Name: "Baiduspider", Operator: "Baidu", Category: botCatSearchEngine, Token: "baiduspider"},
}

var (
	bcBlockRE       = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+botcontrol_block\b.*$`)
	bcRateLimitRE   = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+botcontrol_ratelimit\b.*$`)
	bcAllowRE       = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*botcontrol:[ \t]*allow\b.*$`)
	bcWAFAnchorRE   = regexp.MustCompile(`(?m)^([ \t]*)(coraza_waf\b)`)
	bcProxyAnchorRE = regexp.MustCompile(`(?m)^([ \t]*)(reverse_proxy\b|import[ \t]+reverse_proxy_upstream\b)`)
)

func botValidPolicy(p botPolicy) bool {
	return p == botPolicyAllow || p == botPolicyRateLimit || p == botPolicyBlock
}

func botPolicyFromConfig(content string) botPolicy {
	if bcBlockRE.MatchString(content) {
		return botPolicyBlock
	}
	if bcRateLimitRE.MatchString(content) {
		return botPolicyRateLimit
	}
	return botPolicyAllow
}

func botDirective(domain string, p botPolicy) string {
	switch p {
	case botPolicyBlock:
		return "import botcontrol_block"
	case botPolicyRateLimit:
		return "import botcontrol_ratelimit " + domain
	default:
		return "# botcontrol: allow"
	}
}

// bcAnyLineRE catches any botcontrol line, so ones left outside a site block can be dropped
var bcAnyLineRE = regexp.MustCompile(`^[ \t]*(import[ \t]+botcontrol_(block|ratelimit)\b|#[ \t]*botcontrol:)`)

// botDropTopLevelLines removes botcontrol lines outside any site block, at the top level they'd break the whole Caddyfile
func botDropTopLevelLines(content string) string {
	lines := strings.Split(content, "\n")
	out := lines[:0]
	depth, inRaw := 0, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if depth == 0 && !inRaw && bcAnyLineRE.MatchString(line) {
			continue
		}
		out = append(out, line)
		if !inRaw && strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, c := range line {
			switch {
			case c == '`':
				inRaw = !inRaw
			case inRaw:
			case c == '{':
				depth++
			case c == '}' && depth > 0:
				depth--
			}
		}
	}
	return strings.Join(out, "\n")
}

func botRewritePolicy(content, domain string, p botPolicy) (string, error) {
	directive := botDirective(domain, p)
	content = botDropTopLevelLines(content)
	switch {
	case bcBlockRE.MatchString(content):
		return bcBlockRE.ReplaceAllString(content, "    "+directive), nil
	case bcRateLimitRE.MatchString(content):
		return bcRateLimitRE.ReplaceAllString(content, "    "+directive), nil
	case bcAllowRE.MatchString(content):
		return bcAllowRE.ReplaceAllString(content, "    "+directive), nil
	case bcWAFAnchorRE.MatchString(content):
		return bcWAFAnchorRE.ReplaceAllString(content, "${1}"+directive+"\n\n${1}${2}"), nil
	case bcProxyAnchorRE.MatchString(content):
		return bcProxyAnchorRE.ReplaceAllString(content, "${1}"+directive+"\n\n${1}${2}"), nil
	case p == botPolicyAllow:
		return content, nil
	default:
		return "", errBotNoAnchor
	}
}

func botReadDomainPolicy(domain string) botPolicy {
	b, err := os.ReadFile(filepath.Join(CaddyDomainsConfDir, domain+".conf"))
	if err != nil {
		return botPolicyAllow
	}
	return botPolicyFromConfig(string(b))
}

// botApplyPolicy writes p to every domain, reloads once and puts all files back if caddy rejects it, so caddy still starts next time
func botApplyPolicy(ctx context.Context, domains []string, p botPolicy) (changed int, skipped []string, err error) {
	type saved struct {
		path string
		old  []byte
	}
	var written []saved
	restore := func() {
		for _, w := range written {
			_ = os.WriteFile(w.path, w.old, 0o644)
		}
	}
	for _, d := range domains {
		path := filepath.Join(CaddyDomainsConfDir, d+".conf")
		old, rerr := os.ReadFile(path)
		if rerr != nil {
			skipped = append(skipped, d)
			continue
		}
		updated, rerr := botRewritePolicy(string(old), d, p)
		if rerr != nil {
			skipped = append(skipped, d)
			continue
		}
		if updated == string(old) {
			continue
		}
		if werr := os.WriteFile(path, []byte(updated), 0o644); werr != nil {
			restore()
			return 0, skipped, werr
		}
		written = append(written, saved{path, old})
	}
	if len(written) == 0 {
		return 0, skipped, nil
	}
	if rerr := botReloadCaddy(ctx); rerr != nil {
		restore()
		return 0, skipped, fmt.Errorf("caddy rejected the change, nothing was changed: %w", rerr)
	}
	return len(written), skipped, nil
}

func botClassifyUA(ua string) (name, operator string, cat botCategory, isAI bool) {
	lower := strings.ToLower(strings.TrimSpace(ua))
	if lower == "" {
		return "Empty User-Agent", "Unknown", botCatScraper, false
	}
	for _, b := range knownBots {
		if strings.Contains(lower, b.Token) {
			return b.Name, b.Operator, b.Category, b.Category != botCatSearchEngine
		}
	}
	if strings.Contains(lower, "curl/") || strings.Contains(lower, "python-requests") || strings.Contains(lower, "go-http-client") || strings.Contains(lower, "wget/") {
		fields := strings.Fields(ua)
		tok := "script"
		if len(fields) > 0 {
			tok = fields[0]
			if len(tok) > 24 {
				tok = tok[:24]
			}
		}
		return "CLI / Script (" + tok + ")", "Scripted", botCatScraper, false
	}
	return "Browser / Other", "-", botCatHuman, false
}

// BotDomainRow represents one domain row in the /security/bot-control table.
type BotDomainRow struct {
	Domain       string    `json:"domain"`
	Owner        string    `json:"owner"`
	Policy       botPolicy `json:"policy"`
	Requests     int       `json:"requests"`
	AIRequests   int       `json:"ai_requests"`
	BlockedCount int       `json:"blocked_count"`
}

// BotTrafficReport holds the "Who's hitting me" aggregated breakdown.
type BotTrafficReport struct {
	Scope         string       `json:"scope"`
	Window        string       `json:"window"`
	TotalRequests int          `json:"total_requests"`
	AIRequests    int          `json:"ai_requests"`
	BlockedCount  int          `json:"blocked_count"`
	TopBots       []BotStatRow `json:"top_bots"`
	TopNetworks   []NetStatRow `json:"top_networks"`
	TopURLs       []URLStatRow `json:"top_urls"`
}

// BotStatRow ranks one User-Agent/bot family.
type BotStatRow struct {
	Name     string      `json:"name"`
	Operator string      `json:"operator"`
	Category botCategory `json:"category"`
	Requests int         `json:"requests"`
	Blocked  int         `json:"blocked"`
	SharePct float64     `json:"share_pct"`
}

// NetStatRow ranks one ASN or /24 / /48 IP prefix.
type NetStatRow struct {
	Network   string `json:"network"`
	ASN       string `json:"asn"`
	Requests  int    `json:"requests"`
	Blocked   int    `json:"blocked"`
	UniqueIPs int    `json:"unique_ips"`
}

// URLStatRow ranks one target path (prefixed with domain when viewing server-wide).
type URLStatRow struct {
	Target      string `json:"target"`
	Requests    int    `json:"requests"`
	BotRequests int    `json:"bot_requests"`
	Blocked     int    `json:"blocked"`
}

type botASNEntry struct {
	prefix netip.Prefix
	asn    string
}

func botLoadASNPrefixes(path string) []botASNEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []botASNEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		pfx, err := netip.ParsePrefix(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		label := strings.TrimSpace(parts[1])
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			label += " (" + strings.TrimSpace(parts[2]) + ")"
		}
		out = append(out, botASNEntry{prefix: pfx, asn: label})
	}
	return out
}

func botLookupNet(ipStr string, table []botASNEntry) (network, asn string) {
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		if ipStr == "" {
			return "Unknown", "-"
		}
		return ipStr, "-"
	}
	for _, e := range table {
		if e.prefix.Contains(addr) {
			return e.prefix.String(), e.asn
		}
	}
	if addr.Is4() {
		return netip.PrefixFrom(addr, 24).Masked().String(), "-"
	}
	return netip.PrefixFrom(addr, 48).Masked().String(), "-"
}

func botWindowDuration(w string) (string, time.Duration) {
	switch w {
	case "1h":
		return "1h", time.Hour
	case "7d":
		return "7d", 7 * 24 * time.Hour
	default:
		return "24h", 24 * time.Hour
	}
}

type botCaddyLogLine struct {
	TS      float64 `json:"ts"`
	Status  int     `json:"status"`
	Request struct {
		ClientIP string              `json:"client_ip"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
}

func botNormalizePath(raw string) string {
	if raw == "" {
		return "/"
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" {
		return "/"
	}
	if len(raw) > 100 {
		return raw[:100] + "..."
	}
	return raw
}

func botFirstHeader(headers map[string][]string, key string) string {
	for k, vals := range headers {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

const (
	botMaxLogTailBytes = 8 * 1024 * 1024
	botMaxTopRows      = 20
)

func botAnalyzeDomains(domains []string, selectedDomain, window string, now time.Time) (BotTrafficReport, map[string][3]int) {
	winLabel, dur := botWindowDuration(window)
	scopeLabel := "All domains"
	if selectedDomain != "" {
		scopeLabel = selectedDomain
	}

	report := BotTrafficReport{
		Scope:       scopeLabel,
		Window:      winLabel,
		TopBots:     []BotStatRow{},
		TopNetworks: []NetStatRow{},
		TopURLs:     []URLStatRow{},
	}
	perDomainCounts := make(map[string][3]int, len(domains))

	type botKey struct {
		name, op string
		cat      botCategory
	}
	type netAcc struct {
		net, asn      string
		reqs, blocked int
		ips           map[string]struct{}
	}
	type urlAcc struct {
		reqs, botReqs, blocked int
	}

	bots := map[botKey]*BotStatRow{}
	nets := map[string]*netAcc{}
	urls := map[string]*urlAcc{}
	cutoff := float64(now.Add(-dur).UnixNano()) / 1e9
	asnTable := botLoadASNPrefixes(BotASNPrefixesPath)

	for _, dom := range domains {
		logPath := filepath.Join(CaddyDomlogsDir, dom, "access.log")
		f, err := os.Open(logPath)
		if err != nil {
			continue
		}
		info, statErr := f.Stat()
		if statErr != nil || info.Size() == 0 {
			f.Close()
			continue
		}

		var r io.Reader = f
		if info.Size() > botMaxLogTailBytes {
			if _, seekErr := f.Seek(-botMaxLogTailBytes, io.SeekEnd); seekErr == nil {
				br := bufio.NewReader(f)
				_, _ = br.ReadString('\n')
				r = br
			}
		}

		var domTot, domAI, domBlk int
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var entry botCaddyLogLine
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			if entry.TS > 0 && entry.TS < cutoff {
				continue
			}

			domTot++
			isBlk := entry.Status == 403 || entry.Status == 429
			if isBlk {
				domBlk++
			}
			ua := botFirstHeader(entry.Request.Headers, "User-Agent")
			name, op, cat, isAI := botClassifyUA(ua)
			if isAI {
				domAI++
			}

			// Always roll up per-domain counts for the domain table, and filter the "Who's hitting me" view when a single domain is picked.
			if selectedDomain != "" && dom != selectedDomain {
				continue
			}

			report.TotalRequests++
			if isAI {
				report.AIRequests++
			}
			if isBlk {
				report.BlockedCount++
			}

			bk := botKey{name: name, op: op, cat: cat}
			bs, ok := bots[bk]
			if !ok {
				bs = &BotStatRow{Name: name, Operator: op, Category: cat}
				bots[bk] = bs
			}
			bs.Requests++
			if isBlk {
				bs.Blocked++
			}

			ipStr := strings.TrimSpace(entry.Request.ClientIP)
			netLabel, asnLabel := botLookupNet(ipStr, asnTable)
			na, ok := nets[netLabel]
			if !ok {
				na = &netAcc{net: netLabel, asn: asnLabel, ips: map[string]struct{}{}}
				nets[netLabel] = na
			}
			na.reqs++
			if isBlk {
				na.blocked++
			}
			if ipStr != "" {
				na.ips[ipStr] = struct{}{}
			}

			target := botNormalizePath(entry.Request.URI)
			if selectedDomain == "" {
				target = dom + target
			}
			uaAcc, ok := urls[target]
			if !ok {
				uaAcc = &urlAcc{}
				urls[target] = uaAcc
			}
			uaAcc.reqs++
			if cat != botCatHuman {
				uaAcc.botReqs++
			}
			if isBlk {
				uaAcc.blocked++
			}
		}
		f.Close()
		perDomainCounts[dom] = [3]int{domTot, domAI, domBlk}
	}

	for _, b := range bots {
		if report.TotalRequests > 0 {
			b.SharePct = float64(int(float64(b.Requests)*1000/float64(report.TotalRequests)+0.5)) / 10
		}
		report.TopBots = append(report.TopBots, *b)
	}
	sort.Slice(report.TopBots, func(i, j int) bool {
		if report.TopBots[i].Requests != report.TopBots[j].Requests {
			return report.TopBots[i].Requests > report.TopBots[j].Requests
		}
		return report.TopBots[i].Name < report.TopBots[j].Name
	})
	if len(report.TopBots) > botMaxTopRows {
		report.TopBots = report.TopBots[:botMaxTopRows]
	}

	for _, n := range nets {
		report.TopNetworks = append(report.TopNetworks, NetStatRow{
			Network:   n.net,
			ASN:       n.asn,
			Requests:  n.reqs,
			Blocked:   n.blocked,
			UniqueIPs: len(n.ips),
		})
	}
	sort.Slice(report.TopNetworks, func(i, j int) bool {
		if report.TopNetworks[i].Requests != report.TopNetworks[j].Requests {
			return report.TopNetworks[i].Requests > report.TopNetworks[j].Requests
		}
		return report.TopNetworks[i].Network < report.TopNetworks[j].Network
	})
	if len(report.TopNetworks) > botMaxTopRows {
		report.TopNetworks = report.TopNetworks[:botMaxTopRows]
	}

	for tgt, u := range urls {
		report.TopURLs = append(report.TopURLs, URLStatRow{
			Target:      tgt,
			Requests:    u.reqs,
			BotRequests: u.botReqs,
			Blocked:     u.blocked,
		})
	}
	sort.Slice(report.TopURLs, func(i, j int) bool {
		if report.TopURLs[i].Requests != report.TopURLs[j].Requests {
			return report.TopURLs[i].Requests > report.TopURLs[j].Requests
		}
		return report.TopURLs[i].Target < report.TopURLs[j].Target
	})
	if len(report.TopURLs) > botMaxTopRows {
		report.TopURLs = report.TopURLs[:botMaxTopRows]
	}

	return report, perDomainCounts
}

// ServeBotControl handles GET/POST /security/bot-control.
func (bc *BotControl) ServeBotControl(w http.ResponseWriter, r *http.Request) {
	wf := &WAF{Sessions: bc.Sessions, MySQL: bc.MySQL}
	domains, owners := wf.wafDomainsFor(r)

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		action := strings.TrimSpace(r.PostFormValue("action"))

		if action == "enable_module" {
			restarted, err := botSetModuleEnabled(r.Context(), true)
			if err != nil {
				auth.AddFlash(w, r, bc.Sessions, "Failed to enable AI & Bot Control module: "+err.Error(), "error")
			} else if restarted {
				auth.AddFlash(w, r, bc.Sessions, "AI & Bot Control module enabled and OpenPanel restarted.", "success")
			} else {
				auth.AddFlash(w, r, bc.Sessions, "AI & Bot Control module enabled. Restart OpenPanel to expose it to users.", "success")
			}
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}

		if action == "disable_module" {
			restarted, err := botSetModuleEnabled(r.Context(), false)
			if err != nil {
				auth.AddFlash(w, r, bc.Sessions, "Failed to disable AI & Bot Control module: "+err.Error(), "error")
				http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
				return
			}
			if _, _, err := botApplyPolicy(r.Context(), domains, botPolicyAllow); err != nil {
				auth.AddFlash(w, r, bc.Sessions, "Module disabled, but resetting domains to Allow failed: "+err.Error(), "error")
				http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
				return
			}
			if restarted {
				auth.AddFlash(w, r, bc.Sessions, "AI & Bot Control module disabled, all domains reset to Allow, and OpenPanel restarted.", "success")
			} else {
				auth.AddFlash(w, r, bc.Sessions, "AI & Bot Control module disabled and all domains reset to Allow. Restart OpenPanel to update user menus.", "success")
			}
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}

		if !botModuleEnabled() {
			auth.AddFlash(w, r, bc.Sessions, "Enable the AI & Bot Control module before updating domain policies.", "error")
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}

		policy := botPolicy(strings.TrimSpace(r.PostFormValue("policy")))
		if !botValidPolicy(policy) {
			auth.AddFlash(w, r, bc.Sessions, "Invalid AI & bot policy.", "error")
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}

		if action == "apply_all" {
			_, skipped, err := botApplyPolicy(r.Context(), domains, policy)
			switch {
			case err != nil:
				auth.AddFlash(w, r, bc.Sessions, "Failed to apply '"+string(policy)+"': "+err.Error(), "error")
			case len(skipped) > 0:
				auth.AddFlash(w, r, bc.Sessions, "Applied '"+string(policy)+"' policy, skipped (no Caddy config to change): "+strings.Join(skipped, ", "), "error")
			default:
				auth.AddFlash(w, r, bc.Sessions, "Applied '"+string(policy)+"' policy to all domains and reloaded Caddy.", "success")
			}
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}

		dom := strings.TrimSpace(r.PostFormValue("domain"))
		if dom == "" || !wafContains(domains, dom) {
			auth.AddFlash(w, r, bc.Sessions, "Domain not found or outside your scope.", "error")
			http.Redirect(w, r, "/security/bot-control", http.StatusSeeOther)
			return
		}
		_, skipped, err := botApplyPolicy(r.Context(), []string{dom}, policy)
		if err == nil && len(skipped) > 0 {
			err = errBotNoAnchor
		}
		if err != nil {
			auth.AddFlash(w, r, bc.Sessions, "Failed to update "+dom+": "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, bc.Sessions, "Updated AI & bot policy for "+dom+" to '"+string(policy)+"'.", "success")
		}
		redirectURL := "/security/bot-control"
		if sel := strings.TrimSpace(r.PostFormValue("selected_domain")); sel != "" {
			redirectURL += "?domain=" + sel
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}

	selectedDomain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if selectedDomain != "" && !wafContains(domains, selectedDomain) {
		selectedDomain = ""
	}
	window := strings.TrimSpace(r.URL.Query().Get("window"))

	report, perDomainCounts := botAnalyzeDomains(domains, selectedDomain, window, time.Now())
	rows := make([]BotDomainRow, 0, len(domains))
	policyCounts := map[string]int{"allow": 0, "ratelimit": 0, "block": 0}
	for _, d := range domains {
		p := botReadDomainPolicy(d)
		policyCounts[string(p)]++
		c := perDomainCounts[d]
		rows = append(rows, BotDomainRow{
			Domain:       d,
			Owner:        owners[d],
			Policy:       p,
			Requests:     c[0],
			AIRequests:   c[1],
			BlockedCount: c[2],
		})
	}

	moduleEnabled := botModuleEnabled()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, map[string]any{
			"module_enabled": moduleEnabled,
			"domains":        rows,
			"policy_counts":  policyCounts,
			"report":         report,
		})
		return
	}

	webtemplates.Render(w, "security_bot_control.html", mergeChrome(map[string]any{
		"ModuleEnabled":  moduleEnabled,
		"Domains":        rows,
		"TotalDomains":   len(rows),
		"PolicyCounts":   policyCounts,
		"Report":         report,
		"SelectedDomain": selectedDomain,
		"CSRFToken":      csrf.Token(r),
		"Flashes":        auth.PopFlashes(w, r, bc.Sessions),
	}, r, "AI & Bot Control"))
}
