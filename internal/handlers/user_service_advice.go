package handlers

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// UserHomeBase / UserCoreUsersDir are where a user's .env, vhosts and per-account markers live
var (
	UserHomeBase     = "/home"
	UserCoreUsersDir = "/etc/openpanel/openpanel/core/users"
)

var phpFPMNameRe = regexp.MustCompile(`^php-fpm-(\d+\.\d+)$`)

// userServiceState is everything the services tab advice looks at, gathered once per page load
type userServiceState struct {
	WebServer, DefaultPHP string
	// domain -> php version from its vhost, empty when the vhost has none
	DomainPHP    map[string]string
	Services     []string
	Running      map[string]bool
	AdminBackups bool
}

func readUserEnvValue(path, key string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// readDomainPHPVersions reads the php-fpm-X.Y each domain's vhost points to, same as opencli php-domain does
func readDomainPHPVersions(vhostDir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(vhostDir)
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`php-fpm-(\d+\.\d+)`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(vhostDir, e.Name()))
		if err != nil {
			continue
		}
		version := ""
		if m := re.FindSubmatch(raw); m != nil {
			version = string(m[1])
		}
		out[strings.TrimSuffix(e.Name(), ".conf")] = version
	}
	return out
}

// userServiceStateRun is swapped out in tests
var userServiceStateRun = func(db *sql.DB, username string, services []composeServiceView) userServiceState {
	ctx, err := queryContextByUsername(db, username)
	if err != nil || ctx == "" {
		ctx = username
	}
	home := filepath.Join(UserHomeBase, ctx)
	st := userServiceState{
		WebServer:  readUserEnvValue(filepath.Join(home, ".env"), "WEB_SERVER"),
		DefaultPHP: readUserEnvValue(filepath.Join(home, ".env"), "DEFAULT_PHP_VERSION"),
		DomainPHP:  readDomainPHPVersions(filepath.Join(home, "docker-data", "volumes", ctx+"_webserver_data", "_data")),
	}
	for _, s := range services {
		st.Services = append(st.Services, s.ContainerName)
	}
	if out, err := podmanRunContextRun(ctx, "ps", "--format", "{{.Names}}"); err == nil {
		st.Running = map[string]bool{}
		for _, name := range strings.Fields(out) {
			st.Running[name] = true
		}
	}
	_, flagErr := os.Stat(filepath.Join(UserCoreUsersDir, ctx, "admin.backups"))
	st.AdminBackups = flagErr == nil
	return st
}

// adviseUserServices turns the state into the recommendations shown on the services tab
func adviseUserServices(username string, st userServiceState) []string {
	var out []string
	has := map[string]bool{}
	for _, s := range st.Services {
		has[s] = true
	}
	ws := strings.ToLower(st.WebServer)

	if len(st.DomainPHP) > 0 && st.Running != nil && ws != "" && has[ws] && !st.Running[ws] {
		out = append(out, fmt.Sprintf("The %s webserver isn't running, so this user's %d domain(s) are down. Recommended: start %s.", ws, len(st.DomainPHP), ws))
	}

	var runningPHP []string
	for name := range st.Running {
		if m := phpFPMNameRe.FindStringSubmatch(name); m != nil {
			runningPHP = append(runningPHP, m[1])
		}
	}
	sort.Strings(runningPHP)

	if ws == "openlitespeed" || ws == "litespeed" {
		name := map[string]string{"openlitespeed": "OpenLiteSpeed", "litespeed": "LiteSpeed"}[ws]
		for _, v := range runningPHP {
			out = append(out, fmt.Sprintf("php-fpm-%s isn't needed, %s uses its own LSPHP inside the same container. Recommended: stop php-fpm-%s.", v, name, v))
		}
	} else if len(st.DomainPHP) > 0 {
		used := map[string][]string{}
		for domain, v := range st.DomainPHP {
			if v != "" {
				used[v] = append(used[v], domain)
			}
		}
		for _, v := range runningPHP {
			if len(used[v]) == 0 && v != st.DefaultPHP {
				out = append(out, fmt.Sprintf("php-fpm-%s is running but no domain uses it and it isn't the default PHP version. Recommended: stop php-fpm-%s.", v, v))
			}
		}
		if st.DefaultPHP != "" && len(used) > 0 && len(used[st.DefaultPHP]) == 0 {
			// suggest the version most domains already use
			best := ""
			for v, domains := range used {
				if best == "" || len(domains) > len(used[best]) || (len(domains) == len(used[best]) && compareVersionTags(v, best) > 0) {
					best = v
				}
			}
			msg := fmt.Sprintf("No domain uses the default PHP %s, %d domain(s) use PHP %s. Recommended: set PHP %s as the default with opencli php-default %s --update %s", st.DefaultPHP, len(used[best]), best, best, username, best)
			if st.Running[fmt.Sprintf("php-fpm-%s", st.DefaultPHP)] {
				msg += fmt.Sprintf(", then stop php-fpm-%s to save resources", st.DefaultPHP)
			}
			out = append(out, msg+".")
		}
	}

	if !st.AdminBackups && has["backup"] && st.Running != nil && !st.Running["backup"] {
		out = append(out, "Backups aren't configured for this user: the backup service isn't running and backups aren't admin-configured.")
	}
	return out
}

// adviseServiceLimits flags service limits above the plan (the plan is the ceiling) or above the server itself (the container can't start), 0 is unlimited
func adviseServiceLimits(services []composeServiceView, planName, planCPU, planRAM string, host thresholdHost) []string {
	pCPU, pRAM := parseCPULimit(planCPU), parseRAMLimitGB(planRAM)
	var out []string
	for _, s := range services {
		cpu, _ := strconv.ParseFloat(strings.TrimSpace(s.CPULimit), 64)
		ram, _ := strconv.ParseFloat(strings.TrimSpace(s.MemoryLimitGB), 64)
		switch {
		case cpu > 0 && host.Cores > 0 && cpu > float64(host.Cores):
			out = append(out, fmt.Sprintf("%s can't be started with a CPU limit of %s, the server has %d CPU cores. Set it under %d.", s.ContainerName, formatLimit(cpu), host.Cores, host.Cores))
		case cpu > 0 && pCPU > 0 && cpu > pCPU:
			out = append(out, fmt.Sprintf("%s has a CPU limit of %s, above the %s plan's %s CPU. The plan limit is the ceiling, so it can't use more than %s.", s.ContainerName, formatLimit(cpu), planName, formatLimit(pCPU), formatLimit(pCPU)))
		}
		switch {
		case ram > 0 && host.RAMGB > 0 && ram > host.RAMGB:
			out = append(out, fmt.Sprintf("%s can't be started with a memory limit of %s GB, the server has %.1f GB RAM. Set it under %.1f GB.", s.ContainerName, formatLimit(ram), host.RAMGB, host.RAMGB))
		case ram > 0 && pRAM > 0 && ram > pRAM:
			out = append(out, fmt.Sprintf("%s has a memory limit of %s GB, above the %s plan's %s GB. The plan limit is the ceiling, so it can't use more than %s GB.", s.ContainerName, formatLimit(ram), planName, formatLimit(pRAM), formatLimit(pRAM)))
		}
	}
	return out
}
