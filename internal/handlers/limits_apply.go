package handlers

import (
	"os"
	"regexp"
	"strings"

	"openadmin/internal/podman"
)

// the compose file the .env limits feed, a saved limit is pushed to its running container from here
var LimitsComposePath = "/root/docker-compose.yml"

var (
	composeContainerRe = regexp.MustCompile(`^\s*container_name:\s*"?([A-Za-z0-9_.-]+)"?`)
	composeLimitRe     = regexp.MustCompile(`^\s*(cpus|memory|mem_limit):\s*"?\$\{([A-Z0-9_]+)`)
)

type limitTarget struct{ container, kind string }

// limitTargets maps each .env limit key to the container and setting it feeds in the compose file
func limitTargets(compose string) map[string]limitTarget {
	out := map[string]limitTarget{}
	container := ""
	for _, line := range strings.Split(compose, "\n") {
		// a new top level service resets the container
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			container = ""
		}
		if m := composeContainerRe.FindStringSubmatch(line); m != nil {
			container = m[1]
			continue
		}
		if m := composeLimitRe.FindStringSubmatch(line); m != nil && container != "" {
			kind := "cpus"
			if m[1] != "cpus" {
				kind = "memory"
			}
			out[m[2]] = limitTarget{container, kind}
		}
	}
	return out
}

var limitsPodmanUpdateRun = func(container, flag string) error {
	cmd, err := podman.Command("default", "update", flag, container)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// applyLimitsLive pushes changed CPU/RAM limits to running containers, the rest apply when the container is next created
func applyLimitsLive(changed map[string]string) (applied, failed []string) {
	raw, err := os.ReadFile(LimitsComposePath)
	if err != nil {
		return nil, nil
	}
	targets := limitTargets(string(raw))
	seen := map[string]bool{}
	for key, value := range changed {
		t, ok := targets[key]
		if !ok || value == "" || value == "0" {
			continue
		}
		flag := "--cpus=" + value
		if t.kind == "memory" {
			flag = "--memory=" + strings.ToLower(value)
		}
		if err := limitsPodmanUpdateRun(t.container, flag); err != nil {
			failed = append(failed, t.container)
			continue
		}
		if !seen[t.container] {
			seen[t.container] = true
			applied = append(applied, t.container)
		}
	}
	return applied, failed
}
