// This file implements the domains-list PHP-version editor: GET/POST
// /php/{value}, where value is either a username (the default PHP version
// new domains get, via `opencli php-default`) or a domain (that domain's
// own PHP version override, via `opencli php-domain`) -- distinguished by
// isDomain, the same way the rest of this codebase tells the two apart,
// since a domain always contains a dot and a username never does. Also
// implements GET /php/{username}/available, which lists that user's
// PHP-FPM versions as declared by their docker-compose.yml, cached for an
// hour since that file rarely changes.
package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// phpDomainVersionGetRun / phpDomainVersionSetRun are injectable so tests
// never shell out to a real opencli binary. Like
// phpDefaultVersionGetRun/SetRun in php.go, a nonzero exit is not itself
// an error -- only a failure to even start the process is. This matters
// more here than for php-default: opencli php-domain's LiteSpeed-rejection
// case ("ERROR: PHP version can not be changed on ...") exits 0 despite
// being a failure, so the output text always has to be inspected
// regardless of exit code.
var phpDomainVersionGetRun = func(domain string) (string, error) {
	out, err := exec.Command("opencli", "php-domain", domain).CombinedOutput()
	if _, isExit := err.(*exec.ExitError); isExit {
		return string(out), nil
	}
	return string(out), err
}

var phpDomainVersionSetRun = func(domain, version string) (string, error) {
	out, err := exec.Command("opencli", "php-domain", domain, "--update", version).CombinedOutput()
	if _, isExit := err.(*exec.ExitError); isExit {
		return string(out), nil
	}
	return string(out), err
}

// domainPHPVersionRE parses `opencli php-domain <domain>`'s success line:
// "Domain '<domain>' (owned by user: <owner>) uses PHP version: <version>".
var domainPHPVersionRE = regexp.MustCompile(`^Domain '.+' \(owned by user: (.+)\) uses PHP version: (.+)$`)

// parseDefaultPHPVersionOutput extracts the version from `opencli
// php-default <username>`'s "Default PHP version for user '<username>' is:
// <version>" success line, returning "" for anything else (unset default,
// missing .env, unknown user).
func parseDefaultPHPVersionOutput(username, output string) string {
	output = strings.TrimSpace(output)
	prefix := "Default PHP version for user '" + username + "' is: "
	if strings.HasPrefix(output, prefix) {
		return strings.TrimPrefix(output, prefix)
	}
	return ""
}

// defaultPHPVersionFor returns username's default PHP version, or "" if it
// can't be determined (used to conditionally show the field on
// /users/<username> without surfacing plumbing errors there).
func defaultPHPVersionFor(username string) string {
	output, err := phpDefaultVersionGetRun(username)
	if err != nil {
		return ""
	}
	return parseDefaultPHPVersionOutput(username, output)
}

// ServePHPVersion handles GET/POST /php/{value}.
func (p *PHP) ServePHPVersion(w http.ResponseWriter, r *http.Request) {
	value := r.PathValue("value")
	if isDomain(value) {
		p.servePHPVersionForDomain(w, r, value)
		return
	}
	p.servePHPVersionForUser(w, r, value)
}

func (p *PHP) servePHPVersionForUser(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method == http.MethodPost {
		version := strings.TrimSpace(r.FormValue("version"))
		if version == "" {
			writeJSONError(w, http.StatusBadRequest, "version is required")
			return
		}
		if err := phpDefaultVersionSetRun(username, version); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
				"error":   "Failed to update default PHP version",
				"details": err.Error(),
			})
			return
		}
		writeJSON(w, map[string]string{"username": username, "version": version})
		return
	}

	output, err := phpDefaultVersionGetRun(username)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "Failed to retrieve default PHP version",
			"details": err.Error(),
		})
		return
	}
	if version := parseDefaultPHPVersionOutput(username, output); version != "" {
		writeJSON(w, map[string]string{"username": username, "version": version})
		return
	}
	writeJSONError(w, http.StatusBadRequest, firstLine(strings.TrimSpace(output)))
}

func (p *PHP) servePHPVersionForDomain(w http.ResponseWriter, r *http.Request, domain string) {
	if r.Method == http.MethodPost {
		version := strings.TrimSpace(r.FormValue("version"))
		if version == "" {
			writeJSONError(w, http.StatusBadRequest, "version is required")
			return
		}
		output, err := phpDomainVersionSetRun(domain, version)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
				"error":   "Failed to update PHP version",
				"details": err.Error(),
			})
			return
		}
		trimmed := strings.TrimSpace(output)
		if strings.HasPrefix(trimmed, "Updated PHP version") {
			writeJSON(w, map[string]string{"domain": domain, "version": version})
			return
		}
		writeJSONError(w, http.StatusBadRequest, firstLine(trimmed))
		return
	}

	output, err := phpDomainVersionGetRun(domain)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "Failed to retrieve PHP version",
			"details": err.Error(),
		})
		return
	}
	trimmed := strings.TrimSpace(output)
	if m := domainPHPVersionRE.FindStringSubmatch(trimmed); m != nil {
		writeJSON(w, map[string]string{"domain": domain, "owner": m[1], "version": m[2]})
		return
	}
	writeJSONError(w, http.StatusBadRequest, firstLine(trimmed))
}

// phpFPMServiceRE matches a docker-compose service name declaring a
// PHP-FPM version, e.g. "php-fpm-8.2".
var phpFPMServiceRE = regexp.MustCompile(`^php-fpm-(\d+\.\d+)$`)

// availablePHPVersionsRun reads context's docker-compose.yml directly (no
// podman-compose invocation needed -- the service names are already
// literal in the file) and returns every php-fpm-<version> service name it
// declares, sorted. Injectable so tests don't need a real
// docker-compose.yml on disk.
var availablePHPVersionsRun = func(context string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join("/home", context, "docker-compose.yml"))
	if err != nil {
		return nil, err
	}
	return parsePHPFPMServiceVersions(data)
}

// parsePHPFPMServiceVersions extracts every php-fpm-<version> service name
// declared in a docker-compose.yml's top-level "services" map, sorted.
// Split out from availablePHPVersionsRun so the YAML-parsing logic can be
// tested against arbitrary bytes without a real file on disk.
func parsePHPFPMServiceVersions(data []byte) ([]string, error) {
	var parsed struct {
		Services map[string]interface{} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(parsed.Services))
	for name := range parsed.Services {
		if m := phpFPMServiceRE.FindStringSubmatch(name); m != nil {
			versions = append(versions, m[1])
		}
	}
	sort.Strings(versions)
	return versions, nil
}

type phpAvailableVersionsCacheEntry struct {
	versions []string
	err      error
	expires  time.Time
}

var (
	phpAvailableVersionsCacheMu sync.Mutex
	phpAvailableVersionsCache   = map[string]phpAvailableVersionsCacheEntry{}
	phpAvailableVersionsTTL     = time.Hour
)

// cachedAvailablePHPVersions caches availablePHPVersionsRun's result (value
// and error alike) per context for phpAvailableVersionsTTL, since reading
// and re-parsing docker-compose.yml on every keystroke of an admin opening
// the editor is wasted work -- that file only changes when the user's
// webserver/PHP setup itself changes.
func cachedAvailablePHPVersions(context string) ([]string, error) {
	phpAvailableVersionsCacheMu.Lock()
	if entry, ok := phpAvailableVersionsCache[context]; ok && time.Now().Before(entry.expires) {
		phpAvailableVersionsCacheMu.Unlock()
		return entry.versions, entry.err
	}
	phpAvailableVersionsCacheMu.Unlock()

	versions, err := availablePHPVersionsRun(context)

	phpAvailableVersionsCacheMu.Lock()
	phpAvailableVersionsCache[context] = phpAvailableVersionsCacheEntry{versions: versions, err: err, expires: time.Now().Add(phpAvailableVersionsTTL)}
	phpAvailableVersionsCacheMu.Unlock()

	return versions, err
}

// ServePHPAvailableVersions handles GET /php/{username}/available.
func (p *PHP) ServePHPAvailableVersions(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	context, err := queryContextByUsername(p.MySQL, username)
	if err != nil || context == "" {
		writeJSONError(w, http.StatusNotFound, "User not found")
		return
	}
	versions, err := cachedAvailablePHPVersions(context)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "Failed to read available PHP versions",
			"details": err.Error(),
		})
		return
	}
	writeJSON(w, map[string]interface{}{"username": username, "versions": versions})
}
