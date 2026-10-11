package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestOpenSSHPeers(t *testing.T) {
	out := `0      0      161.97.157.83:22     109.245.165.249:46484 users:(("sshd",pid=3632389,fd=4))
0      0      161.97.157.83:22     109.245.165.249:34200 users:(("sshd",pid=4130311,fd=4),("sshd",pid=4130310,fd=4))
0      0      161.97.157.83:22     109.245.165.249:34200 users:(("sshd",pid=4130312,fd=4))
0      0      [::ffff:161.97.157.83]:2222 [::ffff:203.0.113.9]:5000 users:(("sshd",pid=1,fd=4))
0      0      161.97.157.83:443    198.51.100.7:5000 users:(("caddy",pid=2,fd=9))`
	got := openSSHPeers(out)
	if len(got) != 2 || got[0] != (sshPeer{"109.245.165.249", 2}) || got[1] != (sshPeer{"203.0.113.9", 1}) {
		t.Fatalf("openSSHPeers = %+v", got)
	}
}

func TestTrackWebTerminal(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws/terminal", nil)
	r.RemoteAddr = "203.0.113.4:5555"
	done := trackWebTerminal(r, "host")
	other := trackWebTerminal(r, "bob/php-fpm-8.4")
	if got := openWebTerminals("host"); len(got) != 1 || got[0].IP != "203.0.113.4" {
		t.Fatalf("openWebTerminals(host) = %+v", got)
	}
	done()
	other()
	if got := openWebTerminals("host"); len(got) != 0 {
		t.Fatalf("expected the session removed once closed, got %+v", got)
	}
}
