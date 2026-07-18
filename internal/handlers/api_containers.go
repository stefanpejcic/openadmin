// This file implements the JSON REST API's container-management routes:
// GET /api/users/{username}/containers (the full podman-compose config, or
// -- with ?stats= -- a live `podman stats` snapshot) and POST
// /api/users/{username}/containers/{action}/{container_name} (start, stop,
// restart, or a cpu/ram limit change). Both reuse the same underlying
// podman-compose/podman plumbing as the HTML /containers/* routes in
// users_containers.go -- only the response shape (JSON, not a flash +
// redirect) differs.
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// APIContainers bundles the /api/users/{username}/containers* handlers.
type APIContainers struct {
	MySQL *sql.DB
}

// apiIsJSONContentType reports whether the Content-Type header's media
// type is exactly "application/json" or ends in "+json", ignoring
// parameters like charset.
func apiIsJSONContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// apiJSONGetSilent looks up key in a JSON object body without ever
// returning a parse error to the caller as such: a body that isn't valid
// JSON, or that doesn't decode to a JSON object, reports crashed=true so
// the handler can reproduce the same unhandled-crash response the route
// gives for that case, instead of quietly treating the field as absent.
func apiJSONGetSilent(body []byte, key string) (value interface{}, crashed bool) {
	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, true
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return nil, true
	}
	return obj[key], false
}

// apiJSONValueToString renders a decoded JSON value as plain text for
// interpolation into a command argument or message: strings pass through
// unquoted, numbers/bools use their natural text form.
func apiJSONValueToString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// apiJSONTruthy reports whether a decoded JSON value should be treated as
// present/on: only nil, false, zero, an empty string, or an empty
// array/object count as falsy.
func apiJSONTruthy(v interface{}) bool {
	switch val := v.(type) {
	case nil:
		return false
	case bool:
		return val
	case float64:
		return val != 0
	case string:
		return val != ""
	case []interface{}:
		return len(val) > 0
	case map[string]interface{}:
		return len(val) > 0
	default:
		return true
	}
}

// apiContainersDisplayUsername mirrors get_containers()'s "SUSPENDED_"
// handling: when present, only the text after the last underscore is used
// in the error message below (a suspended account's username is stored as
// "SUSPENDED_<timestamp>_<original>", so this recovers the original name).
func apiContainersDisplayUsername(username string) string {
	if !strings.Contains(username, "SUSPENDED_") {
		return username
	}
	if idx := strings.LastIndex(username, "_"); idx != -1 {
		return username[idx+1:]
	}
	return username
}

// apiContainersData renders the podman-compose config for a user's
// containers, in the same shape get_containers() returns: a plain error
// dict (still meant to be serialized with a 200 status -- the caller here
// never inspects it) for a missing context, a failed podman-compose call,
// or a config that doesn't contain a "services" key. unhandled is non-nil
// only for the failure modes get_containers() itself doesn't catch (a
// podman-compose invocation that couldn't even run, or output that isn't
// valid JSON) -- those crash out to a plain 500 in the caller, same as an
// uncaught exception would.
func apiContainersData(mysqlDB *sql.DB, username string) (data map[string]interface{}, unhandled error) {
	context, ctxErr := queryContextByUsername(mysqlDB, username)
	if ctxErr != nil {
		return map[string]interface{}{
			"error":   "No context found for user",
			"details": "username: " + apiContainersDisplayUsername(username),
		}, nil
	}

	composePath := "/home/" + context + "/docker-compose.yml"
	stdout, _, runErr := containerComposeCaptureRun(context, "", "-f", composePath, "config")
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); ok {
			return map[string]interface{}{
				"error":   "Failed to fetch container data",
				"details": runErr.Error(),
			}, nil
		}
		return nil, runErr
	}

	var dockerData map[string]interface{}
	if err := yaml.Unmarshal([]byte(stdout), &dockerData); err != nil {
		return nil, err
	}
	if _, ok := dockerData["services"]; !ok {
		return map[string]interface{}{
			"error":   "Invalid data format",
			"details": "docker_data does not contain 'services'.",
		}, nil
	}
	return dockerData, nil
}

// ServeUserContainers handles GET /api/users/{username}/containers. With a
// non-empty ?stats= query value (including "0" or "false" -- any non-empty
// string takes this branch) it returns a live `podman stats` snapshot
// instead of the podman-compose config.
func (c *APIContainers) ServeUserContainers(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")

	if r.URL.Query().Get("stats") != "" {
		context, err := queryContextByUsername(c.MySQL, username)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "No context found for user")
			return
		}
		stats, ok := getAllContainersStats(context)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "Could not retrieve container stats")
			return
		}
		writeJSON(w, map[string]interface{}{"container_stats": stats})
		return
	}

	data, err := apiContainersData(c.MySQL, username)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

// ServeManageContainer handles POST
// /api/users/{username}/containers/{action}/{container_name}.
func (c *APIContainers) ServeManageContainer(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	containerName := r.PathValue("container_name")
	username := r.PathValue("username")

	switch action {
	case "start", "stop", "restart", "cpu", "ram":
	default:
		writeJSONError(w, http.StatusBadRequest, "Invalid action. Use start, stop, restart, cpu, or ram.")
		return
	}

	context, ctxErr := queryContextByUsername(c.MySQL, username)
	if ctxErr != nil {
		writeJSONError(w, http.StatusNotFound, "No context found for user")
		return
	}

	if action == "cpu" || action == "ram" {
		var value string
		haveValue := false
		if apiIsJSONContentType(r) {
			body, _ := io.ReadAll(r.Body)
			v, crashed := apiJSONGetSilent(body, "value")
			if crashed {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if v != nil {
				value = apiJSONValueToString(v)
				haveValue = true
			}
		} else {
			r.ParseForm()
			if r.PostForm.Has("value") {
				value = r.PostFormValue("value")
				haveValue = true
			}
		}
		if !haveValue {
			writeJSONError(w, http.StatusBadRequest, "value is required")
			return
		}

		result, err := updateContainerRAMOrCPU(context, containerName, action, value)
		if err != nil {
			// The one case update_container_ram_or_cpu doesn't catch itself
			// (a restart failure while resetting the limit to 0/unlimited)
			// -- unhandled here too, same as the HTML page's version.
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if result.IsDict && !result.Success {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": result.Success, "message": result.Message})
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": result.Message})
		return
	}

	if action == "restart" {
		if err := restartContainerCmd(context, containerName); err != nil {
			if _, ok := err.(*exec.ExitError); ok {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": fmt.Sprintf("Container %s restarted successfully.", containerName)})
		return
	}

	actionType := "deactivate"
	pull := false
	if action == "start" {
		actionType = "activate"
		if apiIsJSONContentType(r) {
			body, _ := io.ReadAll(r.Body)
			v, crashed := apiJSONGetSilent(body, "pull")
			if crashed {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			pull = apiJSONTruthy(v)
		}
	}

	result := startOrStopContainer(context, containerName, actionType, pull)
	if !result.Success {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "message": result.Message})
		return
	}
	writeJSON(w, map[string]interface{}{
		"success": true,
		"response": map[string]interface{}{
			"success": true,
			"message": result.Message,
		},
	})
}
