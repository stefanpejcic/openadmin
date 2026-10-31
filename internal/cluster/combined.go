package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"openadmin/internal/admindb"
)

const (
	combinedTimeout  = readTimeout
	combinedLogLimit = 200
)

// OpenLink is for handlers showing something that lives on another server
func (m *Manager) OpenLink(userID int64, id, next string) string { return m.openLink(userID, id, next) }

// CallAll asks every other online server the same thing, a slow one is left out rather than holding up the page
func (m *Manager) CallAll(action string, fn func(id, name string, raw json.RawMessage)) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range m.LiveNodes() {
		if h, ok := m.nodeHealth(n.ID); ok && !h.Online {
			continue
		}
		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			var raw json.RawMessage
			if m.rpcContext(ctx, n, action, nil, &raw) != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			fn(n.ID, n.Name, raw)
		}(n)
	}
	wg.Wait()
}

type combinedPage struct {
	id, name string
	body     []byte
}

// allPages reads the same json route from every server, the master's own included, paused ones only when asked
func (m *Manager) allPages(r *http.Request, u *admindb.User, next http.Handler, withPaused bool) []combinedPage {
	q := r.URL.Query()
	q.Del("cluster")
	uri := (&url.URL{Path: r.URL.Path, RawQuery: q.Encode()}).RequestURI()
	local := r.Clone(r.Context())
	local.URL = &url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	local.RequestURI = uri
	local.Header.Del("Accept-Encoding")
	rec := &pageRecorder{header: http.Header{}, status: http.StatusOK}
	next.ServeHTTP(rec, local)
	pages := []combinedPage{}
	if rec.status == http.StatusOK {
		pages = append(pages, combinedPage{LocalNode, m.displayName(), rec.body.Bytes()})
	}

	nodes := m.LiveNodes()
	if withPaused {
		nodes = m.State().Nodes
	}
	ctx, cancel := context.WithTimeout(r.Context(), combinedTimeout)
	defer cancel()
	ip := clientIPOf(r)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodes {
		if h, ok := m.nodeHealth(n.ID); ok && !h.Online {
			continue
		}
		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			if body := m.fetchNodeBytes(ctx, n, uri, u, ip, true); body != nil {
				mu.Lock()
				name := n.Name
				if n.Paused {
					name += " (paused)"
				}
				pages = append(pages, combinedPage{n.ID, name, body})
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	return pages
}

// serveCombined is what the master's dashboard asks for with ?cluster=all, an unknown route just answers as usual
func (m *Manager) serveCombined(w http.ResponseWriter, r *http.Request, u *admindb.User, next http.Handler) {
	var out any
	switch r.URL.Path {
	case "/tasks":
		out = combineTasks(m.allPages(r, u, next, false), func(id, pid string) string {
			return m.openLink(u.ID, id, "/server/processes/"+pid+"/strace")
		})
	case "/json/combined_activity":
		out = combineActivity(m.allPages(r, u, next, false))
	case "/server/resource-usage/history/data":
		out = combineUsage(m.allPages(r, u, next, false))
	case "/json/cpu":
		out = combineCPU(m.allPages(r, u, next, true))
	case "/json/memory":
		out = combineMemory(m.allPages(r, u, next, true))
	case "/json/load":
		out = combineLoad(m.allPages(r, u, next, true))
	case "/json/disk":
		out = combineDisk(m.allPages(r, u, next, true))
	case "/emails/deliverability":
		out = combineDeliverability(m.allPages(r, u, next, false))
	default:
		q := r.URL.Query()
		q.Del("cluster")
		r.URL.RawQuery = q.Encode()
		next.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func combineTasks(pages []combinedPage, link func(id, pid string) string) []map[string]any {
	out := []map[string]any{}
	for _, p := range pages {
		var tasks []map[string]any
		if json.Unmarshal(p.body, &tasks) != nil {
			continue
		}
		for _, t := range tasks {
			t["server"] = p.name
			if p.id != LocalNode {
				if pid, err := json.Marshal(t["pid"]); err == nil {
					t["link"] = link(p.id, strings.Trim(string(pid), `"`))
				}
			}
			out = append(out, t)
		}
	}
	return out
}

// combineActivity keeps each line as the page expects it, a log line starts with its date so plain sorting orders them
func combineActivity(pages []combinedPage) map[string][]string {
	logs := []string{}
	for _, p := range pages {
		var data struct {
			Logs []string `json:"combined_logs"`
		}
		if json.Unmarshal(p.body, &data) != nil {
			continue
		}
		for _, line := range data.Logs {
			logs = append(logs, line+" ("+p.name+")")
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(logs)))
	if len(logs) > combinedLogLimit {
		logs = logs[:combinedLogLimit]
	}
	return map[string][]string{"combined_logs": logs}
}

type usageSnapshot struct {
	TS     string  `json:"ts"`
	CPUPct float64 `json:"cpu_pct"`
	Mem    struct {
		Pct float64 `json:"pct"`
	} `json:"mem"`
}

// combineUsage averages the servers, lined up from the newest sample since their clocks and sample counts can differ a little
func combineUsage(pages []combinedPage) map[string]any {
	type serverSeries struct {
		Name      string            `json:"name"`
		Snapshots []json.RawMessage `json:"snapshots"`
	}
	var series [][]usageSnapshot
	perServer := []serverSeries{}
	longest := 0
	for _, p := range pages {
		var data struct {
			Snapshots []usageSnapshot `json:"snapshots"`
		}
		if json.Unmarshal(p.body, &data) != nil || len(data.Snapshots) == 0 {
			continue
		}
		series = append(series, data.Snapshots)
		// whole snapshots, the history page shows load, swap and checks too
		var full struct {
			Snapshots []json.RawMessage `json:"snapshots"`
		}
		json.Unmarshal(p.body, &full)
		perServer = append(perServer, serverSeries{p.name, full.Snapshots})
		longest = max(longest, len(data.Snapshots))
	}
	out := make([]usageSnapshot, longest)
	for back := 0; back < longest; back++ {
		var cpu, mem float64
		count := 0
		ts := ""
		for _, s := range series {
			i := len(s) - 1 - back
			if i < 0 {
				continue
			}
			cpu += s[i].CPUPct
			mem += s[i].Mem.Pct
			if ts == "" {
				ts = s[i].TS
			}
			count++
		}
		snap := usageSnapshot{TS: ts, CPUPct: cpu / float64(count)}
		snap.Mem.Pct = mem / float64(count)
		out[longest-1-back] = snap
	}
	// the dashboard draws a line per server, the average stays for anything that only wants one
	return map[string]any{"snapshots": out, "servers": len(series), "per_server": perServer}
}

// combineCPU sends one average and core count per server, the header shows the busiest
func combineCPU(pages []combinedPage) map[string]any {
	type server struct {
		Name  string  `json:"name"`
		Avg   float64 `json:"avg"`
		Cores int     `json:"cores"`
	}
	servers := []server{}
	for _, p := range pages {
		var cores map[string]float64
		if json.Unmarshal(p.body, &cores) != nil || len(cores) == 0 {
			continue
		}
		sum := 0.0
		for _, v := range cores {
			sum += v
		}
		servers = append(servers, server{p.name, sum / float64(len(cores)), len(cores)})
	}
	return map[string]any{"servers": servers}
}

type memoryLine struct {
	Used    string `json:"used"`
	Total   string `json:"total"`
	Percent string `json:"percent"`
}

type memoryHuman struct {
	RAM  memoryLine `json:"ram"`
	Swap memoryLine `json:"swap"`
}

func combineMemory(pages []combinedPage) map[string]any {
	type server struct {
		Name string `json:"name"`
		memoryHuman
	}
	servers := []server{}
	highest := -1.0
	for _, p := range pages {
		var data struct {
			Human memoryHuman `json:"human_readable"`
		}
		if json.Unmarshal(p.body, &data) != nil {
			continue
		}
		servers = append(servers, server{p.name, data.Human})
		if v, err := strconv.ParseFloat(strings.TrimRight(data.Human.RAM.Percent, "% "), 64); err == nil && v > highest {
			highest = v
		}
	}
	shown := "0%"
	if highest >= 0 {
		shown = strconv.FormatFloat(highest, 'f', 1, 64) + "%"
	}
	return map[string]any{"human_readable": memoryHuman{RAM: memoryLine{Percent: shown}}, "servers": servers}
}

func combineLoad(pages []combinedPage) map[string]any {
	type load struct {
		Name string  `json:"name"`
		One  float64 `json:"load1min"`
		Five float64 `json:"load5min"`
		Fift float64 `json:"load15min"`
	}
	servers := []load{}
	var total load
	for _, p := range pages {
		var l load
		if json.Unmarshal(p.body, &l) != nil {
			continue
		}
		l.Name = p.name
		servers = append(servers, l)
		total.One = max(total.One, l.One)
		total.Five = max(total.Five, l.Five)
		total.Fift = max(total.Fift, l.Fift)
	}
	return map[string]any{"load1min": total.One, "load5min": total.Five, "load15min": total.Fift, "servers": servers}
}

type diskLine struct {
	Device     string  `json:"device"`
	Mountpoint string  `json:"mountpoint"`
	Fstype     string  `json:"fstype"`
	Used       float64 `json:"used"`
	Total      float64 `json:"total"`
	Percent    float64 `json:"percent"`
}

// combineDisk leads with the fullest disk, which is what the header shows, then each server's root disk for the tooltip
func combineDisk(pages []combinedPage) []diskLine {
	total := diskLine{Device: "Fullest server", Mountpoint: "/"}
	out := []diskLine{}
	for _, p := range pages {
		var disks []diskLine
		if json.Unmarshal(p.body, &disks) != nil {
			continue
		}
		for _, d := range disks {
			if d.Mountpoint == "/" {
				d.Device = p.name
				out = append(out, d)
				if d.Percent > total.Percent {
					total.Percent, total.Used, total.Total = d.Percent, d.Used, d.Total
				}
			}
		}
	}
	return append([]diskLine{total}, out...)
}

func combineDeliverability(pages []combinedPage) map[string]any {
	domains := []map[string]any{}
	for _, p := range pages {
		var data struct {
			Domains []map[string]any `json:"domains"`
		}
		if json.Unmarshal(p.body, &data) != nil {
			continue
		}
		for _, d := range data.Domains {
			d["server"] = p.name
			domains = append(domains, d)
		}
	}
	return map[string]any{"domains": domains}
}

const userSearchPrefix = "/search/users/"

// handleUserSearch is the user picker on forms, a username can exist on several servers so each result says which one it is
func (m *Manager) handleUserSearch(w http.ResponseWriter, r *http.Request, u *admindb.User, next http.Handler) {
	out := []string{}
	for _, p := range m.allPages(r, u, next, false) {
		var names []string
		if json.Unmarshal(p.body, &names) != nil {
			continue
		}
		for _, name := range names {
			out = append(out, name+" ("+p.name+")")
		}
	}
	sort.Strings(out)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// splitUserLabel undoes what the user picker shows, the server name has to be a real member so a username with brackets stays as it is
func (m *Manager) splitUserLabel(label string) (name, id string, ok bool) {
	open := strings.LastIndex(label, " (")
	if open < 0 || !strings.HasSuffix(label, ")") {
		return "", "", false
	}
	name, server := label[:open], label[open+2:len(label)-1]
	if server == m.displayName() {
		return name, LocalNode, true
	}
	for _, n := range m.LiveNodes() {
		if n.Name == server {
			return name, n.ID, true
		}
	}
	return "", "", false
}

// forms where the user was picked from every server, the request goes to that user's server
var userRoutedPaths = map[string]bool{"/domains/add": true, "/websites/scan": true}

// routeUserPost returns false when the form isn't using a labelled user, the request is then handled as usual
func (m *Manager) routeUserPost(w http.ResponseWriter, r *http.Request, activeID string, u *admindb.User, next http.Handler, run func(http.ResponseWriter, *http.Request)) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, applyMaxBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	q, err := url.ParseQuery(string(body))
	if err != nil {
		return false
	}
	name, id, ok := m.splitUserLabel(q.Get("username"))
	if !ok {
		return false
	}
	q.Set("username", name)
	q.Del("cluster_server")
	clean := []byte(q.Encode())
	r.Body = io.NopCloser(bytes.NewReader(clean))
	r.ContentLength = int64(len(clean))
	r.Header.Set("Content-Length", strconv.Itoa(len(clean)))
	if id == activeID {
		run(w, r)
		return true
	}
	if ou, err := url.Parse(r.Header.Get("Origin")); err != nil || ou.Host != r.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return true
	}
	if id == LocalNode {
		tok := m.localToken(r, next)
		if tok == "" {
			http.Error(w, "could not reach that server", http.StatusBadGateway)
			return true
		}
		r.Header.Set("X-CSRFToken", tok)
		r.Header.Del("Accept-Encoding")
		next.ServeHTTP(w, r)
		return true
	}
	n, known := m.liveNodeByID(id)
	if !known {
		http.Error(w, "unknown server", http.StatusNotFound)
		return true
	}
	probeCtx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	tok := csrfMetaRe.FindSubmatch(m.fetchNodePage(probeCtx, n, tokenProbePath, u, clientIPOf(r)))
	if tok == nil {
		http.Error(w, "could not reach that server", http.StatusBadGateway)
		return true
	}
	r.Header.Set("X-CSRFToken", html.UnescapeString(string(tok[1])))
	m.proxy(w, r, n, u, next)
	m.reportNodeFlash(r, n, u)
	return true
}
