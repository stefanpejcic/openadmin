package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestOpenSSHPeers(t *testing.T) {
	// captured on Debian 13 (OpenSSH 10.0), plus OpenSSH 9.x style "sshd: " titles on the :2222 lines
	out := `0      0      192.0.2.10:22    203.0.113.5:57786 users:(("sshd-session",pid=55,fd=7),("sshd-session",pid=44,fd=7))
0      0      192.0.2.10:22    203.0.113.5:57782 users:(("sshd-session",pid=56,fd=7),("sshd-session",pid=41,fd=7))
0      0      192.0.2.10:22    203.0.113.5:57794 users:(("sshd-session",pid=66,fd=7),("sshd-session",pid=59,fd=7))
0      0      192.0.2.10:52862 198.51.100.80:8080 users:(("sshd-session",pid=66,fd=8))
0      0      192.0.2.10:22    203.0.113.5:53240 users:(("sshd-session",pid=91,fd=7),("sshd-session",pid=84,fd=7))
0      0      192.0.2.10:22    198.51.100.23:57802 users:(("sshd-session",pid=69,fd=7))
0      0      192.0.2.10:22    198.51.100.23:57798 users:(("sshd-session",pid=67,fd=7))
0      0      192.0.2.10:22    198.51.100.23:53238 users:(("sshd-session",pid=82,fd=7))
0      0      [::ffff:192.0.2.10]:2222 [::ffff:203.0.113.9]:5000 users:(("sshd",pid=101,fd=4))
0      0      [::ffff:192.0.2.10]:2222 [::ffff:203.0.113.9]:5000 users:(("sshd",pid=102,fd=4))
0      0      [::ffff:192.0.2.10]:2222 [::ffff:198.51.100.23]:5001 users:(("sshd",pid=103,fd=4),("sshd",pid=104,fd=4))
0      0      192.0.2.10:443   198.51.100.7:5000 users:(("caddy",pid=2,fd=9))`
	titles := map[string]string{
		"44":  "sshd-session: root [priv]",
		"55":  "sshd-session: root@pts/0",
		"41":  "sshd-session: root [priv]",
		"56":  "sshd-session: root@notty",
		"59":  "sshd-session: root [priv]",
		"66":  "sshd-session: root",
		"84":  "sshd-session: root [priv]",
		"91":  "sshd-session: root",
		"69":  "sshd-session: [accepted]",
		"67":  "sshd-session: unknown [priv]",
		"82":  "sshd-session: root [priv]",
		"101": "sshd: admin [priv]",
		"102": "sshd: admin@pts/1",
		"103": "sshd: root [priv]",
		"104": "sshd: root [net]",
		"2":   "caddy run",
	}
	orig := sshProcTitle
	sshProcTitle = func(pid string) string { return titles[pid] }
	defer func() { sshProcTitle = orig }()

	got := openSSHPeers(out)
	if len(got) != 2 || got[0] != (sshPeer{"203.0.113.5", 4}) || got[1] != (sshPeer{"203.0.113.9", 1}) {
		t.Fatalf("openSSHPeers = %+v, want only logged-in sessions", got)
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
