package cluster

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/server"
)

const (
	sessionKeyNode = "cluster_node"
	LocalNode      = "local"
	wsNodeParam    = "cluster_node"
	maxHTMLBody    = 64 << 20
	sessionIdleTTL = 12 * time.Hour
)

//go:embed switcher.js
var switcherJS []byte

// these cookies belong to one server each, the browser only ever holds the master's copy
var perNodeCookies = map[string]bool{"OPENADMIN": true, "_gorilla_csrf": true}

type nodeSession struct {
	cookies  map[string]*http.Cookie
	created  time.Time
	lastUsed time.Time
}

func (m *Manager) sessionUser(r *http.Request) *admindb.User {
	sess, err := m.Sessions.Get(r)
	if err != nil {
		return nil
	}
	id, ok := sess.Values["user_id"].(int64)
	if !ok {
		return nil
	}
	u, err := m.DB.UserByID(id)
	if err != nil || !u.IsActive {
		return nil
	}
	return u
}

// activeNode is the server this admin is looking at, falling back to local if it was removed
func (m *Manager) activeNode(r *http.Request) (Node, bool) {
	sess, err := m.Sessions.Get(r)
	if err != nil {
		return Node{}, false
	}
	id, _ := sess.Values[sessionKeyNode].(string)
	if id == "" || id == LocalNode {
		return Node{}, false
	}
	n, ok := m.nodeByID(id)
	if !ok || n.Paused {
		return Node{}, false
	}
	return n, true
}

// neverProxied stays on the master no matter which server is active
func neverProxied(path string) bool {
	if strings.HasPrefix(path, "/login/token/") {
		return false
	}
	// the dashboard layout is the admin's own, kept on the master so every server's dashboard looks the same
	return path == "/login" || strings.HasPrefix(path, "/login/") || path == "/logout" || strings.HasPrefix(path, "/_cluster/") || path == "/dashboard/layout"
}

var savedLayoutRe = regexp.MustCompile(`var saved = [^\n]*;\n`)

// Middleware serves /_cluster/* and sends everything else to the active server
func (m *Manager) Middleware(next http.Handler) http.Handler {
	internal := m.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, nodeForwardPrefix) {
			m.handleNodeForward(w, r, next)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_cluster/") {
			internal.ServeHTTP(w, r)
			return
		}
		if !m.IsMaster() {
			next.ServeHTTP(w, r)
			return
		}
		u := m.sessionUser(r)
		if u == nil || u.Role == "reseller" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/logout" {
			m.dropUserSessions(u.ID)
		}
		node, proxied := m.activeNode(r)
		if neverProxied(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// the inner chain handles the ip mismatch logout, the proxy path would skip it
		if !m.sessionIPOK(r) {
			next.ServeHTTP(w, r)
			return
		}
		// a terminal in a stale tab must not reconnect to whichever server is active now
		if r.Header.Get("Upgrade") != "" {
			want := LocalNode
			if proxied {
				want = node.ID
			}
			if r.URL.Query().Get(wsNodeParam) != want {
				http.Error(w, "This page belongs to another server, reload it.", http.StatusConflict)
				return
			}
			q := r.URL.Query()
			q.Del(wsNodeParam)
			r.URL.RawQuery = q.Encode()
		}
		if mergeable(r) {
			r = takeServerSort(r)
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bulk") {
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, applyMaxBody))
			if err != nil {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var req bulkBody
			activeID := LocalNode
			if proxied {
				activeID = node.ID
			}
			if json.Unmarshal(raw, &req) == nil {
				if targets, remote := bulkTargets(req.Items, activeID); remote {
					m.handleClusterBulk(w, r, raw, req, targets, u, next)
					return
				}
			}
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, userSearchPrefix) {
			m.handleUserSearch(w, r, u, next)
			return
		}
		if r.Method == http.MethodPost && userRoutedPaths[r.URL.Path] {
			activeID := LocalNode
			if proxied {
				activeID = node.ID
			}
			if m.routeUserPost(w, r, activeID, u, next, func(rw http.ResponseWriter, req *http.Request) {
				if proxied {
					m.proxy(rw, req, node, u, next)
				} else {
					next.ServeHTTP(rw, req)
				}
			}) {
				return
			}
		}
		if r.Method == http.MethodPost && r.URL.Path == "/websites/scan" {
			activeID := LocalNode
			if proxied {
				activeID = node.ID
			}
			m.handleWebsiteScan(w, r, activeID, u, next, func(rw http.ResponseWriter, req *http.Request) {
				if proxied {
					m.proxy(rw, req, node, u, next)
				} else {
					next.ServeHTTP(rw, req)
				}
			})
			return
		}
		if r.Method == http.MethodPost {
			if c, err := r.Cookie(applyCookie); goesEverywhere(w, r) || (applyPath(r.URL.Path) && err == nil && c.Value == "1") {
				activeID := LocalNode
				if proxied {
					activeID = node.ID
				}
				m.runWithApplyAll(w, r, activeID, u, next, func(rw http.ResponseWriter, req *http.Request) {
					if proxied {
						m.proxy(rw, req, node, u, next)
					} else {
						next.ServeHTTP(rw, req)
					}
				})
				return
			}
		}
		// a dashboard opened on one member shows that member, only the master's own adds them all up
		if !proxied && r.Method == http.MethodGet && r.URL.Query().Get("cluster") == "all" {
			m.serveCombined(w, r, u, next)
			return
		}
		if !proxied {
			if !mergeable(r) {
				next.ServeHTTP(w, r)
				return
			}
			// some handlers compress their own html, this layer has to read it and the gzip layer outside compresses the result anyway
			r = r.Clone(r.Context())
			r.Header.Del("Accept-Encoding")
			cw := &captureWriter{ResponseWriter: w}
			next.ServeHTTP(cw, r)
			if cw.capture {
				out := m.mergePage(cw.body.Bytes(), r, LocalNode, m.displayName(), u, next)
				w.Header().Del("Content-Length")
				w.WriteHeader(cw.status)
				w.Write(out)
			}
			return
		}
		m.proxy(w, r, node, u, next)
	})
}

func (m *Manager) sessionIPOK(r *http.Request) bool {
	if !m.ValidateSessionIP {
		return true
	}
	sess, err := m.Sessions.Get(r)
	if err != nil {
		return false
	}
	ip, _ := sess.Values["user_ip"].(string)
	return ip == "" || ip == server.GetClientIP(r)
}

func sessionKey(userID int64, nodeID, ip string) string {
	return fmt.Sprintf("%d|%s|%s", userID, nodeID, ip)
}

func (m *Manager) dropUserSessions(userID int64) {
	prefix := fmt.Sprintf("%d|", userID)
	m.sessMu.Lock()
	defer m.sessMu.Unlock()
	for k := range m.sessions {
		if strings.HasPrefix(k, prefix) {
			delete(m.sessions, k)
		}
	}
}

func (m *Manager) dropNodeSessions(nodeID string) {
	m.sessMu.Lock()
	defer m.sessMu.Unlock()
	for k := range m.sessions {
		if strings.Split(k, "|")[1] == nodeID {
			delete(m.sessions, k)
		}
	}
}

// nodeLogin returns the slave session for this admin, logging in with a signed claim when there's none yet
func (m *Manager) nodeLogin(n Node, u *admindb.User, ip string) (*nodeSession, error) {
	key := sessionKey(u.ID, n.ID, ip)
	now := time.Now()
	m.sessMu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]*nodeSession{}
	}
	for k, s := range m.sessions {
		if now.Sub(s.lastUsed) > sessionIdleTTL {
			delete(m.sessions, k)
		}
	}
	if s, ok := m.sessions[key]; ok {
		s.lastUsed = now
		m.sessMu.Unlock()
		return s, nil
	}
	m.sessMu.Unlock()

	body, err := m.sign(Claims{Aud: n.ID, Action: "sso", Sub: u.Username, Role: u.Role, IP: ip})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL+"/_cluster/sso", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client(n).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s refused the login: %s", n.Name, strings.TrimSpace(string(msg)))
	}
	s := &nodeSession{cookies: map[string]*http.Cookie{}, created: now, lastUsed: now}
	for _, c := range resp.Cookies() {
		if perNodeCookies[c.Name] {
			s.cookies[c.Name] = c
		}
	}
	m.sessMu.Lock()
	m.sessions[key] = s
	m.sessMu.Unlock()
	return s, nil
}

func clientIPOf(r *http.Request) string { return server.GetClientIP(r) }

func (m *Manager) proxy(w http.ResponseWriter, r *http.Request, n Node, u *admindb.User, next http.Handler) {
	clientIP := server.GetClientIP(r)
	s, err := m.nodeLogin(n, u, clientIP)
	if err != nil {
		m.renderDown(w, n, err)
		return
	}
	target, err := url.Parse(n.URL)
	if err != nil {
		m.renderDown(w, n, err)
		return
	}
	key := sessionKey(u.ID, n.ID, clientIP)
	origURI := r.URL.RequestURI()
	isGet := r.Method == http.MethodGet

	p := &httputil.ReverseProxy{
		Transport:     m.client(n).Transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			h := pr.Out.Header
			// we buffer html to add the switcher, and the outer gzip layer compresses for the browser anyway
			h.Del("Accept-Encoding")
			h.Del("CF-Connecting-IP")
			h.Del("X-Real-IP")
			h.Del("X-Forwarded-Host")
			h.Del("X-Forwarded-Proto")
			h.Set("X-Forwarded-For", clientIP)
			if h.Get("Origin") != "" {
				h.Set("Origin", n.URL)
			}
			if ref, err := url.Parse(h.Get("Referer")); err == nil && ref.Host != "" {
				ref.Scheme, ref.Host = target.Scheme, target.Host
				h.Set("Referer", ref.String())
			}
			var kept []string
			for _, c := range pr.In.Cookies() {
				if !perNodeCookies[c.Name] {
					kept = append(kept, c.Name+"="+c.Value)
				}
			}
			m.sessMu.Lock()
			for _, c := range s.cookies {
				kept = append(kept, c.Name+"="+c.Value)
			}
			m.sessMu.Unlock()
			h.Del("Cookie")
			if len(kept) > 0 {
				h.Set("Cookie", strings.Join(kept, "; "))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			m.absorbCookies(resp, s)
			if loc := resp.Header.Get("Location"); loc != "" {
				if lu, err := url.Parse(loc); err == nil && lu.Host == target.Host {
					lu.Scheme, lu.Host = "", ""
					loc = lu.RequestURI()
					resp.Header.Set("Location", loc)
				}
				// slave session is gone, log in again unless we literally just did
				if strings.HasPrefix(loc, "/login") && !strings.HasPrefix(loc, "/login/token") {
					if time.Since(s.created) < 10*time.Second {
						return fmt.Errorf("%s keeps rejecting the cluster login", n.Name)
					}
					m.sessMu.Lock()
					delete(m.sessions, key)
					m.sessMu.Unlock()
					if isGet {
						resp.Header.Set("Location", origURI)
					} else {
						resp.Header.Set("Location", "/dashboard")
					}
				}
			}
			return m.finishProxiedPage(resp, r, n, u, next)
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			m.renderDown(rw, n, err)
		},
	}
	p.ServeHTTP(w, r)
}

func (m *Manager) absorbCookies(resp *http.Response, s *nodeSession) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	m.sessMu.Lock()
	defer m.sessMu.Unlock()
	for _, c := range cookies {
		if !perNodeCookies[c.Name] {
			resp.Header.Add("Set-Cookie", c.String())
			continue
		}
		if c.MaxAge < 0 {
			delete(s.cookies, c.Name)
			continue
		}
		s.cookies[c.Name] = c
	}
}

func switcherTag(nodeID string) string {
	return `<script src="/_cluster/switcher.js?node=` + url.QueryEscape(nodeID) + `"></script>`
}

// SwitcherTag is for the master's own pages, proxied ones get theirs injected
func (m *Manager) SwitcherTag() string {
	if !m.IsMaster() {
		return ""
	}
	return switcherTag(LocalNode)
}

func (m *Manager) finishProxiedPage(resp *http.Response, r *http.Request, n Node, u *admindb.User, next http.Handler) error {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBody))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK && mergeable(r) {
		body = m.mergePage(body, r, n.ID, n.Name, u, next)
	}
	if m.DashboardLayout != nil && (r.URL.Path == "/dashboard" || r.URL.Path == "/") {
		layout := m.DashboardLayout(u.ID)
		body = savedLayoutRe.ReplaceAllLiteral(body, []byte("var saved = "+layout+";\n"))
	}
	nodeID := n.ID
	if i := bytes.Index(body, []byte("<head>")); i >= 0 {
		var out bytes.Buffer
		out.Write(body[:i+6])
		out.WriteString(switcherTag(nodeID))
		out.Write(body[i+6:])
		body = out.Bytes()
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("ETag")
	return nil
}

// renderDown keeps the admin able to switch away from a dead server
func (m *Manager) renderDown(w http.ResponseWriter, n Node, cause error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	reason := "unknown error"
	if cause != nil && !errors.Is(cause, context.Canceled) {
		reason = cause.Error()
	}
	fmt.Fprintf(w, `<!doctype html><html><head>%s<meta charset="utf-8"><title>%s unavailable</title></head>
<body style="font-family:system-ui,sans-serif;padding:4rem 2rem;text-align:center"><h1 style="font-size:1.4rem">%s is not reachable</h1>
<p style="color:#6b7280">%s</p><p style="color:#6b7280">Other servers are not affected, use the menu above to switch to another one.</p></body></html>`,
		switcherTag(n.ID), html.EscapeString(n.Name), html.EscapeString(n.Name), html.EscapeString(reason))
}
