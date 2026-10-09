package handlers

import (
	"net/http"

	"openadmin/internal/activity"
	"sort"
	"strings"
)

// readSort returns the ?sort= and ?direction= of a list page, only for keys the page knows
func readSort(r *http.Request, keys map[string]bool) (col, direction string) {
	return readSortParam(r, "sort", "direction", keys)
}

// readSortParam is readSort for pages with more than one sortable table
func readSortParam(r *http.Request, sortParam, directionParam string, keys map[string]bool) (col, direction string) {
	col = r.URL.Query().Get(sortParam)
	if !keys[col] {
		return "", ""
	}
	direction = "asc"
	if strings.EqualFold(r.URL.Query().Get(directionParam), "desc") {
		direction = "desc"
	}
	return col, direction
}

// sortBy sorts rows with less, reversed for "desc", keeping the original order for equal rows
func sortBy[T any](rows []T, direction string, less func(a, b T) bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if direction == "desc" {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}

func boolLess(a, b bool) bool { return !a && b }

// securityScore ranks accounts with both 2FA and passkeys highest
func securityScore(totp, passkeys bool) int {
	n := 0
	if totp {
		n++
	}
	if passkeys {
		n++
	}
	return n
}

var administratorSortKeys = map[string]bool{"username": true, "status": true, "role": true, "security": true, "activity": true}

func sortAdministrators(rows []administratorRow, col, direction string) {
	var less func(a, b administratorRow) bool
	switch col {
	case "username":
		less = func(a, b administratorRow) bool { return strings.ToLower(a.Username) < strings.ToLower(b.Username) }
	case "status":
		less = func(a, b administratorRow) bool { return boolLess(a.IsActive, b.IsActive) }
	case "role":
		less = func(a, b administratorRow) bool { return a.Role < b.Role }
	case "security":
		less = func(a, b administratorRow) bool {
			return securityScore(a.TOTPEnabled, a.PasskeysEnabled) < securityScore(b.TOTPEnabled, b.PasskeysEnabled)
		}
	case "activity":
		// timestamps are "YYYY-MM-DD HH:MM:SS" so they sort as strings, no activity counts as oldest
		less = func(a, b administratorRow) bool { return a.LastActionTime < b.LastActionTime }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var resellerSortKeys = map[string]bool{"username": true, "status": true, "security": true, "activity": true, "accounts": true, "storage": true, "plans": true}

func sortResellers(rows []resellerRow, col, direction string) {
	var less func(a, b resellerRow) bool
	switch col {
	case "username":
		less = func(a, b resellerRow) bool { return strings.ToLower(a.Username) < strings.ToLower(b.Username) }
	case "status":
		less = func(a, b resellerRow) bool { return boolLess(a.IsActive, b.IsActive) }
	case "security":
		less = func(a, b resellerRow) bool {
			return securityScore(a.TOTPEnabled, a.PasskeysEnabled) < securityScore(b.TOTPEnabled, b.PasskeysEnabled)
		}
	case "activity":
		less = func(a, b resellerRow) bool { return a.LastActionTime < b.LastActionTime }
	case "accounts":
		less = func(a, b resellerRow) bool { return a.CurrentAccounts < b.CurrentAccounts }
	case "storage":
		less = func(a, b resellerRow) bool { return a.CurrentDiskBlocks < b.CurrentDiskBlocks }
	case "plans":
		less = func(a, b resellerRow) bool { return len(a.AllowedPlans) < len(b.AllowedPlans) }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var backupArchiveSortKeys = map[string]bool{"name": true, "size": true, "created": true}

func sortBackupArchives(rows []backupArchiveRow, col, direction string) {
	var less func(a, b backupArchiveRow) bool
	switch col {
	case "name":
		less = func(a, b backupArchiveRow) bool { return a.Name < b.Name }
	case "size":
		less = func(a, b backupArchiveRow) bool { return a.sizeBytes < b.sizeBytes }
	case "created":
		less = func(a, b backupArchiveRow) bool { return a.ModTime < b.ModTime }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var backupRunSortKeys = map[string]bool{"timestamp": true, "action": true, "status": true, "archive": true, "duration": true, "detail": true}

func sortBackupRuns(rows []backupRun, col, direction string) {
	var less func(a, b backupRun) bool
	switch col {
	case "timestamp":
		less = func(a, b backupRun) bool { return a.Timestamp < b.Timestamp }
	case "action":
		less = func(a, b backupRun) bool { return a.Action < b.Action }
	case "status":
		less = func(a, b backupRun) bool { return a.Status < b.Status }
	case "archive":
		less = func(a, b backupRun) bool { return a.Archive < b.Archive }
	case "duration":
		less = func(a, b backupRun) bool { return a.Duration < b.Duration }
	case "detail":
		less = func(a, b backupRun) bool { return a.Detail < b.Detail }
	default:
		return
	}
	sortBy(rows, direction, less)
}

// serviceStatusRow is one row of /services, Label is "up", "down" or "unknown"
type serviceStatusRow struct {
	Name     string
	RealName string
	Type     string
	Label    string
}

var serviceSortKeys = map[string]bool{"name": true, "status": true, "realname": true, "type": true}

func sortServiceStatuses(rows []serviceStatusRow, col, direction string) {
	var less func(a, b serviceStatusRow) bool
	switch col {
	case "name":
		less = func(a, b serviceStatusRow) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	case "status":
		less = func(a, b serviceStatusRow) bool { return a.Label < b.Label }
	case "realname":
		less = func(a, b serviceStatusRow) bool { return a.RealName < b.RealName }
	case "type":
		less = func(a, b serviceStatusRow) bool { return a.Type < b.Type }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var cronSortKeys = map[string]bool{"schedule": true, "command": true, "logging": true}

func sortCronJobs(rows []CronJob, col, direction string) {
	var less func(a, b CronJob) bool
	switch col {
	case "schedule":
		less = func(a, b CronJob) bool { return a.Schedule < b.Schedule }
	case "command":
		less = func(a, b CronJob) bool { return a.Command < b.Command }
	case "logging":
		less = func(a, b CronJob) bool { return boolLess(a.Log, b.Log) }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafRuleSortKeys = map[string]bool{"name": true, "rules": true, "status": true}

func sortWAFRules(rows []wafRuleDetail, col, direction string) {
	var less func(a, b wafRuleDetail) bool
	switch col {
	case "name":
		less = func(a, b wafRuleDetail) bool { return a.Name < b.Name }
	case "rules":
		less = func(a, b wafRuleDetail) bool { return a.NumRules < b.NumRules }
	case "status":
		less = func(a, b wafRuleDetail) bool { return a.Status < b.Status }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var activitySortKeys = map[string]bool{"date": true, "ip": true, "action": true}

func sortActivity(rows []activity.Entry, col, direction string) {
	var less func(a, b activity.Entry) bool
	switch col {
	case "date":
		less = func(a, b activity.Entry) bool { return a.Time < b.Time }
	case "ip":
		less = func(a, b activity.Entry) bool { return a.IP < b.IP }
	case "action":
		less = func(a, b activity.Entry) bool { return strings.ToLower(a.Action) < strings.ToLower(b.Action) }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafDomainSortKeys = map[string]bool{"domain": true, "user": true, "mode": true, "level": true, "disabled": true}

// wafLevelRank orders protection levels weakest first, custom last since it can be anything
var wafLevelRank = map[string]int{"compatibility": 1, "standard": 2, "strict": 3, "custom": 4}

func sortWAFDomains(rows []wafDomainRow, col, direction string) {
	var less func(a, b wafDomainRow) bool
	switch col {
	case "domain":
		less = func(a, b wafDomainRow) bool { return a.Domain < b.Domain }
	case "user":
		less = func(a, b wafDomainRow) bool { return a.Owner < b.Owner }
	case "mode":
		less = func(a, b wafDomainRow) bool { return a.Engine < b.Engine }
	case "level":
		less = func(a, b wafDomainRow) bool { return wafLevelRank[a.Level] < wafLevelRank[b.Level] }
	case "disabled":
		less = func(a, b wafDomainRow) bool { return a.Disabled < b.Disabled }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafLogDomainSortKeys = map[string]bool{"domain": true, "user": true, "blocked": true, "would_block": true, "flagged": true, "last_seen": true}

func sortWAFLogDomains(rows []wafDomainHits, col, direction string) {
	var less func(a, b wafDomainHits) bool
	switch col {
	case "domain":
		less = func(a, b wafDomainHits) bool { return a.Domain < b.Domain }
	case "user":
		less = func(a, b wafDomainHits) bool { return a.Owner < b.Owner }
	case "blocked":
		less = func(a, b wafDomainHits) bool { return a.Blocked < b.Blocked }
	case "would_block":
		less = func(a, b wafDomainHits) bool { return a.WouldBlock < b.WouldBlock }
	case "flagged":
		less = func(a, b wafDomainHits) bool { return a.Flagged < b.Flagged }
	case "last_seen":
		less = func(a, b wafDomainHits) bool { return a.LastSeen < b.LastSeen }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafLogRuleSortKeys = map[string]bool{"rule": true, "reason": true, "hits": true, "blocked": true, "domains": true}

func sortWAFLogRules(rows []wafRuleGroup, col, direction string) {
	var less func(a, b wafRuleGroup) bool
	switch col {
	case "rule":
		less = func(a, b wafRuleGroup) bool { return a.ID < b.ID }
	case "reason":
		less = func(a, b wafRuleGroup) bool { return a.Category < b.Category }
	case "hits":
		less = func(a, b wafRuleGroup) bool { return a.Count < b.Count }
	case "blocked":
		less = func(a, b wafRuleGroup) bool { return a.Blocked < b.Blocked }
	case "domains":
		less = func(a, b wafRuleGroup) bool { return len(a.Domains) < len(b.Domains) }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafLogIPSortKeys = map[string]bool{"ip": true, "hits": true, "blocked": true}

func sortWAFLogIPs(rows []wafIPHits, col, direction string) {
	var less func(a, b wafIPHits) bool
	switch col {
	case "ip":
		less = func(a, b wafIPHits) bool { return a.IP < b.IP }
	case "hits":
		less = func(a, b wafIPHits) bool { return a.Count < b.Count }
	case "blocked":
		less = func(a, b wafIPHits) bool { return a.Blocked < b.Blocked }
	default:
		return
	}
	sortBy(rows, direction, less)
}

var wafLogEventSortKeys = map[string]bool{"time": true, "domain": true, "ip": true, "request": true, "status": true, "result": true}

func sortWAFLogEvents(rows []wafLogEvent, col, direction string) {
	var less func(a, b wafLogEvent) bool
	switch col {
	case "time":
		less = func(a, b wafLogEvent) bool { return a.Time < b.Time }
	case "domain":
		less = func(a, b wafLogEvent) bool { return a.Domain < b.Domain }
	case "ip":
		less = func(a, b wafLogEvent) bool { return a.IP < b.IP }
	case "request":
		less = func(a, b wafLogEvent) bool { return a.URI < b.URI }
	case "status":
		less = func(a, b wafLogEvent) bool { return a.Status < b.Status }
	case "result":
		less = func(a, b wafLogEvent) bool { return a.Result < b.Result }
	default:
		return
	}
	sortBy(rows, direction, less)
}
