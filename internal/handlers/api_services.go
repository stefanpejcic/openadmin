// This file implements the JSON REST API's service-management surface:
// reading/writing the monitored-services config file directly, reporting
// each monitored service's live status, and starting/stopping/restarting
// one by name.
package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"openadmin/internal/podman"
)

// APIServices bundles the /api/services* and /api/service/{action}/{name}
// handlers.
type APIServices struct{}

// ServeServicesFile handles GET/PUT /api/services: a thin passthrough onto
// ServicesConfigPath (shared with the HTML /services/edit page in
// services.go) -- GET returns the file's raw parsed JSON content verbatim
// (whatever shape it happens to be), PUT overwrites it wholesale with the
// request body.
func (s *APIServices) ServeServicesFile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		raw, err := os.ReadFile(ServicesConfigPath)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "File not found")
			return
		}
		var content interface{}
		if err := json.Unmarshal(raw, &content); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, content)
		return
	}

	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeJSONError(w, http.StatusBadRequest, "Request must be JSON")
		return
	}
	var data interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request must be JSON")
		return
	}
	pretty, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(ServicesConfigPath, pretty, 0644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"message": ServicesConfigPath + " updated successfully"})
}

// apiServicesDockerPSNamesRun lists the names of currently *running*
// containers (no "-a"), used to answer the docker branch of
// ServeServicesStatus. Injectable so tests never shell out to a real
// podman binary.
var apiServicesDockerPSNamesRun = func() ([]string, error) {
	cmd, err := podman.Command("default", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// apiSystemctlIsActiveRun reports whether name.service is currently active.
// A nonzero exit (the normal "not active" outcome) is not treated as an
// error -- only a genuine invocation failure (e.g. systemctl missing) is.
var apiSystemctlIsActiveRun = func(name string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", name+".service")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}

// ServeServicesStatus handles GET /api/services/status: every monitored
// service flagged on_dashboard, each returned as its full config entry plus
// a "status" field ("Active"/"Inactive", or "Error: ..." if the underlying
// check itself failed).
//
// The docker-container listing is fetched once up front and reused across
// every docker-type entry (rather than re-running `podman ps` per service,
// as a literal per-service check would) -- this produces the exact same
// per-service result since container state can't plausibly change mid
// request, just without the redundant process spawns.
func (s *APIServices) ServeServicesStatus(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(ServicesConfigPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Unable to read services.json file: "+err.Error())
		return
	}
	var services []map[string]interface{}
	if err := json.Unmarshal(raw, &services); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Unable to read services.json file: "+err.Error())
		return
	}

	dockerNames, dockerErr := apiServicesDockerPSNamesRun()

	statusData := []map[string]interface{}{}
	for _, svc := range services {
		onDashboard, _ := svc["on_dashboard"].(bool)
		if !onDashboard {
			continue
		}
		realName, _ := svc["real_name"].(string)
		svcType, _ := svc["type"].(string)

		var status string
		if svcType == "docker" {
			switch {
			case dockerErr != nil:
				status = "Error: " + dockerErr.Error()
			default:
				status = "Inactive"
				for _, n := range dockerNames {
					if n == realName {
						status = "Active"
						break
					}
				}
			}
		} else {
			active, err := apiSystemctlIsActiveRun(realName)
			switch {
			case err != nil:
				status = "Error: " + err.Error()
			case active:
				status = "Active"
			default:
				status = "Inactive"
			}
		}

		entry := make(map[string]interface{}, len(svc)+1)
		for k, v := range svc {
			entry[k] = v
		}
		entry["status"] = status
		statusData = append(statusData, entry)
	}

	writeJSON(w, statusData)
}

// apiLoadAllowedServiceNames returns the real_name of every monitored
// service flagged on_dashboard -- the set HandleManageService accepts.
// This deliberately differs from services.go's loadAllowedServiceNames
// (used by the HTML /service/{action}/{name} route), which allows every
// configured service regardless of on_dashboard.
func apiLoadAllowedServiceNames() map[string]bool {
	allowed := map[string]bool{}
	for _, svc := range loadMonitoredServices() {
		onDashboard, _ := svc["on_dashboard"].(bool)
		if !onDashboard {
			continue
		}
		if name, ok := svc["real_name"].(string); ok {
			allowed[name] = true
		}
	}
	return allowed
}

// apiManageServiceGenericRun runs `service <name> <action>` for anything
// not in the docker-backed set below. Injectable so tests never shell out.
var apiManageServiceGenericRun = func(name, action string) (stdout, stderr string, err error) {
	cmd := exec.Command("service", name, action)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return outBuf.String(), errBuf.String(), runErr
}

// apiManageServiceDockerNames maps the service_name a caller passes in to
// the podman-compose service it actually runs as. This is a narrower,
// independently-maintained set from services.go's own docker-services
// table (used by the HTML /service/{action}/{name} route) -- a name absent
// here falls through to the generic `service <name> <action>` path even if
// it's a "docker" entry in services.json.
var apiManageServiceDockerNames = map[string]string{
	"openpanel_mysql": "openpanel_mysql",
	"caddy":           "caddy",
	"openpanel":       "openpanel",
	"openpanel_dns":   "openpanel_dns",
	"mailserver":      "openadmin_mailserver",
	"roundcube":       "openadmin_roundcube",
}

// HandleManageService handles POST /api/service/{action}/{service_name}.
func (s *APIServices) HandleManageService(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	serviceName := r.PathValue("service_name")

	validActions := map[string]bool{"start": true, "restart": true, "stop": true}
	if !validActions[action] {
		writeJSONError(w, http.StatusBadRequest, "Invalid action: "+action)
		return
	}
	if !apiLoadAllowedServiceNames()[serviceName] {
		writeJSONError(w, http.StatusBadRequest, "Invalid service: "+serviceName)
		return
	}

	const cwd = "/root"
	var stdout, stderr string
	var runErr error

	if svc, ok := apiManageServiceDockerNames[serviceName]; ok {
		switch action {
		case "start":
			stdout, stderr, runErr = containerComposeCaptureRun("default", cwd, "up", "-d", svc)
		case "stop":
			stdout, stderr, runErr = containerComposeCaptureRun("default", cwd, "down", svc)
		case "restart":
			// A failure on the first (down) step -- including a plain
			// nonzero exit -- aborts the whole action immediately rather
			// than falling through to the usual "nonzero exit" 400 below.
			if _, _, downErr := containerComposeCaptureRun("default", cwd, "down", svc); downErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "Exception: "+downErr.Error())
				return
			}
			stdout, stderr, runErr = containerComposeCaptureRun("default", cwd, "up", "-d", svc)
		}
	} else {
		stdout, stderr, runErr = apiManageServiceGenericRun(serviceName, action)
	}
	_ = stdout

	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			writeJSONError(w, http.StatusInternalServerError, "Exception: "+runErr.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Error %sing %s: %s", action, serviceName, strings.TrimSpace(stderr)))
		return
	}

	writeJSON(w, map[string]string{"success": capitalize(serviceName) + " " + action + "ed successfully"})
}
