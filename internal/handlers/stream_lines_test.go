package handlers

import (
	"strings"
	"testing"
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
