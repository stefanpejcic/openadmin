// This file implements the JSON REST API's SSH-management routes: service
// status/control, quick and raw sshd_config editing, and authorized_keys
// management.
package handlers

import "net/http"

// APIServerSSH bundles the /api/server/ssh and /api/server/ssh/config
// handlers.
type APIServerSSH struct{}

type apiServerSSHRequest struct {
	Action          string `json:"action"`
	Config          string `json:"config"`
	NewKey          string `json:"new_key"`
	KeyToRemove     string `json:"key_to_remove"`
	Port            string `json:"port"`
	PasswordAuth    string `json:"password_auth"`
	PubkeyAuth      string `json:"pubkey_auth"`
	PermitRootLogin string `json:"permit_root_login"`
}

// ServeSSH handles GET/POST /api/server/ssh. Reuses the same status/config/
// authorized_keys primitives as the HTML /server/ssh page (ssh.go), but with
// its own request shape: unlike the HTML form, at most one of
// action/config/new_key/key_to_remove/basic-settings is acted on per
// request, in that priority order, and the response reports exactly which
// one ran instead of always redirecting back to the same page.
func (s *APIServerSSH) ServeSSH(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body apiServerSSHRequest
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}

		if body.Port != "" && !isValidSSHPort(body.Port) {
			writeJSONError(w, http.StatusBadRequest, "Invalid SSH port. It must be a number between 22 and 10000.")
			return
		}
		if body.PasswordAuth != "" && !isValidSSHAuthParam(body.PasswordAuth) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for password_auth. It must be "yes" or "no".`)
			return
		}
		if body.PubkeyAuth != "" && !isValidSSHAuthParam(body.PubkeyAuth) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for pubkey_auth. It must be "yes" or "no".`)
			return
		}
		if body.PermitRootLogin != "" && !isValidSSHAuthParam(body.PermitRootLogin) {
			writeJSONError(w, http.StatusBadRequest, `Invalid value for permit_root_login. It must be "yes" or "no".`)
			return
		}

		switch {
		case body.Action != "":
			sshExecuteActionRun(body.Action)
			writeJSON(w, map[string]interface{}{"success": true, "message": "SSH service has been " + body.Action + "ed."})
		case body.Config != "":
			if err := sshUpdateConfigRun(body.Config); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to update SSH configuration: "+err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "SSH configuration updated and service restarted."})
		case body.NewKey != "":
			if err := sshAddAuthorizedKeyRun(body.NewKey); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to add SSH key: "+err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "New SSH key added."})
		case body.KeyToRemove != "":
			if err := sshRemoveAuthorizedKeyRun(body.KeyToRemove); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to remove SSH key: "+err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "SSH key removed."})
		case body.Port != "" || body.PasswordAuth != "" || body.PubkeyAuth != "" || body.PermitRootLogin != "":
			if err := sshUpdateSettingsRun(sshSettings{
				Port:            body.Port,
				PasswordAuth:    body.PasswordAuth,
				PubkeyAuth:      body.PubkeyAuth,
				PermitRootLogin: body.PermitRootLogin,
			}); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to update SSH settings: "+err.Error())
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "message": "SSH settings updated."})
		default:
			writeJSONError(w, http.StatusBadRequest, "No recognized parameters provided.")
		}
		return
	}

	status := sshStatusRun()
	config, err := sshReadConfig()
	if err != nil {
		config = ""
	}
	keys := sshGetAuthorizedKeys()
	settings := sshDefaultSettings()
	if config != "" {
		settings = sshParseSettings(config)
	}

	writeJSON(w, map[string]interface{}{
		"status":            status,
		"config":            config,
		"keys":              keys,
		"port":              settings.Port,
		"password_auth":     settings.PasswordAuth,
		"pubkey_auth":       settings.PubkeyAuth,
		"permit_root_login": settings.PermitRootLogin,
	})
}

// ServeSSHConfig handles GET/POST /api/server/ssh/config.
func (s *APIServerSSH) ServeSSHConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Config string `json:"config"`
		}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		if body.Config == "" {
			writeJSONError(w, http.StatusBadRequest, "config is required")
			return
		}
		if err := sshUpdateConfigRun(body.Config); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to update SSH configuration: "+err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "SSH configuration updated and service restarted."})
		return
	}

	config, err := sshReadConfig()
	if err != nil {
		config = ""
	}
	writeJSON(w, map[string]string{"config": config})
}
