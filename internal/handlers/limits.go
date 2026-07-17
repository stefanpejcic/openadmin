// This file implements the /root/.env CPU/RAM/service-limit editor.
package handlers

import (
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Limits bundles the /services/limits handler.
type Limits struct {
	Sessions *auth.Manager
}

// GlobalEnvPath is the global environment file holding CPU/RAM/service
// limits.
var GlobalEnvPath = "/root/.env"

func envKeyExcluded(key string) bool {
	if key == "VERSION" || key == "PORT" {
		return true
	}
	if strings.HasSuffix(key, "_PORT") && key != "PROXY_HTTP_PORT" {
		return true
	}
	return strings.HasSuffix(key, "_PW") || strings.HasSuffix(key, "_PASSWORD") || strings.HasSuffix(key, "_USER")
}

// readEnvGroups groups /root/.env entries by the portion of the key before
// the first underscore. Returns nil if the file is missing.
func readEnvGroups() map[string]map[string]string {
	raw, err := os.ReadFile(GlobalEnvPath)
	if err != nil {
		return nil
	}

	groups := map[string]map[string]string{"DEFAULTS": {}}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"`)
		value = strings.Trim(value, `'`)

		if envKeyExcluded(key) {
			continue
		}

		prefix, suffix := key, ""
		if idx := strings.Index(key, "_"); idx != -1 {
			prefix, suffix = key[:idx], key[idx+1:]
		}
		if groups[prefix] == nil {
			groups[prefix] = map[string]string{}
		}
		groups[prefix][suffix] = value
	}
	return groups
}

type limitsRow struct {
	// FieldName is built as `{{group_key}}_{{subkey}}` literal
	// concatenation -- including its trailing-underscore quirk for any key
	// that has no underscore in it at all (suffix == ""), e.g. a raw
	// "VARNISH" key renders as field name "VARNISH_", which then can never
	// match on POST since the save loop below looks up the exact original
	// key ("VARNISH", no trailing underscore) in the submitted form.
	// That's a pre-existing UI bug, preserved here rather than silently
	// fixed, since fixing it would change which form field name the
	// (unmodified) HTML template needs to submit.
	FieldName string
	Label     string
	Value     string
}

type limitsGroup struct {
	Name string
	Rows []limitsRow
}

// buildLimitsView renders groups into a deterministically ordered slice for
// the template, excluding "DEFAULTS" and "PHP_FPM". Only the HTML view is
// filtered/sorted this way -- the JSON endpoint returns the raw,
// unfiltered groups.
func buildLimitsView(groups map[string]map[string]string) []limitsGroup {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		if k == "DEFAULTS" || k == "PHP_FPM" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	view := make([]limitsGroup, 0, len(keys))
	for _, k := range keys {
		sub := groups[k]
		subkeys := make([]string, 0, len(sub))
		for sk := range sub {
			subkeys = append(subkeys, sk)
		}
		sort.Strings(subkeys)

		rows := make([]limitsRow, 0, len(subkeys))
		for _, sk := range subkeys {
			rows = append(rows, limitsRow{FieldName: k + "_" + sk, Label: sk, Value: sub[sk]})
		}
		view = append(view, limitsGroup{Name: k, Rows: rows})
	}
	return view
}

// ServeLimits handles GET/POST /services/limits.
func (l *Limits) ServeLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		raw, err := os.ReadFile(GlobalEnvPath)
		if err != nil {
			auth.AddFlash(w, r, l.Sessions, "Environment file not found.", "error")
			http.Redirect(w, r, r.URL.String(), http.StatusSeeOther)
			return
		}
		r.ParseForm()

		// SplitAfter keeps each line's own "\n" attached, and we drop the
		// trailing "" piece left behind when the file ends in a newline,
		// so unmodified lines are written back byte-for-byte.
		rawLines := strings.SplitAfter(string(raw), "\n")
		if n := len(rawLines); n > 0 && rawLines[n-1] == "" {
			rawLines = rawLines[:n-1]
		}

		newLines := make([]string, 0, len(rawLines))
		for _, line := range rawLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(line, "=") {
				newLines = append(newLines, line)
				continue
			}

			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])

			if _, ok := r.PostForm[key]; ok {
				newValue := strings.TrimSpace(r.PostFormValue(key))
				if strings.HasSuffix(key, "_RAM") && newValue != "0" {
					newValue += "G"
				}
				newLines = append(newLines, key+`="`+newValue+"\"\n")
			} else {
				newLines = append(newLines, line)
			}
		}

		if err := os.WriteFile(GlobalEnvPath, []byte(strings.Join(newLines, "")), 0644); err != nil {
			auth.AddFlash(w, r, l.Sessions, "Failed to update limits: "+err.Error(), "error")
		} else {
			auth.AddFlash(w, r, l.Sessions, "New limits saved successfully!", "success")
		}
	}

	groups := readEnvGroups()

	if r.URL.Query().Get("output") == "json" {
		writeJSON(w, groups)
		return
	}

	webtemplates.Render(w, "services_limits.html", mergeChrome(map[string]interface{}{
		"Groups":    buildLimitsView(groups),
		"CSRFToken": csrf.Token(r),
		"Flashes":   auth.PopFlashes(w, r, l.Sessions),
	}, r, "Service Limits"))
}
