// Resellers can open some admin pages limited to the users they own and those users' domains, this is the shared check for both the GUI and the API.
package handlers

import (
	"database/sql"
	"net/http"
	"strings"

	"openadmin/internal/auth"
)

// resellerScope is nil for administrators, so every Allows* call on it returns true
type resellerScope struct {
	Reseller string
	users    map[string]bool
	domains  map[string]string // domain -> owning username
}

// actingReseller returns the reseller behind a GUI session or API token, "" for administrators
func actingReseller(r *http.Request) string {
	if u := auth.CurrentUser(r); u != nil {
		if u.Role == "reseller" {
			return u.Username
		}
		return ""
	}
	if u := APIUserFromContext(r); u != nil && u.Role == "reseller" {
		return u.Username
	}
	return ""
}

// scopeFor loads the reseller's users and domains, a reseller with a broken db gets an empty scope, never an unrestricted one
func scopeFor(r *http.Request, db *sql.DB) *resellerScope {
	reseller := actingReseller(r)
	if reseller == "" {
		return nil
	}
	s := &resellerScope{Reseller: reseller, users: map[string]bool{}, domains: map[string]string{}}
	if db == nil {
		return s
	}
	if rows, err := db.Query(`SELECT username FROM users WHERE owner = ?`, reseller); err == nil {
		for rows.Next() {
			var u string
			if rows.Scan(&u) == nil {
				s.users[u] = true
			}
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT d.domain_url, u.username FROM domains d JOIN users u ON d.user_id = u.id WHERE u.owner = ?`, reseller); err == nil {
		for rows.Next() {
			var d, u string
			if rows.Scan(&d, &u) == nil {
				s.domains[strings.ToLower(d)] = u
			}
		}
		rows.Close()
	}
	return s
}

func (s *resellerScope) IsReseller() bool { return s != nil }

func (s *resellerScope) AllowsUser(username string) bool {
	if s == nil {
		return true
	}
	return s.users[stripSuspendedPrefix(username)] || s.users[username]
}

func (s *resellerScope) AllowsDomain(domain string) bool {
	if s == nil {
		return true
	}
	_, ok := s.domains[strings.ToLower(strings.TrimSpace(domain))]
	return ok
}

// AllowsEmail checks the domain part of an address
func (s *resellerScope) AllowsEmail(address string) bool {
	if s == nil {
		return true
	}
	at := strings.LastIndex(address, "@")
	return at > 0 && s.AllowsDomain(address[at+1:])
}

// Users lists the owned usernames, nil for administrators
func (s *resellerScope) Users() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.users))
	for u := range s.users {
		out = append(out, u)
	}
	return out
}

// Domains lists the owned domains, nil for administrators
func (s *resellerScope) Domains() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.domains))
	for d := range s.domains {
		out = append(out, d)
	}
	return out
}

// filterRows keeps rows whose key column passes allow, admins get rows back untouched
func filterRows[T any](s *resellerScope, rows []T, allow func(T) bool) []T {
	if s == nil {
		return rows
	}
	out := rows[:0:0]
	for _, row := range rows {
		if allow(row) {
			out = append(out, row)
		}
	}
	return out
}

// denyReseller writes a 403 for actions resellers can't take on an otherwise shared page
func denyReseller(w http.ResponseWriter, r *http.Request) bool {
	if actingReseller(r) == "" {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusForbidden, "Forbidden: resellers can't do this")
	} else {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}
	return true
}

// scopedBatch runs a bulk batch only on items the reseller owns, the rest fail without touching opencli
func scopedBatch(s *resellerScope, allow func(string) bool, batch BulkBatch) BulkBatch {
	if s == nil || batch == nil {
		return batch
	}
	return func(action, value string, items []string) ([]BulkResult, bool) {
		var allowed []string
		for _, it := range items {
			if allow(it) {
				allowed = append(allowed, it)
			}
		}
		var res []BulkResult
		if len(allowed) > 0 {
			var ok bool
			if res, ok = batch(action, value, allowed); !ok {
				return nil, false
			}
		}
		byItem := map[string]BulkResult{}
		for _, r := range res {
			byItem[r.Item] = r
		}
		out := make([]BulkResult, len(items))
		for i, it := range items {
			if r, found := byItem[it]; found && allow(it) {
				out[i] = r
			} else {
				out[i] = BulkResult{Item: it, Message: "Not owned by one of your users."}
			}
		}
		return out, true
	}
}

// IsReseller is for route wiring in main
func IsReseller(r *http.Request) bool { return actingReseller(r) != "" }

// DenyReseller is denyReseller for route wiring in main
func DenyReseller(w http.ResponseWriter, r *http.Request) bool { return denyReseller(w, r) }

// actingUsername is the OpenAdmin account behind a GUI session or API token
func actingUsername(r *http.Request) string {
	if u := auth.CurrentUser(r); u != nil {
		return u.Username
	}
	if u := APIUserFromContext(r); u != nil {
		return u.Username
	}
	return ""
}
