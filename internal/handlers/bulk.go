package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// BulkRequest is what bulk-actions.js posts: the action key, the optional input value and the selected row keys
type BulkRequest struct {
	Action string   `json:"action"`
	Field  string   `json:"field"`
	Value  string   `json:"value"`
	Items  []string `json:"items"`
}

type BulkResult struct {
	Item    string `json:"item"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// BulkCall is one in-process request to an existing route, JSON is sent as the body when set, otherwise Form
type BulkCall struct {
	Label  string // shown in the summary instead of the item key
	Method string
	Path   string
	Form   url.Values
	Query  url.Values
	JSON   any
}

// BulkRoute maps one selected item to the existing route that handles it, or returns nil and the item's result when nothing needs to run
type BulkRoute func(action, value, item string) (*BulkCall, BulkResult)

// BulkSkip is a BulkRoute result for an item that can't run the action
func BulkSkip(msg string) (*BulkCall, BulkResult) { return nil, BulkResult{Message: msg} }

// BulkDo is a BulkRoute result that replays call
func BulkDo(call BulkCall) (*BulkCall, BulkResult) { return &call, BulkResult{} }

// ServeBulkDispatch is a complete POST /<page>/bulk handler: validate the action, then replay each item through its single-item route on h
func ServeBulkDispatch(sessions *auth.Manager, h http.Handler, w http.ResponseWriter, r *http.Request, actions []webtemplates.BulkAction, route BulkRoute) {
	serveBulkDispatchOrdered(sessions, h, w, r, actions, route, nil)
}

// serveBulkDispatchOrdered lets order rearrange the items first, for routes where running one item changes the keys of the others
func serveBulkDispatchOrdered(sessions *auth.Manager, h http.Handler, w http.ResponseWriter, r *http.Request, actions []webtemplates.BulkAction, route BulkRoute, order func(action string, items []string)) {
	req, ok := decodeBulkRequest(w, r)
	if !ok {
		return
	}
	if order != nil {
		order(req.Action, req.Items)
	}
	act, found := findBulkAction(actions, req.Action)
	if !found {
		bulkError(w, "Unknown bulk action.")
		return
	}
	in, prefix := act.Input, ""
	if in != nil && len(in.Fields) > 0 {
		f, ok := in.Field(req.Field)
		if !ok {
			bulkError(w, "Pick what to change.")
			return
		}
		// routes get "field=value"
		in, prefix = &f.Input, f.Key+"="
	}
	// passwords keep their spaces
	if in == nil || in.Type != "password" {
		req.Value = strings.TrimSpace(req.Value)
	}
	if in != nil {
		if req.Value == "" {
			bulkError(w, "A value is required for this action.")
			return
		}
		if msg := validateBulkValue(in, req.Value); msg != "" {
			bulkError(w, msg)
			return
		}
		req.Value = prefix + req.Value
	}
	results := make([]BulkResult, 0, len(req.Items))
	for _, item := range req.Items {
		call, res := route(req.Action, req.Value, item)
		if call != nil {
			res = bulkDispatch(sessions, h, r, *call)
			res.Item = call.Label
		}
		if res.Item == "" {
			res.Item = item
		}
		results = append(results, res)
	}
	finishBulk(sessions, w, r, act.Label, results)
}

// validateBulkValue checks the value against the same limits the browser was given, once, before any row runs
func validateBulkValue(in *webtemplates.BulkInput, v string) string {
	switch in.Type {
	case "select":
		for _, o := range in.Options {
			if o.Value == v {
				return ""
			}
		}
		return "Pick one of the listed options."
	case "number":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "Enter a number."
		}
		if in.Step == "1" && n != math.Trunc(n) {
			return "Enter a whole number."
		}
		if min, err := strconv.ParseFloat(in.Min, 64); err == nil && n < min {
			return "The value can't be less than " + in.Min + "."
		}
		if max, err := strconv.ParseFloat(in.Max, 64); err == nil && n > max {
			return "The value can't be more than " + in.Max + "."
		}
	case "email":
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			return "Enter a valid email address."
		}
	}
	if in.Pattern != "" {
		if re, err := regexp.Compile("^(?:" + in.Pattern + ")$"); err == nil && !re.MatchString(v) {
			if in.Hint != "" {
				return "Invalid value, " + in.Hint + "."
			}
			return "Invalid value."
		}
	}
	return ""
}

func decodeBulkRequest(w http.ResponseWriter, r *http.Request) (BulkRequest, bool) {
	var req BulkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bulkError(w, "Invalid request body.")
		return req, false
	}
	if len(req.Items) == 0 {
		bulkError(w, "Nothing selected.")
		return req, false
	}
	return req, true
}

// bulkError rejects the whole request, the bulk bar shows the message as a toast
func bulkError(w http.ResponseWriter, msg string) {
	writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func findBulkAction(actions []webtemplates.BulkAction, key string) (webtemplates.BulkAction, bool) {
	for _, act := range actions {
		if act.Key == key {
			return act, true
		}
	}
	return webtemplates.BulkAction{}, false
}

// finishBulk flashes one summary banner for the page reload and returns the per-item results as JSON
func finishBulk(sessions *auth.Manager, w http.ResponseWriter, r *http.Request, actionLabel string, results []BulkResult) {
	_ = auth.AddFlash(w, r, sessions, bulkFlashMessage(actionLabel, results), bulkFlashCategory(results))
	writeJSON(w, map[string]any{"results": results})
}

func bulkFlashCategory(results []BulkResult) string {
	failed := 0
	for _, res := range results {
		if !res.OK {
			failed++
		}
	}
	switch {
	case failed == 0:
		return "success"
	case failed == len(results):
		return "danger"
	default:
		return "warning"
	}
}

// bulkFlashMessage is plain text, the layout escapes flashes itself
func bulkFlashMessage(actionLabel string, results []BulkResult) string {
	var failed []BulkResult
	for _, res := range results {
		if !res.OK {
			failed = append(failed, res)
		}
	}
	total := strconv.Itoa(len(results))
	if len(failed) == 0 {
		return actionLabel + ": completed successfully for all " + total + " selected item(s). " + bulkItemList(results)
	}
	msg := actionLabel + ": " + strconv.Itoa(len(failed)) + " of " + total + " selected item(s) failed."
	for _, res := range failed {
		msg += " " + res.Item + " (" + res.Message + ")."
	}
	return msg
}

// bulkItemList names the items in a success summary, capped so a huge selection stays readable
func bulkItemList(results []BulkResult) string {
	const maxNames = 20
	names := make([]string, 0, maxNames)
	for i, res := range results {
		if i == maxNames {
			names = append(names, "+"+strconv.Itoa(len(results)-maxNames))
			break
		}
		names = append(names, res.Item)
	}
	return strings.Join(names, ", ") + "."
}

// bulkReplayHeader marks a replayed request, for handlers that render the page (and pop the flash) right after a POST instead of redirecting
const bulkReplayHeader = "X-OpenAdmin-Bulk"

// withoutSessionCache hides gorilla/sessions' per-request session cache, so a replayed handler loads its own copy of the session instead of adding its flashes to the real request's one
type withoutSessionCache struct{ context.Context }

func (c withoutSessionCache) Value(key any) any {
	if t := reflect.TypeOf(key); t != nil && t.PkgPath() == "github.com/gorilla/sessions" {
		return nil
	}
	return c.Context.Value(key)
}

// newBulkRequest builds the replayed request with the current request's session and client details
func newBulkRequest(r *http.Request, call BulkCall) *http.Request {
	target := call.Path
	if len(call.Query) > 0 {
		target += "?" + call.Query.Encode()
	}
	var body io.Reader = strings.NewReader("")
	contentType := ""
	switch {
	case call.JSON != nil:
		b, _ := json.Marshal(call.JSON)
		body, contentType = bytes.NewReader(b), "application/json"
	case len(call.Form) > 0:
		body, contentType = strings.NewReader(call.Form.Encode()), "application/x-www-form-urlencoded"
	}

	req := httptest.NewRequest(call.Method, target, body)
	req = req.WithContext(withoutSessionCache{r.Context()})
	req.RemoteAddr = r.RemoteAddr
	req.Host = r.Host
	for _, k := range []string{"Cookie", "X-Forwarded-For", "X-Real-IP", "User-Agent", "Referer"} {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set(bulkReplayHeader, "1")
	return req
}

// bulkGetJSON reads a page's ?output=json data in-process, as the current user
func bulkGetJSON(h http.Handler, r *http.Request, path string, out any) error {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newBulkRequest(r, BulkCall{Method: http.MethodGet, Path: path}))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("%s: %s", path, http.StatusText(rec.Code))
	}
	// json.Number keeps 1000000 from turning into 1e+06 when posted back
	dec := json.NewDecoder(rec.Body)
	dec.UseNumber()
	return dec.Decode(out)
}

// bulkDispatch runs call against h with the current request's session, so a bulk action goes through the same handler, checks and messages as the single-item button
func bulkDispatch(sessions *auth.Manager, h http.Handler, r *http.Request, call BulkCall) BulkResult {
	oldFlashes := 0
	for _, c := range r.Cookies() {
		if c.Name == auth.SessionCookieName {
			oldFlashes = len(sessions.FlashesInCookie(c))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newBulkRequest(r, call))
	return bulkDispatchResult(sessions, rec, oldFlashes)
}

var bulkTagRE = regexp.MustCompile(`<[^>]*>`)

// bulkDispatchResult reads the outcome the way a browser would see it: the flash the handler set, its JSON reply, or the status code, skipping the flashes that were already in the user's cookie
func bulkDispatchResult(sessions *auth.Manager, rec *httptest.ResponseRecorder, oldFlashes int) BulkResult {
	if strings.Contains(rec.Header().Get("Location"), "/login") {
		return BulkResult{Message: "Not authenticated."}
	}

	// redirect handlers report errors only in the flash, the status is a plain 303
	var flashes []auth.Flash
	for _, c := range rec.Result().Cookies() {
		if c.Name != auth.SessionCookieName {
			continue
		}
		if got := sessions.FlashesInCookie(c); len(got) > oldFlashes {
			flashes = append(flashes, got[oldFlashes:]...)
		}
	}
	var warning string
	hasSuccess := false
	for _, f := range flashes {
		switch f.Category {
		case "error", "danger":
			return BulkResult{Message: cleanBulkMessage(f.Message)}
		case "warning":
			warning = f.Message
		case "success", "info":
			hasSuccess = true
		}
	}
	if warning != "" && !hasSuccess {
		return BulkResult{Message: cleanBulkMessage(warning)}
	}

	raw := strings.TrimSpace(rec.Body.String())
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) == nil {
		if e, _ := obj["error"].(string); e != "" {
			return BulkResult{Message: cleanBulkMessage(e)}
		}
		if ok, isBool := obj["success"].(bool); isBool && !ok {
			return BulkResult{Message: cleanBulkMessage(fmt.Sprint(obj["message"]))}
		}
	}
	if rec.Code >= 400 {
		msg := raw
		if obj != nil {
			msg = fmt.Sprint(obj["message"])
		}
		if msg == "" || msg == "<nil>" {
			msg = http.StatusText(rec.Code)
		}
		return BulkResult{Message: cleanBulkMessage(msg)}
	}

	msg := "Done."
	if len(flashes) > 0 {
		msg = cleanBulkMessage(flashes[len(flashes)-1].Message)
	} else if m, _ := obj["message"].(string); m != "" {
		msg = cleanBulkMessage(m)
	}
	return BulkResult{OK: true, Message: msg}
}

// cleanBulkMessage turns handler output into one short plain-text line
func cleanBulkMessage(s string) string {
	s = strings.TrimSpace(html.UnescapeString(bulkTagRE.ReplaceAllString(s, " ")))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
