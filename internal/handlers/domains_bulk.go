package handlers

import (
	"fmt"

	"net/http"
	"openadmin/internal/activity"
	"regexp"
	"strings"
	"sync"
)

// bulkDomainLine is one parsed line of the bulk Add Domain textarea
type bulkDomainLine struct {
	Line       int    `json:"line"`
	Domain     string `json:"domain"`
	Username   string `json:"username"`
	Docroot    string `json:"docroot,omitempty"`
	PHPVersion string `json:"php_version,omitempty"`
}

const bulkDomainsMax = 500

var (
	bulkDomainRe   = regexp.MustCompile(`^[\p{L}\p{N}]([\p{L}\p{N}-]*[\p{L}\p{N}])?(\.[\p{L}\p{N}]([\p{L}\p{N}-]*[\p{L}\p{N}])?)+$`)
	bulkUsernameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
	bulkPHPRe      = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

// parseBulkDomains reads "<domain>|<user>[|<docroot>][|<php>]" lines, spaces work instead of |, and returns every problem found so the admin can fix them in one go
func parseBulkDomains(text string, allowDocroot bool) ([]bulkDomainLine, []string) {
	var lines []bulkDomainLine
	var errs []string
	seen := map[string]int{}

	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		n := i + 1
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}

		var fields []string
		if strings.Contains(raw, "|") {
			for _, f := range strings.Split(raw, "|") {
				if f = strings.TrimSpace(f); f != "" {
					fields = append(fields, f)
				}
			}
		} else {
			fields = strings.Fields(raw)
		}

		if len(fields) < 2 {
			errs = append(errs, fmt.Sprintf("Line %d: expected <domain>|<username>, got %q.", n, raw))
			continue
		}
		if len(fields) > 4 {
			errs = append(errs, fmt.Sprintf("Line %d: too many fields, expected <domain>|<username>|<docroot>|<php version>.", n))
			continue
		}

		entry := bulkDomainLine{Line: n, Domain: strings.ToLower(fields[0]), Username: fields[1]}
		lineOK := true
		fail := func(msg string) {
			errs = append(errs, fmt.Sprintf("Line %d: %s", n, msg))
			lineOK = false
		}

		if len(entry.Domain) > 253 || !bulkDomainRe.MatchString(entry.Domain) {
			fail(fmt.Sprintf("%q is not a valid domain.", fields[0]))
		} else if prev, dup := seen[entry.Domain]; dup {
			fail(fmt.Sprintf("%s is already on line %d.", entry.Domain, prev))
		}
		if !bulkUsernameRe.MatchString(entry.Username) {
			fail(fmt.Sprintf("%q is not a valid username.", entry.Username))
		}

		// docroot starts with /, php version looks like 8.3, so either can be left out
		for _, f := range fields[2:] {
			switch {
			case strings.HasPrefix(f, "/"):
				if entry.Docroot != "" {
					fail("more than one docroot.")
				} else if !allowDocroot {
					fail("a custom docroot requires an Enterprise license.")
				} else if msg := validateDocroot(f); msg != "" {
					fail(msg)
				} else {
					entry.Docroot = f
				}
			case bulkPHPRe.MatchString(f):
				if entry.PHPVersion != "" {
					fail("more than one PHP version.")
				} else {
					entry.PHPVersion = f
				}
			default:
				fail(fmt.Sprintf("%q is neither a docroot (/var/www/html/...) nor a PHP version (e.g. 8.3).", f))
			}
		}

		if lineOK {
			seen[entry.Domain] = n
			lines = append(lines, entry)
		}
	}

	if len(errs) == 0 && len(lines) == 0 {
		errs = append(errs, "No domains to add.")
	}
	if len(lines) > bulkDomainsMax {
		errs = append(errs, fmt.Sprintf("At most %d domains can be added at once.", bulkDomainsMax))
	}
	return lines, errs
}

// missingBulkUsers returns the usernames that don't exist, suspended accounts count as missing
func (d *Domains) missingBulkUsers(lines []bulkDomainLine) []string {
	if d.MySQL == nil {
		return nil
	}
	checked := map[string]bool{}
	var missing []string
	for _, l := range lines {
		if _, done := checked[l.Username]; done {
			continue
		}
		var found int
		err := d.MySQL.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", l.Username).Scan(&found)
		checked[l.Username] = err == nil && found > 0
		if !checked[l.Username] {
			missing = append(missing, l.Username)
		}
	}
	return missing
}

type bulkDomainFailure struct {
	Line   int    `json:"line"`
	Domain string `json:"domain"`
	Error  string `json:"error"`
}

type bulkDomainsResult struct {
	Done      bool                `json:"done"`
	Total     int                 `json:"total"`
	Completed int                 `json:"completed"`
	Current   string              `json:"current"`
	Added     []string            `json:"added"`
	Failed    []bulkDomainFailure `json:"failed"`
	Message   string              `json:"message"`
}

var (
	pendingBulkDomainsMu sync.Mutex
	pendingBulkDomains   *bulkDomainsResult
)

// bulkDomainsRun is a var so tests can stub the opencli call
var bulkDomainsRun = func(args ...string) (bool, string) {
	return runOpenCLI("", args...)
}

// HandleBulkAdd handles POST /domains/bulk-add, validates every line first and then adds the domains one by one in the background
func (d *Domains) HandleBulkAdd(w http.ResponseWriter, r *http.Request) {
	lines, errs := parseBulkDomains(r.FormValue("domains"), chromeSite.LicenseType == "Enterprise")
	if len(errs) == 0 {
		for _, u := range d.missingBulkUsers(lines) {
			errs = append(errs, fmt.Sprintf("User %q does not exist.", u))
		}
	}
	if len(errs) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"errors": errs})
		return
	}

	pendingBulkDomainsMu.Lock()
	if pendingBulkDomains != nil && !pendingBulkDomains.Done {
		pendingBulkDomainsMu.Unlock()
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"errors": []string{"Domains are already being added, wait for that to finish."}})
		return
	}
	names := make([]string, len(lines))
	for i, l := range lines {
		names[i] = l.Domain
	}
	activity.Describe(r.Context(), fmt.Sprintf("Started bulk add of %d domains: %s", len(lines), strings.Join(names, ", ")))

	result := &bulkDomainsResult{Total: len(lines), Added: []string{}, Failed: []bulkDomainFailure{}}
	pendingBulkDomains = result
	pendingBulkDomainsMu.Unlock()

	go func() {
		for _, l := range lines {
			pendingBulkDomainsMu.Lock()
			result.Current = l.Domain
			pendingBulkDomainsMu.Unlock()

			args := []string{"opencli", "domains-add", l.Domain, l.Username}
			if l.Docroot != "" {
				args = append(args, "--docroot", l.Docroot)
			}
			if l.PHPVersion != "" {
				args = append(args, "--php_version", l.PHPVersion)
			}
			ok, output := bulkDomainsRun(args...)

			pendingBulkDomainsMu.Lock()
			result.Completed++
			if ok {
				result.Added = append(result.Added, l.Domain)
			} else {
				result.Failed = append(result.Failed, bulkDomainFailure{Line: l.Line, Domain: l.Domain, Error: output})
			}
			pendingBulkDomainsMu.Unlock()
		}

		pendingBulkDomainsMu.Lock()
		result.Done = true
		result.Current = ""
		result.Message = fmt.Sprintf("Added %d/%d domains.", len(result.Added), result.Total)
		pendingBulkDomainsMu.Unlock()
	}()

	writeJSON(w, map[string]interface{}{"scheduled": true, "total": len(lines)})
}

// ServeBulkAddStatus handles GET /domains/bulk-add-status
func (d *Domains) ServeBulkAddStatus(w http.ResponseWriter, r *http.Request) {
	pendingBulkDomainsMu.Lock()
	defer pendingBulkDomainsMu.Unlock()
	if pendingBulkDomains == nil {
		writeJSON(w, bulkDomainsResult{Done: true, Message: "No bulk add has run yet."})
		return
	}
	writeJSON(w, *pendingBulkDomains)
}
