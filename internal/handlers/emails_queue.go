package handlers

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"openadmin/internal/podman"
)

// queueRecipient is one recipient of a queued message with postfix's reason for not delivering it yet
type queueRecipient struct {
	Address string
	Reason  string
	Summary string
}

// queueRow is a postqueue -j message flattened for the table, the json output keeps postfix's own shape
type queueRow struct {
	ID         string
	Queue      string
	Sender     string
	Recipients []queueRecipient
	Size       int64
	Arrival    int64
	Reason     string
	Summary    string
}

func queueRowsFrom(queue []map[string]interface{}) []queueRow {
	rows := make([]queueRow, 0, len(queue))
	for _, msg := range queue {
		r := queueRow{}
		r.ID, _ = msg["queue_id"].(string)
		r.Queue, _ = msg["queue_name"].(string)
		r.Sender, _ = msg["sender"].(string)
		if f, ok := msg["message_size"].(float64); ok {
			r.Size = int64(f)
		}
		if f, ok := msg["arrival_time"].(float64); ok {
			r.Arrival = int64(f)
		}
		if list, ok := msg["recipients"].([]interface{}); ok {
			for _, item := range list {
				m, _ := item.(map[string]interface{})
				addr, _ := m["address"].(string)
				reason, _ := m["delay_reason"].(string)
				r.Recipients = append(r.Recipients, queueRecipient{Address: addr, Reason: reason, Summary: queueReasonSummary(reason)})
				if r.Reason == "" && reason != "" {
					r.Reason, r.Summary = reason, queueReasonSummary(reason)
				}
			}
		}
		rows = append(rows, r)
	}
	return rows
}

var (
	queueInReplyRe = regexp.MustCompile(`\s*\(in reply to [^)]*\)\s*$`)
	queueSpacesRe  = regexp.MustCompile(`\s+`)
	queueConnectRe = regexp.MustCompile(`^connect to ([^\[\s:]+)[^:]*(?::\d+)?: (.+)$`)
)

// queueReasonSummary keeps the part an admin acts on: what the other server said, or why it couldn't be reached
func queueReasonSummary(reason string) string {
	s := queueSpacesRe.ReplaceAllString(strings.TrimSpace(reason), " ")
	if s == "" {
		return ""
	}
	s = queueInReplyRe.ReplaceAllString(s, "")
	if host, said, ok := strings.Cut(s, " said: "); ok {
		host = strings.TrimPrefix(host, "host ")
		if i := strings.IndexAny(host, "[ "); i > 0 {
			host = host[:i]
		}
		s = said + " (" + host + ")"
	} else if m := queueConnectRe.FindStringSubmatch(s); m != nil {
		s = m[2] + " (" + m[1] + ")"
	}
	if utf8.RuneCountInString(s) > 110 {
		s = string([]rune(s)[:110]) + "…"
	}
	return s
}

var queueSortKeys = map[string]bool{"id": true, "queue": true, "from": true, "to": true, "size": true, "arrival": true, "reason": true}

// sortQueueRows keeps postqueue's order when no column is picked
func sortQueueRows(rows []queueRow, col, direction string) {
	firstTo := func(r queueRow) string {
		if len(r.Recipients) == 0 {
			return ""
		}
		return strings.ToLower(r.Recipients[0].Address)
	}
	less := map[string]func(a, b queueRow) bool{
		"id":      func(a, b queueRow) bool { return a.ID < b.ID },
		"queue":   func(a, b queueRow) bool { return a.Queue < b.Queue },
		"from":    func(a, b queueRow) bool { return strings.ToLower(a.Sender) < strings.ToLower(b.Sender) },
		"to":      func(a, b queueRow) bool { return firstTo(a) < firstTo(b) },
		"size":    func(a, b queueRow) bool { return a.Size < b.Size },
		"arrival": func(a, b queueRow) bool { return a.Arrival < b.Arrival },
		"reason":  func(a, b queueRow) bool { return strings.ToLower(a.Summary) < strings.ToLower(b.Summary) },
	}[col]
	if less != nil {
		sortBy(rows, direction, less)
	}
}

var queueIDRe = regexp.MustCompile(`^[0-9A-Za-z]{5,20}$`)

// queueMessageHeaders is the message's headers from postcat, capped so a huge header block can't flood the page
var queueMessageHeaders = func(id string) string {
	if !queueIDRe.MatchString(id) {
		return ""
	}
	cmd, err := podman.Command("default", "exec", emailsMailserverContainerName, "postcat", "-h", "-q", id)
	if err != nil {
		return ""
	}
	out, _ := cmd.Output()
	const max = 32 << 10
	if len(out) > max {
		out = append(out[:max], []byte("\n…")...)
	}
	return strings.TrimSpace(string(out))
}

// queueCounts is how many messages sit in each queue, for the summary above the table
func queueCounts(rows []queueRow) []struct {
	Name  string
	Count int
} {
	n := map[string]int{}
	for _, r := range rows {
		n[r.Queue]++
	}
	out := make([]struct {
		Name  string
		Count int
	}, 0, len(n))
	for name, c := range n {
		out = append(out, struct {
			Name  string
			Count int
		}{name, c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// postqueue -j message, only the fields the page shows
type postqueueMsg struct {
	QueueName   string `json:"queue_name"`
	QueueID     string `json:"queue_id"`
	ArrivalTime int64  `json:"arrival_time"`
	MessageSize int64  `json:"message_size"`
	Sender      string `json:"sender"`
	Recipients  []struct {
		Address     string `json:"address"`
		DelayReason string `json:"delay_reason"`
	} `json:"recipients"`
}

// streamEmailQueueRows decodes postqueue -j as it arrives, a big queue is 100+ MB of json with the same few delay reasons over and over, so those are kept once
func streamEmailQueueRows() []queueRow {
	cmd, err := podman.Command("default", "exec", emailsMailserverContainerName, "postqueue", "-j")
	if err != nil {
		return nil
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if cmd.Start() != nil {
		return nil
	}
	defer cmd.Wait()
	seen := map[string]string{}
	intern := func(v string) string {
		if v == "" {
			return ""
		}
		if s, ok := seen[v]; ok {
			return s
		}
		seen[v] = v
		return v
	}
	summaries := map[string]string{}
	summary := func(reason string) string {
		if s, ok := summaries[reason]; ok {
			return s
		}
		s := intern(queueReasonSummary(reason))
		summaries[reason] = s
		return s
	}
	var rows []queueRow
	dec := json.NewDecoder(out)
	for {
		var m postqueueMsg
		if err := dec.Decode(&m); err != nil {
			break
		}
		r := queueRow{ID: m.QueueID, Queue: intern(m.QueueName), Sender: intern(m.Sender), Size: m.MessageSize, Arrival: m.ArrivalTime}
		r.Recipients = make([]queueRecipient, 0, len(m.Recipients))
		for _, rc := range m.Recipients {
			reason := intern(rc.DelayReason)
			r.Recipients = append(r.Recipients, queueRecipient{Address: rc.Address, Reason: reason, Summary: summary(reason)})
			if r.Reason == "" && reason != "" {
				r.Reason, r.Summary = reason, summary(reason)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// loadEmailQueueRows is a var so tests can hand the page a queue
var loadEmailQueueRows = streamEmailQueueRows

// postqueue -j takes seconds on a big queue, so paging, sorting and searching reuse one read for a short while
const emailQueueCacheTTL = 30 * time.Second

var emailQueueCache struct {
	mu   sync.Mutex
	rows []queueRow
	at   time.Time
}

func cachedEmailQueueRows() []queueRow {
	emailQueueCache.mu.Lock()
	defer emailQueueCache.mu.Unlock()
	if emailQueueCache.at.IsZero() || time.Since(emailQueueCache.at) > emailQueueCacheTTL {
		emailQueueCache.rows = loadEmailQueueRows()
		emailQueueCache.at = time.Now()
	}
	return emailQueueCache.rows
}

// invalidateEmailQueueCache makes the next page load read the queue again, after a retry or delete
func invalidateEmailQueueCache() {
	emailQueueCache.mu.Lock()
	emailQueueCache.at = time.Time{}
	emailQueueCache.rows = nil
	emailQueueCache.mu.Unlock()
}

// queueRowVisible is queueMessageVisible for a parsed row
func (s *resellerScope) queueRowVisible(r queueRow) bool {
	if s == nil {
		return true
	}
	if r.Sender != "" && s.AllowsEmail(r.Sender) {
		return true
	}
	if len(r.Recipients) == 0 {
		return false
	}
	for _, rc := range r.Recipients {
		if !s.AllowsEmail(rc.Address) {
			return false
		}
	}
	return true
}

// queueRowMatches is the search box: id, sender, reason or any recipient
func queueRowMatches(r queueRow, q string) bool {
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(r.ID+" "+r.Sender+" "+r.Summary), q) {
		return true
	}
	for _, rc := range r.Recipients {
		if strings.Contains(strings.ToLower(rc.Address), q) {
			return true
		}
	}
	return false
}

const emailQueuePageSize = 100
