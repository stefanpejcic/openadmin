package handlers

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
)

// the compose file the .env limits feed, a saved limit is pushed to its running container from here
var LimitsComposePath = "/root/docker-compose.yml"

var (
	composeServiceRe   = regexp.MustCompile(`^  ([A-Za-z0-9_.-]+):\s*$`)
	composeContainerRe = regexp.MustCompile(`^\s*container_name:\s*"?([A-Za-z0-9_.-]+)"?`)
	composeLimitRe     = regexp.MustCompile(`^\s*(cpus|memory|mem_limit):\s*"?\$\{([A-Z0-9_]+)`)
)

type limitTarget struct{ service, container, kind string }

// limitTargets maps each .env limit key to the compose service, container and setting it feeds
func limitTargets(compose string) map[string]limitTarget {
	out := map[string]limitTarget{}
	service, container := "", ""
	var keys []string
	kinds := map[string]string{}
	flush := func() {
		for _, k := range keys {
			if container != "" {
				out[k] = limitTarget{service, container, kinds[k]}
			}
		}
		keys = nil
	}
	for _, line := range strings.Split(compose, "\n") {
		if m := composeServiceRe.FindStringSubmatch(line); m != nil {
			flush()
			service, container = m[1], ""
			continue
		}
		if m := composeContainerRe.FindStringSubmatch(line); m != nil {
			container = m[1]
			continue
		}
		if m := composeLimitRe.FindStringSubmatch(line); m != nil && service != "" {
			kind := "cpus"
			if m[1] != "cpus" {
				kind = "memory"
			}
			kinds[m[2]] = kind
			keys = append(keys, m[2])
		}
	}
	flush()
	return out
}

// isLimitKey is whether a .env key is a CPU or RAM limit
func isLimitKey(key string) bool {
	for _, s := range []string{"_CPU", "_CPUS", "_RAM"} {
		if strings.HasSuffix(key, s) {
			return true
		}
	}
	return false
}

var limitsPodmanUpdateRun = func(container, flag string) error {
	cmd, err := podman.Command("default", "update", flag, container)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return podmanError(out, err)
	}
	return nil
}

var limitsContainerRunning = func(container string) bool {
	cmd, err := podman.Command("default", "inspect", "--format", "{{.State.Running}}", container)
	if err != nil {
		return false
	}
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func podmanError(out []byte, err error) error {
	if msg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "Error: ")); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return err
}

type limitFailure struct {
	Container string `json:"container"`
	Error     string `json:"error"`
}

// limitsResult says what happened to each changed limit after the .env was saved
type limitsResult struct {
	Applied []string       `json:"applied"`
	Pending []string       `json:"pending"`
	Failed  []limitFailure `json:"failed"`
	// set to 0, podman update can't lift a limit so these need a restart
	Recreate []string `json:"recreate"`
	// limit keys the compose file doesn't use as ${KEY}, they only apply on a restart
	Unmapped []string `json:"unmapped"`
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// applyLimitsLive pushes changed CPU/RAM limits to running containers, the rest apply when the container is next created
func applyLimitsLive(changed map[string]string) limitsResult {
	res := limitsResult{Applied: []string{}, Pending: []string{}, Failed: []limitFailure{}, Recreate: []string{}, Unmapped: []string{}}
	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return res
	}
	sort.Strings(keys)
	raw, err := os.ReadFile(LimitsComposePath)
	if err != nil {
		for _, k := range keys {
			if isLimitKey(k) {
				res.Unmapped = append(res.Unmapped, k)
			}
		}
		return res
	}
	targets := limitTargets(string(raw))
	envTargets := composeKeyTargets(string(raw))
	running := map[string]bool{}
	isRunning := func(c string) bool {
		if _, checked := running[c]; !checked {
			running[c] = limitsContainerRunning(c)
		}
		return running[c]
	}
	failed := map[string]bool{}
	for _, key := range keys {
		value := changed[key]
		t, ok := targets[key]
		if !ok {
			// a version, timezone or any other value the container reads only takes effect when it's recreated
			if et, used := envTargets[key]; used {
				if isRunning(et.container) {
					res.Recreate = appendOnce(res.Recreate, et.container)
				} else {
					res.Pending = appendOnce(res.Pending, et.container)
				}
			} else if isLimitKey(key) {
				res.Unmapped = append(res.Unmapped, key)
			}
			continue
		}
		if value == "" {
			continue
		}
		if !isRunning(t.container) {
			res.Pending = appendOnce(res.Pending, t.container)
			continue
		}
		// podman update ignores 0 and rejects -1, only a recreate drops the limit
		if value == "0" {
			res.Recreate = appendOnce(res.Recreate, t.container)
			continue
		}
		flag := "--cpus=" + value
		if t.kind == "memory" {
			flag = "--memory=" + strings.ToLower(value)
		}
		if err := limitsPodmanUpdateRun(t.container, flag); err != nil {
			failed[t.container] = true
			res.Failed = append(res.Failed, limitFailure{t.container, key + ": " + err.Error()})
			continue
		}
		res.Applied = appendOnce(res.Applied, t.container)
	}
	// a container with one failed key isn't fully applied
	kept := res.Applied[:0]
	for _, c := range res.Applied {
		if !failed[c] {
			kept = append(kept, c)
		}
	}
	res.Applied = kept
	return res
}

// limitsMessage is the one line summary shown after a save
func limitsMessage(res limitsResult) string {
	msg := "New limits saved successfully!"
	if len(res.Applied) > 0 {
		msg += " Applied right away to: " + strings.Join(res.Applied, ", ") + "."
	}
	if len(res.Pending) > 0 {
		msg += " Not running, applied when started from Services: " + strings.Join(res.Pending, ", ") + "."
	}
	for _, f := range res.Failed {
		msg += " Failed to apply to " + f.Container + " (" + f.Error + "), restart it to apply."
	}
	if len(res.Recreate) > 0 {
		msg += " Restart to apply: " + strings.Join(res.Recreate, ", ") + "."
	}
	if len(res.Unmapped) > 0 {
		msg += " Applied after the service restarts: " + strings.Join(res.Unmapped, ", ") + "."
	}
	return msg
}

// composeContainers maps each container_name in the compose file to its service
func composeContainers(compose string) map[string]string {
	out := map[string]string{}
	service := ""
	for _, line := range strings.Split(compose, "\n") {
		if m := composeServiceRe.FindStringSubmatch(line); m != nil {
			service = m[1]
			continue
		}
		if m := composeContainerRe.FindStringSubmatch(line); m != nil && service != "" {
			out[m[1]] = service
		}
	}
	return out
}

var limitsPodmanOut = func(args ...string) (string, error) {
	cmd, err := podman.Command("default", args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	return string(out), err
}

// containerDependents lists every container that needs this one, compose depends_on becomes podman --requires
func containerDependents(container string) ([]string, error) {
	ids, err := limitsPodmanOut("ps", "-aq", "--no-trunc")
	if err != nil {
		return nil, err
	}
	list := strings.Fields(ids)
	if len(list) == 0 {
		return nil, nil
	}
	out, err := limitsPodmanOut(append([]string{"inspect", "--format", "{{.Name}}|{{.Id}}|{{range .Dependencies}}{{.}},{{end}}"}, list...)...)
	if err != nil {
		return nil, err
	}
	nameByID := map[string]string{}
	needs := map[string][]string{}
	targetID := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		nameByID[parts[1]] = parts[0]
		if parts[0] == container {
			targetID = parts[1]
		}
		for _, dep := range strings.Split(parts[2], ",") {
			if dep != "" {
				needs[dep] = append(needs[dep], parts[1])
			}
		}
	}
	var res []string
	seen := map[string]bool{targetID: true}
	queue := []string{targetID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range needs[id] {
			if !seen[d] {
				seen[d] = true
				res = append(res, nameByID[d])
				queue = append(queue, d)
			}
		}
	}
	sort.Strings(res)
	return res, nil
}

var limitsComposeRun = func(args ...string) ([]byte, error) {
	cmd, err := podman.ComposeCommand("default", args...)
	if err != nil {
		return nil, err
	}
	cmd.Dir = "/root"
	return cmd.CombinedOutput()
}

// limitsRecreateRun removes the container and whatever depends on it and brings them back from compose, so they pick up the .env as is
var limitsRecreateRun = func(t limitTarget) (string, []string, error) {
	raw, err := os.ReadFile(LimitsComposePath)
	if err != nil {
		return "", nil, err
	}
	services := composeContainers(string(raw))
	deps, err := containerDependents(t.container)
	if err != nil {
		return "", nil, err
	}
	toUp := []string{t.service}
	for _, d := range deps {
		svc, ok := services[d]
		if !ok {
			// it'd be removed with no way to bring it back from here
			return "", nil, fmt.Errorf("%s depends on it and isn't in %s, restart it from Services instead", d, LimitsComposePath)
		}
		toUp = append(toUp, svc)
	}
	_, _ = limitsPodmanOut(append([]string{"stop", "-t", "10"}, append(deps, t.container)...)...)
	if _, err := limitsPodmanOut("rm", "-f", "--depend", t.container); err != nil {
		return "", nil, fmt.Errorf("could not remove %s: %v", t.container, err)
	}
	out, err := limitsComposeRun(append([]string{"up", "-d", "--no-deps"}, toUp...)...)
	if err != nil {
		// a dependency that wasn't running has to come up too
		out, err = limitsComposeRun(append([]string{"up", "-d"}, toUp...)...)
	}
	if err == nil && (t.container == "openpanel" || slices.Contains(deps, "openpanel")) {
		os.WriteFile(OpenpanelRestartFlagPath, []byte(""), 0644)
	}
	return string(out), deps, err
}

// ServeRecreate handles POST /services/limits/recreate, it restarts one compose container so a limit set to 0 is dropped
func (l *Limits) ServeRecreate(w http.ResponseWriter, r *http.Request) {
	container := strings.TrimSpace(r.FormValue("container"))
	var target *limitTarget
	if raw, err := os.ReadFile(LimitsComposePath); err == nil {
		for _, t := range limitTargets(string(raw)) {
			if t.container == container {
				t := t
				target = &t
				break
			}
		}
	}
	switch {
	case container == "" || target == nil:
		auth.AddFlash(w, r, l.Sessions, "Unknown service.", "error")
	default:
		if out, deps, err := limitsRecreateRun(*target); err != nil {
			msg := strings.TrimSpace(out)
			if msg == "" {
				msg = err.Error()
			}
			auth.AddFlash(w, r, l.Sessions, "Failed to restart "+container+": "+msg, "error")
		} else {
			msg := container + " restarted with the new limits."
			if len(deps) > 0 {
				msg += " Also restarted " + strings.Join(deps, ", ") + " since they depend on it."
			}
			auth.AddFlash(w, r, l.Sessions, msg, "success")
		}
	}
	http.Redirect(w, r, "/services/limits", http.StatusSeeOther)
}
