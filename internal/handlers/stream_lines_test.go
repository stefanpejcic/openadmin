package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCommandScannerSplitsCarriageReturnsAndLongLines(t *testing.T) {
	progress := strings.Repeat("Copying blob 12% \r", 10000) // ~180KB with no newline, like podman pull
	in := "Configuring rootless Podman\n" + progress + "\nuser created successfully\n"
	s := newCommandScanner(strings.NewReader(in))
	var last string
	for s.Scan() {
		if line := strings.TrimSpace(s.Text()); line != "" {
			last = line
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if last != "user created successfully" {
		t.Fatalf("last line %q", last)
	}
}

func TestLongStreamOutlivesTheServerWriteTimeout(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		longStream(w, time.Minute)
		fmt.Fprintln(w, "start")
		w.(http.Flusher).Flush()
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprintln(w, "Successfully added user")
	})
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = time.Second
	srv.Start()
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Successfully added user") {
		t.Fatalf("stream was cut: %q", body)
	}
}
