// This file implements the JSON REST API's admin-level security surface:
// basic-auth protection for OpenAdmin itself, the useragent blacklist,
// disabling OpenAdmin, the ConfigServer Firewall passthrough, and
// CorazaWAF status/rule-set management. Each reuses the same on-disk paths
// and opencli/csf.pl plumbing as its HTML admin-page equivalent
// (security_toggles.go, firewall.go, waf.go) -- only the response shape
// differs.
package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"openadmin/internal/bootstrap"
	"openadmin/internal/config"
)

// APISecurity bundles the admin-level /api/security/* handlers.
type APISecurity struct{}

// --- basic auth ---

// ServeBasicAuth handles GET/POST /api/security/basic-auth.
func (s *APISecurity) ServeBasicAuth(w http.ResponseWriter, r *http.Request) {
	cfg := config.Load(config.AdminConfigPath)

	if r.Method == http.MethodPost {
		var data map[string]string
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}

		if v, ok := data["basic_auth"]; ok {
			cfg.Set("SECURITY", "basic_auth", v)
		}
		if v, ok := data["basic_auth_username"]; ok {
			cfg.Set("SECURITY", "basic_auth_username", v)
		}
		if v, ok := data["basic_auth_password"]; ok {
			cfg.Set("SECURITY", "basic_auth_password", v)
		}

		if err := config.Save(config.AdminConfigPath, cfg); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to write config: "+err.Error())
			return
		}
		os.WriteFile(bootstrap.RestartFlagPath, []byte("Restart needed"), 0644)
		writeJSON(w, map[string]interface{}{
			"success": true,
			"message": "Basic_auth settings for OpenAdmin edited successfully.",
		})
		return
	}

	writeJSON(w, map[string]string{
		"basic_auth":          cfg.Get("SECURITY", "basic_auth", ""),
		"basic_auth_username": cfg.Get("SECURITY", "basic_auth_username", ""),
		"basic_auth_password": cfg.Get("SECURITY", "basic_auth_password", ""),
	})
}

// --- blacklist useragents ---

// ServeBlacklistUseragents handles GET/POST /api/security/blacklist-useragents.
func (s *APISecurity) ServeBlacklistUseragents(w http.ResponseWriter, r *http.Request) {
	enabledOut, _, _ := runOpenCLICaptured("opencli", "config", "get", "blacklist_useragents")
	enabled := strings.TrimSpace(enabledOut)

	content, err := os.ReadFile(BlacklistUseragentsFilePath)
	list := string(content)
	if err != nil {
		list = ""
	}

	if r.Method == http.MethodPost {
		var data struct {
			BlacklistUseragents string `json:"blacklist_useragents"`
			Enabled             string `json:"enabled"`
		}
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		updated := false

		if data.BlacklistUseragents != "" {
			updated = true
			os.WriteFile(BlacklistUseragentsFilePath, []byte(data.BlacklistUseragents), 0644)
		}

		if data.Enabled != "" && enabled != data.Enabled {
			updated = true
			_, _, _ = runOpenCLICaptured("opencli", "config", "update", "blacklist_useragents", data.Enabled)
			enabled = data.Enabled
		}

		if updated {
			os.WriteFile(OpenpanelRestartFlagPath, []byte("Restart needed for OpenPanel service."), 0644)
			writeJSON(w, map[string]interface{}{
				"success":                      true,
				"message":                      "Saved blacklisted useragents.",
				"blacklist_useragents_enabled": enabled,
			})
			return
		}
		writeJSONError(w, http.StatusBadRequest, "Nothing to update.")
		return
	}

	writeJSON(w, map[string]string{
		"blacklist_useragents_enabled": enabled,
		"blacklist_useragents":         list,
	})
}

// --- disable OpenAdmin ---

// apiSecurityDisableAdminRun is injectable so tests never shell out for
// real. The real implementation starts the command and doesn't wait for
// it to finish, same as ServeDisableAdmin's HTML equivalent.
var apiSecurityDisableAdminRun = func() {
	_ = exec.Command("opencli", "admin", "off").Start()
}

// HandleDisableAdmin handles POST /api/security/disable-admin. Unlike the
// HTML page backing the same action (SecurityToggles.ServeDisableAdmin),
// this route carries no additional in-handler role check beyond the
// shared admin-or-user gate applied by RequireAPIAdmin -- a "user" role
// caller can trigger this the same as an "admin" caller.
func (s *APISecurity) HandleDisableAdmin(w http.ResponseWriter, r *http.Request) {
	apiSecurityDisableAdminRun()
	writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "OpenAdmin is now disabled and all further actions need to be performed via terminal.",
	})
}

// --- firewall (CSF) ---

// ServeFirewall handles GET/POST /api/security/firewall.
func (s *APISecurity) ServeFirewall(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]bool{"available": firewallCommandAvailableRun("csf")})
		return
	}

	var data map[string]interface{}
	if !apiDecodeJSONBody(r, &data) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	// Map iteration order is unspecified, same as the HTML iframe wrapper's
	// equivalent query-string builder (ServeCSFIframe in firewall.go) --
	// csf.pl doesn't care about key ordering.
	pairs := make([]string, 0, len(data))
	for k, v := range data {
		pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
	}
	qs := strings.Join(pairs, "&")

	tmp, err := os.CreateTemp("", "csf-api-")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.WriteString(qs)
	tmp.Close()
	defer func() { _ = os.Remove(tmpName) }()
	if writeErr != nil {
		writeJSONError(w, http.StatusInternalServerError, writeErr.Error())
		return
	}

	output, err := firewallCSFRun(tmpName)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]string{"output": output})
}

// --- WAF status ---

// apiWAFModuleStatus reports "on"/"off" the same way wafModuleEnabled does
// (an unstructured, case-insensitive search for "waf" anywhere in
// openpanel.config), but -- unlike wafModuleEnabled, which silently treats
// a missing/unreadable config file as "off" -- surfaces the read error so
// the caller can report it as a failure instead of a false "off" reading.
func apiWAFModuleStatus() (string, error) {
	f, err := os.Open(config.OpenpanelConfigPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	status := "off"
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(strings.ToLower(scanner.Text()), "waf") {
			status = "on"
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return status, nil
}

// ServeWAFStatus handles GET/POST /api/security/waf.
func (s *APISecurity) ServeWAFStatus(w http.ResponseWriter, r *http.Request) {
	status, err := apiWAFModuleStatus()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.Method == http.MethodPost {
		var data struct {
			Status string `json:"status"`
		}
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}

		switch strings.ToLower(data.Status) {
		case "yes":
			if status == "on" {
				writeJSONError(w, http.StatusBadRequest, "The module is already enabled.")
				return
			}
			if err := wafRunOpenCLIRun("waf", "enable"); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to change WAF status: "+err.Error())
				return
			}
			status = "on"
		case "no":
			if status == "off" {
				writeJSONError(w, http.StatusBadRequest, "The module is already disabled.")
				return
			}
			if err := wafRunOpenCLIRun("waf", "disable", "-y"); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to change WAF status: "+err.Error())
				return
			}
			status = "off"
		default:
			writeJSONError(w, http.StatusBadRequest, "status must be 'yes' or 'no'")
			return
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

	writeJSON(w, map[string]interface{}{
		"status":        status,
		"total_sets":    totalCount,
		"active_sets":   confCount,
		"inactive_sets": confDisabledCount,
	})
}

// --- WAF rules ---

// ServeWAFRules handles GET/POST /api/security/waf/rules.
func (s *APISecurity) ServeWAFRules(w http.ResponseWriter, r *http.Request) {
	ruleFiles := wafListRuleFiles()

	if r.Method == http.MethodPost {
		var data struct {
			RuleName string `json:"rule_name"`
			Action   string `json:"action"`
		}
		if !apiDecodeJSONBody(r, &data) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}

		var matchedFile string
		for _, f := range ruleFiles {
			if strings.TrimSuffix(f, ".conf") == data.RuleName || strings.TrimSuffix(f, ".conf.disabled") == data.RuleName {
				matchedFile = f
				break
			}
		}
		if matchedFile == "" {
			writeJSONError(w, http.StatusNotFound, "rule_name not found.")
			return
		}

		rulePath := filepath.Join(WAFRulesDir, matchedFile)
		switch {
		case data.Action == "off" && strings.HasSuffix(matchedFile, ".conf"):
			if err := os.Rename(rulePath, rulePath+".disabled"); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Rules set disabled. Restart Caddy to apply changes."})
		case data.Action == "on" && strings.HasSuffix(matchedFile, ".conf.disabled"):
			newPath := strings.TrimSuffix(rulePath, ".disabled")
			if err := os.Rename(rulePath, newPath); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "Rules set enabled. Restart Caddy to apply changes."})
		default:
			writeJSONError(w, http.StatusBadRequest, "Invalid action.")
		}
		return
	}

	details := make([]wafRuleDetail, 0, len(ruleFiles))
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
		details = append(details, wafRuleDetail{
			Name:     name,
			Path:     path,
			NumRules: wafCountNonEmptyLines(path),
			Status:   status,
		})
	}

	writeJSON(w, details)
}
