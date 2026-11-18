package handlers

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"openadmin/internal/paneldb"
)

// where caddy writes the Coraza audit log and the access log of each domain
var (
	wafAuditDir   = "/var/log/caddy/coraza_waf"
	domainLogsDir = "/var/log/caddy/domlogs"
)

// summaryWAF is what the web firewall blocked that day
type summaryWAF struct {
	Blocked int
	Rules   []summaryWAFRule
	Domains []summaryWAFDomain
	IPs     []summaryWAFIP
}

type summaryWAFRule struct {
	ID, Msg string
	Count   int
}

type summaryWAFDomain struct {
	Name, Username, Link, Server string
	Count                        int
}

type summaryWAFIP struct {
	IP, Country string
	Count       int
	// how many websites it hit
	Domains int
}

// BlockedLabel and the CountLabels are the numbers with thousands separators for the page
func (w summaryWAF) BlockedLabel() string     { return countWord(w.Blocked, "request", "requests") }
func (r summaryWAFRule) CountLabel() string   { return commaNum(r.Count) }
func (d summaryWAFDomain) CountLabel() string { return commaNum(d.Count) }
func (ip summaryWAFIP) CountLabel() string    { return commaNum(ip.Count) }

// Favicon is the website's icon, same source as the websites table
func (d summaryWAFDomain) Favicon() string { return faviconBase() + d.Name }

var (
	wafRuleRe = regexp.MustCompile(`\[id "(\d+)"\].*?\[msg "((?:[^"\\]|\\.)*)"\]`)
	// the anomaly score checks only say that the rules above them added up, they're not a reason of their own
	wafScoreRules = map[string]bool{"949110": true, "949111": true, "959100": true, "980130": true, "980140": true, "980170": true}
)

type wafBlock struct {
	domain, uri, edge string
	ts                time.Time
	rules             []summaryWAFRule
}

// summaryWAFFor reads the day's blocked requests from the Coraza audit logs, the real visitor IP and country come from the domain's access log since behind Cloudflare the audit log only has the edge
func summaryWAFFor(db *sql.DB, day string) *summaryWAF {
	from, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return nil
	}
	to := from.AddDate(0, 0, 1)
	files, _ := filepath.Glob(filepath.Join(wafAuditDir, "*.log*"))
	var blocks []wafBlock
	for _, f := range files {
		if info, err := os.Stat(f); err != nil || info.ModTime().Before(from) {
			continue
		}
		eachLogLine(f, func(raw []byte) {
			if !bytes.Contains(raw, []byte(`"is_interrupted":true`)) {
				return
			}
			var e struct {
				Transaction struct {
					UnixTimestamp int64  `json:"unix_timestamp"`
					ClientIP      string `json:"client_ip"`
					ServerID      string `json:"server_id"`
					Request       struct {
						URI string `json:"uri"`
					} `json:"request"`
					IsInterrupted bool `json:"is_interrupted"`
				} `json:"transaction"`
				Messages []struct {
					ErrorMessage string `json:"error_message"`
				} `json:"messages"`
			}
			if json.Unmarshal(raw, &e) != nil || !e.Transaction.IsInterrupted {
				return
			}
			ts := time.Unix(0, e.Transaction.UnixTimestamp)
			if ts.Before(from) || !ts.Before(to) {
				return
			}
			b := wafBlock{domain: e.Transaction.ServerID, uri: e.Transaction.Request.URI, edge: e.Transaction.ClientIP, ts: ts}
			if b.domain == "" {
				b.domain = strings.SplitN(filepath.Base(f), ".log", 2)[0]
			}
			seen := map[string]bool{}
			for _, m := range e.Messages {
				for _, r := range wafRuleRe.FindAllStringSubmatch(m.ErrorMessage, -1) {
					if wafScoreRules[r[1]] || seen[r[1]] {
						continue
					}
					seen[r[1]] = true
					b.rules = append(b.rules, summaryWAFRule{ID: r[1], Msg: strings.ReplaceAll(r[2], `\"`, `"`)})
				}
			}
			blocks = append(blocks, b)
		})
	}
	if len(blocks) == 0 {
		return nil
	}

	visitors := wafVisitors(blocks, from, to)
	owners := map[string]string{}
	if db != nil {
		if rows, err := paneldb.GetAllDomains(db); err == nil {
			for _, r := range rows {
				owners[fmt.Sprint(r["domain_url"])] = fmt.Sprint(r["username"])
			}
		}
	}
	rules := map[string]*summaryWAFRule{}
	domains := map[string]int{}
	type ipHit struct {
		country string
		count   int
		domains map[string]bool
	}
	ips := map[string]*ipHit{}
	for i, b := range blocks {
		for _, r := range b.rules {
			if rules[r.ID] == nil {
				rules[r.ID] = &summaryWAFRule{ID: r.ID, Msg: r.Msg}
			}
			rules[r.ID].Count++
		}
		domains[b.domain]++
		v := visitors[i]
		if v.ip == "" {
			v.ip = b.edge
		}
		h := ips[v.ip]
		if h == nil {
			h = &ipHit{domains: map[string]bool{}}
			ips[v.ip] = h
		}
		h.count++
		h.domains[b.domain] = true
		if v.country != "" {
			h.country = v.country
		}
	}
	out := &summaryWAF{Blocked: len(blocks)}
	for _, r := range rules {
		out.Rules = append(out.Rules, *r)
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		if out.Rules[i].Count != out.Rules[j].Count {
			return out.Rules[i].Count > out.Rules[j].Count
		}
		return out.Rules[i].ID < out.Rules[j].ID
	})
	for name, n := range domains {
		d := summaryWAFDomain{Name: name, Count: n, Username: owners[name]}
		if d.Username != "" {
			d.Link = "/users/" + d.Username
		}
		out.Domains = append(out.Domains, d)
	}
	sort.Slice(out.Domains, func(i, j int) bool {
		if out.Domains[i].Count != out.Domains[j].Count {
			return out.Domains[i].Count > out.Domains[j].Count
		}
		return out.Domains[i].Name < out.Domains[j].Name
	})
	for ip, h := range ips {
		out.IPs = append(out.IPs, summaryWAFIP{IP: ip, Country: h.country, Count: h.count, Domains: len(h.domains)})
	}
	sort.Slice(out.IPs, func(i, j int) bool {
		if out.IPs[i].Count != out.IPs[j].Count {
			return out.IPs[i].Count > out.IPs[j].Count
		}
		return out.IPs[i].IP < out.IPs[j].IP
	})
	out.Rules, out.Domains, out.IPs = firstN(out.Rules, 5), firstN(out.Domains, 5), firstN(out.IPs, 5)
	return out
}

func firstN[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type wafVisitor struct{ ip, country string }

// wafVisitors finds each block's 403 in the access log of its domain, same uri and edge ip within 2 seconds
func wafVisitors(blocks []wafBlock, from, to time.Time) []wafVisitor {
	type hit struct {
		ts      float64
		edge    string
		visitor wafVisitor
		used    bool
	}
	byKey := map[string][]*hit{}
	wanted := map[string]bool{}
	for _, b := range blocks {
		wanted[b.domain] = true
	}
	for domain := range wanted {
		files, _ := filepath.Glob(filepath.Join(domainLogsDir, domain, "access*.log*"))
		for _, f := range files {
			if info, err := os.Stat(f); err != nil || info.ModTime().Before(from) {
				continue
			}
			eachLogLine(f, func(raw []byte) {
				if !bytes.Contains(raw, []byte(`"status":403`)) {
					return
				}
				var e struct {
					TS      float64 `json:"ts"`
					Request struct {
						RemoteIP string              `json:"remote_ip"`
						ClientIP string              `json:"client_ip"`
						Host     string              `json:"host"`
						URI      string              `json:"uri"`
						Headers  map[string][]string `json:"headers"`
					} `json:"request"`
					Status int `json:"status"`
				}
				if json.Unmarshal(raw, &e) != nil || e.Status != 403 {
					return
				}
				t := time.Unix(0, int64(e.TS*1e9))
				if t.Before(from.Add(-time.Minute)) || t.After(to.Add(time.Minute)) {
					return
				}
				v := wafVisitor{ip: e.Request.ClientIP}
				if h := e.Request.Headers["Cf-Connecting-Ip"]; len(h) > 0 {
					v.ip = h[0]
				}
				if h := e.Request.Headers["Cf-Ipcountry"]; len(h) > 0 {
					v.country = h[0]
				}
				key := domain + "|" + e.Request.URI
				byKey[key] = append(byKey[key], &hit{ts: e.TS, edge: e.Request.RemoteIP, visitor: v})
			})
		}
	}
	out := make([]wafVisitor, len(blocks))
	for i, b := range blocks {
		ts := float64(b.ts.UnixNano()) / 1e9
		var best *hit
		for _, h := range byKey[b.domain+"|"+b.uri] {
			if h.used || (h.edge != "" && b.edge != "" && h.edge != b.edge) {
				continue
			}
			if d := h.ts - ts; d > -2 && d < 2 && (best == nil || abs(d) < abs(best.ts-ts)) {
				best = h
			}
		}
		if best != nil {
			best.used = true
			out[i] = best.visitor
		}
	}
	return out
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// eachLogLine calls fn for every line of a plain or gzipped log
func eachLogLine(path string, fn func([]byte)) {
	rc, err := openBackupLog(path)
	if err != nil {
		return
	}
	defer rc.Close()
	sc := bufio.NewScanner(io.Reader(rc))
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		fn(sc.Bytes())
	}
}

// combinedWAF adds up the firewall of every server, the busiest rules, websites and addresses across all of them
func combinedWAF(days []summaryServerDay) *summaryWAF {
	out := &summaryWAF{}
	rules := map[string]*summaryWAFRule{}
	ips := map[string]*summaryWAFIP{}
	for _, sd := range days {
		w := sd.Day.WAF
		if w == nil {
			continue
		}
		out.Blocked += w.Blocked
		for _, r := range w.Rules {
			if rules[r.ID] == nil {
				rules[r.ID] = &summaryWAFRule{ID: r.ID, Msg: r.Msg}
			}
			rules[r.ID].Count += r.Count
		}
		for _, d := range w.Domains {
			if len(days) > 1 && d.Server == "" {
				d.Server = sd.Name
			}
			out.Domains = append(out.Domains, d)
		}
		for _, ip := range w.IPs {
			if ips[ip.IP] == nil {
				c := ip
				ips[ip.IP] = &c
				continue
			}
			ips[ip.IP].Count += ip.Count
			ips[ip.IP].Domains += ip.Domains
		}
	}
	if out.Blocked == 0 {
		return nil
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, *r)
	}
	for _, ip := range ips {
		out.IPs = append(out.IPs, *ip)
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		if out.Rules[i].Count != out.Rules[j].Count {
			return out.Rules[i].Count > out.Rules[j].Count
		}
		return out.Rules[i].ID < out.Rules[j].ID
	})
	sort.SliceStable(out.Domains, func(i, j int) bool { return out.Domains[i].Count > out.Domains[j].Count })
	sort.Slice(out.IPs, func(i, j int) bool {
		if out.IPs[i].Count != out.IPs[j].Count {
			return out.IPs[i].Count > out.IPs[j].Count
		}
		return out.IPs[i].IP < out.IPs[j].IP
	})
	out.Rules, out.Domains, out.IPs = firstN(out.Rules, 5), firstN(out.Domains, 5), firstN(out.IPs, 5)
	return out
}
