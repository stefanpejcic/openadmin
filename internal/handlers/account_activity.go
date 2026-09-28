package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"openadmin/internal/activity"
	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

const activityPerPage = 50

// AccountActivity serves the per-account activity log pages
type AccountActivity struct {
	DB       *admindb.DB
	Sessions *auth.Manager
}

type accountActivityPageData struct {
	webtemplates.Chrome
	Username   string
	Entries    []activity.Entry
	Total      int
	Query      string
	Page       int
	TotalPages int
	PrevURL    string
	NextURL    string
	BackURL    string
	Flashes    []auth.Flash
}

// Serve handles GET /administrators/activity/{username} and /resellers/activity/{username}, a reseller can only open their own
func (a *AccountActivity) Serve(listURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backURL := listURL
		username := r.PathValue("username")
		current := auth.CurrentUser(r)
		if current.Role == "reseller" {
			if username != current.Username {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			backURL = "/account"
		}
		if _, err := a.DB.UserByUsername(username); err != nil {
			auth.AddFlash(w, r, a.Sessions, "Error: Account "+username+" does not exist!", "error")
			http.Redirect(w, r, backURL, http.StatusSeeOther)
			return
		}

		query := strings.TrimSpace(r.URL.Query().Get("q"))
		entries := filterActivity(activity.Read(username), query)

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		totalPages := (len(entries) + activityPerPage - 1) / activityPerPage
		if totalPages < 1 {
			totalPages = 1
		}
		if page < 1 {
			page = 1
		}
		if page > totalPages {
			page = totalPages
		}
		start := (page - 1) * activityPerPage
		end := start + activityPerPage
		if end > len(entries) {
			end = len(entries)
		}

		pageURL := func(p int) string {
			v := url.Values{"page": {strconv.Itoa(p)}}
			if query != "" {
				v.Set("q", query)
			}
			return r.URL.Path + "?" + v.Encode()
		}

		webtemplates.Render(w, "account_activity.html", accountActivityPageData{
			Chrome:     buildChrome(r, "Activity Log: "+username),
			Username:   username,
			Entries:    entries[start:end],
			Total:      len(entries),
			Query:      query,
			Page:       page,
			TotalPages: totalPages,
			PrevURL:    pageURL(page - 1),
			NextURL:    pageURL(page + 1),
			BackURL:    backURL,
			Flashes:    auth.PopFlashes(w, r, a.Sessions),
		})
	}
}

func filterActivity(entries []activity.Entry, query string) []activity.Entry {
	if query == "" {
		return entries
	}
	q := strings.ToLower(query)
	var out []activity.Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Time+" "+e.IP+" "+e.Action), q) {
			out = append(out, e)
		}
	}
	return out
}
