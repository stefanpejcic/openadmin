package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCSFTCPPortOpen(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "csf.conf")
	if !csfTCPPortOpen("2087", conf) {
		t.Fatal("expected open when there's no CSF config")
	}
	os.WriteFile(conf, []byte("TCP_OUT = \"2087\"\nTCP_IN = \"22,80,443,30000:31000\"\n"), 0644)
	cases := map[string]bool{"2087": false, "443": true, "30500": true, "31001": false, "abc": false}
	for port, want := range cases {
		if got := csfTCPPortOpen(port, conf); got != want {
			t.Fatalf("csfTCPPortOpen(%s) = %v, want %v", port, got, want)
		}
	}
}
