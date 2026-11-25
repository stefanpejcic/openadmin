package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"openadmin/internal/podman"
)

var (
	composeKeyRefRe = regexp.MustCompile(`\$\{([A-Z0-9_]+)`)
	// image: repo:${VAR...}, the tag is the part the page can pick
	composeImageVarRe = regexp.MustCompile(`^\s*image:\s*"?([^\s:"${}]+(?:/[^\s:"${}]+)*):\$\{([A-Z0-9_]+)`)
	hubTagRe          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// composeKeyTargets maps every ${KEY} the compose file reads to the service using it, not only the cpu/ram ones
func composeKeyTargets(compose string) map[string]limitTarget {
	out := map[string]limitTarget{}
	service, container := "", ""
	var keys []string
	flush := func() {
		for _, k := range keys {
			if container != "" {
				if _, taken := out[k]; !taken {
					out[k] = limitTarget{service, container, "env"}
				}
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
		if strings.HasPrefix(strings.TrimSpace(line), "#") || service == "" {
			continue
		}
		if m := composeContainerRe.FindStringSubmatch(line); m != nil {
			container = m[1]
		}
		for _, m := range composeKeyRefRe.FindAllStringSubmatch(line, -1) {
			keys = append(keys, m[1])
		}
	}
	flush()
	return out
}

// composeImageRepos maps a tag variable to the image repo it picks the tag of
func composeImageRepos(compose string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(compose, "\n") {
		if m := composeImageVarRe.FindStringSubmatch(line); m != nil {
			out[m[2]] = m[1]
		}
	}
	return out
}

// groupContainers maps a limits page group (the key prefix) to the container its keys feed
func groupContainers(compose string) map[string]string {
	out := map[string]string{}
	for key, t := range composeKeyTargets(compose) {
		prefix := key
		if i := strings.Index(key, "_"); i != -1 {
			prefix = key[:i]
		}
		if _, ok := out[prefix]; !ok {
			out[prefix] = t.container
		}
	}
	return out
}

// --- live usage ---

var limitsStatsRun = func() ([]byte, error) {
	cmd, err := podman.Command("default", "stats", "--all", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}

// limitsUsage is one container's live usage, cpu is percent of one core like podman reports it
type limitsUsage struct {
	CPU      float64 `json:"cpu"`
	MemUsage float64 `json:"mem_usage"`
	MemLimit float64 `json:"mem_limit"`
	MemPerc  float64 `json:"mem_perc"`
	PIDs     int     `json:"pids"`
}

func parseLimitsStats(raw []byte) map[string]limitsUsage {
	out := map[string]limitsUsage{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c struct {
			Name     string
			CPU      float64
			MemUsage float64
			MemLimit float64
			MemPerc  float64
			PIDs     int
		}
		if json.Unmarshal([]byte(line), &c) == nil && c.Name != "" {
			out[c.Name] = limitsUsage{c.CPU, c.MemUsage, c.MemLimit, c.MemPerc, c.PIDs}
		}
	}
	return out
}

// ServeUsage handles GET /services/limits/usage, what each system container uses right now
func (l *Limits) ServeUsage(w http.ResponseWriter, r *http.Request) {
	raw, err := limitsStatsRun()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "Could not read container stats"})
		return
	}
	writeJSON(w, parseLimitsStats(raw))
}

// --- docker hub tags ---

var (
	hubTagsCacheDir = "/var/cache/openadmin/hubtags"
	hubTagsTTL      = 24 * time.Hour
)

// hubRepoPath turns an image repo into Docker Hub's namespace/name, false for other registries
func hubRepoPath(repo string) (string, bool) {
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "index.docker.io/")
	parts := strings.Split(repo, "/")
	if strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost" {
		return "", false
	}
	switch len(parts) {
	case 1:
		return "library/" + parts[0], true
	case 2:
		return repo, true
	}
	return "", false
}

func keepHubTag(tag string) bool {
	return !strings.HasPrefix(tag, "sha256-") && !strings.HasSuffix(tag, ".sig") && !strings.HasSuffix(tag, ".att") && hubTagRe.MatchString(tag)
}

var hubTagsFetchRun = func(ctx context.Context, hubRepo string, maxTags int) ([]string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	next := "https://hub.docker.com/v2/repositories/" + hubRepo + "/tags?page_size=100&ordering=last_updated"
	var tags []string
	for page := 0; page < 5 && next != "" && len(tags) < maxTags; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, errors.New("Docker Hub returned " + resp.Status)
		}
		var payload struct {
			Next    string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range payload.Results {
			if keepHubTag(r.Name) {
				tags = append(tags, r.Name)
			}
		}
		// only follow Docker Hub's own next links
		if u, err := url.Parse(payload.Next); err != nil || u.Host != "hub.docker.com" {
			next = ""
		} else {
			next = payload.Next
		}
	}
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	return tags, nil
}

// hubTags is the cached tag list of a repo, refetched once it's older than a day
func hubTags(ctx context.Context, hubRepo string) ([]string, error) {
	path := filepath.Join(hubTagsCacheDir, strings.ReplaceAll(hubRepo, "/", "__")+".json")
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < hubTagsTTL {
		var tags []string
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &tags) == nil {
			return tags, nil
		}
	}
	tags, err := hubTagsFetchRun(ctx, hubRepo, 300)
	if err != nil {
		// a stale list beats none when Docker Hub is down or rate limiting
		if raw, rerr := os.ReadFile(path); rerr == nil {
			var old []string
			if json.Unmarshal(raw, &old) == nil {
				return old, nil
			}
		}
		return nil, err
	}
	if os.MkdirAll(hubTagsCacheDir, 0755) == nil {
		if raw, err := json.Marshal(tags); err == nil {
			os.WriteFile(path, raw, 0644)
		}
	}
	return tags, nil
}

// ServeTags handles GET /services/limits/tags?key=BIND9_VERSION, the Docker Hub tags of the image that variable picks
func (l *Limits) ServeTags(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	raw, err := os.ReadFile(LimitsComposePath)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"tags": []string{}, "error": "Compose file not found."})
		return
	}
	repo, ok := composeImageRepos(string(raw))[key]
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"tags": []string{}, "error": "That variable doesn't set an image tag."})
		return
	}
	hubRepo, ok := hubRepoPath(repo)
	if !ok {
		writeJSON(w, map[string]interface{}{"tags": []string{}, "error": "Only Docker Hub images can list tags."})
		return
	}
	tags, err := hubTags(r.Context(), hubRepo)
	if err != nil {
		writeJSON(w, map[string]interface{}{"tags": []string{}, "error": "Could not load tags: " + err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"tags": tags, "repo": hubRepo})
}
