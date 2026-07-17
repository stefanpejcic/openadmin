// This file implements the per-domain webserver access log viewer at
// GET /domains/log, GET /domains/log/, and GET /domains/log/{domain_name}.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// AccessLogsDir is the directory containing each domain's webserver access
// log (/var/log/caddy/domlogs/<domain>/access.log).
var AccessLogsDir = "/var/log/caddy/domlogs"

// AccessLogTotalAllowedForShowAll is only ever used to decide whether the
// "Show all N rows" checkbox is displayed at all; it does NOT cap how many
// rows show_all=true actually returns.
const AccessLogTotalAllowedForShowAll = 10000

// AccessLogPageSize is the page size used when not showing all rows.
const AccessLogPageSize = 1000

// AccessLogs bundles the /domains/log handlers.
type AccessLogs struct {
	MySQL    *sql.DB
	Sessions *auth.Manager
}

// clampSlice returns logs[start:end] with start/end clamped into range and
// negative values treated as counting back from the end (Python-style
// slicing semantics), since page/query params can otherwise produce a
// negative start index (e.g. page=0 or a negative page query param).
func clampSlice(logs []map[string]interface{}, start, end int) []map[string]interface{} {
	n := len(logs)
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	s, e := norm(start), norm(end)
	if s >= e {
		return []map[string]interface{}{}
	}
	return logs[s:e]
}

// ServeAccessLog handles GET /domains/log, GET /domains/log/ and
// GET /domains/log/{domain_name}.
func (h *AccessLogs) ServeAccessLog(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")

	if domainName == "" {
		domains, err := paneldb.GetAllDomains(h.MySQL)
		mysqlIsDown := err != nil
		if mysqlIsDown {
			domains = nil
		}
		webtemplates.Render(w, "domains_logs.html", mergeChrome(map[string]interface{}{
			"Domains":     domains,
			"MySQLIsDown": mysqlIsDown,
			"DomainName":  "",
			"JSONLogs":    nil,
			"CSRFToken":   csrf.Token(r),
			"Flashes":     auth.PopFlashes(w, r, h.Sessions),
		}, r, "Access Logs"))
		return
	}

	if !isDomain(domainName) {
		auth.AddFlash(w, r, h.Sessions, "Invalid domain name format.", "danger")
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}

	logFilePath := filepath.Join(AccessLogsDir, domainName, "access.log")

	info, statErr := os.Stat(logFilePath)
	if statErr != nil {
		auth.AddFlash(w, r, h.Sessions, "Log file not found for domain "+domainName+".", "error")
		http.Redirect(w, r, "/domains/log", http.StatusSeeOther)
		return
	}

	if info.Size() == 0 {
		auth.AddFlash(w, r, h.Sessions, "Log file for domain "+domainName+" is empty.", "info")
		http.Redirect(w, r, "/domains/log", http.StatusSeeOther)
		return
	}

	content, readErr := os.ReadFile(logFilePath)
	if readErr != nil {
		auth.AddFlash(w, r, h.Sessions, "Error reading log file: "+readErr.Error(), "danger")
		http.Redirect(w, r, "/domains/log", http.StatusSeeOther)
		return
	}

	// A malformed line flashes an error and redirects rather than crashing
	// the request.
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	jsonLogs := make([]map[string]interface{}, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			auth.AddFlash(w, r, h.Sessions, "Error reading log file: "+err.Error(), "danger")
			http.Redirect(w, r, "/domains/log", http.StatusSeeOther)
			return
		}
		jsonLogs = append(jsonLogs, entry)
	}
	// json_logs.reverse()
	for i, j := 0, len(jsonLogs)-1; i < j; i, j = i+1, j-1 {
		jsonLogs[i], jsonLogs[j] = jsonLogs[j], jsonLogs[i]
	}

	totalLogs := len(jsonLogs)
	showAll := r.URL.Query().Get("show_all") == "true"

	var itemsPerPage, totalPages int
	if showAll {
		itemsPerPage = totalLogs
		totalPages = 1
	} else {
		itemsPerPage = AccessLogPageSize
		totalPages = totalLogs / itemsPerPage
		if totalLogs%itemsPerPage != 0 {
			totalPages++
		}
	}

	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			page = parsed
		}
	}

	startIdx := (page - 1) * itemsPerPage
	endIdx := startIdx + itemsPerPage
	paginated := clampSlice(jsonLogs, startIdx, endIdx)

	webtemplates.Render(w, "domains_logs.html", mergeChrome(map[string]interface{}{
		"Domains":                     nil,
		"DomainName":                  domainName,
		"JSONLogs":                    paginated,
		"ShowAll":                     showAll,
		"CurrentPage":                 page,
		"ItemsPerPage":                itemsPerPage,
		"TotalPages":                  totalPages,
		"TotalLines":                  totalLogs,
		"TotalAllowedLinesForShowAll": AccessLogTotalAllowedForShowAll,
		"CSRFToken":                   csrf.Token(r),
		"Flashes":                     auth.PopFlashes(w, r, h.Sessions),
	}, r, domainName+" Access Log"))
}
