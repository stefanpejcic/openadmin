package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"

	"openadmin/internal/admindb"
)

const (
	mergeFetchTimeout = readTimeout
	mainMarker        = `id="no_margin_for_iframe"`
)

var (
	// per server config lists would only repeat the same rows, the rest have no list worth merging
	noMergePrefixes = []string{"/server/cluster", "/dashboard", "/search", "/json/", "/api/", "/static/"}
	// these are the server's own config or a live console, repeating them per server in one table says nothing
	noTablePrefixes = []string{"/settings/", "/features/", "/terminal", "/domains/file-templates", "/domains/vhost-templates", "/domains/zone-templates", "/server/crons"}
	// pages about one server, a table on them can still merge but everything else gets a server picker
	pickerPrefixes = []string{"/server/", "/services", "/settings/", "/backups", "/emails", "/terminal", "/import", "/user/import", "/features", "/plans/new", "/domains/file-templates", "/domains/vhost-templates", "/domains/zone-templates", "/domains/ratelimit"}
	// the reports page simply follows the server picked in the sidebar
	pickerSkip   = []string{"/security/2fa", "/security/passkeys", "/terminal/", "/emails/reports", "/user/import", "/server/resource-usage/history"}
	timeHeaderRe = regexp.MustCompile(`(?i)^(time|date|created|last seen|last login|last run|registered|when)\b`)
	timeLayouts  = []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339, "2006-01-02"}
)

type cellSpan struct {
	start, end int // whole cell including its tags
	openEnd    int // end of the opening tag
	class      string
	colspan    bool
	text       string
	checkbox   bool
	sortKey    string
}

type rowSpan struct {
	start, end int
	cells      []cellSpan
}

type tableSpan struct {
	id          string
	hasTemplate bool
	headRow     *rowSpan
	rows        []rowSpan
	bodyEnd     int // offset of </tbody>
}

func attrOf(t xhtml.Token, name string) string {
	for _, a := range t.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// parseTables finds the outer tables of the page body by byte offset, the page itself is spliced and never reserialized so alpine attributes survive untouched
func parseTables(page []byte) []tableSpan {
	offset := bytes.Index(page, []byte(mainMarker))
	if offset < 0 {
		offset = 0
	}
	z := xhtml.NewTokenizer(bytes.NewReader(page[offset:]))
	var out []tableSpan
	var cur *tableSpan
	var row *rowSpan
	var cell *cellSpan
	depth, inHead, inBody := 0, false, false
	pos := offset
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return out
		}
		raw := z.Raw()
		start := pos
		pos += len(raw)
		if tt != xhtml.StartTagToken && tt != xhtml.EndTagToken && tt != xhtml.SelfClosingTagToken {
			if tt == xhtml.TextToken && cell != nil && depth == 1 {
				cell.text += string(raw)
			}
			continue
		}
		t := z.Token()
		name := t.Data
		if name == "table" {
			if tt == xhtml.StartTagToken {
				depth++
				if depth == 1 {
					out = append(out, tableSpan{id: attrOf(t, "id")})
					cur = &out[len(out)-1]
				}
			} else if tt == xhtml.EndTagToken {
				if depth == 1 {
					cur, row, cell = nil, nil, nil
					inHead, inBody = false, false
				}
				if depth > 0 {
					depth--
				}
			}
			continue
		}
		if depth != 1 || cur == nil {
			continue
		}
		switch {
		case name == "thead":
			inHead = tt == xhtml.StartTagToken
		case name == "tbody" && tt == xhtml.StartTagToken:
			inBody = true
		case name == "tbody" && tt == xhtml.EndTagToken:
			inBody = false
			cur.bodyEnd = start
		// a template inside a row is just alpine showing a detail, only one in place of rows means javascript fills the table
		case name == "template" && tt == xhtml.StartTagToken && inBody && row == nil:
			cur.hasTemplate = true
		case name == "tr" && tt == xhtml.StartTagToken && (inHead && cur.headRow == nil || inBody):
			row = &rowSpan{start: start}
		case name == "tr" && tt == xhtml.EndTagToken && row != nil:
			row.end = pos
			if inHead {
				cur.headRow = row
			} else {
				cur.rows = append(cur.rows, *row)
			}
			row = nil
		case (name == "td" || name == "th") && tt == xhtml.StartTagToken && row != nil:
			row.cells = append(row.cells, cellSpan{start: start, openEnd: pos, class: attrOf(t, "class"), colspan: attrOf(t, "colspan") != ""})
			cell = &row.cells[len(row.cells)-1]
		case name == "input" && cell != nil && attrOf(t, "type") == "checkbox":
			cell.checkbox = true
		case name == "a" && cell != nil && inHead && cell.sortKey == "":
			if u, err := url.Parse(attrOf(t, "href")); err == nil {
				// pages that sort with a "-" prefix link both directions, the key is the same column
				cell.sortKey = strings.TrimPrefix(u.Query().Get("sort"), "-")
			}
		case (name == "td" || name == "th") && tt == xhtml.EndTagToken && cell != nil:
			cell.end = pos
			cell = nil
		}
	}
}

// realRows tells a table with data from one showing a placeholder or filled later by javascript
func (t *tableSpan) realRows() bool {
	if t.hasTemplate || t.headRow == nil || len(t.rows) == 0 {
		return false
	}
	return !(len(t.rows) == 1 && len(t.rows[0].cells) <= 1)
}

// detailRow is the single spanning cell some tables keep under a row
func detailRow(r rowSpan) bool { return len(r.cells) == 1 }

// tableColumn puts Server right after the first column that names the row, like Username, ID columns are skipped since they're often hidden
func tableColumn(t *tableSpan) int {
	head := t.headRow
	var sample *rowSpan
	for i := range t.rows {
		if !detailRow(t.rows[i]) {
			sample = &t.rows[i]
			break
		}
	}
	start := 0
	for i, c := range head.cells {
		if c.checkbox || (sample != nil && i < len(sample.cells) && sample.cells[i].checkbox) {
			start = i + 1
		}
	}
	for i := start; i < len(head.cells); i++ {
		if slices.Contains(strings.Fields(head.cells[i].class), "hidden") {
			continue
		}
		name := strings.TrimSpace(html.UnescapeString(head.cells[i].text))
		if name != "" && !strings.EqualFold(name, "id") && name != "#" {
			return i + 1
		}
	}
	return min(start+1, len(head.cells))
}

// rowColumn matches the header position to a row, some tables have a hidden header cell with no cell under it in the rows
func rowColumn(t *tableSpan, r rowSpan) int {
	col := tableColumn(t)
	if extra := len(t.headRow.cells) - len(r.cells); extra > 0 && col-extra >= 0 {
		return col - extra
	}
	return col
}

// visibleClass drops what hides the neighbouring cell the classes are copied from, the Server column always shows
func visibleClass(class string) string {
	var kept []string
	for _, c := range strings.Fields(class) {
		if c == "hidden" || strings.HasSuffix(c, ":hidden") || strings.HasSuffix(c, ":table-cell") {
			continue
		}
		kept = append(kept, c)
	}
	return strings.Join(kept, " ")
}

func serverCell(tag, class, inner string) string {
	return "<" + tag + ` data-cluster-server class="` + html.EscapeString(visibleClass(class)) + ` whitespace-nowrap">` + inner + "</" + tag + ">"
}

// remoteRow is a row of another server, it can't act on this page so its links open that server and its controls are off
func remoteRow(raw []byte, base int, r rowSpan, col int, serverHTML string, open func(href string) string, id string, keepScripts bool) []byte {
	var out bytes.Buffer
	insertAt := -1
	class := ""
	if !detailRow(r) && len(r.cells) > 0 {
		col = min(col, len(r.cells))
		if col < len(r.cells) {
			insertAt = r.cells[col].start - base
			class = r.cells[col].class
		} else {
			insertAt = r.cells[col-1].end - base
			class = r.cells[col-1].class
		}
	}
	z := xhtml.NewTokenizer(bytes.NewReader(raw))
	pos := 0
	inserted := false
	inForm := false
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		tok := z.Raw()
		if insertAt >= 0 && !inserted && pos >= insertAt {
			out.WriteString(serverCell("td", class, serverHTML))
			inserted = true
		}
		pos += len(tok)
		if tt == xhtml.EndTagToken && z.Token().Data == "form" {
			inForm = false
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			out.Write(tok)
			continue
		}
		t := z.Token()
		if t.Data == "form" {
			inForm = true
		}
		// marks whose row it is, scripts and tests can tell rows of other servers apart
		if t.Data == "tr" && attrOf(t, "data-cluster-node") == "" {
			t.Attr = append(t.Attr, xhtml.Attribute{Key: "data-cluster-node", Val: id})
			out.WriteString(t.String())
			continue
		}
		out.WriteString(rewriteRow(t, tok, open, id, keepScripts, inForm))
	}
	if insertAt >= 0 && !inserted {
		out.WriteString(serverCell("td", class, serverHTML))
	}
	return out.Bytes()
}

func hasHandlers(t xhtml.Token) bool {
	for _, a := range t.Attr {
		if isHandlerAttr(a.Key) && !localToggle(a.Val) {
			return true
		}
	}
	return false
}

var localToggleRe = regexp.MustCompile(`^\s*(\w+\s*=\s*(!\w+|true|false|'[^']*')\s*;?\s*)+$`)

// localToggle is a script that only opens or closes something on the page, like a hover menu, it can't reach a server
func localToggle(script string) bool { return localToggleRe.MatchString(script) }

func isHandlerAttr(k string) bool {
	return strings.HasPrefix(k, "@") || strings.HasPrefix(k, "x-on:") || strings.HasPrefix(k, "on") || k == "x-model"
}

func neutralize(t xhtml.Token, open func(string) string) string {
	kept := t.Attr[:0:0]
	for _, a := range t.Attr {
		if (isHandlerAttr(a.Key) && !localToggle(a.Val)) || a.Key == "id" || (t.Data == "input" && a.Key == "name") || a.Key == "form" {
			continue
		}
		if t.Data == "a" && a.Key == "href" {
			if strings.HasPrefix(a.Val, "/") && !strings.HasPrefix(a.Val, "//") {
				a.Val = open(a.Val)
			}
		}
		kept = append(kept, a)
	}
	switch t.Data {
	case "button", "input", "select", "textarea":
		kept = append(kept, xhtml.Attribute{Key: "disabled"})
	}
	t.Attr = kept
	return t.String()
}

var bulkKeyRe = regexp.MustCompile(`bulkSelected\[(".*?")\]`)

// bulkBox keeps a row's checkbox working, its key says which server the row is on and the master splits the selection
func bulkBox(t xhtml.Token, id string) string {
	for i, a := range t.Attr {
		switch a.Key {
		case "value":
			t.Attr[i].Val = bulkItemPrefix + id + ":" + a.Val
		case ":checked":
			t.Attr[i].Val = bulkKeyRe.ReplaceAllStringFunc(a.Val, func(found string) string {
				key := strings.TrimSuffix(strings.TrimPrefix(found, `bulkSelected["`), `"]`)
				return `bulkSelected["` + bulkItemPrefix + id + ":" + key + `"]`
			})
		}
	}
	return t.String()
}

// rewriteRow is every tag of a row from another server: links open that server, forms post to it, and a script that would hit the page's own server is switched off
func rewriteRow(t xhtml.Token, raw []byte, open func(string) string, id string, keepScripts, inForm bool) string {
	switch t.Data {
	case "a":
		return neutralize(t, open)
	case "form":
		if strings.EqualFold(attrOf(t, "method"), "post") {
			for i, a := range t.Attr {
				if a.Key == "action" && strings.HasPrefix(a.Val, "/") {
					t.Attr[i].Val = nodeForwardPrefix + id + a.Val
				}
			}
		}
		return t.String()
	case "input":
		if attrOf(t, "type") == "checkbox" {
			if strings.Contains(attrOf(t, "class"), "bulk-select-box") {
				return bulkBox(t, id)
			}
			return neutralize(t, open)
		}
		if attrOf(t, "type") == "hidden" {
			return string(raw)
		}
	}
	if keepScripts {
		return string(raw)
	}
	// no script of its own means a hover menu or a plain submit, those work as they are, a page's shared row menu has to know whose row opened it
	if t.Data == "button" && !hasHandlers(t) {
		if strings.Contains(attrOf(t, "class"), "-menu-btn") {
			t.Attr = append(t.Attr, xhtml.Attribute{Key: "data-cluster-node", Val: id})
			return t.String()
		}
		return string(raw)
	}
	if !hasHandlers(t) && t.Data != "select" && t.Data != "textarea" && t.Data != "input" {
		return string(raw)
	}
	// a confirm stays since the row's form still posts
	if inForm && t.Data == "button" && strings.Contains(attrOf(t, "onclick"), "confirm(") && !strings.EqualFold(attrOf(t, "type"), "button") {
		return string(raw)
	}
	// its script would run against the page's own server, so it opens the row's server to do it there
	if t.Data == "button" {
		return openButton(t, open(""))
	}
	return neutralize(t, open)
}

func openButton(t xhtml.Token, link string) string {
	kept := []xhtml.Attribute{{Key: "type", Val: "button"}, {Key: "onclick", Val: "window.location.href='" + template.JSEscapeString(link) + "'"}}
	for _, a := range t.Attr {
		if !isHandlerAttr(a.Key) && a.Key != "id" && a.Key != "type" && a.Key != "title" && a.Key != "disabled" && !strings.HasPrefix(a.Key, ":") && !strings.HasPrefix(a.Key, "x-") && a.Key != "form" {
			kept = append(kept, a)
		}
	}
	t.Attr = kept
	return t.String()
}

func cellTime(c cellSpan) (time.Time, bool) {
	text := strings.TrimSpace(html.UnescapeString(c.text))
	for _, layout := range timeLayouts {
		if ts, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

// mergedRow keeps a row together with the details row under it when a list gets sorted
type mergedRow struct {
	sortText string
	html     []byte
	time     time.Time
	has      bool
	server   string
}

type remotePage struct {
	id, name string
	body     []byte
}

// same arrows as the other sortable columns, the one in effect is dimmed
const (
	sortUpIcon   = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="currentColor" aria-hidden="true" class="size-3.5 text-gray-900 dark:text-gray-50 %s"><path d="M11.9999 10.8284L7.0502 15.7782L5.63599 14.364L11.9999 8L18.3639 14.364L16.9497 15.7782L11.9999 10.8284Z"></path></svg>`
	sortDownIcon = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="currentColor" aria-hidden="true" class="size-3.5 text-gray-900 dark:text-gray-50 %s"><path d="M11.9999 13.1714L16.9497 8.22168L18.3639 9.63589L11.9999 15.9999L5.63599 9.63589L7.0502 8.22168L11.9999 13.1714Z"></path></svg>`
)

// mergeOptions is how the merged list gets ordered, serverDir is "asc" or "desc" when sorting by the Server column
type mergeOptions struct {
	sortTime  bool
	serverDir string
	ascLink   string
	descLink  string
	// server filter, only is empty or the id of the one server to show
	only     string
	activeID string
	servers  []serverEntry
	// sort the page's own column across servers, sortKey is what its header links call it
	sortKey string
	sortDir string
	// show the column and filter even while no server has a row
	forceColumn  bool
	selectFilter bool
	filterURL    func(id string) string
	noFilter     bool
	// row actions go through to the row's server instead of being switched off
	actionable bool
}

// filterSelect sits next to the page's own filters, the page reloads with ?server= so the backend does the filtering, window. is needed since URL inside an inline handler means document.URL
func (o mergeOptions) filterSelect() string {
	var b strings.Builder
	b.WriteString(`<select aria-label="Filter by server" onchange="var u=new window.URL(window.location.href);this.value?u.searchParams.set('server',this.value):u.searchParams.delete('server');u.searchParams.delete('page');window.location.href=u.href" class="w-full rounded border border-gray-300 bg-white px-2 py-1.5 font-medium text-gray-600 sm:w-fit sm:text-xs dark:border-gray-700 dark:bg-gray-950 dark:text-gray-400">`)
	b.WriteString(`<option value="">All servers</option>`)
	for _, e := range o.servers {
		selected := ""
		if e.ID == o.only {
			selected = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(e.ID) + `"` + selected + `>` + html.EscapeString(e.Name) + `</option>`)
	}
	b.WriteString(`</select>`)
	return b.String()
}

const (
	filterButtonClass = "flex w-full items-center gap-x-1.5 whitespace-nowrap rounded border border-gray-300 px-2 py-1.5 font-medium text-gray-600 hover:bg-gray-50 sm:w-fit sm:text-xs dark:border-gray-700 dark:text-gray-400 hover:dark:bg-gray-900 border-dashed cursor-pointer list-none"
	filterMenuClass   = "absolute z-10 mt-1 text-sm shadow-md border border-gray-200 dark:border-gray-800 text-gray-900 dark:text-gray-50 bg-gray-50 dark:bg-gray-950 rounded-lg w-48 p-2 space-y-1"
	filterChevron     = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="currentColor" aria-hidden="true" class="remixicon size-5 shrink-0 text-gray-500 sm:size-4"><path d="M11.9999 13.1714L16.9497 8.22168L18.3639 9.63589L11.9999 15.9999L5.63599 9.63589L7.0502 8.22168L11.9999 13.1714Z"></path></svg>`
)

type dropItem struct {
	label, href string
	selected    bool
}

// dropdown looks like the filters next to the search box, plain details and links so it also works under the terminal's strict csp
func dropdown(label string, items []dropItem) string {
	var b strings.Builder
	b.WriteString(`<details class="cluster-dd relative"><summary class="` + filterButtonClass + `"><span class="w-full text-left sm:w-fit">` + html.EscapeString(label) + `</span>` + filterChevron + `</summary><ul class="` + filterMenuClass + `">`)
	for _, it := range items {
		mark := ""
		if it.selected {
			mark = ` font-semibold`
		}
		b.WriteString(`<li><a class="block px-2 py-1 rounded-sm hover:bg-gray-100 dark:hover:bg-gray-600 truncate` + mark + `" href="` + html.EscapeString(it.href) + `">` + html.EscapeString(it.label) + `</a></li>`)
	}
	b.WriteString(`</ul></details>`)
	return b.String()
}

// filterControl is what goes next to the search box, notifications keeps its row of selects
func (o mergeOptions) filterControl() string {
	if o.selectFilter || o.filterURL == nil {
		return o.filterSelect()
	}
	label := "Server"
	items := []dropItem{{label: "All servers", href: o.filterURL(""), selected: o.only == ""}}
	for _, e := range o.servers {
		items = append(items, dropItem{label: e.Name, href: o.filterURL(e.ID), selected: e.ID == o.only})
		if e.ID == o.only {
			label = "Server: " + e.Name
		}
	}
	return dropdown(label, items)
}

func isSearchModel(t xhtml.Token) bool {
	for _, a := range t.Attr {
		if strings.HasPrefix(a.Key, "x-model") && a.Val == "searchQuery" {
			return true
		}
	}
	return false
}

// filterAnchor is the offset right after the box around the page's search input, -1 when the page has none
func filterAnchor(page []byte) int {
	offset := bytes.Index(page, []byte(mainMarker))
	if offset < 0 {
		return -1
	}
	z := xhtml.NewTokenizer(bytes.NewReader(page[offset:]))
	pos, depth, target := offset, 0, -1
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return -1
		}
		pos += len(z.Raw())
		if tt != xhtml.StartTagToken && tt != xhtml.EndTagToken && tt != xhtml.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		switch {
		case t.Data == "table" && tt == xhtml.StartTagToken && target < 0:
			return -1
		case t.Data == "input" && target < 0 && (isSearchModel(t) || attrOf(t, "type") == "search"):
			target = depth
		case t.Data == "div" && tt == xhtml.StartTagToken:
			depth++
		case t.Data == "div" && tt == xhtml.EndTagToken:
			if target >= 0 && depth == target {
				return pos
			}
			depth--
		}
	}
}

func (o mergeOptions) serverHeader() string {
	dim := func(dir string) string {
		if o.serverDir == dir {
			return "opacity-30"
		}
		return ""
	}
	return `<div class="-mx-2 inline-flex cursor-pointer items-center gap-2 rounded px-2 py-1 hover:bg-gray-50 hover:dark:bg-gray-900"><span>Server</span><div class="-space-y-2">` +
		`<a href="` + html.EscapeString(o.descLink) + `">` + fmt.Sprintf(sortUpIcon, dim("desc")) + `</a>` +
		`<a href="` + html.EscapeString(o.ascLink) + `">` + fmt.Sprintf(sortDownIcon, dim("asc")) + `</a></div></div>`
}

// mergeTables adds the Server column and the rows of the other servers, nil means there was nothing to merge and the page stays as it was
func mergeTables(page []byte, activeName string, remotes []remotePage, opts mergeOptions, open func(id, href string) string) []byte {
	tables := parseTables(page)
	if len(tables) == 0 {
		return nil
	}
	type splice struct {
		at, cut int
		text    []byte
	}
	var splices []splice
	total := 0
	for i := range tables {
		t := &tables[i]
		if t.hasTemplate || t.headRow == nil || t.bodyEnd == 0 {
			continue
		}
		var rows []mergedRow
		timeCol := -1
		for ci, c := range t.headRow.cells {
			if timeHeaderRe.MatchString(strings.TrimSpace(html.UnescapeString(c.text))) {
				// the Server column goes in front of it
				timeCol = ci
			}
		}
		sortCol := -1
		if opts.sortKey != "" {
			for ci, c := range t.headRow.cells {
				if c.sortKey == opts.sortKey {
					sortCol = ci
				}
			}
		}
		add := func(raw []byte, r rowSpan, server string) {
			if detailRow(r) && len(rows) > 0 {
				rows[len(rows)-1].html = append(rows[len(rows)-1].html, raw...)
				return
			}
			m := mergedRow{html: raw, server: server}
			if sortCol >= 0 {
				if i := sortCol - max(0, len(t.headRow.cells)-len(r.cells)); i >= 0 && i < len(r.cells) {
					m.sortText = strings.TrimSpace(html.UnescapeString(r.cells[i].text))
				}
			}
			if timeCol >= 0 && timeCol < len(r.cells) {
				m.time, m.has = cellTime(r.cells[timeCol])
			}
			rows = append(rows, m)
		}

		remoteAny := false
		for _, rp := range remotes {
			other := matchTable(parseTables(rp.body), t, i)
			if other == nil || !other.realRows() {
				continue
			}
			remoteAny = true
			link := open(rp.id, "")
			name := `<a href="` + html.EscapeString(link) + `">` + html.EscapeString(rp.name) + `</a>`
			for _, r := range other.rows {
				add(remoteRow(rp.body[r.start:r.end], r.start, r, rowColumn(other, r), name, func(href string) string { return open(rp.id, href) }, rp.id, opts.actionable), r, rp.name)
			}
		}
		// local data with no rows elsewhere still shows where it lives, a js-filled table has neither and is left alone
		showLocal := opts.only == "" || opts.only == opts.activeID
		if !remoteAny && !t.realRows() && opts.only == "" && !opts.forceColumn {
			continue
		}

		localRows := make([]mergedRow, 0, len(t.rows))
		remoteRows := rows
		rows = localRows
		if t.realRows() && showLocal {
			for _, r := range t.rows {
				if detailRow(r) {
					add(page[r.start:r.end], r, activeName)
					continue
				}
				col := min(rowColumn(t, r), len(r.cells))
				var b bytes.Buffer
				cls := r.cells[min(col, len(r.cells)-1)].class
				if col < len(r.cells) {
					b.Write(page[r.start:r.cells[col].start])
					b.WriteString(serverCell("td", cls, html.EscapeString(activeName)))
					b.Write(page[r.cells[col].start:r.end])
				} else {
					b.Write(page[r.start:r.cells[len(r.cells)-1].end])
					b.WriteString(serverCell("td", cls, html.EscapeString(activeName)))
					b.Write(page[r.cells[len(r.cells)-1].end:r.end])
				}
				add(b.Bytes(), r, activeName)
			}
		}
		rows = append(rows, remoteRows...)
		switch {
		case sortCol >= 0 && remoteAny:
			sort.SliceStable(rows, func(a, b int) bool {
				if opts.sortDir == "desc" {
					return lessText(rows[b].sortText, rows[a].sortText)
				}
				return lessText(rows[a].sortText, rows[b].sortText)
			})
		case opts.serverDir != "":
			sort.SliceStable(rows, func(a, b int) bool {
				x, y := strings.ToLower(rows[a].server), strings.ToLower(rows[b].server)
				if opts.serverDir == "desc" {
					return x > y
				}
				return x < y
			})
		case opts.sortTime && timeCol >= 0:
			sort.SliceStable(rows, func(a, b int) bool {
				if rows[a].has != rows[b].has {
					return rows[a].has
				}
				return rows[a].time.After(rows[b].time)
			})
		}

		head := t.headRow
		col := tableColumn(t)
		headClass := ""
		if len(head.cells) > 0 {
			headClass = head.cells[min(col, len(head.cells)-1)].class
		}
		at := head.end - len("</tr>")
		if col < len(head.cells) {
			at = head.cells[col].start
		}
		splices = append(splices, splice{at: at, text: []byte(serverCell("th", headClass, opts.serverHeader()))})

		var body bytes.Buffer
		for _, r := range rows {
			body.Write(r.html)
		}
		if len(rows) == 0 {
			fmt.Fprintf(&body, `<tr><td colspan="%d" class="p-6 text-center text-sm text-gray-500">Nothing here.</td></tr>`, len(head.cells)+1)
		}
		if total == 0 {
			total = len(rows)
		}
		first := t.bodyEnd
		if len(t.rows) > 0 {
			first = t.rows[0].start
		}
		splices = append(splices, splice{at: first, cut: t.bodyEnd - first, text: body.Bytes()})
	}
	if len(splices) == 0 {
		return nil
	}
	if at := filterAnchor(page); at >= 0 && !opts.noFilter {
		splices = append(splices, splice{at: at, text: []byte(opts.filterControl())})
	}
	sort.Slice(splices, func(a, b int) bool { return splices[a].at < splices[b].at })
	var out bytes.Buffer
	pos := 0
	for _, s := range splices {
		out.Write(page[pos:s.at])
		out.Write(s.text)
		pos = s.at + s.cut
	}
	out.Write(page[pos:])
	return fixCounts(out.Bytes(), total, max(len(opts.servers), 1))
}

var (
	onThisServerRe = regexp.MustCompile(`<b>\d+</b>([^<]*?)\s+(?:hosted\s+)?on this server\.?`)
	otherCountRe   = regexp.MustCompile(`<b>\d+</b>(\s+websites?\s+in Site Manager|\s+databases?\s+across)`)
	allUsersRe     = regexp.MustCompile(`(in Site Manager\s+across all users)`)
	showingOfRe    = regexp.MustCompile(`(?s)(Showing\s*<span[^>]*>[^<]*</span>\s*of\s*<span[^>]*>)[^<]*(</span>)`)
)

// fixCounts makes the page's totals count every server, not only the one it came from
func fixCounts(page []byte, total, servers int) []byte {
	page = onThisServerRe.ReplaceAll(page, []byte(fmt.Sprintf(`<b>%d</b>$1 across <b>%d</b> servers.`, total, servers)))
	page = otherCountRe.ReplaceAll(page, []byte(fmt.Sprintf(`<b>%d</b>$1`, total)))
	page = allUsersRe.ReplaceAll(page, []byte(`$1 on all servers`))
	return showingOfRe.ReplaceAll(page, []byte(fmt.Sprintf("${1}%d${2}", total)))
}

// lessText compares like a person would, numbers as numbers and leading units ignored
func lessText(a, b string) bool {
	x, xErr := strconv.ParseFloat(strings.TrimRight(strings.Fields(a + " 0")[0], "%"), 64)
	y, yErr := strconv.ParseFloat(strings.TrimRight(strings.Fields(b + " 0")[0], "%"), 64)
	if xErr == nil && yErr == nil && x != y {
		return x < y
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

func matchTable(tables []tableSpan, t *tableSpan, index int) *tableSpan {
	if t.id != "" {
		for i := range tables {
			if tables[i].id == t.id {
				return &tables[i]
			}
		}
		return nil
	}
	if index < len(tables) {
		return &tables[index]
	}
	return nil
}

func mergeable(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Upgrade") != "" || r.URL.Query().Get("output") != "" {
		return false
	}
	for _, p := range noMergePrefixes {
		if strings.HasPrefix(r.URL.Path, p) {
			return false
		}
	}
	return !neverProxied(r.URL.Path)
}

// fetchNodePage reads a page with the admin's session there, cookies aren't kept so a pending flash message isn't used up
func (m *Manager) fetchNodePage(ctx context.Context, n Node, uri string, u *admindb.User, ip string) []byte {
	return m.fetchNodeBytes(ctx, n, uri, u, ip, false)
}

func (m *Manager) fetchNodeBytes(ctx context.Context, n Node, uri string, u *admindb.User, ip string, anyType bool) []byte {
	body, relogin := m.fetchNodePageOnce(ctx, n, uri, u, ip, anyType)
	if relogin {
		// the session there is gone, the admin's own page view would log in again too
		m.sessMu.Lock()
		delete(m.sessions, sessionKey(u.ID, n.ID, ip))
		m.sessMu.Unlock()
		body, _ = m.fetchNodePageOnce(ctx, n, uri, u, ip, anyType)
	}
	return body
}

func (m *Manager) fetchNodePageOnce(ctx context.Context, n Node, uri string, u *admindb.User, ip string, anyType bool) ([]byte, bool) {
	s, err := m.nodeLogin(n, u, ip)
	if err != nil {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.URL+uri, nil)
	if err != nil {
		return nil, false
	}
	m.sessMu.Lock()
	for _, c := range s.cookies {
		req.AddCookie(c)
	}
	m.sessMu.Unlock()
	req.Header.Set("X-Forwarded-For", ip)
	client := &http.Client{Transport: m.client(n).Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		return nil, true
	}
	// the page's forms carry a token for this cookie, the flash in the session cookie stays untouched
	m.sessMu.Lock()
	for _, c := range resp.Cookies() {
		if c.Name == csrfCookie && c.MaxAge >= 0 {
			s.cookies[c.Name] = c
		}
	}
	m.sessMu.Unlock()
	if resp.StatusCode != http.StatusOK || (!anyType && !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")) {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBody))
	if err != nil {
		return nil, false
	}
	return body, false
}

const (
	tokenProbePath    = "/server/timezone"
	nodeForwardPrefix = "/_cluster/n/"
	csrfCookie        = "_gorilla_csrf"
)

// actionablePages list what a row of another server can do from a merged page, keep it short since each entry means real actions across servers
var (
	// their lists are server rendered, an empty one is a real empty list and not one javascript fills later
	forceColumnPages = map[string]bool{"/emails/queue": true, "/services/ftp": true, "/plans/resellers": true, "/resellers": true, "/websites": true, "/databases": true, "/backups/user": true, "/domains": true, "/users": true}
	// these keep their own page scripts on rows from other servers, their scripts post through the row's form
	actionablePages = map[string]bool{"/services": true}
	forwardGet      = map[string]bool{"/services/action-status": true, "/services/admin/status": true, "/json/system": true, "/emails/accounts": true}
	forwardDenied   = []string{"/login", "/logout", "/ws/", "/terminal", "/_cluster/", "/api/"}
	csrfMetaRe      = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
)

// handleNodeForward runs one allowed action on a server other than the active one, the csrf token in the form is checked by that server against this admin's session there
func (m *Manager) handleNodeForward(w http.ResponseWriter, r *http.Request, next http.Handler) {
	u := m.sessionUser(r)
	if u == nil || u.Role == "reseller" || !m.IsMaster() || !m.sessionIPOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, nodeForwardPrefix), "/")
	rest = "/" + rest
	allowed := ok && ((r.Method == http.MethodGet && forwardGet[rest]) || r.Method == http.MethodPost)
	for _, p := range forwardDenied {
		if strings.HasPrefix(rest, p) {
			allowed = false
		}
	}
	if !allowed {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if rest == "/emails/accounts" && r.URL.Query().Get("output") != "json" {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	out := r.Clone(r.Context())
	out.URL = &url.URL{Path: rest, RawQuery: r.URL.RawQuery}
	out.RequestURI = out.URL.RequestURI()
	n, known := m.liveNodeByID(id)
	if id != LocalNode && !known {
		http.Error(w, "unknown server", http.StatusNotFound)
		return
	}
	// a script call carries the token of the page it runs on, a custom header from our own origin can't come from another site so it's swapped for the target's
	if r.Header.Get("X-CSRFToken") != "" {
		if ou, err := url.Parse(r.Header.Get("Origin")); err != nil || ou.Host != r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body []byte
		if id == LocalNode {
			probe := r.Clone(r.Context())
			probe.Method, probe.Body = http.MethodGet, http.NoBody
			probe.URL = &url.URL{Path: tokenProbePath}
			probe.RequestURI = tokenProbePath
			probe.Header.Del("Accept-Encoding")
			probe.Header.Del("Content-Type")
			probe.Header.Del("Content-Length")
			probe.ContentLength = 0
			body = m.fetchLocalPage(probe, next)
		} else {
			probeCtx, cancel := context.WithTimeout(r.Context(), readTimeout)
			body = m.fetchNodePage(probeCtx, n, tokenProbePath, u, clientIPOf(r))
			cancel()
		}
		tok := csrfMetaRe.FindSubmatch(body)
		if tok == nil {
			http.Error(w, "could not reach that server", http.StatusBadGateway)
			return
		}
		out.Header.Set("X-CSRFToken", html.UnescapeString(string(tok[1])))
	}
	if id == LocalNode {
		out.Header.Del("Accept-Encoding")
		next.ServeHTTP(w, out)
		return
	}
	m.proxy(w, out, n, u, next)
	if r.Method == http.MethodPost && r.Header.Get("X-Requested-With") == "" {
		m.reportNodeFlash(r, n, u)
	}
}

type serverSortKey struct{}

type serverFilterKey struct{}

// sortDirection reads ?direction=desc or the "-" prefix of ?sort=-pid that the process list uses
func sortDirection(q url.Values) string {
	if strings.HasPrefix(q.Get("sort"), "-") {
		return "desc"
	}
	return q.Get("direction")
}

// takeServerSort pulls ?sort=server and ?server= off the request, only the master knows them and the servers behind it would reject them
func takeServerSort(r *http.Request) *http.Request {
	q := r.URL.Query()
	only := q.Get("server")
	if q.Get("sort") != "server" && only == "" {
		return r
	}
	ctx := r.Context()
	if q.Get("sort") == "server" {
		dir := "asc"
		if strings.EqualFold(q.Get("direction"), "desc") {
			dir = "desc"
		}
		ctx = context.WithValue(ctx, serverSortKey{}, dir)
		q.Del("sort")
		q.Del("direction")
	}
	if only != "" {
		ctx = context.WithValue(ctx, serverFilterKey{}, only)
		q.Del("server")
	}
	out := r.Clone(ctx)
	out.URL.RawQuery = q.Encode()
	out.RequestURI = out.URL.RequestURI()
	return out
}

func sortLink(r *http.Request, dir string) string {
	q := r.URL.Query()
	if only, _ := r.Context().Value(serverFilterKey{}).(string); only != "" {
		q.Set("server", only)
	}
	q.Set("sort", "server")
	q.Set("direction", dir)
	return r.URL.Path + "?" + q.Encode()
}

// pageRecorder keeps what the master's own handlers answer, used when another server is the active one
type pageRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *pageRecorder) Header() http.Header         { return p.header }
func (p *pageRecorder) WriteHeader(status int)      { p.status = status }
func (p *pageRecorder) Write(b []byte) (int, error) { return p.body.Write(b) }

func (m *Manager) fetchLocalPage(r *http.Request, next http.Handler) []byte {
	rec := &pageRecorder{header: http.Header{}, status: http.StatusOK}
	local := r.Clone(r.Context())
	local.Header.Del("Accept-Encoding")
	next.ServeHTTP(rec, local)
	if rec.status != http.StatusOK || !strings.HasPrefix(rec.header.Get("Content-Type"), "text/html") {
		return nil
	}
	return rec.body.Bytes()
}

// mergePage is the backend side of every list showing all servers, the active server's page is what the visitor keeps
var userPageRe = regexp.MustCompile(`^/users/([^/]+)$`)

// transferPicker offers the cluster's members on the account's transfer form, the ssh fields stay for a server outside it
func (m *Manager) transferPicker(page []byte, account, activeID string) []byte {
	at := bytes.Index(page, []byte(`id="transfer-form"`))
	if at < 0 {
		return page
	}
	end := bytes.IndexByte(page[at:], '>')
	if end < 0 {
		return page
	}
	at += end + 1
	var b strings.Builder
	fmt.Fprintf(&b, `<div id="cluster-transfer" class="mb-4" data-account="%s" data-from="%s"><label class="text-sm leading-none text-gray-900 dark:text-gray-50 font-medium" for="cluster-transfer-to">Destination</label><div class="relative w-full mt-2"><select id="cluster-transfer-to" class="relative block w-full appearance-none rounded border px-2.5 py-2 shadow-sm outline-none transition sm:text-sm border-gray-300 dark:border-gray-800 text-gray-900 dark:text-gray-50 bg-white dark:bg-gray-950"><option value="">Another server over SSH</option>`, html.EscapeString(account), html.EscapeString(activeID))
	add := func(id, name string, online bool) {
		if id == activeID {
			return
		}
		disabled := ""
		if !online {
			disabled = " disabled"
			name += " (offline)"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, html.EscapeString(id), disabled, html.EscapeString(name))
	}
	add(LocalNode, m.displayName(), true)
	for _, n := range m.LiveNodes() {
		online := true
		if h, ok := m.nodeHealth(n.ID); ok {
			online = h.Online
		}
		add(n.ID, n.Name, online)
	}
	b.WriteString(`</select></div><div id="cluster-transfer-member" class="mt-4" style="display:none"><label class="text-sm font-medium" for="cluster-transfer-reseller">If the account belongs to a reseller</label><select id="cluster-transfer-reseller" class="mt-2 mb-3 relative block w-full appearance-none rounded border px-2.5 py-2 shadow-sm outline-none sm:text-sm border-gray-300 dark:border-gray-800 text-gray-900 dark:text-gray-50 bg-white dark:bg-gray-950"><option value="copy">Copy its reseller to the new server</option><option value="drop">Leave it without a reseller</option></select><label class="flex items-center gap-2 text-sm"><input type="checkbox" id="cluster-transfer-live" checked> Live: stop the account for the final copy and forward its websites until DNS moves</label><button type="button" id="cluster-transfer-start" class="mt-3 relative inline-flex items-center justify-center whitespace-nowrap rounded border px-3 py-2 text-center text-sm font-medium shadow-sm border-transparent text-white dark:text-gray-900 bg-indigo-600 dark:bg-indigo-500 hover:bg-indigo-500 cursor-pointer">Start move</button><p id="cluster-transfer-result" class="mt-2 text-sm text-gray-600 dark:text-gray-400"></p></div></div>`)
	out := make([]byte, 0, len(page)+b.Len())
	out = append(out, page[:at]...)
	out = append(out, b.String()...)
	return append(out, page[at:]...)
}

var (
	createFormPages   = map[string]bool{"/administrators": true, "/resellers": true}
	alwaysPickerPages = map[string]bool{"/services/podman": true, "/domains/ratelimit": true}
)

// createFormPicker puts the Server choice first in a page's create form, the form that asks for a username
func (m *Manager) createFormPicker(page []byte, path, activeID string, u *admindb.User) []byte {
	from := 0
	for {
		rel := bytes.Index(page[from:], []byte("<form "))
		if rel < 0 {
			return page
		}
		start := from + rel
		end := bytes.Index(page[start:], []byte("</form>"))
		open := bytes.IndexByte(page[start:], '>')
		if end < 0 || open < 0 {
			return page
		}
		if bytes.Contains(page[start:start+end], []byte(`name="username"`)) {
			return insertServerPicker(page, start+open+1, m, path, activeID, u)
		}
		from = start + end
	}
}

// newUserForm is where an account gets created, picking a server opens the form of that server since plans and options are per server
const newUserForm = `<form id="userForm"`

func (m *Manager) serverPicker(page []byte, activeID string, u *admindb.User) []byte {
	at := bytes.Index(page, []byte(newUserForm))
	if at < 0 {
		return page
	}
	end := bytes.IndexByte(page[at:], '>')
	if end < 0 {
		return page
	}
	return insertServerPicker(page, at+end+1, m, "/user/new", activeID, u)
}

func insertServerPicker(page []byte, at int, m *Manager, path, activeID string, u *admindb.User) []byte {
	option := func(id, name string, online bool) string {
		attrs := ""
		if id == activeID {
			attrs = " selected"
		} else if !online {
			attrs = " disabled"
			name += " (offline)"
		}
		return `<option value="` + html.EscapeString(m.openLink(u.ID, id, path)) + `"` + attrs + `>` + html.EscapeString(name) + `</option>`
	}
	var b strings.Builder
	b.WriteString(`<div class="mb-4"><label class="flex items-center h-5 text-sm leading-none text-gray-900 dark:text-gray-50 font-medium" for="cluster_server">Server</label><div class="relative w-full mt-2">`)
	b.WriteString(`<select id="cluster_server" onchange="window.location.href=this.value" class="relative block w-full appearance-none rounded border px-2.5 py-2 shadow-sm outline-none transition sm:text-sm border-gray-300 dark:border-gray-800 text-gray-900 dark:text-gray-50 bg-white dark:bg-gray-950">`)
	b.WriteString(option(LocalNode, m.displayName(), true))
	for _, n := range m.LiveNodes() {
		online := true
		if h, ok := m.nodeHealth(n.ID); ok {
			online = h.Online
		}
		b.WriteString(option(n.ID, n.Name, online))
	}
	b.WriteString(`</select></div></div>`)
	out := make([]byte, 0, len(page)+b.Len())
	out = append(out, page[:at]...)
	out = append(out, b.String()...)
	return append(out, page[at:]...)
}

func (m *Manager) mergePage(page []byte, r *http.Request, activeID, activeName string, u *admindb.User, next http.Handler) []byte {
	return m.decoratePage(m.mergePageBody(page, r, activeID, activeName, u, next), r.URL.Path, activeID, u)
}

func (m *Manager) mergePageBody(page []byte, r *http.Request, activeID, activeName string, u *admindb.User, next http.Handler) []byte {
	if r.URL.Path == "/user/new" {
		return m.serverPicker(page, activeID, u)
	}
	if r.URL.Path == "/user/import" {
		if at := bytes.Index(page, []byte(`<form id="userimportForm"`)); at >= 0 {
			if end := bytes.IndexByte(page[at:], '>'); end >= 0 {
				return insertServerPicker(page, at+end+1, m, r.URL.Path, activeID, u)
			}
		}
	}
	if match := userPageRe.FindStringSubmatch(r.URL.Path); match != nil {
		return m.transferPicker(page, match[1], activeID)
	}
	if !tablesMerge(r.URL.Path) || !bytes.Contains(page, []byte("<table")) {
		return m.pagePicker(page, r, activeID, activeName, u)
	}
	type target struct {
		id, name string
		node     *Node
	}
	only, _ := r.Context().Value(serverFilterKey{}).(string)
	wanted := func(id string) bool { return id != activeID && (only == "" || only == id) }
	servers := []serverEntry{{ID: LocalNode, Name: m.displayName()}}
	targets := []target{}
	if wanted(LocalNode) {
		targets = append(targets, target{id: LocalNode, name: m.displayName()})
	}
	for _, n := range m.LiveNodes() {
		servers = append(servers, serverEntry{ID: n.ID, Name: n.Name})
		if !wanted(n.ID) {
			continue
		}
		if h, ok := m.nodeHealth(n.ID); ok && !h.Online {
			continue
		}
		node := n
		targets = append(targets, target{id: n.ID, name: n.Name, node: &node})
	}
	ctx, cancel := context.WithTimeout(r.Context(), mergeFetchTimeout)
	defer cancel()
	uri := r.URL.RequestURI()
	ip := clientIPOf(r)
	remotes := make([]remotePage, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			var body []byte
			if t.node != nil {
				body = m.fetchNodePage(ctx, *t.node, uri, u, ip)
			} else {
				body = m.fetchLocalPage(r, next)
			}
			remotes[i] = remotePage{id: t.id, name: t.name, body: body}
		}(i, t)
	}
	wg.Wait()
	kept := remotes[:0]
	for _, rp := range remotes {
		if rp.body != nil {
			kept = append(kept, rp)
		}
	}
	dir, _ := r.Context().Value(serverSortKey{}).(string)
	filterURL := func(id string) string {
		q := r.URL.Query()
		q.Del("page")
		if id == "" {
			q.Del("server")
		} else {
			q.Set("server", id)
		}
		if enc := q.Encode(); enc != "" {
			return r.URL.Path + "?" + enc
		}
		return r.URL.Path
	}
	opts := mergeOptions{
		selectFilter: r.URL.Path == "/notifications", filterURL: filterURL, noFilter: noFilterPages[r.URL.Path],
		sortTime: r.URL.Query().Get("sort") == "", serverDir: dir, ascLink: sortLink(r, "asc"), descLink: sortLink(r, "desc"),
		only: only, activeID: activeID, servers: servers, actionable: actionablePages[r.URL.Path],
		sortKey: strings.TrimPrefix(r.URL.Query().Get("sort"), "-"), sortDir: sortDirection(r.URL.Query()), forceColumn: forceColumnPages[r.URL.Path],
	}
	if r.URL.Path == "/emails/accounts" {
		if merged := m.mergeEmails(page, activeID, activeName, kept, opts, u); merged != nil {
			return merged
		}
		return m.pagePicker(page, r, activeID, activeName, u)
	}
	if r.URL.Path == "/services" {
		if merged := mergeServices(page, activeName, kept, opts, func(id, href string) string {
			if href == "" {
				href = uri
			}
			return m.openLink(u.ID, id, href)
		}); merged != nil {
			return merged
		}
	}
	merged := mergeTables(page, activeName, kept, opts, func(id, href string) string {
		if href == "" {
			href = uri
		}
		return m.openLink(u.ID, id, href)
	})
	if merged == nil {
		return m.pagePicker(page, r, activeID, activeName, u)
	}
	if createFormPages[r.URL.Path] {
		merged = m.createFormPicker(merged, r.URL.Path, activeID, u)
	}
	// a merged page with no search box had nowhere to put the filter, like the waf profiles
	if filterAnchor(page) < 0 || alwaysPickerPages[r.URL.Path] {
		return m.pagePicker(merged, r, activeID, activeName, u)
	}
	return merged
}

func tablesMerge(path string) bool {
	// waf profiles and rules are the same on the whole fleet, one row each is the truth
	if path == "/security/waf" || strings.HasPrefix(path, "/security/waf/rules") {
		return false
	}
	// its update log is the one list on a settings page that differs per server
	if path == "/settings/updates" {
		return true
	}
	for _, p := range noTablePrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	return true
}

func pickerPage(path string) bool {
	// this list shows every server's accounts, there's nothing to pick
	if noFilterPages[path] {
		return false
	}
	for _, p := range pickerSkip {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	for _, p := range pickerPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return strings.HasPrefix(path, "/security/")
}

// pagePicker is for pages that show one server, reports, settings, logs and so on, picking another one opens the same page there
func (m *Manager) pagePicker(page []byte, r *http.Request, activeID, activeName string, u *admindb.User) []byte {
	if !pickerPage(r.URL.Path) {
		// a plan's page has no twin at the same id elsewhere, it only gets the apply box
		if planEditRe.MatchString(r.URL.Path) {
			if at := pickerAnchor(page); at >= 0 {
				toggle := `<div class="mt-2">` + applyToggle(activeID == LocalNode) + `</div>`
				if save := rightAnchor(page); save >= 0 {
					at = save
					toggle = `<div class="mr-1 flex items-center">` + applyToggle(activeID == LocalNode) + `</div>`
				}
				return []byte(string(page[:at]) + toggle + string(page[at:]))
			}
		}
		return page
	}
	at := pickerAnchor(page)
	if at < 0 {
		return page
	}
	uri := r.URL.RequestURI()
	items := []dropItem{{label: m.displayName(), href: m.openLink(u.ID, LocalNode, uri), selected: activeID == LocalNode}}
	for _, n := range m.LiveNodes() {
		label := n.Name
		if h, ok := m.nodeHealth(n.ID); ok && !h.Online {
			label += " (offline)"
		}
		items = append(items, dropItem{label: label, href: m.openLink(u.ID, n.ID, uri), selected: n.ID == activeID})
	}
	extra := ""
	if applyPage(r.URL.Path) && !bottomTogglePages[r.URL.Path] && !formTogglePages[r.URL.Path] {
		extra += applyToggle(activeID == LocalNode && !perServerDefault[r.URL.Path])
	}
	if bottomTogglePages[r.URL.Path] {
		if submit := bytes.LastIndex(page, []byte(`type="submit"`)); submit >= 0 {
			if btn := bytes.LastIndex(page[:submit], []byte("<button")); btn >= 0 {
				toggle := `<div class="mb-3">` + applyToggle(activeID == LocalNode) + `</div>`
				page = []byte(string(page[:btn]) + toggle + string(page[btn:]))
				at = pickerAnchor(page)
			}
		}
	}
	if r.URL.Path == "/settings/updates" {
		page = m.memberVersions(page)
		at = pickerAnchor(page)
	}
	// the right side of the header when it has one, otherwise floated right on the title's line, pages with no title get it above their content on the right
	spacing := "mt-2"
	float := ""
	menu := dropdown("Server: "+activeName, items)
	if save := rightAnchor(page); save >= 0 {
		at = save
		spacing = "mr-1"
		menu = strings.Replace(menu, filterMenuClass, "right-0 "+filterMenuClass, 1)
	} else if title := titleAnchor(page); title >= 0 {
		at = title
		spacing = "ml-4"
		float = ` style="float:right;margin-left:auto"`
		menu = strings.Replace(menu, filterMenuClass, "right-0 "+filterMenuClass, 1)
	} else {
		spacing = "px-6 pt-3 justify-end"
		menu = strings.Replace(menu, filterMenuClass, "right-0 "+filterMenuClass, 1)
	}
	control := `<div class="` + spacing + ` flex flex-wrap items-center gap-3 cluster-page-picker"` + float + `>` + menu + extra + `</div>`
	if bytes.Contains(page, []byte(terminalShell)) {
		control = menu
	}
	out := make([]byte, 0, len(page)+len(control))
	out = append(out, page[:at]...)
	out = append(out, control...)
	return append(out, page[at:]...)
}

var installedVersionRe = regexp.MustCompile(`(?s)(<div id="installed_version"[^>]*>).*?(</div>)`)

// memberVersions puts every member's version where the page shows the installed one
func (m *Manager) memberVersions(page []byte) []byte {
	var b strings.Builder
	line := func(name, version string) {
		b.WriteString(`<span class="flex justify-between gap-4 text-sm"><span>` + html.EscapeString(name) + `</span><span class="font-medium">` + html.EscapeString(version) + `</span></span>`)
	}
	line(m.displayName(), m.Version)
	for _, n := range m.LiveNodes() {
		version := "offline"
		if h, ok := m.nodeHealth(n.ID); ok && h.Online {
			version = h.Version
		}
		line(n.Name, version)
	}
	return installedVersionRe.ReplaceAll(page, []byte("${1}"+strings.ReplaceAll(b.String(), "$", "$$")+"${2}"))
}

// these keep Apply on all servers right above their submit button at the bottom of the page
var bottomTogglePages = map[string]bool{"/services/ftp/settings": true}

// these have several forms and only some are shared, so the box is inside those forms
var noFilterPages = map[string]bool{"/services/ftp": true}

var formTogglePages = map[string]bool{"/domains/ratelimit": true, "/emails/settings": true, "/backups/user": true}

var (
	scanFormRe     = regexp.MustCompile(`(?s)<form[^>]*action="/websites/scan"[^>]*>.*?</form>`)
	backupConfigRe = regexp.MustCompile(`(?s)<form[^>]*action="/backups/user/configuration"[^>]*>.*?</form>`)
	rateFormRe     = regexp.MustCompile(`(?s)<form[^>]*action="/domains/ratelimit"[^>]*>\s*<input[^>]*>\s*<input[^>]*name="action" value="default">.*?</form>`)
	submitButton   = []byte(`<button type="submit"`)
)

// beforeSubmit puts piece right in front of a form's submit button
func beforeSubmit(page []byte, form *regexp.Regexp, piece string) []byte {
	return form.ReplaceAllFunc(page, func(found []byte) []byte {
		at := bytes.LastIndex(found, submitButton)
		if at < 0 {
			return found
		}
		return []byte(string(found[:at]) + piece + string(found[at:]))
	})
}

// scanServerSelect is the server choice next to the username on the website scan form
func (m *Manager) scanServerSelect() string {
	var b strings.Builder
	b.WriteString(`<div><label for="scan_server" class="text-sm font-medium">Server</label><select name="cluster_server" id="scan_server" class="mt-2 block rounded border px-2.5 py-2 shadow-sm sm:text-sm border-gray-300 dark:border-gray-800 text-gray-900 dark:text-gray-50 bg-white dark:bg-gray-950"><option value="">All servers</option>`)
	fmt.Fprintf(&b, `<option value="%s">%s</option>`, LocalNode, html.EscapeString(m.displayName()))
	for _, n := range m.LiveNodes() {
		fmt.Fprintf(&b, `<option value="%s">%s</option>`, html.EscapeString(n.ID), html.EscapeString(n.Name))
	}
	b.WriteString(`</select></div>`)
	return b.String()
}

// decoratePage adds the controls that sit inside a page's own forms
func (m *Manager) decoratePage(page []byte, path, activeID string, u *admindb.User) []byte {
	switch path {
	case "/websites":
		return beforeSubmit(page, scanFormRe, m.scanServerSelect())
	case "/domains/ratelimit":
		return beforeSubmit(page, rateFormRe, `<div class="sm:self-center">`+applyToggle(activeID == LocalNode)+`</div>`)
	case "/backups/user":
		return beforeSubmit(page, backupConfigRe, `<div class="mb-2">`+applyToggle(activeID == LocalNode)+`</div>`)
	case "/emails/settings":
		control := `<div class="mt-4 mb-2 flex flex-wrap items-center gap-3">` + m.serverMenu("/emails/settings", activeID, u) + applyToggle(activeID == LocalNode) + `</div>`
		for _, marker := range []string{`id="save_path_btn"`, `>Enable Services<`, `id="relay-form"`} {
			at := bytes.Index(page, []byte(marker))
			if at < 0 {
				continue
			}
			btn := -1
			if marker == `id="save_path_btn"` {
				btn = bytes.LastIndex(page[:at], []byte("<button"))
			} else if rel := bytes.Index(page[at:], submitButton); rel >= 0 {
				btn = at + rel
			}
			if btn >= 0 {
				page = []byte(string(page[:btn]) + control + string(page[btn:]))
			}
		}
		return page
	case "/emails/domain-limits":
		menu := `<div class="mb-3">` + m.serverMenu("/emails/domain-limits", activeID, u) + `</div>`
		for _, marker := range []string{`<label for="restore_user"`, `<label for="remove_user"`} {
			page = bytes.Replace(page, []byte(marker), []byte(menu+marker), 1)
		}
		return page
	}
	return page
}

// serverMenu opens the same page on another server
func (m *Manager) serverMenu(path, activeID string, u *admindb.User) string {
	label := m.displayName()
	items := []dropItem{{label: m.displayName(), href: m.openLink(u.ID, LocalNode, path), selected: activeID == LocalNode}}
	for _, n := range m.LiveNodes() {
		items = append(items, dropItem{label: n.Name, href: m.openLink(u.ID, n.ID, path), selected: n.ID == activeID})
		if n.ID == activeID {
			label = n.Name
		}
	}
	return dropdown("Server: "+label, items)
}

const terminalShell = `<select id="shell"`

// saveAnchor is right in front of the last button in the page's header, where settings pages have Save
func saveAnchor(page []byte) int {
	start := bytes.Index(page, []byte(mainMarker))
	if start < 0 {
		return -1
	}
	open := bytes.Index(page[start:], []byte("<header"))
	if open < 0 {
		return -1
	}
	open += start
	end := bytes.Index(page[open:], []byte("</header>"))
	if end < 0 {
		return -1
	}
	rel := bytes.LastIndex(page[open:open+end], []byte("<button"))
	if rel < 0 {
		return -1
	}
	// a button in its own little form is an action like Disable, the controls can't go inside that form
	before := page[open : open+rel]
	if bytes.LastIndex(before, []byte("<form")) > bytes.LastIndex(before, []byte("</form>")) {
		return -1
	}
	// only a real save, not a help or create button
	btn := page[open+rel : open+end]
	if close := bytes.Index(btn, []byte("</button>")); close >= 0 {
		btn = btn[:close]
	}
	if !bytes.Contains(btn, []byte(`type="submit"`)) && !bytes.Contains(btn, []byte("Save")) {
		return -1
	}
	return open + rel
}

// rightAnchor is before the Save button, or else first in the block on the right of the page header
func rightAnchor(page []byte) int {
	if at := saveAnchor(page); at >= 0 {
		return at
	}
	start := bytes.Index(page, []byte(mainMarker))
	if start < 0 {
		return -1
	}
	open := bytes.Index(page[start:], []byte("<header"))
	if open < 0 {
		return -1
	}
	open += start
	end := bytes.Index(page[open:], []byte("</header>"))
	if end < 0 {
		return -1
	}
	// a page with tabs in its header keeps the server choice in that row, right before them
	if tabs := bytes.Index(page[open:open+end], []byte(`<div role="tablist"`)); tabs >= 0 {
		return open + tabs
	}
	rel := bytes.Index(page[open:open+end], []byte(`<div class="flex flex-none`))
	if rel < 0 {
		return -1
	}
	tag := bytes.IndexByte(page[open+rel:open+end], '>')
	if tag < 0 {
		return -1
	}
	return open + rel + tag + 1
}

// titleAnchor is right in front of the page title, the only spot where a float shares its line
func titleAnchor(page []byte) int {
	start := bytes.Index(page, []byte(mainMarker))
	if start < 0 {
		return -1
	}
	rel := bytes.Index(page[start:], []byte("<h1"))
	if rel < 0 {
		return -1
	}
	return start + rel
}

// pickerAnchor is under the page title, on the terminal right in front of the shell choice
func pickerAnchor(page []byte) int {
	if at := bytes.Index(page, []byte(terminalShell)); at >= 0 {
		return at
	}
	start := bytes.Index(page, []byte(mainMarker))
	if start < 0 {
		return -1
	}
	rel := bytes.Index(page[start:], []byte("</h1>"))
	if rel < 0 {
		if end := bytes.IndexByte(page[start:], '>'); end >= 0 {
			return start + end + 1
		}
		return -1
	}
	at := start + rel + len("</h1>")
	// the subtitle belongs with the title
	if next := bytes.Index(page[at:], []byte("</h2>")); next >= 0 && next < 600 && !bytes.Contains(page[at:at+next], []byte("</div>")) {
		at += next + len("</h2>")
	}
	return at
}

var (
	emailDataRe   = regexp.MustCompile(`const EMAIL_DATA = (\[.*?\]|null);`)
	domainSelect  = regexp.MustCompile(`(?s)(<select name="domain"[^>]*>)(.*?)(</select>)`)
	optionRe      = regexp.MustCompile(`<option value="([^"]+)">`)
	emailFormCSRF = regexp.MustCompile(`(?s)action="/emails/new".*?name="csrf_token" value="([^"]+)"`)
)

func emailRows(page []byte) ([][]any, bool) {
	m := emailDataRe.FindSubmatch(page)
	if m == nil {
		return nil, false
	}
	var rows [][]any
	if err := json.Unmarshal(m[1], &rows); err != nil {
		return nil, false
	}
	return rows, true
}

// mergeEmails works on the data the page's own javascript draws from, each row carries its server and the add form offers every server's domains
func (m *Manager) mergeEmails(page []byte, activeID, activeName string, remotes []remotePage, opts mergeOptions, u *admindb.User) []byte {
	local, ok := emailRows(page)
	if !ok {
		return nil
	}
	all := [][]any{}
	addRows := func(rows [][]any, id, name string) {
		for _, row := range rows {
			if len(row) < 3 {
				continue
			}
			base, link := "", ""
			if id != activeID {
				base = id
				if email, ok := row[0].(string); ok {
					link = m.openLink(u.ID, id, "/emails/webmail/"+email)
				}
			}
			all = append(all, []any{row[0], row[1], row[2], base, name, link})
		}
	}
	if opts.only == "" || opts.only == activeID {
		addRows(local, activeID, activeName)
	}
	var options strings.Builder
	for _, rp := range remotes {
		if rows, ok := emailRows(rp.body); ok {
			addRows(rows, rp.id, rp.name)
		}
		sel := domainSelect.FindSubmatch(rp.body)
		tok := emailFormCSRF.FindSubmatch(rp.body)
		if sel == nil || tok == nil {
			continue
		}
		for _, o := range optionRe.FindAllSubmatch(sel[2], -1) {
			fmt.Fprintf(&options, `<option value="%s" data-action="%s" data-token="%s">%s (%s)</option>`, o[1], nodeForwardPrefix+rp.id+"/emails/new", tok[1], o[1], html.EscapeString(rp.name))
		}
	}
	data, err := json.Marshal(all)
	if err != nil {
		return nil
	}
	out := emailDataRe.ReplaceAllFunc(page, func([]byte) []byte { return append(append([]byte("const EMAIL_DATA = "), data...), ';') })
	out = domainSelect.ReplaceAllFunc(out, func(found []byte) []byte {
		parts := domainSelect.FindSubmatch(found)
		own := optionRe.ReplaceAll(parts[2], []byte(`<option value="$1">`))
		return []byte(string(parts[1]) + string(own) + options.String() + string(parts[3]))
	})
	if at := filterAnchor(out); at >= 0 {
		out = append(out[:at:at], append([]byte(opts.filterControl()), out[at:]...)...)
	}
	return fixCounts(out, len(all), max(len(opts.servers), 1))
}

func (m *Manager) openSig(userID int64, id, next string) string {
	mac := hmac.New(sha256.New, []byte(m.State().SeedHex))
	fmt.Fprintf(mac, "open|%d|%s|%s", userID, id, next)
	return hex.EncodeToString(mac.Sum(nil))
}

// openLink is signed for this admin, a plain link would let any site change which server they're looking at
func (m *Manager) openLink(userID int64, id, next string) string {
	q := url.Values{"node": {id}, "next": {next}, "sig": {m.openSig(userID, id, next)}}
	return "/_cluster/open?" + q.Encode()
}

func (m *Manager) handleOpen(w http.ResponseWriter, r *http.Request) {
	u := m.sessionUser(r)
	if u == nil || u.Role == "reseller" || !m.IsMaster() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	id, next := q.Get("node"), q.Get("next")
	if !hmac.Equal([]byte(q.Get("sig")), []byte(m.openSig(u.ID, id, next))) || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		http.Error(w, "this link is not valid", http.StatusForbidden)
		return
	}
	if id != LocalNode {
		if _, ok := m.liveNodeByID(id); !ok {
			http.Error(w, "unknown server", http.StatusNotFound)
			return
		}
	}
	sess, err := m.Sessions.Get(r)
	if err != nil {
		http.Error(w, "no session", http.StatusForbidden)
		return
	}
	sess.Values[sessionKeyNode] = id
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "could not save session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// captureWriter buffers only html pages of the master, downloads and everything else pass straight through
type captureWriter struct {
	http.ResponseWriter
	status  int
	decided bool
	capture bool
	body    bytes.Buffer
}

func (c *captureWriter) decide(status int) {
	if c.decided {
		return
	}
	c.decided = true
	c.status = status
	if status == http.StatusOK && strings.HasPrefix(c.Header().Get("Content-Type"), "text/html") {
		c.capture = true
		return
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) WriteHeader(status int) { c.decide(status) }

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.decided {
		if c.Header().Get("Content-Type") == "" {
			c.Header().Set("Content-Type", http.DetectContentType(b))
		}
		c.decide(http.StatusOK)
	}
	if c.capture {
		return c.body.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *captureWriter) Flush() {
	if !c.capture {
		if f, ok := c.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// rewriteFragment is rewriteRow for a piece of a row, used where cells get moved around
func rewriteFragment(raw []byte, open func(string) string, id string, keepScripts bool) string {
	var out strings.Builder
	z := xhtml.NewTokenizer(bytes.NewReader(raw))
	inForm := false
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return out.String()
		}
		tok := z.Raw()
		if tt == xhtml.EndTagToken && z.Token().Data == "form" {
			inForm = false
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			out.Write(tok)
			continue
		}
		copied := append([]byte(nil), tok...)
		t := z.Token()
		if t.Data == "form" {
			inForm = true
		}
		out.WriteString(rewriteRow(t, copied, open, id, keepScripts, inForm))
	}
}

func headerIndex(t *tableSpan, name string) int {
	for i, c := range t.headRow.cells {
		if strings.EqualFold(strings.TrimSpace(html.UnescapeString(c.text)), name) {
			return i
		}
	}
	return -1
}

func cellInner(body []byte, c cellSpan) []byte {
	end := bytes.LastIndex(body[c.openEnd:c.end], []byte("</"))
	if end < 0 {
		return nil
	}
	return body[c.openEnd : c.openEnd+end]
}

// mergeServices shows each service once with a status and actions column per server, the same service runs everywhere so repeating its row says nothing
// restartBadgeRe is the RESTART NEEDED tag next to openpanel and admin, it belongs to one server so it goes in that server's column
var restartBadgeRe = regexp.MustCompile(`(?s)<span[^>]*>\s*RESTART NEEDED\s*</span>`)

func mergeServices(page []byte, activeName string, remotes []remotePage, opts mergeOptions, open func(id, href string) string) []byte {
	tables := parseTables(page)
	if len(tables) == 0 || !tables[0].realRows() {
		return nil
	}
	t := &tables[0]
	status, real, kind, actions := headerIndex(t, "Status"), headerIndex(t, "Real Name"), headerIndex(t, "Type"), len(t.headRow.cells)-1
	nameCol := headerIndex(t, "Service")
	if status < 0 || real < 0 || nameCol < 0 {
		return nil
	}
	// these fold into the service's own cell to leave room for a column per server
	folded := func(i int) bool { return i == status || i == actions || i == real || i == kind }

	type column struct {
		id, name string
		body     []byte
		table    *tableSpan
		rows     map[string]rowSpan
	}
	byName := func(table *tableSpan, body []byte) map[string]rowSpan {
		out := map[string]rowSpan{}
		for _, r := range table.rows {
			if real < len(r.cells) && !detailRow(r) {
				out[strings.TrimSpace(html.UnescapeString(r.cells[real].text))] = r
			}
		}
		return out
	}
	var columns []column
	if opts.only == "" || opts.only == opts.activeID {
		columns = append(columns, column{opts.activeID, activeName, page, t, byName(t, page)})
	}
	for _, rp := range remotes {
		other := matchTable(parseTables(rp.body), t, 0)
		if other == nil || other.headRow == nil || len(other.headRow.cells) != len(t.headRow.cells) {
			continue
		}
		columns = append(columns, column{rp.id, rp.name, rp.body, other, byName(other, rp.body)})
	}
	if len(columns) == 0 {
		return nil
	}

	cellClass := visibleClass(t.rows[0].cells[status].class)
	var head bytes.Buffer
	head.Write(page[t.headRow.start:t.headRow.cells[0].start])
	for i, c := range t.headRow.cells {
		if !folded(i) {
			head.Write(page[c.start:c.end])
		}
	}
	for _, col := range columns {
		head.WriteString(`<th class="` + html.EscapeString(visibleClass(t.headRow.cells[status].class)) + `">` + html.EscapeString(col.name) + `</th>`)
	}
	head.WriteString(`</tr>`)

	// services follow the page's own order, ones only another server has come after
	order := []string{}
	seen := map[string]bool{}
	for _, table := range append([]*tableSpan{t}, func() []*tableSpan {
		var rest []*tableSpan
		for _, col := range columns {
			rest = append(rest, col.table)
		}
		return rest
	}()...) {
		for _, r := range table.rows {
			if real < len(r.cells) && !detailRow(r) {
				name := strings.TrimSpace(html.UnescapeString(r.cells[real].text))
				if !seen[name] {
					seen[name] = true
					order = append(order, name)
				}
			}
		}
	}

	var body bytes.Buffer
	for _, name := range order {
		base, baseBody, baseID := rowSpan{}, []byte(nil), ""
		for _, col := range columns {
			if r, ok := col.rows[name]; ok {
				base, baseBody, baseID = r, col.body, col.id
				break
			}
		}
		if baseBody == nil {
			continue
		}
		link := func(id string) func(string) string {
			return func(href string) string { return open(id, href) }
		}
		common := func(raw []byte) string {
			if baseID == opts.activeID {
				return string(raw)
			}
			return rewriteFragment(raw, link(baseID), baseID, true)
		}
		body.WriteString(common(baseBody[base.start:base.cells[0].start]))
		for i, c := range base.cells {
			if folded(i) {
				continue
			}
			cell := common(baseBody[c.start:c.end])
			if i == nameCol {
				cell = restartBadgeRe.ReplaceAllString(cell, "")
				sub := html.EscapeString(strings.TrimSpace(html.UnescapeString(base.cells[real].text)))
				if kind >= 0 && kind < len(base.cells) {
					if v := strings.TrimSpace(html.UnescapeString(base.cells[kind].text)); v != "" {
						sub += " &middot; " + html.EscapeString(v)
					}
				}
				if end := strings.LastIndex(cell, "</td>"); end >= 0 {
					cell = cell[:end] + `<span class="block text-xs font-normal text-gray-500 dark:text-gray-400">` + sub + `</span>` + cell[end:]
				}
			}
			body.WriteString(cell)
		}
		for _, col := range columns {
			r, ok := col.rows[name]
			if !ok || actions >= len(r.cells) {
				body.WriteString(`<td class="` + html.EscapeString(cellClass) + `">-</td>`)
				continue
			}
			inner := append(append([]byte(nil), cellInner(col.body, r.cells[status])...), cellInner(col.body, r.cells[actions])...)
			if nameCol < len(r.cells) {
				if badge := restartBadgeRe.Find(col.body[r.cells[nameCol].start:r.cells[nameCol].end]); badge != nil {
					inner = append(inner, badge...)
				}
			}
			piece := string(inner)
			if col.id != opts.activeID {
				piece = rewriteFragment(inner, link(col.id), col.id, true)
			}
			body.WriteString(`<td class="` + html.EscapeString(cellClass) + `"><div class="flex items-center gap-3 whitespace-nowrap">` + piece + `</div></td>`)
		}
		body.WriteString(`</tr>`)
	}

	var out bytes.Buffer
	out.Write(page[:t.headRow.start])
	out.Write(head.Bytes())
	out.Write(page[t.headRow.end:t.rows[0].start])
	out.Write(body.Bytes())
	out.Write(page[t.bodyEnd:])
	result := out.Bytes()
	if at := filterAnchor(result); at >= 0 {
		result = append(result[:at:at], append([]byte(opts.filterControl()), result[at:]...)...)
	}
	return result
}
