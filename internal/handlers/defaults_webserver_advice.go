package handlers

import (
	"database/sql"
	"fmt"
	"strings"
)

// webserverAdvice recommends a default webserver from the sites already on the server, OK lists the choices that need no hint
type webserverAdvice struct {
	Text string
	OK   []string
}

type siteGroup struct {
	label, advice string
	ok            []string
}

var (
	wpSiteGroup    = siteGroup{"WordPress", "which performs best on OpenLiteSpeed, or Apache if your sites rely on .htaccess rules", []string{"openlitespeed", "apache"}}
	proxySiteGroup = siteGroup{"Node.js/Python apps", "which run best behind Nginx, the fastest reverse proxy", []string{"nginx"}}
	luaSiteGroup   = siteGroup{"Java/Ruby apps", "which run best on OpenResty since it can be scripted with Lua", []string{"openresty"}}
	otherSiteGroup = siteGroup{"other CMS/PHP sites", "which run well on Apache or Nginx", []string{"apache", "nginx"}}
)

// order breaks ties between groups with the same number of sites
var siteGroupOrder = []*siteGroup{&wpSiteGroup, &proxySiteGroup, &luaSiteGroup, &otherSiteGroup}

func siteGroupFor(siteType string) *siteGroup {
	switch strings.ToLower(strings.TrimSpace(siteType)) {
	case "wordpress":
		return &wpSiteGroup
	case "nodejs", "node", "python", "n8n":
		return &proxySiteGroup
	case "java", "ruby":
		return &luaSiteGroup
	}
	return &otherSiteGroup
}

// adviseWebserver returns nil when there are no sites, the page hides the text while a webserver from OK is selected
func adviseWebserver(typeCounts map[string]int) *webserverAdvice {
	counts := map[*siteGroup]int{}
	total := 0
	for t, n := range typeCounts {
		counts[siteGroupFor(t)] += n
		total += n
	}
	if total == 0 {
		return nil
	}
	var top *siteGroup
	for _, g := range siteGroupOrder {
		if top == nil || counts[g] > counts[top] {
			top = g
		}
	}
	return &webserverAdvice{
		Text: fmt.Sprintf("%d of %d sites on this server are %s, %s.", counts[top], total, top.label, top.advice),
		OK:   top.ok,
	}
}

func countSitesOfType(typeCounts map[string]int, siteType string) int {
	n := 0
	for t, c := range typeCounts {
		if strings.EqualFold(strings.TrimSpace(t), siteType) {
			n += c
		}
	}
	return n
}

func totalSites(typeCounts map[string]int) int {
	n := 0
	for _, c := range typeCounts {
		n += c
	}
	return n
}

// defaultsSiteTypesRun is swapped out in tests
var defaultsSiteTypesRun = func(db *sql.DB) map[string]int {
	out := map[string]int{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT type, COUNT(*) FROM sites GROUP BY type`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t sql.NullString
		var n int
		if rows.Scan(&t, &n) == nil {
			out[t.String] += n
		}
	}
	return out
}
