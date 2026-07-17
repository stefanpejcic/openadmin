// This file implements the CorazaWAF status page and rule-set manager
// fronting /etc/openpanel/caddy/coreruleset/rules/.
package handlers

import (
	"bufio"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/config"
	"openadmin/internal/webtemplates"
)

// WAF bundles the /security/waf and /security/waf/rules handlers.
type WAF struct {
	Sessions *auth.Manager
}

// WAFRulesDir is the hardcoded directory holding the CorazaWAF rule files.
var WAFRulesDir = "/etc/openpanel/caddy/coreruleset/rules/"

// wafRunOpenCLIRun is injectable so tests never shell out to a real
// opencli binary. A nonzero exit is reported as an error.
var wafRunOpenCLIRun = func(args ...string) error {
	return exec.Command("opencli", args...).Run()
}

// wafResolveRuleFile resolves filename against rulesDir and confirms the
// result stays inside rulesDir. filename is sometimes already an absolute
// path -- the "View" link passes the rule's already-absolute file path as
// "edit" -- and in that case it's used as-is instead of being joined under
// rulesDir. Go's filepath.Join has no such override -- it always
// concatenates -- so that case is handled explicitly here to avoid
// mangling every legitimate "View" link into a nonexistent nested path.
func wafResolveRuleFile(rulesDir, filename string) (string, bool) {
	if filename == "" {
		return "", false
	}
	cleanRulesDir := filepath.Clean(rulesDir)
	var candidate string
	if filepath.IsAbs(filename) {
		candidate = filepath.Clean(filename)
	} else {
		candidate = filepath.Clean(filepath.Join(cleanRulesDir, filename))
	}
	if candidate != cleanRulesDir && !strings.HasPrefix(candidate, cleanRulesDir+string(os.PathSeparator)) {
		return "", false
	}
	return candidate, true
}

// ServeWAFViewRules handles GET /security/waf/view-rules.
func (wf *WAF) ServeWAFViewRules(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("edit")
	if filename == "" {
		auth.AddFlash(w, r, wf.Sessions, "Hacker? No filename provided in the query parameters.", "error")
		http.Redirect(w, r, "/security/waf/rules", http.StatusSeeOther)
		return
	}

	absPath, ok := wafResolveRuleFile(WAFRulesDir, filename)
	if !ok {
		auth.AddFlash(w, r, wf.Sessions, "Hacker? Invalid file path", "error")
		http.Redirect(w, r, "/security/waf/rules", http.StatusSeeOther)
		return
	}

	if !strings.HasSuffix(filename, ".conf") && !strings.HasSuffix(filename, ".conf.disabled") {
		auth.AddFlash(w, r, wf.Sessions, "Hacker? Invalid file extension. Only .conf and .conf.disabled files are allowed.", "error")
		http.Redirect(w, r, "/security/waf/rules", http.StatusSeeOther)
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		// The "file not found" branch flashes the wrong (success-sounding)
		// message, "Files updated successfully!", under the "error"
		// category. Not a security issue, so this misleading text is
		// preserved as-is rather than corrected.
		auth.AddFlash(w, r, wf.Sessions, "Files updated successfully!", "error")
		http.Redirect(w, r, "/security/waf/rules", http.StatusSeeOther)
		return
	}

	w.Write([]byte("<pre>" + html.EscapeString(string(content)) + "</pre>"))
}

type wafRuleDetail struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	NumRules int    `json:"num_rules"`
	Status   string `json:"status"`
}

func wafListRuleFiles() []string {
	entries, err := os.ReadDir(WAFRulesDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".conf") || strings.HasSuffix(name, ".conf.disabled") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files
}

func wafCountNonEmptyLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

// ServeWAFRules handles GET/POST /security/waf/rules.
func (wf *WAF) ServeWAFRules(w http.ResponseWriter, r *http.Request) {
	ruleFiles := wafListRuleFiles()

	if r.Method == http.MethodPost {
		r.ParseForm()
		ruleName := r.PostFormValue("rule_name")
		action := r.PostFormValue("action")

		var matchedFile string
		for _, f := range ruleFiles {
			if strings.TrimSuffix(f, ".conf") == ruleName || strings.TrimSuffix(f, ".conf.disabled") == ruleName {
				matchedFile = f
				break
			}
		}

		if matchedFile != "" {
			rulePath := filepath.Join(WAFRulesDir, matchedFile)
			switch {
			case action == "off" && strings.HasSuffix(matchedFile, ".conf"):
				if err := os.Rename(rulePath, rulePath+".disabled"); err == nil {
					auth.AddFlash(w, r, wf.Sessions, "Rules set disabled. Restart Caddy to apply changes.", "success")
				}
			case action == "on" && strings.HasSuffix(matchedFile, ".conf.disabled"):
				// TrimSuffix strips the literal ".disabled" suffix. This
				// only needs to handle filenames that always end in the
				// fixed "...conf.disabled" pattern, so a plain literal
				// suffix strip is sufficient here.
				newPath := strings.TrimSuffix(rulePath, ".disabled")
				if err := os.Rename(rulePath, newPath); err == nil {
					auth.AddFlash(w, r, wf.Sessions, "Rules set enabled. Restart Caddy to apply changes.", "success")
				}
			default:
				auth.AddFlash(w, r, wf.Sessions, "Hacker! Invalid action.", "error")
			}
		} else {
			auth.AddFlash(w, r, wf.Sessions, "rule_file is missing from the POST request.", "error")
		}

		http.Redirect(w, r, "/security/waf/rules", http.StatusSeeOther)
		return
	}

	rulesDetails := make([]wafRuleDetail, 0, len(ruleFiles))
	for _, f := range ruleFiles {
		path := filepath.Join(WAFRulesDir, f)
		var name, status string
		if strings.HasSuffix(f, ".conf.disabled") {
			name = strings.TrimSuffix(f, ".conf.disabled")
			status = "off"
		} else {
			name = strings.TrimSuffix(f, ".conf")
			status = "on"
		}
		rulesDetails = append(rulesDetails, wafRuleDetail{
			Name:     name,
			Path:     path,
			NumRules: wafCountNonEmptyLines(path),
			Status:   status,
		})
	}

	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rulesDetails)
		return
	}

	webtemplates.Render(w, "security_coraza_rules.html", mergeChrome(map[string]interface{}{
		"RulesDetails": rulesDetails,
		"CSRFToken":    csrf.Token(r),
		"Flashes":      auth.PopFlashes(w, r, wf.Sessions),
	}, r, "Edit CorazaWAF Rules"))
}

// wafModuleEnabled does a plain, unstructured substring search for "waf"
// (case-insensitive) anywhere in openpanel.config, not scoped to any
// particular key -- any line mentioning "waf" at all (e.g. in an unrelated
// value) flips this on. This broad/fragile detection is preserved as-is
// rather than tightened, since it's not security-relevant.
func wafModuleEnabled() bool {
	f, err := os.Open(config.OpenpanelConfigPath)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(strings.ToLower(scanner.Text()), "waf") {
			return true
		}
	}
	return false
}

// ServeWAFStatus handles GET/POST /security/waf.
func (wf *WAF) ServeWAFStatus(w http.ResponseWriter, r *http.Request) {
	status := "off"
	if wafModuleEnabled() {
		status = "on"
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		enabledValue := strings.ToLower(r.PostFormValue("status"))
		switch enabledValue {
		case "yes":
			if status == "on" {
				auth.AddFlash(w, r, wf.Sessions, "The module is already enabled; WAF will be enabled for any new domains.", "error")
			} else if err := wafRunOpenCLIRun("waf", "enable"); err != nil {
				auth.AddFlash(w, r, wf.Sessions, "Failed to change WAF status: "+err.Error(), "error")
			} else {
				status = "on"
			}
		case "no":
			if status == "off" {
				auth.AddFlash(w, r, wf.Sessions, "The module is already disabled; WAF will not be enabled for any new domains.", "error")
			} else if err := wafRunOpenCLIRun("waf", "disable", "-y"); err != nil {
				auth.AddFlash(w, r, wf.Sessions, "Failed to change WAF status: "+err.Error(), "error")
			} else {
				status = "off"
			}
		}
	}

	ruleFiles := wafListRuleFiles()
	confCount, confDisabledCount, totalCount := 0, 0, 0
	for _, f := range ruleFiles {
		if strings.HasSuffix(f, ".conf.disabled") {
			confDisabledCount++
		} else {
			confCount++
		}
		totalCount++
	}

	if r.URL.Query().Get("output") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        status,
			"total_sets":    totalCount,
			"active_sets":   confCount,
			"inactive_sets": confDisabledCount,
		})
		return
	}

	webtemplates.Render(w, "security_coraza_waf.html", mergeChrome(map[string]interface{}{
		"Status":       status,
		"ActiveSets":   confCount,
		"InactiveSets": confDisabledCount,
		"TotalSets":    totalCount,
		"CSRFToken":    csrf.Token(r),
		"Flashes":      auth.PopFlashes(w, r, wf.Sessions),
	}, r, "CorazaWAF"))
}
