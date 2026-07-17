// This file implements the command-palette search index, domain-owner
// lookup, and website/user autocomplete search.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// Search bundles /search/*, /domains/{domain_name} handlers.
type Search struct {
	MySQL        *sql.DB
	Sessions     *auth.Manager
	JSONFilePath string
}

// SearchFilteredJSONPath / SearchFallbackJSONPath are the two candidate
// search-index files -- ResolveSearchJSONFilePath picks between them.
var (
	SearchFilteredJSONPath = "/usr/local/admin/core/search/filtered.json"
	SearchFallbackJSONPath = "/usr/local/admin/core/search/filter.json"
)

// ResolveSearchJSONFilePath makes a decided-once choice between the two
// candidate files (preferring "filtered.json" if it exists at that
// moment). Meant to be called once at process startup and stored, not
// re-evaluated per request.
func ResolveSearchJSONFilePath() string {
	if _, err := os.Stat(SearchFilteredJSONPath); err == nil {
		return SearchFilteredJSONPath
	}
	return SearchFallbackJSONPath
}

// ServeSearchFilter handles GET /search/pages.
func (s *Search) ServeSearchFilter(w http.ResponseWriter, r *http.Request) {
	const limitResults = 100

	if _, err := os.Stat(s.JSONFilePath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("[]"))
		return
	}

	raw, err := os.ReadFile(s.JSONFilePath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error loading JSON data")
		return
	}

	var data []json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error loading JSON data")
		return
	}
	if len(data) > limitResults {
		data = data[:limitResults]
	}
	writeJSON(w, data)
}

// searchDomainWhoOwnsRun is injectable so tests never shell out to a real
// opencli binary.
var searchDomainWhoOwnsRun = func(domain string) (string, error) {
	out, err := exec.Command("opencli", "domains-whoowns", domain).Output()
	return string(out), err
}

func renderSearchSystemCustom(w http.ResponseWriter, r *http.Request, message string) {
	webtemplates.Render(w, "system_custom.html", mergeChrome(map[string]interface{}{
		"Message": message,
	}, r, "Error"))
}

// ServeDomainOwner handles GET /domains/{domain_name...}. Registered as
// a wildcard fallback behind the more specific literal /domains routes
// (list, add, zone-templates, ...) -- Go's ServeMux prefers a more
// specific literal route over a wildcard pattern for the same prefix.
func (s *Search) ServeDomainOwner(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	if idx := strings.Index(domainName, "/"); idx != -1 {
		domainName = domainName[:idx]
	}
	wantJSON := r.URL.Query().Get("output") == "json"

	output, err := searchDomainWhoOwnsRun(domainName)
	if err != nil {
		if wantJSON {
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		} else {
			renderSearchSystemCustom(w, r, "Internal Server Error")
		}
		return
	}

	if strings.Contains(output, "not found in the database") {
		if wantJSON {
			writeJSONError(w, http.StatusNotFound, "Domain not found")
		} else {
			renderSearchSystemCustom(w, r, "Domain not found")
		}
		return
	}

	parts := strings.Split(output, ":")
	username := strings.TrimSpace(parts[len(parts)-1])

	if wantJSON {
		writeJSON(w, map[string]string{"domain_name": domainName, "username": username})
		return
	}
	http.Redirect(w, r, "/users/"+username+"#nav-user-data", http.StatusSeeOther)
}

// ServeSearchWebsites handles GET /search/websites and
// /search/websites/{site_name}. Note this returns a nested array of
// 1-tuples here ([["a.com"], ["b.com"]]), whereas ServeSearchUsers below
// returns a flat string list -- a real asymmetry between the two
// nearly-identical endpoints, kept as-is rather than normalized since
// existing API consumers may depend on each shape.
func (s *Search) ServeSearchWebsites(w http.ResponseWriter, r *http.Request) {
	siteName := r.PathValue("site_name")

	var names []string
	var err error
	if siteName != "" {
		names, err = paneldb.SearchSiteNames(s.MySQL, siteName)
	} else {
		names, err = paneldb.ListSiteNames(s.MySQL)
	}
	if err != nil {
		writeJSON(w, [][]string{})
		return
	}

	if len(names) > 10 {
		names = names[:10]
	}
	rows := make([][]string, len(names))
	for i, n := range names {
		rows[i] = []string{n}
	}
	writeJSON(w, rows)
}

// ServeSearchUsers handles GET /search/users and /search/users/{username}.
func (s *Search) ServeSearchUsers(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	resellerOwner := ""
	if cu := auth.CurrentUser(r); cu != nil && cu.Role == "reseller" {
		resellerOwner = cu.Username
	}

	var names []string
	var err error
	if username != "" {
		names, err = paneldb.SearchUsernames(s.MySQL, username, resellerOwner)
	} else {
		names, err = paneldb.ListUsernames(s.MySQL, resellerOwner)
	}
	if err != nil {
		writeJSON(w, []string{})
		return
	}

	if len(names) > 10 {
		names = names[:10]
	}
	writeJSON(w, names)
}
