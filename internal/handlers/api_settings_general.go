// This file implements the JSON REST API's general-settings endpoint
// (domain/ports/proxy/dev-mode), reusing the same opencli-backed getters
// and setters as the HTML /settings/general page (general.go) -- only the
// request/response shape and a couple of validation details differ.
package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

// APISettingsGeneral bundles the /api/settings/general handler.
type APISettingsGeneral struct {
	// DevMode mirrors General.DevMode: read once at process startup and
	// never refreshed afterward, so this endpoint keeps reporting (and
	// comparing against) the old value until OpenAdmin actually restarts.
	DevMode bool
}

type apiSettingsGeneralBody struct {
	ForceDomain    string `json:"force_domain"`
	AdminPort      string `json:"2087_port"`
	OpenpanelPort  string `json:"2083_port"`
	OpenpanelProxy string `json:"openpanel_proxy"`
	DevMode        string `json:"dev_mode"`
}

// ServeSettingsGeneral handles GET/POST /api/settings/general. Wrap with
// (*APIAuth).RequireAPIAdmin.
func (g *APISettingsGeneral) ServeSettingsGeneral(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		g.handlePost(w, r)
		return
	}

	devModeStr := "off"
	if g.DevMode {
		devModeStr = "on"
	}
	writeJSON(w, map[string]interface{}{
		"port":         generalOpenpanelPort(),
		"admin_port":   generalOpenadminPort(),
		"proxy":        generalOpenpanelProxy(),
		"force_domain": generalAdminDomain(),
		"dev_mode":     devModeStr,
	})
}

func (g *APISettingsGeneral) handlePost(w http.ResponseWriter, r *http.Request) {
	var body apiSettingsGeneralBody
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	// A non-numeric port value is rejected with a 400 here rather than
	// left to crash the request the way an unhandled int() conversion
	// error would.
	adminPort := 2087
	if body.AdminPort != "" {
		n, err := strconv.Atoi(body.AdminPort)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid 2087_port value.")
			return
		}
		adminPort = n
	}
	openpanelPort := 2083
	if body.OpenpanelPort != "" {
		n, err := strconv.Atoi(body.OpenpanelPort)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid 2083_port value.")
			return
		}
		openpanelPort = n
	}

	changes := []string{}
	openpanelRestartNeeded := false
	openadminRestartNeeded := false

	forceDomainCurrentValue := generalAdminDomain()
	openpanelPortCurrentValue, _ := strconv.Atoi(generalOpenpanelPort())

	if openpanelPort != openpanelPortCurrentValue {
		generalSetOpenpanelPortRun(strconv.Itoa(openpanelPort))
		changes = append(changes, "user-panel port")
		openpanelRestartNeeded = true
	}

	if body.ForceDomain != "" && forceDomainCurrentValue != body.ForceDomain {
		generalSetDomainRun(body.ForceDomain)
		changes = append(changes, "domain")
		openpanelRestartNeeded = true
		openadminRestartNeeded = true
	}

	devModeCurrent := "off"
	if g.DevMode {
		devModeCurrent = "on"
	}
	if (devModeCurrent == "on" && body.DevMode == "off") || (devModeCurrent == "off" && body.DevMode == "on") {
		generalSetDevModeRun(body.DevMode)
		changes = append(changes, "dev_mode")
		openpanelRestartNeeded = true
		openadminRestartNeeded = true
	}

	openadminPortCurrentValue, _ := strconv.Atoi(generalOpenadminPort())
	if adminPort != openadminPortCurrentValue {
		generalSetAdminPortRun(strconv.Itoa(adminPort))
		changes = append(changes, "admin-panel port")
		openadminRestartNeeded = true
	}

	// The proxy is (re)applied on every POST -- to the submitted value, or
	// back to "openpanel" when none was given -- and the OpenAdmin restart
	// flag always ends up set below regardless of whether the proxy (or
	// anything else) actually changed.
	if body.OpenpanelProxy != "" && body.OpenpanelProxy != "openpanel" {
		generalSetProxyRun(body.OpenpanelProxy)
		changes = append(changes, "proxy")
	} else {
		generalSetProxyRun("openpanel")
	}
	openadminRestartNeeded = true

	if openpanelRestartNeeded {
		os.WriteFile(GeneralOpenpanelRestartFlagPath, []byte("Restart needed"), 0644)
	}
	if openadminRestartNeeded {
		os.WriteFile(GeneralOpenadminRestartFlagPath, []byte("Restart needed"), 0644)
	}

	message := "No changes made."
	if len(changes) > 0 {
		message = "Settings updated: " + strings.Join(changes, ", ")
	}
	writeJSON(w, map[string]interface{}{"success": true, "changes": changes, "message": message})
}
