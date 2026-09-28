// Package activity keeps a per-account log of the changes each administrator and reseller makes in OpenAdmin
package activity

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Dir is a var so tests can point it at a temp dir
var Dir = "/var/log/openpanel/admin/activity"

const (
	maxSize  = 2_000_000
	keepSize = 1_000_000
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Entry is one line of an activity log
type Entry struct {
	Time   string
	IP     string
	Action string
}

var mu sync.Mutex

// Path returns the log file for username, empty for names that aren't safe to use as a file name
func Path(username string) string {
	if !usernameRe.MatchString(username) || username == "." || username == ".." {
		return ""
	}
	return filepath.Join(Dir, username+".log")
}

// Record appends a line to the account's log and trims it once it grows past maxSize
func Record(username, ip, action string) error {
	path := Path(username)
	if path == "" || action == "" {
		return nil
	}
	if ip == "" {
		ip = "0.0.0.0"
	}
	// one entry per line, whatever the handler put in the description
	action = strings.Join(strings.Fields(action), " ")
	line := time.Now().Format("2006-01-02 15:04:05") + "  " + ip + "  " + action + "\n"

	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(line)
	f.Close()
	if err != nil {
		return err
	}
	trim(path)
	return nil
}

// trim keeps the newest keepSize bytes, cut at a line boundary
func trim(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxSize {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(-keepSize, io.SeekEnd); err != nil {
		return
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, tail, 0o600) == nil {
		os.Rename(tmp, path)
	}
}

func parse(line string) (Entry, bool) {
	parts := strings.SplitN(line, "  ", 3)
	if len(parts) != 3 {
		return Entry{}, false
	}
	return Entry{Time: parts[0], IP: parts[1], Action: parts[2]}, true
}

// Read returns every entry, newest first
func Read(username string) []Entry {
	path := Path(username)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if e, ok := parse(sc.Text()); ok {
			entries = append(entries, e)
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

// Last returns the newest entry without reading the whole file
func Last(username string) (Entry, bool) {
	path := Path(username)
	if path == "" {
		return Entry{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return Entry{}, false
	}
	size := info.Size()
	chunk := int64(8192)
	if chunk > size {
		chunk = size
	}
	buf := make([]byte, chunk)
	if _, err := f.ReadAt(buf, size-chunk); err != nil && err != io.EOF {
		return Entry{}, false
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	return parse(lines[len(lines)-1])
}

// request carries what the middleware needs back from deeper handlers, which often work on a copy of the request
type request struct {
	mu       sync.Mutex
	username string
	action   string
	skip     bool
	failed   bool
}

type ctxKey struct{}

// WithRequest attaches a fresh holder to ctx, the middleware reads it back after the handler ran
func WithRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, &request{})
}

func from(ctx context.Context) *request {
	h, _ := ctx.Value(ctxKey{}).(*request)
	return h
}

// SetActor records who made an API call, sessions are resolved by the middleware itself
func SetActor(ctx context.Context, username string) {
	if h := from(ctx); h != nil {
		h.mu.Lock()
		h.username = username
		h.mu.Unlock()
	}
}

// Describe overrides the route's default description, for handlers that know more (JSON bodies, bulk actions)
func Describe(ctx context.Context, action string) {
	if h := from(ctx); h != nil {
		h.mu.Lock()
		h.action = action
		h.mu.Unlock()
	}
}

// Skip keeps this request out of the log, for POSTs that don't change anything
func Skip(ctx context.Context) {
	if h := from(ctx); h != nil {
		h.mu.Lock()
		h.skip = true
		h.mu.Unlock()
	}
}

// Fail marks the action as failed, for handlers that report errors with a flash and a redirect instead of a status code
func Fail(ctx context.Context) {
	if h := from(ctx); h != nil {
		h.mu.Lock()
		h.failed = true
		h.mu.Unlock()
	}
}

// Result returns what handlers set on ctx
func Result(ctx context.Context) (username, action string, skip, failed bool) {
	h := from(ctx)
	if h == nil {
		return "", "", false, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.username, h.action, h.skip, h.failed
}
