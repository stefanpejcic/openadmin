// Package webtemplates holds this application's html/template pages.
// Templates are embedded into the binary via go:embed for the
// single-static-binary deployment story.
package webtemplates

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed *.html
var files embed.FS

var funcMap = template.FuncMap{
	"contains": strings.Contains,
	// jsStr escapes a value for embedding inside a single-quoted JS string
	// literal within an HTML attribute (e.g. Alpine's x-show="'...'"). Go's
	// html/template only applies JS escaping in contexts it recognizes as
	// script (inline <script>, on* handlers) -- custom attributes like
	// x-show are just plain HTML attribute text to it, so a value containing
	// a literal quote (e.g. a notification title like "User account
	// 'bob' created") breaks the embedded JS expression unless escaped here.
	"jsStr":      template.JSEscapeString,
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"mul":        func(a, b int) int { return a * b },
	"splitSpace": strings.Fields,
	// seq builds an inclusive [start, end] integer range for a pagination
	// window, empty if start > end.
	"seq": func(start, end int) []int {
		if start > end {
			return nil
		}
		out := make([]int, 0, end-start+1)
		for i := start; i <= end; i++ {
			out = append(out, i)
		}
		return out
	},

	// formatDate renders a registered_date value as "dd.mm.yyyy hh:mm".
	// registered_date decodes as time.Time (mysqldb sets ParseTime=true),
	// but a defensive string fallback is kept since not every caller of
	// this filter is guaranteed a driver-parsed value.
	"formatDate": func(v interface{}) string {
		switch t := v.(type) {
		case time.Time:
			if t.IsZero() {
				return ""
			}
			return t.Format("02.01.2006 15:04")
		case string:
			return t
		default:
			return ""
		}
	},
	// formatDateTime is like formatDate but keeps seconds, and also parses
	// a raw MySQL "2006-01-02 15:04:05" string (sql.NullString columns come
	// back as the driver's raw text, not a parsed time.Time).
	"formatDateTime": func(v interface{}) string {
		switch t := v.(type) {
		case time.Time:
			if t.IsZero() {
				return ""
			}
			return t.Format("02.01.2006 15:04:05")
		case string:
			if parsed, err := time.Parse("2006-01-02 15:04:05", t); err == nil {
				return parsed.Format("02.01.2006 15:04:05")
			}
			return t
		default:
			return ""
		}
	},
	"firstSegment": func(s, sep string) string {
		parts := strings.SplitN(s, sep, 2)
		return parts[0]
	},
	"hasPrefix": strings.HasPrefix,
	"hasSuffix": strings.HasSuffix,
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	// localeFlag maps a language code to the country-flag icon that
	// represents it, for languages that aren't already named after a
	// country (e.g. "en" has no country of its own -- shown as GB).
	"localeFlag": func(locale string) string {
		if strings.ToLower(locale) == "en" {
			return "gb"
		}
		return locale
	},
	"rstrip":    func(s, cutset string) string { return strings.TrimRight(s, cutset) },
	"toInt": func(v interface{}) int {
		switch t := v.(type) {
		case int64:
			return int(t)
		case int:
			return t
		case float64:
			return int(t)
		case string:
			n, _ := strconv.Atoi(t)
			return n
		default:
			return 0
		}
	},
	// dotThousands / millionsAbbrev are number-formatting helpers for the
	// plans templates.
	"dotThousands": func(n int) string {
		s := strconv.Itoa(n)
		neg := strings.HasPrefix(s, "-")
		if neg {
			s = s[1:]
		}
		var out []byte
		for i, c := range []byte(s) {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, '.')
			}
			out = append(out, c)
		}
		if neg {
			return "-" + string(out)
		}
		return string(out)
	},
	"millionsAbbrev": func(n int) string {
		m := math.Round(float64(n)/1_000_000*10) / 10
		if m == math.Trunc(m) {
			return strconv.Itoa(int(m)) + "M"
		}
		return strconv.FormatFloat(m, 'f', 1, 64) + "M"
	},
	// serviceColumns is the #services tab's column-visibility list, in
	// display order -- a plain map/dict would work too, but html/template
	// sorts map ranges by key, which would reorder the dropdown away from
	// the Name/Image/Ports/Env/CPU/Memory/PIDs/Actions order the JS column
	// state (userDetailPageData's settingsEditor) also uses.
	"serviceColumns": func() []struct{ Key, Label string } {
		return []struct{ Key, Label string }{
			{"name", "Name"},
			{"image", "Image"},
			{"ports", "Ports"},
			{"env", "Environment"},
			{"cpu", "CPU Usage"},
			{"ram", "Memory Usage"},
			{"pids", "PIDs Usage"},
			{"actions", "Actions"},
		}
	},
	// dict supports macros.html-style helper templates that take several
	// named arguments: {{template "x" (dict
	// "ID" .Foo "Label" "Bar")}} builds the map inline at the call site.
	"dict": func(pairs ...interface{}) (map[string]interface{}, error) {
		if len(pairs)%2 != 0 {
			return nil, fmt.Errorf("dict: odd number of arguments")
		}
		m := make(map[string]interface{}, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			key, ok := pairs[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
			}
			m[key] = pairs[i+1]
		}
		return m, nil
	},
	"replace": func(v interface{}, old, new string) string {
		s, _ := v.(string)
		return strings.ReplaceAll(s, old, new)
	},
	"firstUpper": func(s string) string {
		if s == "" {
			return ""
		}
		return strings.ToUpper(s[:1])
	},
	// titleWords replaces underscores with spaces and title-cases each word.
	"titleWords": func(s string) string {
		s = strings.ReplaceAll(s, "_", " ")
		words := strings.Fields(s)
		for i, w := range words {
			if w != "" {
				words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
			}
		}
		return strings.Join(words, " ")
	},
	"lastSegment": func(s, sep string) string {
		parts := strings.Split(s, sep)
		return parts[len(parts)-1]
	},
	// stripSuspendedPrefix mirrors handlers.stripSuspendedPrefix (duplicated
	// here rather than shared, since handlers imports webtemplates and a
	// back-import would cycle). Unlike lastSegment, this only strips the
	// "SUSPENDED_<timestamp>_" prefix when it's actually present, so it's
	// safe to use on any username -- including ones containing "_" that
	// aren't suspended.
	"stripSuspendedPrefix": func(username string) string {
		if idx := strings.LastIndex(username, "_"); strings.Contains(username, "SUSPENDED_") && idx != -1 {
			return username[idx+1:]
		}
		return username
	},
	"hasModule": func(mods []string, name string) bool {
		for _, m := range mods {
			if m == name {
				return true
			}
		}
		return false
	},
	// extractUsersList does an ad-hoc extraction of the users list from a
	// MySQL FK-constraint error string, for the plan-delete flash message.
	"extractUsersList": func(msg string) string {
		const marker = `users": [`
		idx := strings.Index(msg, marker)
		if idx == -1 {
			return ""
		}
		start := idx + len(marker)
		end := strings.Index(msg[start:], "]")
		if end == -1 {
			return msg[start:]
		}
		return msg[start : start+end]
	},
	// toJSON embeds a Go value as a JS literal inside an inline x-data
	// attribute.
	"toJSON": func(v interface{}) template.JS {
		b, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return template.JS(b)
	},
	// suspendedLabel extracts the timestamp embedded in a
	// "SUSPENDED_<datetime>_<user>" username and formats it as
	// "on: dd.mm.yyyy hh:mm". Returns "" for a non-suspended username.
	"suspendedLabel": func(username string) string {
		if !strings.Contains(username, "SUSPENDED_") {
			return ""
		}
		parts := strings.Split(username, "_")
		if len(parts) < 2 || len(parts[1]) < 12 {
			return ""
		}
		dt := parts[1]
		year, month, day, hour, minute := dt[0:4], dt[4:6], dt[6:8], dt[8:10], dt[10:12]
		return "on: " + day + "." + month + "." + year + " " + hour + ":" + minute
	},
	// inodesMillions formats an inode count in millions, rounded to 2
	// decimals, with 0 rendered as the infinity symbol -- note this is a
	// different formula/precision than the dotThousands/millionsAbbrev pair
	// (2 decimals, always kept even when trailing zero, e.g. "2.0M" not
	// "2M"), so it's intentionally not reusing those.
	"inodesMillions": func(v interface{}) string {
		var n float64
		switch t := v.(type) {
		case int64:
			n = float64(t)
		case int:
			n = float64(t)
		case float64:
			n = t
		case string:
			n, _ = strconv.ParseFloat(t, 64)
		}
		if n == 0 {
			return "&#8734;"
		}
		rounded := math.Round(n/1_000_000*100) / 100
		s := strconv.FormatFloat(rounded, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s + "M"
	},
	// formatInodes renders values >= 1,000,000 as "1.2M".
	"formatInodes": func(v interface{}) string {
		var n float64
		switch t := v.(type) {
		case int64:
			n = float64(t)
		case float64:
			n = t
		case string:
			n, _ = strconv.ParseFloat(t, 64)
		}
		if n >= 1_000_000 {
			return strconv.FormatFloat(n/1_000_000, 'f', 1, 64) + "M"
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	},
	// unixDateTimeUTC formats a raw unix timestamp as "yyyy-MM-dd HH:mm" in
	// UTC, used by the emails queue template for each message's
	// arrival_time. Distinct format/precision from formatDate/formatDateTime
	// (which take MySQL datetimes, not raw unix timestamps).
	"unixDateTimeUTC": func(v interface{}) string {
		var secs int64
		switch t := v.(type) {
		case int64:
			secs = t
		case int:
			secs = int64(t)
		case float64:
			secs = int64(t)
		case string:
			f, _ := strconv.ParseFloat(t, 64)
			secs = int64(f)
		}
		return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04")
	},
	// kbRound1 converts bytes to KB, rounded to 1 decimal (half away from
	// zero), for the emails queue template's message size column.
	"kbRound1": func(v interface{}) string {
		var n float64
		switch t := v.(type) {
		case int64:
			n = float64(t)
		case int:
			n = float64(t)
		case float64:
			n = t
		case string:
			n, _ = strconv.ParseFloat(t, 64)
		}
		kb := n / 1024
		rounded := math.Round(kb*10) / 10
		return strconv.FormatFloat(rounded, 'f', 1, 64)
	},
	// jinjaTruncate truncates a string to roughly `length` characters
	// (with a leeway of 5, no word-splitting): used by the emails queue
	// template's truncated failure-reason column. If within length+leeway
	// characters, the string is returned unchanged; otherwise it's cut at
	// the last space before length-len(end) and "..." appended.
	"jinjaTruncate": func(s string, length int) string {
		const end = "..."
		const leeway = 5
		if len(s) <= length+leeway {
			return s
		}
		cut := length - len(end)
		if cut < 0 {
			cut = 0
		}
		if cut > len(s) {
			cut = len(s)
		}
		head := s[:cut]
		if idx := strings.LastIndex(head, " "); idx >= 0 {
			head = head[:idx]
		}
		return head + end
	},
	// intOrNull renders an *int as its numeric value or the literal `null`,
	// emitted unquoted inside a JS object literal (x-data="ruleRow({...
	// current: <this> ...})"). postfwdRule.Current is a *int (nil when
	// postfwd reports no counter for that user yet).
	"intOrNull": func(v *int) template.JS {
		if v == nil {
			return "null"
		}
		return template.JS(strconv.Itoa(*v))
	},
}

var templates = template.Must(template.New("").Funcs(funcMap).ParseFS(files, "*.html"))

// Render executes the named template (e.g. "login.html") with data,
// writing directly to w. Errors are logged by the caller via the returned
// error rather than partially written here.
func Render(w http.ResponseWriter, name string, data interface{}) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return templates.ExecuteTemplate(w, name, data)
}

// RenderToString executes the named template into a string rather than an
// http.ResponseWriter -- used for non-HTTP-response output like email
// bodies.
func RenderToString(name string, data interface{}) (string, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
