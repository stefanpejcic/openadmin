// This file implements the JSON REST API's domain diagnostics routes: SSL
// status/management, the paginated JSON-lines webserver access log, and the
// pre-generated GoAccess HTML stats report. Each reuses the same on-disk
// paths and opencli plumbing as its HTML admin-page equivalent
// (domains_ssl.go, domains_logs.go, domains_stats.go) -- only the response
// shape differs. Unlike the HTML SSL page, this route's control flow never
// crashes: every branch below returns a clean JSON body.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// APIDomainStats bundles the /api/domains/{domain}/ssl,
// /api/domains/{domain}/log and /api/domains/{domain}/stats/{username}
// handlers.
type APIDomainStats struct{}

// ServeDomainSSL handles GET/POST /api/domains/{domain_name}/ssl.
func (a *APIDomainStats) ServeDomainSSL(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	if !isDomain(domainName) {
		writeJSONError(w, http.StatusBadRequest, "Invalid domain name.")
		return
	}

	if r.Method == http.MethodPost {
		var body map[string]interface{}
		if !apiDecodeJSONBody(r, &body) {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		action, _ := body["action"].(string)

		switch action {
		case "custom":
			publicPath, _ := body["public_path"].(string)
			privatePath, _ := body["private_path"].(string)
			if publicPath == "" || privatePath == "" {
				writeJSONError(w, http.StatusBadRequest, "public_path and private_path must be provided.")
				return
			}
			resolvedPublic := resolveSSLKeyPath(publicPath)
			resolvedPrivate := resolveSSLKeyPath(privatePath)
			if !isRelativeToSSLHomeDir(resolvedPublic) {
				writeJSONError(w, http.StatusBadRequest, "public_path must be inside '/var/www/html/' directory.")
				return
			}
			if !isRelativeToSSLHomeDir(resolvedPrivate) {
				writeJSONError(w, http.StatusBadRequest, "private_path must be inside '/var/www/html/' directory.")
				return
			}
			stdout, stderr, exitCode, err := opencliSSLRun(domainName, "custom", resolvedPublic, resolvedPrivate)
			if err == nil && exitCode == 0 {
				writeJSON(w, map[string]interface{}{"success": true, "message": strings.TrimSpace(stdout)})
				return
			}
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": strings.TrimSpace(stderr)})
			return

		case "autossl":
			stdout, stderr, exitCode, err := opencliSSLRun(domainName, "auto")
			if err == nil && exitCode == 0 {
				writeJSON(w, map[string]interface{}{"success": true, "message": strings.TrimSpace(stdout)})
				return
			}
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": strings.TrimSpace(stderr)})
			return

		case "logs":
			stdout, _, exitCode, err := opencliSSLRun(domainName, "logs", "1000")
			logs := ""
			if err == nil && exitCode == 0 {
				logs = strings.TrimSpace(stdout)
			}
			writeJSON(w, map[string]interface{}{"logs": logs})
			return

		default:
			writeJSONError(w, http.StatusBadRequest, "Invalid action. Use 'autossl', 'custom' or 'logs'.")
			return
		}
	}

	statusStdout, statusStderr, statusExit, statusErr := opencliSSLRun(domainName, "status")
	if statusErr != nil || statusExit != 0 {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": strings.TrimSpace(statusStderr)})
		return
	}
	currentSetting := strings.ToLower(strings.TrimSpace(statusStdout))

	infoStdout, _, infoExit, infoErr := opencliSSLRun(domainName, "info")
	keys := ""
	if infoErr == nil && infoExit == 0 {
		keys = strings.TrimSpace(infoStdout)
	}

	writeJSON(w, map[string]interface{}{"domain": domainName, "status": currentSetting, "info": keys})
}

// ServeDomainAccessLog handles GET /api/domains/{domain_name}/log.
func (a *APIDomainStats) ServeDomainAccessLog(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	if !isDomain(domainName) {
		writeJSONError(w, http.StatusBadRequest, "Invalid domain name.")
		return
	}
	logFilePath := filepath.Join(AccessLogsDir, domainName, "access.log")

	info, statErr := os.Stat(logFilePath)
	if statErr != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Log file not found for domain %s", domainName))
		return
	}
	if info.Size() == 0 {
		writeJSON(w, map[string]interface{}{"domain": domainName, "total_lines": 0, "logs": []interface{}{}})
		return
	}

	content, readErr := os.ReadFile(logFilePath)
	if readErr != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error reading log file: "+readErr.Error())
		return
	}

	// A malformed line reports a 500 with the parse error rather than
	// silently skipping it, matching a plain json.loads() failure.
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	jsonLogs := make([]map[string]interface{}, 0, len(lines))
	for _, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Error reading log file: "+err.Error())
			return
		}
		jsonLogs = append(jsonLogs, entry)
	}
	for i, j := 0, len(jsonLogs)-1; i < j; i, j = i+1, j-1 {
		jsonLogs[i], jsonLogs[j] = jsonLogs[j], jsonLogs[i]
	}

	totalLogs := len(jsonLogs)
	showAll := r.URL.Query().Get("show_all") == "true"

	var itemsPerPage, totalPages, page int
	if showAll {
		itemsPerPage = totalLogs
		if itemsPerPage == 0 {
			itemsPerPage = 1
		}
		totalPages = 1
		page = 1
	} else {
		itemsPerPage = AccessLogPageSize
		totalPages = totalLogs / itemsPerPage
		if totalLogs%itemsPerPage != 0 {
			totalPages++
		}
		if totalPages < 1 {
			totalPages = 1
		}
		page = 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				page = parsed
			}
		}
	}

	startIdx := (page - 1) * itemsPerPage
	endIdx := startIdx + itemsPerPage
	paginated := clampSlice(jsonLogs, startIdx, endIdx)

	writeJSON(w, map[string]interface{}{
		"domain":         domainName,
		"current_page":   page,
		"items_per_page": itemsPerPage,
		"total_pages":    totalPages,
		"total_lines":    totalLogs,
		"logs":           paginated,
	})
}

// ServeDomainStats handles GET /api/domains/{domain_name}/stats/{username}.
func (a *APIDomainStats) ServeDomainStats(w http.ResponseWriter, r *http.Request) {
	domainName := r.PathValue("domain_name")
	username := r.PathValue("username")
	if !isDomain(domainName) {
		writeJSONError(w, http.StatusBadRequest, "Invalid domain name.")
		return
	}

	statsFilePath := filepath.Join(GoAccessStatsDir, username, domainName+".html")
	content, err := os.ReadFile(statsFilePath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Stats file for domain %s not found. Data is generated every 24h.", domainName))
		return
	}
	writeJSON(w, map[string]interface{}{"domain": domainName, "html": string(content)})
}
