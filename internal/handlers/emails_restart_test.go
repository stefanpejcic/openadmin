package handlers

import (
	"strings"
	"testing"
)

func TestEmailsRestartMailserverScriptRecreatesWithRoundcube(t *testing.T) {
	s := emailsRestartMailserverScript
	if !strings.Contains(s, "podman-compose down roundcube mailserver") {
		t.Fatalf("roundcube has to go down with the mailserver, got %q", s)
	}
	if !strings.Contains(s, "podman-compose up -d mailserver") || !strings.Contains(s, `if [ -n "$rc" ]; then podman-compose up -d roundcube; fi`) {
		t.Fatalf("mailserver comes back and roundcube only if it was running, got %q", s)
	}
}
