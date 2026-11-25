// This file implements the container-management routes: POST
// /containers/{username}/{action}/{container_name}
// (start/stop/restart/cpu/ram), GET /containers/stats/{username}, and the
// read side used by the #services tab (composeServicesForUser).
package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/podman"
)

// containerEnvVarSanitizeRe replaces '.' and '-' with '_' when deriving an
// env var name from a container name in update_container_ram_or_cpu().
var containerEnvVarSanitizeRe = regexp.MustCompile(`[.-]`)

// composePortView is one resolved port mapping for a compose service, as
// shown in the #services tab.
type composePortView struct {
	HostIP    string
	Published string
	Target    string
}

// composeEnvView is one environment variable for a compose service. Whether
// it's masked by default (password/token/... in the key) is decided
// client-side by the template's Alpine component, matching the original
// Python/Jinja page.
type composeEnvView struct {
	Key   string
	Value string
}

// composeServiceView is one service parsed from a user's docker-compose.yml,
// as rendered in the #services tab's table.
type composeServiceView struct {
	Name          string
	ContainerName string // falls back to Name when the compose file doesn't set one
	Image         string
	Ports         []composePortView
	Environment   []composeEnvView
	CPULimit      string // raw "cpus" value (e.g. "0.5" or "0"), "" if unset -- "0" itself is NOT unlimited here, the template's JS decides that, matching the original page
	MemoryLimitGB string // memory converted to GB and formatted to 2 decimals, "" if unset or zero
	PIDsLimit     string // raw "pids" value (e.g. "100" or "0"), "" if unset
	// shown when the row is expanded
	Restart     string
	DependsOn   []string
	Volumes     []string
	Networks    []string
	Command     string
	Healthcheck string
}

// composeList reads a compose field that can be a list or a map (depends_on, networks) as sorted names
func composeList(v interface{}) []string {
	var out []string
	switch t := v.(type) {
	case []interface{}:
		for _, x := range t {
			out = append(out, composeStringOrEmpty(x))
		}
	case map[string]interface{}:
		for k := range t {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// composeCommand joins a command or healthcheck test, which compose allows as a string or a list
func composeCommand(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, composeStringOrEmpty(x))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func composeVolumes(v interface{}) []string {
	list, _ := v.([]interface{})
	out := make([]string, 0, len(list))
	for _, x := range list {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case map[string]interface{}:
			line := composeStringOrEmpty(t["source"]) + " → " + composeStringOrEmpty(t["target"])
			if t["source"] == nil {
				line = composeStringOrEmpty(t["target"])
			}
			if ro, _ := t["read_only"].(bool); ro {
				line += " (read-only)"
			}
			out = append(out, line)
		}
	}
	return out
}

// composeStringOrEmpty stringifies a decoded JSON value, treating a missing
// key (nil) as "" rather than apiJSONValueToString's literal "<nil>".
func composeStringOrEmpty(v interface{}) string {
	if v == nil {
		return ""
	}
	return apiJSONValueToString(v)
}

// composeSizeToBytes converts a resolved compose memory limit into bytes.
// podman-compose's resolved config normally emits this as a plain number of
// bytes, but a string like "512M"/"0.5G" is handled defensively too, in
// case some version leaves it unresolved. ok is false for a zero, missing,
// or unparseable value -- matching the original page treating those as
// "no limit set", not "limit is zero". apiContainersData decodes the
// resolved config with yaml.v3 (podman-compose's "config" output is YAML,
// not JSON, even though it's JSON-compatible), which decodes a bare
// integer literal into Go's int, not float64 -- unlike encoding/json --
// so both numeric cases are needed here.
func composeSizeToBytes(v interface{}) (bytes float64, ok bool) {
	switch t := v.(type) {
	case float64:
		return t, t != 0
	case int:
		return float64(t), t != 0
	case int64:
		return float64(t), t != 0
	case string:
		s := strings.TrimSuffix(strings.TrimSpace(strings.ToUpper(t)), "B")
		if s == "" || s == "0" {
			return 0, false
		}
		mult := 1.0
		switch {
		case strings.HasSuffix(s, "G"):
			mult, s = 1<<30, strings.TrimSuffix(s, "G")
		case strings.HasSuffix(s, "M"):
			mult, s = 1<<20, strings.TrimSuffix(s, "M")
		case strings.HasSuffix(s, "K"):
			mult, s = 1<<10, strings.TrimSuffix(s, "K")
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || n == 0 {
			return 0, false
		}
		return n * mult, true
	default:
		return 0, false
	}
}

// composeServicesForUser parses a user's resolved docker-compose config
// (via apiContainersData's same podman-compose plumbing) into the
// #services tab's view model. A missing/invalid compose file, or one with
// no "services" key, returns nil -- the template shows "invalid or
// missing" in that case, same as the original Python/Jinja page did.
// Services are sorted by name for a stable render (unlike Python's
// dict-preserves-insertion-order, Go's JSON decoding into a map does not
// preserve the compose file's declaration order).
func composeServicesForUser(mysqlDB *sql.DB, username string) []composeServiceView {
	data, err := apiContainersData(mysqlDB, username)
	if err != nil {
		return nil
	}
	rawServices, _ := data["services"].(map[string]interface{})
	if len(rawServices) == 0 {
		return nil
	}

	views := make([]composeServiceView, 0, len(rawServices))
	for name, raw := range rawServices {
		svc, _ := raw.(map[string]interface{})
		view := composeServiceView{Name: name, ContainerName: name}
		if cn, ok := svc["container_name"].(string); ok && cn != "" {
			view.ContainerName = cn
		}
		if img, ok := svc["image"].(string); ok {
			view.Image = img
		}
		if ports, ok := svc["ports"].([]interface{}); ok {
			for _, p := range ports {
				pm, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				view.Ports = append(view.Ports, composePortView{
					HostIP:    composeStringOrEmpty(pm["host_ip"]),
					Published: composeStringOrEmpty(pm["published"]),
					Target:    composeStringOrEmpty(pm["target"]),
				})
			}
		}
		if env, ok := svc["environment"].(map[string]interface{}); ok {
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				view.Environment = append(view.Environment, composeEnvView{Key: k, Value: composeStringOrEmpty(env[k])})
			}
		}
		view.Restart = composeStringOrEmpty(svc["restart"])
		view.DependsOn = composeList(svc["depends_on"])
		view.Networks = composeList(svc["networks"])
		view.Volumes = composeVolumes(svc["volumes"])
		view.Command = composeCommand(svc["command"])
		if hc, ok := svc["healthcheck"].(map[string]interface{}); ok {
			test := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(composeCommand(hc["test"]), "CMD-SHELL"), "CMD"))
			if test != "" && test != "NONE" {
				view.Healthcheck = test
				if iv := composeStringOrEmpty(hc["interval"]); iv != "" {
					view.Healthcheck += " (every " + iv + ")"
				}
			}
		}
		if deploy, ok := svc["deploy"].(map[string]interface{}); ok {
			if resources, ok := deploy["resources"].(map[string]interface{}); ok {
				if limits, ok := resources["limits"].(map[string]interface{}); ok {
					view.CPULimit = composeStringOrEmpty(limits["cpus"])
					if b, ok := composeSizeToBytes(limits["memory"]); ok {
						view.MemoryLimitGB = strconv.FormatFloat(b/(1<<30), 'f', 2, 64)
					}
					view.PIDsLimit = composeStringOrEmpty(limits["pids"])
				}
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

// splitFileLinesPreserving splits a text file into lines the way a
// line-by-line file iteration would: each element keeps its original
// trailing "\n" (the last line only if the file itself ends with one), with
// no synthetic empty trailing element the way a naive
// strings.Split(content, "\n") would add.
func splitFileLinesPreserving(content string) []string {
	if content == "" {
		return nil
	}
	parts := strings.SplitAfter(content, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// containerComposeCaptureRun is injectable so tests never shell out to a
// real podman-compose binary. A nonzero exit is reported via err (an
// *exec.ExitError).
var containerComposeCaptureRun = func(context, dir string, args ...string) (stdout, stderr string, err error) {
	cmd, cmdErr := podman.ComposeCommand(context, args...)
	if cmdErr != nil {
		return "", "", cmdErr
	}
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return outBuf.String(), errBuf.String(), runErr
}

// containerExistsRun is injectable so tests never shell out to a real
// podman binary. Any failure (nonzero exit, exec error) reads as "doesn't
// exist" -- this check only needs to know whether the container is present,
// not why a lookup might have failed.
var containerExistsRun = func(context, containerName string) bool {
	cmd, err := podman.Command(context, "inspect", containerName)
	if err != nil {
		return false
	}
	return cmd.Run() == nil
}

// containerPodmanUpdateRun is injectable so tests never shell out to a real
// podman binary; it runs the `podman update ...` call used in
// updateContainerRAMOrCPU.
var containerPodmanUpdateRun = func(context, dir string, args []string) error {
	cmd, err := podman.Command(context, args...)
	if err != nil {
		return err
	}
	cmd.Dir = dir
	return cmd.Run()
}

// restartContainerCmd does podman-compose down then up -d, with no error
// handling of its own -- a failure here is meant to propagate all the way
// out uncaught.
func restartContainerCmd(context, containerName string) error {
	path := filepath.Join(containerHomeBase, context)
	if _, stderr, err := containerComposeCaptureRun(context, path, "down", containerName); err != nil {
		return composeRunError(stderr, err)
	}
	_, stderr, err := containerComposeCaptureRun(context, path, "up", "-d", containerName)
	return composeRunError(stderr, err)
}

// composeRunError puts the last line podman-compose printed into the error, a bare exit status says nothing
func composeRunError(stderr string, err error) error {
	if err == nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return fmt.Errorf("%v: %s", err, last)
	}
	return err
}

// containerActionResult models updateContainerRAMOrCPU's two possible
// return shapes: a plain success string (IsDict=false) or a
// {"success": bool, "message": str} failure dict (IsDict=true, Success is
// always false in practice -- see updateContainerRAMOrCPU).
type containerActionResult struct {
	IsDict  bool
	Success bool
	Message string
	// cpu or ram set to 0, podman update can't lift those so the service needs a restart
	RestartNeeded bool
	Value         string
}

// containerHomeBase is where user contexts keep their compose project, swapped in tests
var containerHomeBase = "/home"

// updateContainerRAMOrCPU updates a container's RAM, CPU, or PIDs limit. The
// returned error is non-nil only for the one case that's genuinely
// unhandled here (a restart_container() failure while resetting a limit to
// 0/unlimited) -- callers should treat that as a bare 500, not a flash.
func updateContainerRAMOrCPU(context, containerName, action, value string) (containerActionResult, error) {
	path := filepath.Join(containerHomeBase, context)
	envVar := containerEnvVarSanitizeRe.ReplaceAllString(strings.ToUpper(containerName), "_") + "_" + strings.ToUpper(action)

	lowerAction := strings.ToLower(action)
	if lowerAction != "ram" && lowerAction != "cpu" && lowerAction != "pids" {
		return containerActionResult{IsDict: true, Message: fmt.Sprintf("Unsupported action: %s. Use 'ram', 'cpu', or 'pids'.", action)}, nil
	}

	if lowerAction == "ram" {
		if value != "0" && !strings.HasSuffix(strings.ToUpper(value), "G") {
			value = value + "G"
		}
	}

	envPath := filepath.Join(path, ".env")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		return containerActionResult{IsDict: true, Message: fmt.Sprintf(".env file not found at %s", envPath)}, nil
	}

	lines := splitFileLinesPreserving(string(raw))
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, envVar+"=") {
			lines[i] = envVar + `="` + value + "\"\n"
			found = true
		}
	}
	// the compose file can only read it if it's there, saying "set" and dropping it misled admins
	if !found {
		if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
			lines[n-1] += "\n"
		}
		lines = append(lines, envVar+`="`+value+"\"\n")
	}
	if err := os.WriteFile(envPath, []byte(strings.Join(lines, "")), 0644); err != nil {
		return containerActionResult{IsDict: true, Message: fmt.Sprintf("Failed to update .env: %v", err)}, nil
	}
	if lowerAction == "pids" {
		composePath := filepath.Join(path, "docker-compose.yml")
		if compose, err := os.ReadFile(composePath); err == nil {
			if fixed, changed := pidsFromEnv(string(compose), containerName, envVar); changed {
				writeKeepingOwner(composePath, fixed)
			}
		}
	}

	unlimited := value == "0" || value == "0G"
	if containerExistsRun(context, containerName) {
		var updateArgs []string
		switch {
		case unlimited && lowerAction != "pids":
			// podman update ignores 0 and rejects -1 for these, the .env is saved and a restart picks it up
			msg := fmt.Sprintf("%s limit for %s set to unlimited, restart the service to apply it.", strings.ToUpper(action), containerName)
			return containerActionResult{Message: msg, RestartNeeded: true, Value: value}, nil
		case lowerAction == "ram":
			updateArgs = []string{"update", "--memory-swap", value, "--memory", value, containerName}
		case lowerAction == "pids":
			pids := value
			if unlimited {
				pids = "-1"
			}
			updateArgs = []string{"update", "--pids-limit", pids, containerName}
		default:
			updateArgs = []string{"update", "--cpus", value, containerName}
		}
		if err := containerPodmanUpdateRun(context, path, updateArgs); err != nil {
			if _, ok := err.(*exec.ExitError); ok {
				return containerActionResult{IsDict: true, Message: fmt.Sprintf("Podman command failed: %v", err)}, nil
			}
			return containerActionResult{IsDict: true, Message: fmt.Sprintf("Unexpected error: %v", err)}, nil
		}
	}

	var message string
	if unlimited {
		message = fmt.Sprintf("%s limits for container %s removed and set to unlimited", strings.ToUpper(action), containerName)
	} else {
		message = fmt.Sprintf("Max %s for container %s set to %s", strings.ToUpper(action), containerName, value)
	}
	return containerActionResult{IsDict: false, Message: message, Value: value}, nil
}

// startStopResult is start_or_stop_container's always-a-dict return shape.
type startStopResult struct {
	Success bool
	Message string
}

// startOrStopContainer starts or stops a container via podman-compose.
func startOrStopContainer(context, containerName, action string, pull bool) startStopResult {
	path := "/home/" + context
	var args []string
	switch action {
	case "activate":
		args = []string{"up", "-d", containerName}
		if pull {
			args = append([]string{"--pull"}, args...)
		}
	case "deactivate":
		args = []string{"down", containerName}
	default:
		return startStopResult{Success: false, Message: "Invalid action: " + action}
	}

	stdout, stderr, err := containerComposeCaptureRun(context, path, args...)
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return startStopResult{Success: false, Message: "Command failed with error:\n" + stderr}
		}
		return startStopResult{Success: false, Message: "Unexpected error: " + err.Error()}
	}
	message := stdout
	if message == "" {
		message = fmt.Sprintf("Container '%s' %sd successfully.", containerName, action)
	}
	return startStopResult{Success: true, Message: message}
}

// ServeManageContainer handles POST
// /containers/{username}/{action}/{container_name}. Like every other route
// in this file, it never strips a "SUSPENDED_..." prefix from username
// before the ownership check.
func (u *Users) ServeManageContainer(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	action := r.PathValue("action")
	containerName := r.PathValue("container_name")
	currentUser := auth.CurrentUser(r)

	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch action {
	case "start", "stop", "restart", "cpu", "ram", "pids":
	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	context, _ := queryContextByUsername(u.MySQL, username)
	ajax := r.Header.Get("X-Requested-With") == "XMLHttpRequest"

	switch action {
	case "cpu", "ram", "pids":
		value := r.PostFormValue("value")
		result, err := updateContainerRAMOrCPU(context, containerName, action, value)
		if ajax {
			if err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
				return
			}
			if result.IsDict && !result.Success {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": result.Message})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{
				"success": true, "message": result.Message, "restart_needed": result.RestartNeeded, "value": result.Value,
			})
			return
		}
		if err != nil {
			// restart_container() failing while resetting the limit to
			// 0/unlimited is treated as a bare 500, no flash message.
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		switch {
		case result.IsDict && !result.Success:
			auth.AddFlash(w, r, u.Sessions, result.Message, "error")
		case result.IsDict:
			// Dead in practice (updateContainerRAMOrCPU never actually
			// returns a success dict, only a plain string on success), kept
			// for structural parity with the IsDict-based branching above.
			auth.AddFlash(w, r, u.Sessions, result.Message, "success")
		default:
			auth.AddFlash(w, r, u.Sessions, result.Message, "info")
		}

	case "restart":
		if ajax {
			if err := restartContainerCmd(context, containerName); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Failed to restart " + containerName + ": " + err.Error()})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Container '" + containerName + "' restarted successfully."})
			return
		}
		if err := restartContainerCmd(context, containerName); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		auth.AddFlash(w, r, u.Sessions, "Container '"+containerName+"' restarted successfully.", "success")

	default: // start, stop
		actionType := "deactivate"
		if action == "start" {
			actionType = "activate"
		}
		result := startOrStopContainer(context, containerName, actionType, false)
		category := "error"
		if result.Success {
			category = "success"
		}
		auth.AddFlash(w, r, u.Sessions, result.Message, category)
	}

	http.Redirect(w, r, "/users/"+username+"#services", http.StatusSeeOther)
}

// containerStatsRun is injectable so tests never shell out to a real podman
// binary. Runs `podman stats --all --no-stream --format {{json .}}` (the
// exit code is inspected manually rather than treating any nonzero exit as
// a hard error).
var containerStatsRun = func(context string) (stdout string, exitCode int, err error) {
	cmd, cmdErr := podman.Command(context, "stats", "--all", "--no-stream", "--format", "{{json .}}")
	if cmdErr != nil {
		return "", 0, cmdErr
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), exitErr.ExitCode(), nil
		}
		return "", 0, runErr
	}
	return outBuf.String(), 0, nil
}

// getAllContainersStats returns a nil/false result to cover both "the
// command itself failed to run" and "it ran but returned nonzero" -- both
// map to ServeContainersStats's 500 response. Any individual line that
// isn't valid JSON is silently skipped rather than fatal to the rest.
func getAllContainersStats(context string) ([]json.RawMessage, bool) {
	stdout, exitCode, err := containerStatsRun(context)
	if err != nil || exitCode != 0 {
		return nil, false
	}
	trimmed := strings.TrimSpace(stdout)
	stats := []json.RawMessage{}
	if trimmed == "" {
		return stats, true
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if json.Valid([]byte(line)) {
			stats = append(stats, json.RawMessage(line))
		}
	}
	return stats, true
}

// ServeContainersStats handles GET /containers/stats/{username}.
func (u *Users) ServeContainersStats(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)

	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	context, _ := queryContextByUsername(u.MySQL, username)
	stats, ok := getAllContainersStats(context)
	if !ok {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "Could not retrieve container stats"})
		return
	}
	writeJSON(w, map[string]interface{}{"container_stats": stats})
}

// containerInspectRun is swapped in tests, it returns podman inspect's json for one container
var containerInspectRun = func(context, containerName string) ([]byte, error) {
	cmd, err := podman.Command(context, "inspect", "--type", "container", containerName)
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}

// containerRuntimeInfo is what the expanded #services row shows about the running container
type containerRuntimeInfo struct {
	Exists       bool              `json:"exists"`
	Status       string            `json:"status"`
	Health       string            `json:"health"`
	StartedAt    string            `json:"started_at"`
	FinishedAt   string            `json:"finished_at"`
	ExitCode     int               `json:"exit_code"`
	RestartCount int               `json:"restart_count"`
	OOMKilled    bool              `json:"oom_killed"`
	Created      string            `json:"created"`
	ImageID      string            `json:"image_id"`
	ImageName    string            `json:"image_name"`
	IPs          map[string]string `json:"ips"`
	Mounts       []string          `json:"mounts"`
}

func parseContainerInspect(raw []byte) containerRuntimeInfo {
	var list []struct {
		Created   string
		Image     string
		ImageName string
		State     struct {
			Status     string
			StartedAt  string
			FinishedAt string
			ExitCode   int
			OOMKilled  bool
			Health     *struct{ Status string }
		}
		RestartCount    int
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
		Mounts []struct {
			Type        string
			Name        string
			Source      string
			Destination string
			RW          bool
		}
	}
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return containerRuntimeInfo{}
	}
	c := list[0]
	info := containerRuntimeInfo{
		Exists: true, Status: c.State.Status, StartedAt: c.State.StartedAt, FinishedAt: c.State.FinishedAt,
		ExitCode: c.State.ExitCode, OOMKilled: c.State.OOMKilled, RestartCount: c.RestartCount,
		Created: c.Created, ImageName: c.ImageName, IPs: map[string]string{}, Mounts: []string{},
	}
	if len(c.Image) > 12 {
		info.ImageID = c.Image[:12]
	}
	if c.State.Health != nil {
		info.Health = c.State.Health.Status
	}
	for name, n := range c.NetworkSettings.Networks {
		info.IPs[name] = n.IPAddress
	}
	for _, m := range c.Mounts {
		src := m.Source
		if m.Type == "volume" && m.Name != "" {
			src = m.Name
		}
		line := src + " → " + m.Destination
		if !m.RW {
			line += " (read-only)"
		}
		info.Mounts = append(info.Mounts, line)
	}
	return info
}

// ServeContainerInspect handles GET /containers/{username}/inspect/{container_name} for the expanded #services row
func (u *Users) ServeContainerInspect(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	context, _ := queryContextByUsername(u.MySQL, username)
	raw, err := containerInspectRun(context, r.PathValue("container_name"))
	if err != nil {
		// not created yet or removed, the row still shows what compose says
		writeJSON(w, containerRuntimeInfo{})
		return
	}
	writeJSON(w, parseContainerInspect(raw))
}
