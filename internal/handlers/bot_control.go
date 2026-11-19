// This file implements /security/bot-control: per-site AI crawler control and the "Who's hitting the server" traffic breakdown.
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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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

// BotControlConfPath holds the crawler snippets, the rate limit lives in it so caddy and the panel read the same number
var BotControlConfPath = "/etc/openpanel/caddy/botcontrol.conf"

const (
	botDefaultRateLimit = 10
	botMaxRateLimit     = 100000
)

var bcEventsRE = regexp.MustCompile(`(?s)(\(botcontrol_ratelimit\)\s*\{.*?\bevents[ \t]+)(\d+)`)

// botRateLimit is the requests per minute each crawler gets per site, 10 when the file has no usable value
func botRateLimit() int {
	b, err := os.ReadFile(BotControlConfPath)
	if err != nil {
		return botDefaultRateLimit
	}
	m := bcEventsRE.FindSubmatch(b)
	if m == nil {
		return botDefaultRateLimit
	}
	n, err := strconv.Atoi(string(m[2]))
	if err != nil || n < 1 {
		return botDefaultRateLimit
	}
	return n
}

// botSetRateLimit rewrites events in the botcontrol_ratelimit snippet and reloads caddy, the old file goes back if caddy says no
func botSetRateLimit(ctx context.Context, n int) error {
	if n < 1 || n > botMaxRateLimit {
		return fmt.Errorf("the limit has to be between 1 and %d requests per minute", botMaxRateLimit)
	}
	old, err := os.ReadFile(BotControlConfPath)
	if err != nil {
		return err
	}
	if !bcEventsRE.Match(old) {
		return errors.New("no botcontrol_ratelimit snippet in " + BotControlConfPath)
	}
	updated := bcEventsRE.ReplaceAll(old, []byte("${1}"+strconv.Itoa(n)))
	if string(updated) == string(old) {
		return nil
	}
	// written in place, caddy has the file bind-mounted
	if err := os.WriteFile(BotControlConfPath, updated, 0o644); err != nil {
		return err
	}
	if err := botReloadCaddy(ctx); err != nil {
		_ = os.WriteFile(BotControlConfPath, old, 0o644)
		return fmt.Errorf("caddy rejected the change, nothing was changed: %w", err)
	}
	return nil
}

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

// BotTrafficReport holds the "Who's hitting the server" aggregated breakdown.
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

			// Always roll up per-domain counts for the domain table, and filter the "Who's hitting the server" view when a single domain is picked.
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
// botActionResult is what a POST did, the page shows it as a flash and the API as JSON
type botActionResult struct {
	Status  int
	Message string
}

func (res botActionResult) ok() bool { return res.Status < 300 }

func botFail(status int, msg string) botActionResult { return botActionResult{status, msg} }
func botDone(msg string) botActionResult             { return botActionResult{http.StatusOK, msg} }

// botAction runs one change, resellers only touch their own domains and never the module or the limit
func (bc *BotControl) botAction(ctx context.Context, r *http.Request, action, policyRaw, dom, limitRaw string) botActionResult {
	wf := &WAF{Sessions: bc.Sessions, MySQL: bc.MySQL}
	domains, _ := wf.wafDomainsFor(r)
	reseller := actingReseller(r) != ""

	switch action {
	case "enable_module", "disable_module", "set_rate_limit":
		if reseller {
			return botFail(http.StatusForbidden, "Only administrators can change the AI & Bot Control module and its rate limit.")
		}
	}

	switch action {
	case "enable_module":
		restarted, err := botSetModuleEnabled(ctx, true)
		switch {
		case err != nil:
			return botFail(http.StatusInternalServerError, "Failed to enable AI & Bot Control module: "+err.Error())
		case restarted:
			return botDone("AI & Bot Control module enabled and OpenPanel restarted.")
		default:
			return botDone("AI & Bot Control module enabled. Restart OpenPanel to expose it to users.")
		}
	case "disable_module":
		restarted, err := botSetModuleEnabled(ctx, false)
		if err != nil {
			return botFail(http.StatusInternalServerError, "Failed to disable AI & Bot Control module: "+err.Error())
		}
		if _, _, err := botApplyPolicy(ctx, domains, botPolicyAllow); err != nil {
			return botFail(http.StatusInternalServerError, "Module disabled, but resetting domains to Allow failed: "+err.Error())
		}
		if restarted {
			return botDone("AI & Bot Control module disabled, all domains reset to Allow, and OpenPanel restarted.")
		}
		return botDone("AI & Bot Control module disabled and all domains reset to Allow. Restart OpenPanel to update user menus.")
	case "set_rate_limit":
		n, err := strconv.Atoi(strings.TrimSpace(limitRaw))
		if err != nil {
			return botFail(http.StatusBadRequest, "The rate limit has to be a whole number of requests per minute.")
		}
		if err := botSetRateLimit(ctx, n); err != nil {
			return botFail(http.StatusBadRequest, "Failed to change the rate limit: "+err.Error())
		}
		return botDone(fmt.Sprintf("AI crawlers on rate-limited domains now get %d requests per minute per site.", n))
	case "", "set_policy", "apply_all":
	default:
		return botFail(http.StatusBadRequest, "Unknown action.")
	}

	if !botModuleEnabled() {
		return botFail(http.StatusConflict, "Enable the AI & Bot Control module before updating domain policies.")
	}
	policy := botPolicy(strings.TrimSpace(policyRaw))
	if !botValidPolicy(policy) {
		return botFail(http.StatusBadRequest, "Invalid AI & bot policy.")
	}

	switch action {
	case "apply_all":
		_, skipped, err := botApplyPolicy(ctx, domains, policy)
		switch {
		case err != nil:
			return botFail(http.StatusInternalServerError, "Failed to apply '"+string(policy)+"': "+err.Error())
		case len(skipped) > 0:
			return botActionResult{http.StatusMultiStatus, "Applied '" + string(policy) + "' policy, skipped (no Caddy config to change): " + strings.Join(skipped, ", ")}
		default:
			return botDone("Applied '" + string(policy) + "' policy to all domains and reloaded Caddy.")
		}
	case "", "set_policy":
		dom = strings.TrimSpace(dom)
		if dom == "" || !wafContains(domains, dom) {
			return botFail(http.StatusNotFound, "Domain not found or outside your scope.")
		}
		_, skipped, err := botApplyPolicy(ctx, []string{dom}, policy)
		if err == nil && len(skipped) > 0 {
			err = errBotNoAnchor
		}
		if err != nil {
			return botFail(http.StatusInternalServerError, "Failed to update "+dom+": "+err.Error())
		}
		return botDone("Updated AI & bot policy for " + dom + " to '" + string(policy) + "'.")
	}
	return botFail(http.StatusBadRequest, "Unknown action.")
}

// botState is the page data, also served as JSON to the page's ?output=json and the API
func (bc *BotControl) botState(r *http.Request) (map[string]any, []BotDomainRow, BotTrafficReport, string) {
	wf := &WAF{Sessions: bc.Sessions, MySQL: bc.MySQL}
	domains, owners := wf.wafDomainsFor(r)

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
	return map[string]any{
		"module_enabled": botModuleEnabled(),
		"rate_limit":     botRateLimit(),
		"domains":        rows,
		"policy_counts":  policyCounts,
		"report":         report,
	}, rows, report, selectedDomain
}

var botSortKeys = map[string]bool{"domain": true, "owner": true, "hits": true, "ai": true, "blocked": true, "policy": true}

// policies sort from the most open to the strictest
var botPolicyOrder = map[string]int{"allow": 0, "ratelimit": 1, "block": 2}

func sortBotRows(rows []BotDomainRow, col, direction string) {
	switch col {
	case "domain":
		sortBy(rows, direction, func(a, b BotDomainRow) bool { return a.Domain < b.Domain })
	case "owner":
		sortBy(rows, direction, func(a, b BotDomainRow) bool { return a.Owner < b.Owner })
	case "hits":
		sortBy(rows, direction, func(a, b BotDomainRow) bool { return a.Requests < b.Requests })
	case "ai":
		sortBy(rows, direction, func(a, b BotDomainRow) bool { return a.AIRequests < b.AIRequests })
	case "blocked":
		sortBy(rows, direction, func(a, b BotDomainRow) bool { return a.BlockedCount < b.BlockedCount })
	case "policy":
		sortBy(rows, direction, func(a, b BotDomainRow) bool {
			return botPolicyOrder[string(a.Policy)] < botPolicyOrder[string(b.Policy)]
		})
	}
}

func (bc *BotControl) ServeBotControl(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		action := strings.TrimSpace(r.PostFormValue("action"))
		res := bc.botAction(r.Context(), r, action, r.PostFormValue("policy"), r.PostFormValue("domain"), r.PostFormValue("rate_limit"))
		kind := "success"
		if !res.ok() || res.Status == http.StatusMultiStatus {
			kind = "error"
		}
		auth.AddFlash(w, r, bc.Sessions, res.Message, kind)
		redirectURL := "/security/bot-control"
		if sel := strings.TrimSpace(r.PostFormValue("selected_domain")); sel != "" && (action == "" || action == "set_policy") {
			redirectURL += "?domain=" + url.QueryEscape(sel)
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}

	state, rows, report, selectedDomain := bc.botState(r)
	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, state)
		return
	}
	sortCol, sortDirection := readSort(r, botSortKeys)
	sortBotRows(rows, sortCol, sortDirection)
	// the day window and the picked domain stay when a column is sorted
	sortExtra := url.Values{}
	for _, k := range []string{"window", "domain"} {
		if v := r.URL.Query().Get(k); v != "" {
			sortExtra.Set(k, v)
		}
	}

	webtemplates.Render(w, "security_bot_control.html", mergeChrome(map[string]any{
		"ModuleEnabled":  state["module_enabled"],
		"RateLimit":      state["rate_limit"],
		"Domains":        rows,
		"TotalDomains":   len(rows),
		"PolicyCounts":   state["policy_counts"],
		"Report":         report,
		"SelectedDomain": selectedDomain,
		"SortCol":        sortCol,
		"SortDirection":  sortDirection,
		"SortExtra":      sortExtra,
		"CSRFToken":      csrf.Token(r),
		"Flashes":        auth.PopFlashes(w, r, bc.Sessions),
	}, r, "AI & Bot Control"))
}

// ServeAPI is GET/POST /api/security/bot-control, same rules as the page
func (bc *BotControl) ServeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, _, _, _ := bc.botState(r)
		writeJSON(w, state)
		return
	}
	var body struct {
		Action    string `json:"action"`
		Domain    string `json:"domain"`
		Policy    string `json:"policy"`
		RateLimit any    `json:"rate_limit"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON body.")
		return
	}
	limit := ""
	if body.RateLimit != nil {
		limit = strings.TrimSpace(fmt.Sprint(body.RateLimit))
	}
	res := bc.botAction(r.Context(), r, strings.TrimSpace(body.Action), body.Policy, body.Domain, limit)
	if !res.ok() {
		writeJSONError(w, res.Status, res.Message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": res.Message, "rate_limit": botRateLimit()})
}
