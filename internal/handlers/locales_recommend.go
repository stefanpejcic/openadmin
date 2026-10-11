package handlers

import (
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalesUsersDir holds each OpenPanel user's .lastlogin file
var LocalesUsersDir = "/etc/openpanel/openpanel/core/users"

type localeSuggestion struct {
	Locale, Country string
	Users           []string
}

// userLoginCountries maps each user to the country of their latest real login, skipping logins from the server itself (OpenAdmin autologin) or private IPs
func userLoginCountries(usersDir, serverIP string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(usersDir, e.Name(), ".lastlogin"))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			// "IP: 1.2.3.4 - Country: RS - Login Time: 2026-10-10 18:15:57"
			ip, country, ok := parseLastLoginLine(lines[i])
			if !ok || ip == serverIP {
				continue
			}
			if parsed := net.ParseIP(ip); parsed == nil || parsed.IsPrivate() || parsed.IsLoopback() {
				continue
			}
			out[e.Name()] = country
			break
		}
	}
	return out
}

func parseLastLoginLine(line string) (ip, country string, ok bool) {
	ipPart, rest, found := strings.Cut(line, " - Country: ")
	if !found || !strings.HasPrefix(ipPart, "IP: ") {
		return "", "", false
	}
	country, _, _ = strings.Cut(rest, " - ")
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return "", "", false
	}
	return strings.TrimPrefix(ipPart, "IP: "), country, true
}

// suggestLocales picks not-installed locales whose region matches where users log in from, most users first
func suggestLocales(rows []localeRow, userCountries map[string]string) []localeSuggestion {
	usersByCountry := map[string][]string{}
	for user, country := range userCountries {
		usersByCountry[country] = append(usersByCountry[country], user)
	}
	var out []localeSuggestion
	for _, row := range rows {
		if row.Installed {
			continue
		}
		_, region, ok := strings.Cut(row.Locale, "-")
		if !ok {
			continue
		}
		country := strings.ToUpper(region)
		users := usersByCountry[country]
		if len(users) == 0 {
			continue
		}
		sort.Strings(users)
		out = append(out, localeSuggestion{Locale: row.Locale, Country: country, Users: users})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Users) > len(out[j].Users) })
	return out
}
