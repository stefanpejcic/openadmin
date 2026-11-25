package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

var (
	composeEnvRefRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)
	envLineRe       = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)
	envKeyRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func lineIndent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// composeServiceBlock finds one service under services: in the raw file, so editing it keeps the rest byte for byte
func composeServiceBlock(lines []string, service string) (start, end int, indent string, ok bool) {
	inServices := false
	childIndent := -1
	start = -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		ind := lineIndent(line)
		if ind == 0 {
			if start >= 0 {
				return start, i, indent, true
			}
			inServices = strings.HasPrefix(trimmed, "services:")
			continue
		}
		if !inServices {
			continue
		}
		if childIndent < 0 {
			childIndent = ind
		}
		if ind == childIndent {
			if start >= 0 {
				return start, i, indent, true
			}
			key := strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
			if key == service+":" || key == `"`+service+`":` || key == `'`+service+`':` {
				start, indent = i, line[:ind]
			}
		}
	}
	if start >= 0 {
		return start, len(lines), indent, true
	}
	return 0, 0, "", false
}

// trimBlockEnd leaves trailing blank lines and comments (they belong to the next service) outside the block
func trimBlockEnd(lines []string, start, end int) int {
	for end > start+1 {
		t := strings.TrimSpace(lines[end-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return end
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// envRefs lists the variables a compose snippet reads, in the order they show up
func envRefs(snippet string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range splitLines(snippet) {
		// a commented out depends_on doesn't use its variables
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range composeEnvRefRe.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// envRow is one variable of the service, ports are set by OpenPanel so they're read only
type envRow struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	ReadOnly bool   `json:"readonly"`
	// used by the service but not in .env yet
	Missing bool `json:"missing"`
	// docker hub repo whose tag this variable picks, the editor shows a select of its tags
	Repo string `json:"repo,omitempty"`
}

// envValue is the value of a KEY=VALUE line without its quotes
func envValue(line string) string {
	v := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v
}

// portKeys are the variables under the service's ports: plus anything with PORT in its name, users can't change those
func portKeys(snippet string, keys []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		if strings.Contains(k, "PORT") {
			out[k] = true
		}
	}
	portsIndent := -1
	for _, line := range splitLines(snippet) {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		ind := lineIndent(line)
		if portsIndent >= 0 && ind <= portsIndent {
			portsIndent = -1
		}
		if strings.HasPrefix(t, "ports:") {
			portsIndent = ind
		}
		if portsIndent >= 0 {
			for _, m := range composeEnvRefRe.FindAllStringSubmatch(line, -1) {
				out[m[1]] = true
			}
		}
	}
	return out
}

// serviceEnvRows are the variables the snippet reads, in the order it reads them
func serviceEnvRows(env, snippet string) []envRow {
	keys := envRefs(snippet)
	readonly := portKeys(snippet, keys)
	values := map[string]string{}
	for _, line := range splitLines(env) {
		if m := envLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			values[m[1]] = envValue(strings.TrimSpace(line))
		}
	}
	repos := composeImageRepos(snippet)
	rows := make([]envRow, 0, len(keys))
	for _, k := range keys {
		v, ok := values[k]
		row := envRow{Key: k, Value: v, ReadOnly: readonly[k], Missing: !ok}
		if repo, isTag := repos[k]; isTag && !row.ReadOnly {
			if _, hub := hubRepoPath(repo); hub {
				row.Repo = repo
			}
		}
		rows = append(rows, row)
	}
	return rows
}

type serviceConfig struct {
	Compose string            `json:"compose"`
	Env     []envRow          `json:"env"`
	Values  map[string]string `json:"-"`
}

func readServiceConfig(compose, env, service string) (serviceConfig, error) {
	lines := splitLines(compose)
	start, end, indent, ok := composeServiceBlock(lines, service)
	if !ok {
		return serviceConfig{}, fmt.Errorf("service %s not found in docker-compose.yml", service)
	}
	end = trimBlockEnd(lines, start, end)
	block := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		block = append(block, strings.TrimPrefix(l, indent))
	}
	snippet := strings.Join(block, "\n") + "\n"
	return serviceConfig{Compose: snippet, Env: serviceEnvRows(env, snippet)}, nil
}

// applyServiceConfig puts an edited service block and its env values back, it refuses anything that wouldn't parse
func applyServiceConfig(compose, env, service string, edit serviceConfig) (string, string, error) {
	var one map[string]interface{}
	if err := yaml.Unmarshal([]byte(edit.Compose), &one); err != nil {
		return "", "", fmt.Errorf("docker-compose.yml: %v", err)
	}
	if len(one) != 1 || one[service] == nil {
		keys := make([]string, 0, len(one))
		for k := range one {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "", "", fmt.Errorf("docker-compose.yml: the snippet has to be only the %s service, got %s", service, strings.Join(keys, ", "))
	}
	if _, ok := one[service].(map[string]interface{}); !ok {
		return "", "", fmt.Errorf("docker-compose.yml: %s has to be a mapping", service)
	}

	old, err := readServiceConfig(compose, env, service)
	if err != nil {
		return "", "", err
	}
	var before map[string]interface{}
	yaml.Unmarshal([]byte(old.Compose), &before)
	oldSvc, _ := before[service].(map[string]interface{})
	newSvc := one[service].(map[string]interface{})
	if !reflect.DeepEqual(oldSvc["ports"], newSvc["ports"]) {
		return "", "", fmt.Errorf("docker-compose.yml: ports are set by OpenPanel and can't be changed here")
	}
	lines := splitLines(compose)
	start, end, indent, _ := composeServiceBlock(lines, service)
	end = trimBlockEnd(lines, start, end)
	var block []string
	for _, l := range splitLines(edit.Compose) {
		if strings.TrimSpace(l) == "" {
			block = append(block, "")
			continue
		}
		block = append(block, indent+l)
	}
	for len(block) > 0 && block[len(block)-1] == "" {
		block = block[:len(block)-1]
	}
	out := append(append(append([]string{}, lines[:start]...), block...), lines[end:]...)
	newCompose := strings.Join(out, "\n") + "\n"

	var full struct {
		Services map[string]interface{} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(newCompose), &full); err != nil {
		return "", "", fmt.Errorf("docker-compose.yml after the change: %v", err)
	}
	if full.Services[service] == nil {
		return "", "", fmt.Errorf("docker-compose.yml after the change has no %s service", service)
	}

	// ports stay as OpenPanel set them, whether the old or the edited block uses them
	locked := portKeys(old.Compose, envRefs(old.Compose))
	for k := range portKeys(edit.Compose, envRefs(edit.Compose)) {
		locked[k] = true
	}
	current := map[string]string{}
	for _, r := range old.Env {
		if !r.Missing {
			current[r.Key] = r.Value
		}
	}
	keys := make([]string, 0, len(edit.Values))
	for k := range edit.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	set := map[string]string{}
	for _, k := range keys {
		v := edit.Values[k]
		if !envKeyRe.MatchString(k) {
			return "", "", fmt.Errorf(".env: %q is not a valid variable name", k)
		}
		if strings.ContainsAny(v, "\n\r\"") {
			return "", "", fmt.Errorf(".env: %s can't contain quotes or line breaks", k)
		}
		cur, exists := current[k]
		if !exists {
			// read it from the file, it may be there for another service
			for _, line := range splitLines(env) {
				if m := envLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil && m[1] == k {
					cur, exists = envValue(strings.TrimSpace(line)), true
				}
			}
		}
		if locked[k] {
			if v != cur {
				return "", "", fmt.Errorf(".env: %s is a port set by OpenPanel and can't be changed here", k)
			}
			continue
		}
		if exists && v == cur {
			continue
		}
		if !exists && v == "" {
			continue
		}
		set[k] = v
	}

	envLines := splitLines(env)
	done := map[string]bool{}
	for i, l := range envLines {
		if m := envLineRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			if v, ok := set[m[1]]; ok {
				envLines[i] = m[1] + `="` + v + `"`
				done[m[1]] = true
			}
		}
	}
	for _, k := range keys {
		if _, ok := set[k]; ok && !done[k] {
			envLines = append(envLines, k+`="`+set[k]+`"`)
		}
	}
	newEnv := strings.Join(envLines, "\n")
	if newEnv != "" {
		newEnv += "\n"
	}
	return newCompose, newEnv, nil
}

// writeKeepingOwner writes the file back with the mode and owner it had, these belong to the user's context
func writeKeepingOwner(path, content string) error {
	mode := os.FileMode(0644)
	st, statErr := os.Stat(path)
	if statErr == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	if statErr == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			os.Chown(path, int(sys.Uid), int(sys.Gid))
		}
	}
	return nil
}

// ServeServiceConfig handles GET/POST /containers/{username}/config/{service}, one service's compose block and its .env lines
func (u *Users) ServeServiceConfig(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	service := r.PathValue("service")
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"success": false, "error": "Forbidden"})
		return
	}
	context, _ := queryContextByUsername(u.MySQL, username)
	if context == "" || strings.ContainsAny(context, "/.") {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "User not found"})
		return
	}
	composePath := filepath.Join(containerHomeBase, context, "docker-compose.yml")
	envPath := filepath.Join(containerHomeBase, context, ".env")
	compose, err := os.ReadFile(composePath)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "docker-compose.yml not found"})
		return
	}
	env, _ := os.ReadFile(envPath)

	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(1 << 20); err != nil && err != http.ErrNotMultipart {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		edit := serviceConfig{Compose: r.PostFormValue("compose"), Values: map[string]string{}}
		if raw := r.PostFormValue("env"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &edit.Values); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": ".env: " + err.Error()})
				return
			}
		}
		newCompose, newEnv, err := applyServiceConfig(string(compose), string(env), service, edit)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		if newEnv != string(env) {
			if err := writeKeepingOwner(envPath, newEnv); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Failed to save .env: " + err.Error()})
				return
			}
		}
		if newCompose != string(compose) {
			if err := writeKeepingOwner(composePath, newCompose); err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "Failed to save docker-compose.yml: " + err.Error()})
				return
			}
		}
		cfg, _ := readServiceConfig(newCompose, newEnv, service)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{
			"success": true, "message": "Saved " + service + ", restart it to apply the changes.",
			"changed": newCompose != string(compose) || newEnv != string(env),
			"compose": cfg.Compose, "env": cfg.Env,
		})
		return
	}

	cfg, err := readServiceConfig(string(compose), string(env), service)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "compose": cfg.Compose, "env": cfg.Env})
}

// ServeServiceTags handles GET /containers/{username}/config/{service}/tags?key=NGINX_VERSION, the Docker Hub tags of the image that variable picks
func (u *Users) ServeServiceTags(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"tags": []string{}, "error": "Forbidden"})
		return
	}
	context, _ := queryContextByUsername(u.MySQL, username)
	if context == "" || strings.ContainsAny(context, "/.") {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"tags": []string{}, "error": "User not found"})
		return
	}
	compose, err := os.ReadFile(filepath.Join(containerHomeBase, context, "docker-compose.yml"))
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"tags": []string{}, "error": "docker-compose.yml not found"})
		return
	}
	cfg, err := readServiceConfig(string(compose), "", r.PathValue("service"))
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"tags": []string{}, "error": err.Error()})
		return
	}
	// only a repo this service's own image line names, not whatever the request asks for
	repo := composeImageRepos(cfg.Compose)[r.URL.Query().Get("key")]
	hubRepo, ok := hubRepoPath(repo)
	if repo == "" || !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"tags": []string{}, "error": "That variable doesn't pick a Docker Hub image tag."})
		return
	}
	tags, err := hubTags(r.Context(), hubRepo)
	if err != nil {
		writeJSON(w, map[string]interface{}{"tags": []string{}, "error": "Could not load tags: " + err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"tags": tags, "repo": hubRepo})
}
