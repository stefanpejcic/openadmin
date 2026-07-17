// This file implements the "HOURLY LIMITS" feature: postfwd-based
// per-domain/per-user email rate limiting.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
	"openadmin/internal/webtemplates"
)

// PostfwdConfigPath / EmailsMailLogPath / EmailsRatelimitScript are the
// paths and command used to read/write postfwd's rate-limit config, tail
// the mail log, and invoke the rate-limit management script.
var (
	PostfwdConfigPath     = "/usr/local/mail/openmail/postfwd/postfwd.cf"
	EmailsMailLogPath     = "/usr/local/mail/openmail/docker-data/dms/mail-logs/mail.log"
	EmailsRatelimitScript = []string{"opencli", "email-ratelimit"}
)

type postfwdRule struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Limit    int    `json:"limit"`
	Current  *int   `json:"current"`
}

var (
	postfwdDomainRe = regexp.MustCompile(`sender=~\.\+@([\w.\-]+)`)
	postfwdLimitRe  = regexp.MustCompile(`_ratelimit/(\d+)/3600`)
)

// splitPostfwdBlocks breaks the content into blocks at every newline
// immediately followed by "id=", without consuming the "id=" itself as
// part of the delimiter. Go's RE2 has no lookahead support, so this walks
// lines directly instead, starting a new block at each line beginning with
// "id=".
func splitPostfwdBlocks(content string) []string {
	lines := strings.Split(content, "\n")
	var blocks []string
	var current []string
	for i, line := range lines {
		if strings.HasPrefix(line, "id=") && i > 0 {
			blocks = append(blocks, strings.Join(current, "\n"))
			current = nil
		}
		current = append(current, line)
	}
	blocks = append(blocks, strings.Join(current, "\n"))
	return blocks
}

func parsePostfwdRules(content string) []postfwdRule {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	blocks := splitPostfwdBlocks(trimmed)

	var rules []postfwdRule
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if !strings.HasPrefix(block, "id=") {
			continue
		}
		domainMatch := postfwdDomainRe.FindStringSubmatch(block)
		limitMatch := postfwdLimitRe.FindStringSubmatch(block)
		if domainMatch == nil || limitMatch == nil {
			continue
		}
		fullID := strings.TrimSpace(strings.SplitN(block, ";", 2)[0])
		fullID = strings.TrimSpace(strings.TrimPrefix(fullID, "id="))
		domain := domainMatch[1]
		domainKey := strings.ReplaceAll(domain, ".", "_")
		suffix := "_" + domainKey
		inner := strings.TrimPrefix(fullID, "limit_")
		username := inner
		if strings.HasSuffix(inner, suffix) {
			username = strings.TrimSuffix(inner, suffix)
		}
		limit, _ := strconv.Atoi(limitMatch[1])
		rules = append(rules, postfwdRule{ID: fullID, Username: username, Domain: domain, Limit: limit})
	}
	return rules
}

func readPostfwdRaw() string {
	raw, err := os.ReadFile(PostfwdConfigPath)
	if err != nil {
		return ""
	}
	return string(raw)
}

// hupPostfwdRun sends a fire-and-forget podman kill, without waiting for it
// to complete.
var hupPostfwdRun = func() {
	cmd, err := podman.Command("default", "kill", "--signal=HUP", "postfwd")
	if err != nil {
		return
	}
	cmd.Start()
}

func buildPostfwdRule(username string, limit int, domain string) string {
	key := strings.ReplaceAll(domain, ".", "_")
	return "id=limit_" + username + "_" + key + " ; sender=~.+@" + domain + " ; protocol_state==RCPT\n" +
		"                action=rate(" + username + "_ratelimit/" + strconv.Itoa(limit) + "/3600/450 4.7.1 sorry, OpenPanel account reached limit of " +
		strconv.Itoa(limit) + " emails per hour)\n\n"
}

// writeDomainRule replaces any existing rule block for this exact rule ID,
// then appends the freshly built rule.
func writeDomainRule(username string, limit int, domain string) (bool, string) {
	key := strings.ReplaceAll(domain, ".", "_")
	ruleID := "limit_" + username + "_" + key

	content := readPostfwdRaw()
	existingRuleRe := regexp.MustCompile(`(?m)^id=` + regexp.QuoteMeta(ruleID) + ` [^\n]*\n[ \t]+action=[^\n]*\n\n?`)
	cleaned := existingRuleRe.ReplaceAllString(content, "")

	newRule := buildPostfwdRule(username, limit, domain)
	var final string
	if strings.TrimSpace(cleaned) != "" {
		final = strings.TrimRight(cleaned, "\n") + "\n\n" + newRule
	} else {
		final = newRule
	}

	if err := os.WriteFile(PostfwdConfigPath, []byte(final), 0644); err != nil {
		return false, err.Error()
	}
	hupPostfwdRun()
	return true, "OK: " + username + " limit=" + strconv.Itoa(limit) + "/hr domain=" + domain
}

// getPostfwdCountersRun queries postfwd's live counters over its
// management port via `nc`, inside the postfwd container.
var getPostfwdCountersRun = func() map[string]int {
	cmd, err := podman.Command("default", "exec", "postfwd", "sh", "-c", `echo "show counters" | nc 127.0.0.1 10040`)
	if err != nil {
		return map[string]int{}
	}
	out, err := cmd.Output()
	if err != nil {
		return map[string]int{}
	}
	counterRe := regexp.MustCompile(`(\w+)_ratelimit/\d+/\d+=(\d+)`)
	counters := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		m := counterRe.FindStringSubmatch(line)
		if m != nil {
			n, _ := strconv.Atoi(m[2])
			counters[m[1]] = n
		}
	}
	return counters
}

// getLimitHitsRun returns the last N mail.log lines mentioning a domain's
// rate-limit rejections.
var getLimitHitsRun = func(domain string, n int) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "grep", "-i", "@"+domain, EmailsMailLogPath).Output()
	if err != nil {
		return []string{}
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "reached limit of") || strings.Contains(l, "4.7.1") {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

var runRatelimitScriptRun = func(skipReload bool, args ...string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := append(append([]string{}, EmailsRatelimitScript...), args...)
	if skipReload {
		cmd = append(cmd, "--skip-reload")
	}
	command := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return false, "Script timed out after 30 seconds."
	}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return false, err.Error()
		}
	}
	return err == nil, strings.TrimSpace(stdout.String() + stderr.String())
}

// ServeDomainLimits handles GET /emails/domain-limits.
func (e *Emails) ServeDomainLimits(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "gui"
	}
	rawContent := readPostfwdRaw()

	var rules []postfwdRule
	if mode == "gui" {
		rules = parsePostfwdRules(rawContent)
	}

	usernameSet := map[string]bool{}
	for _, rule := range rules {
		usernameSet[rule.Username] = true
	}
	usernames := make([]string, 0, len(usernameSet))
	for u := range usernameSet {
		usernames = append(usernames, u)
	}
	sort.Strings(usernames)

	var counters map[string]int
	if mode == "gui" {
		counters = getPostfwdCountersRun()
	}
	for i := range rules {
		if v, ok := counters[rules[i].Username]; ok {
			rules[i].Current = &v
		}
	}

	webtemplates.Render(w, "emails_domain_limits.html", mergeChrome(map[string]interface{}{
		"Mode":       mode,
		"Rules":      rules,
		"Usernames":  usernames,
		"RawContent": rawContent,
		"PostfwdCF":  PostfwdConfigPath,
		"Flashes":    auth.PopFlashes(w, r, e.Sessions),
	}, r, "Email Rate Limits"))
}

// ServeDomainLimitsSaveRaw handles POST /emails/domain-limits/save-raw.
func (e *Emails) ServeDomainLimitsSaveRaw(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	content := r.PostFormValue("raw_content")
	if err := os.WriteFile(PostfwdConfigPath, []byte(content), 0644); err != nil {
		auth.AddFlash(w, r, e.Sessions, "Save failed: "+err.Error(), "danger")
	} else {
		hupPostfwdRun()
		auth.AddFlash(w, r, e.Sessions, "File saved and postfwd reloaded.", "success")
	}
	http.Redirect(w, r, "/emails/domain-limits?mode=raw", http.StatusSeeOther)
}

// ServeDomainLimitsHits handles GET /emails/domain-limits/hits.
func (e *Emails) ServeDomainLimitsHits(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "lines": []string{}})
		return
	}
	lines := getLimitHitsRun(domain, 30)
	writeJSON(w, map[string]interface{}{"ok": true, "domain": domain, "lines": lines})
}

type domainLimitsAPIRequest struct {
	Action   string      `json:"action"`
	Domain   string      `json:"domain"`
	Username string      `json:"username"`
	Limit    interface{} `json:"limit"`
}

// ServeDomainLimitsAPI handles POST /emails/domain-limits/api.
func (e *Emails) ServeDomainLimitsAPI(w http.ResponseWriter, r *http.Request) {
	var body domainLimitsAPIRequest
	json.NewDecoder(r.Body).Decode(&body)
	domain := strings.TrimSpace(body.Domain)
	username := strings.TrimSpace(body.Username)

	switch body.Action {
	case "update-domain":
		if domain == "" || username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain and username are required")
			return
		}
		limit, ok := domainLimitAsPositiveInt(body.Limit)
		if !ok {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "limit must be a positive integer")
			return
		}
		ok2, out := writeDomainRule(username, limit, domain)
		writeJSONOkMsg(w, http.StatusOK, ok2, out)

	case "reset-domain":
		if domain == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--domain="+domain)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "reset-user":
		if username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "username is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--username="+username)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "reset-all":
		ok, out := runRatelimitScriptRun(false, "--all-users")
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-domain":
		if domain == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "domain is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--delete-domain="+domain)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-user":
		if username == "" {
			writeJSONOkMsg(w, http.StatusBadRequest, false, "username is required")
			return
		}
		ok, out := runRatelimitScriptRun(false, "--delete-user="+username)
		writeJSONOkMsg(w, http.StatusOK, ok, out)

	case "delete-all":
		if err := os.WriteFile(PostfwdConfigPath, []byte(""), 0644); err != nil {
			writeJSONOkMsg(w, http.StatusOK, false, err.Error())
			return
		}
		hupPostfwdRun()
		writeJSONOkMsg(w, http.StatusOK, true, "All rate-limit rules removed.")

	default:
		writeJSONOkMsg(w, http.StatusOK, false, "")
	}
}

func domainLimitAsPositiveInt(v interface{}) (int, bool) {
	var n int
	switch val := v.(type) {
	case float64:
		n = int(val)
	case string:
		parsed, err := strconv.Atoi(val)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if n < 1 {
		return 0, false
	}
	return n, true
}

func writeJSONOkMsg(w http.ResponseWriter, status int, ok bool, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": ok, "msg": msg})
}
