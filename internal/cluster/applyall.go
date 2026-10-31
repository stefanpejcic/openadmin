package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"openadmin/internal/admindb"
)

const (
	applyCookie   = "cluster_apply_all"
	applyMaxBody  = 16 << 20
	replayTimeout = 2 * time.Minute
)

var (
	// general holds this server's own domain, ports and ssl, and the test buttons would only send test mail again
	applyPaths = []string{
		"/settings/api", "/settings/caddy", "/settings/custom-code", "/settings/defaults", "/settings/locales", "/settings/modules",
		"/settings/notifications", "/settings/open-panel", "/settings/php", "/settings/updates",
		"/services/limits", "/services/ftp/settings",
		"/services/edit", "/emails/settings", "/backups/user/configuration",
		"/server/crons",
		"/domains/file-templates", "/domains/vhost-templates", "/domains/zone-templates", "/domains/ratelimit",
		"/server/timezone", "/server/root-password", "/server/ssh", "/server/ssh/config",
		"/features", "/plans", "/plans/new", "/api/docker-tags", "/settings/updates/update_now", "/security/blacklist-useragents",
	}
	applyPrefixes = []string{"/features/", "/plan/delete/"}
	applyDenied   = []string{"/settings/notifications/test-", "/settings/updates/log", "/settings/defaults/files"}
	planEditRe    = regexp.MustCompile(`^/plans/(\d+)$`)
	// these describe one machine, and crons post by line number which can differ per server, so sending them everywhere is a choice and not the default
	perServerDefault = map[string]bool{"/server/timezone": true, "/server/root-password": true, "/server/ssh": true, "/server/crons": true}
)

func applyPath(path string) bool {
	for _, p := range applyDenied {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	for _, p := range applyPaths {
		if path == p {
			return true
		}
	}
	for _, p := range applyPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return planEditRe.MatchString(path)
}

// whole-list actions on a page that shows every server's rows, 9999 is how the page says "all of them"
var alwaysApplyPaths = map[string]bool{
	"/notifications/mark_as_read/9999": true, "/notifications/delete/9999": true, "/notifications/pause": true, "/notifications/resume": true,
}

// wafFleetActions are the waf page's actions on the rule sets every server shares, turning the waf on or off stays per server
var wafFleetActions = map[string]bool{"update": true, "check_updates": true, "redownload_profile": true, "install_profiles": true}

// goesEverywhere is whether a post skips the apply box, it looks at the form for pages where only some actions are shared
func goesEverywhere(w http.ResponseWriter, r *http.Request) bool {
	if alwaysApplyPaths[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/security/waf/rules") {
		return true
	}
	if r.URL.Path != "/security/waf" {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, applyMaxBody))
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	q, err := url.ParseQuery(string(body))
	return err == nil && wafFleetActions[q.Get("action")]
}

// applyPage is where the "apply on all servers" box shows, the plan pages are included since ids get mapped by name
func applyPage(path string) bool {
	return (applyPath(path) || path == "/backups/user") && path != "/plans" && path != "/api/docker-tags" && path != "/settings/updates/update_now" && !strings.HasPrefix(path, "/plan/delete/")
}

// applyToggle is on by default from the master, a member picked on purpose is the one being edited
func applyToggle(on bool) string {
	checked := ""
	if on {
		checked = " checked"
	}
	return `<label class="inline-flex items-center gap-1.5 text-xs font-medium text-gray-600 dark:text-gray-400 cursor-pointer whitespace-nowrap"><input type="checkbox" class="cluster-apply-all"` + checked + `> Apply on all servers</label>`
}

type statusWriter struct {
	http.ResponseWriter
	status   int
	location string
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
		s.location = s.Header().Get("Location")
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func responseOK(status int, location string) bool {
	return status > 0 && status < 400 && !strings.HasPrefix(location, "/login")
}

// ApplyResult is what happened on one server, shown once on the admin's next page
type ApplyResult struct {
	Server string `json:"server"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	// what the server itself said, for actions that only went to it
	Note string `json:"note,omitempty"`
}

type applyState struct {
	pending int
	results []ApplyResult
}

var applyStates = struct {
	sync.Mutex
	byUser map[int64]*applyState
}{byUser: map[int64]*applyState{}}

func applyBegin(userID int64, n int) {
	applyStates.Lock()
	defer applyStates.Unlock()
	st := applyStates.byUser[userID]
	if st == nil {
		st = &applyState{}
		applyStates.byUser[userID] = st
	}
	st.pending += n
}

func applyDone(userID int64, r ApplyResult) {
	applyStates.Lock()
	defer applyStates.Unlock()
	st := applyStates.byUser[userID]
	st.pending--
	st.results = append(st.results, r)
}

// applyTake hands over finished results, the page asks again while some are still running
func applyTake(userID int64) ([]ApplyResult, bool) {
	applyStates.Lock()
	defer applyStates.Unlock()
	st := applyStates.byUser[userID]
	if st == nil {
		return nil, false
	}
	out := st.results
	st.results = nil
	return out, st.pending > 0
}

// planID finds a plan by name, ids are different on every server
func (m *Manager) planNameByID(id string) (string, error) {
	var name string
	err := m.MySQL.QueryRow(`SELECT name FROM plans WHERE id = ?`, id).Scan(&name)
	return name, err
}

func (m *Manager) planIDByName(name string) (string, error) {
	var id int64
	if err := m.MySQL.QueryRow(`SELECT id FROM plans WHERE name = ?`, name).Scan(&id); err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// withoutUpsell drops the field that points at another plan's id, those don't carry over between servers
func withoutUpsell(body []byte, contentType string) []byte {
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		return body
	}
	q, err := url.ParseQuery(string(body))
	if err != nil {
		return body
	}
	q.Del("upsell_plan_id")
	return []byte(q.Encode())
}

type replayRequest struct {
	// gets the answer of a server that took the request
	onBody func([]byte)

	path, query, contentType, accept, requested string
	body                                        []byte
	planName                                    string
	ip                                          string
}

func (rr replayRequest) pathOn(m *Manager, id string) (string, []byte, error) {
	if rr.planName == "" {
		return rr.path, rr.body, nil
	}
	var planID string
	if err := m.call(id, "plan_id", rr.planName, &planID); err != nil {
		return "", nil, fmt.Errorf("plan %s doesn't exist there", rr.planName)
	}
	return "/plans/" + planID, withoutUpsell(rr.body, rr.contentType), nil
}

// replayOn sends the admin's change to one more server with a csrf token of that server, gorilla looks at the header before the form field
func (m *Manager) replayOn(n Node, u *admindb.User, rr replayRequest) error {
	path, body, err := rr.pathOn(m, n.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), replayTimeout)
	defer cancel()
	page := m.fetchNodePage(ctx, n, tokenProbePath, u, rr.ip)
	tok := csrfMetaRe.FindSubmatch(page)
	if tok == nil {
		return errors.New("could not open a session there")
	}
	target := n.URL + path
	if rr.query != "" {
		target += "?" + rr.query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	s, err := m.nodeLogin(n, u, rr.ip)
	if err != nil {
		return err
	}
	m.sessMu.Lock()
	for _, c := range s.cookies {
		req.AddCookie(c)
	}
	m.sessMu.Unlock()
	req.Header.Set("X-CSRFToken", html.UnescapeString(string(tok[1])))
	req.Header.Set("Origin", n.URL)
	req.Header.Set("Referer", n.URL+path)
	req.Header.Set("X-Forwarded-For", rr.ip)
	if rr.contentType != "" {
		req.Header.Set("Content-Type", rr.contentType)
	}
	if rr.accept != "" {
		req.Header.Set("Accept", rr.accept)
	}
	if rr.requested != "" {
		req.Header.Set("X-Requested-With", rr.requested)
	}
	client := &http.Client{Transport: m.client(n).Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !responseOK(resp.StatusCode, resp.Header.Get("Location")) {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxRPCBody))
	if rr.onBody != nil {
		rr.onBody(answer)
	}
	return answerError(answer)
}

// answerError catches a refusal that came with a 200, pages that post with fetch report problems in the body
func answerError(body []byte) error {
	var parsed struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	if parsed.Error != "" {
		return errors.New(parsed.Error)
	}
	if parsed.Success != nil && !*parsed.Success {
		if parsed.Message != "" {
			return errors.New(parsed.Message)
		}
		return errors.New("the server refused the change")
	}
	return nil
}

// localToken is the master's own csrf token for this admin, needed when the change started on another server
func (m *Manager) localToken(r *http.Request, next http.Handler) string {
	probe := r.Clone(context.Background())
	probe.Method, probe.Body = http.MethodGet, http.NoBody
	probe.URL = &url.URL{Path: tokenProbePath}
	probe.RequestURI = tokenProbePath
	probe.Header.Del("Accept-Encoding")
	probe.Header.Del("Content-Type")
	probe.Header.Del("Content-Length")
	probe.ContentLength = 0
	tok := csrfMetaRe.FindSubmatch(m.fetchLocalPage(probe, next))
	if tok == nil {
		return ""
	}
	return html.UnescapeString(string(tok[1]))
}

func (m *Manager) replayLocal(r *http.Request, rr replayRequest, next http.Handler) error {
	path, body, err := rr.pathOn(m, LocalNode)
	if err != nil {
		return err
	}
	tok := m.localToken(r, next)
	if tok == "" {
		return errors.New("could not get a token on the master")
	}
	out := r.Clone(context.Background())
	out.URL = &url.URL{Path: path, RawQuery: rr.query}
	out.RequestURI = out.URL.RequestURI()
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.Header.Set("X-CSRFToken", tok)
	out.Header.Del("Accept-Encoding")
	rec := &pageRecorder{header: http.Header{}, status: http.StatusOK}
	next.ServeHTTP(rec, out)
	if !responseOK(rec.status, rec.header.Get("Location")) {
		return fmt.Errorf("answered %d", rec.status)
	}
	if rr.onBody != nil {
		rr.onBody(rec.body.Bytes())
	}
	return answerError(rec.body.Bytes())
}

// runWithApplyAll lets the server the admin is on go first, csrf and validation included, the others only get the change if that worked
func (m *Manager) runWithApplyAll(w http.ResponseWriter, r *http.Request, activeID string, u *admindb.User, next http.Handler, run func(http.ResponseWriter, *http.Request)) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, applyMaxBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	rr := replayRequest{
		path: r.URL.Path, query: r.URL.RawQuery, contentType: r.Header.Get("Content-Type"), accept: r.Header.Get("Accept"),
		requested: r.Header.Get("X-Requested-With"), body: body, ip: clientIPOf(r),
	}
	if match := planEditRe.FindStringSubmatch(r.URL.Path); match != nil {
		var name string
		if err := m.call(activeID, "plan_name", match[1], &name); err != nil {
			http.Error(w, "could not look up that plan", http.StatusBadGateway)
			return
		}
		rr.planName = name
	}
	// only the server default on this page is shared, a limit on one domain belongs to the server that has the domain
	if r.URL.Path == "/domains/ratelimit" {
		if q, err := url.ParseQuery(string(body)); err != nil || q.Get("action") != "default" {
			run(w, r)
			return
		}
	}
	sw := &statusWriter{ResponseWriter: w}
	run(sw, r)
	if !responseOK(sw.status, sw.location) {
		return
	}
	// every server in the cluster except the one that already has the change
	nodes := m.LiveNodes()
	applyBegin(u.ID, len(nodes))
	go func() {
		var wg sync.WaitGroup
		report := func(name string, err error) {
			res := ApplyResult{Server: name, OK: err == nil}
			if err != nil {
				res.Error = err.Error()
			}
			applyDone(u.ID, res)
		}
		if activeID != LocalNode {
			wg.Add(1)
			go func() {
				defer wg.Done()
				report(m.displayName(), m.replayLocal(r, rr, next))
			}()
		}
		for _, n := range nodes {
			if n.ID == activeID {
				continue
			}
			wg.Add(1)
			go func(n Node) {
				defer wg.Done()
				report(n.Name, m.replayOn(n, u, rr))
			}(n)
		}
		wg.Wait()
	}()
}

const bulkItemPrefix = "cluster:"

// bulkTargets splits a selection by server, rows of other servers carry theirs in the key
func bulkTargets(items []string, activeID string) (map[string][]string, bool) {
	out := map[string][]string{}
	remote := false
	for _, it := range items {
		if rest, ok := strings.CutPrefix(it, bulkItemPrefix); ok {
			if id, key, ok := strings.Cut(rest, ":"); ok {
				out[id] = append(out[id], key)
				remote = true
				continue
			}
		}
		out[activeID] = append(out[activeID], it)
	}
	return out, remote
}

type bulkBody struct {
	Action string   `json:"action"`
	Field  string   `json:"field"`
	Value  string   `json:"value"`
	Items  []string `json:"items"`
}

// handleClusterBulk runs a selection with rows from several servers, each server does its own part through its normal bulk route
func (m *Manager) handleClusterBulk(w http.ResponseWriter, r *http.Request, raw []byte, req bulkBody, targets map[string][]string, u *admindb.User, next http.Handler) {
	if ou, err := url.Parse(r.Header.Get("Origin")); r.Header.Get("X-CSRFToken") == "" || err != nil || ou.Host != r.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	applyBegin(u.ID, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := []map[string]any{}
	for id, items := range targets {
		wg.Add(1)
		go func(id string, items []string) {
			defer wg.Done()
			part := req
			part.Items = items
			body, err := json.Marshal(part)
			name := m.serverName(id)
			failed := 0
			if err == nil {
				rr := replayRequest{path: r.URL.Path, contentType: "application/json", accept: "application/json", body: body, ip: clientIPOf(r), onBody: func(answer []byte) {
					var parsed struct {
						Results []map[string]any `json:"results"`
					}
					if json.Unmarshal(answer, &parsed) != nil {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					for _, res := range parsed.Results {
						if ok, _ := res["ok"].(bool); !ok {
							failed++
						}
						res["server"] = name
						results = append(results, res)
					}
				}}
				if id == LocalNode {
					err = m.replayLocal(r, rr, next)
				} else if n, known := m.liveNodeByID(id); known {
					err = m.replayOn(n, u, rr)
				} else {
					err = errors.New("unknown server")
				}
			}
			res := ApplyResult{Server: name, OK: err == nil && failed == 0}
			switch {
			case err != nil:
				res.Error = err.Error()
			case failed > 0:
				res.Error = fmt.Sprintf("%d of %d failed", failed, len(items))
			}
			applyDone(u.ID, res)
		}(id, items)
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// handleWebsiteScan runs the scan on the server picked on the form, or on every server, and goes back to the list
func (m *Manager) handleWebsiteScan(w http.ResponseWriter, r *http.Request, activeID string, u *admindb.User, next http.Handler, run func(http.ResponseWriter, *http.Request)) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, applyMaxBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	q, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	target := q.Get("cluster_server")
	rr := replayRequest{path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: body, ip: clientIPOf(r)}
	if target == "" || target == activeID {
		sw := &statusWriter{ResponseWriter: w}
		run(sw, r)
		if target != "" || !responseOK(sw.status, sw.location) {
			return
		}
	}
	// the active server already ran above when scanning everywhere, the master's csrf check on it guards the rest
	if target != "" && target != activeID {
		if ou, err := url.Parse(r.Header.Get("Origin")); err != nil || ou.Host != r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	ids := []string{}
	if target != "" {
		ids = append(ids, target)
	} else {
		if activeID != LocalNode {
			ids = append(ids, LocalNode)
		}
		for _, n := range m.LiveNodes() {
			if n.ID != activeID {
				ids = append(ids, n.ID)
			}
		}
	}
	applyBegin(u.ID, len(ids))
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var err error
			if id == LocalNode {
				err = m.replayLocal(r, rr, next)
			} else if n, ok := m.liveNodeByID(id); ok {
				err = m.replayOn(n, u, rr)
			} else {
				err = errors.New("unknown server")
			}
			res := ApplyResult{Server: m.serverName(id), OK: err == nil}
			if err != nil {
				res.Error = err.Error()
			}
			applyDone(u.ID, res)
		}(id)
	}
	if target != "" && target != activeID {
		wg.Wait()
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
	}
}

var (
	flashRe     = regexp.MustCompile(`(?s)<span class="sr-only">Notification</span>(.*?)<button`)
	tagRe2      = regexp.MustCompile(`<[^>]+>`)
	failWordsRe = regexp.MustCompile(`(?i)\b(fail|failed|error|must|invalid|cannot|can't|not)\b`)
)

// flashOf pulls a server's message out of a page it rendered
func flashOf(page []byte) string {
	found := flashRe.FindSubmatch(page)
	if found == nil {
		return ""
	}
	return strings.Join(strings.Fields(html.UnescapeString(tagRe2.ReplaceAllString(string(found[1]), ""))), " ")
}

// reportNodeFlash shows the admin what a server answered to a form that went straight to it, its message would otherwise wait in that server's session
func (m *Manager) reportNodeFlash(r *http.Request, n Node, u *admindb.User) {
	ip := clientIPOf(r)
	s, err := m.nodeLogin(n, u, ip)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), combinedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.URL+tokenProbePath, nil)
	if err != nil {
		return
	}
	m.sessMu.Lock()
	for _, c := range s.cookies {
		req.AddCookie(c)
	}
	m.sessMu.Unlock()
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := m.client(n).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	m.absorbCookies(resp, s)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBody))
	if err != nil {
		return
	}
	note := flashOf(body)
	if note == "" {
		return
	}
	applyBegin(u.ID, 1)
	applyDone(u.ID, ApplyResult{Server: n.Name, OK: !failWordsRe.MatchString(note), Note: note})
}
