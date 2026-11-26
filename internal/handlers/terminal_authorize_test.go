package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestTerminalAuthorizeAndGate(t *testing.T) {
	term := &Terminal{}
	srv, client := newTerminalTestServer(t, term, "admin")

	post := func(pw string) *http.Response {
		resp, err := client.PostForm(srv.URL+"/terminal/authorize", url.Values{"password": {pw}})
		if err != nil {
			t.Fatalf("authorize post: %v", err)
		}
		return resp
	}

	// wrong password is refused
	if r := post("nope"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", r.StatusCode)
	}

	// the right password (newTerminalTestServer uses "pw") returns a token
	r := post("pw")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("right password: got %d, want 200", r.StatusCode)
	}
	var body struct{ Token string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		t.Fatalf("no token in response: %v (%+v)", err, body)
	}
	r.Body.Close()

	// the websocket is refused without a valid token
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	conn, resp, err := (&websocket.Dialer{Jar: client.Jar}).Dial(wsURL, nil)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("ws without token should be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ws without token: want 403, got %v", resp)
	}
}
