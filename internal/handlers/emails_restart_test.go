package handlers

import (
	"strings"
	"testing"
)

func TestEmailsRestartMailserverScriptRecreatesWithWebmail(t *testing.T) {
	s := emailsRestartMailserverScript
	if !strings.Contains(s, "podman-compose down roundcube golub mailserver") {
		t.Fatalf("the webmail has to go down with the mailserver, got %q", s)
	}
	for _, want := range []string{
		"podman-compose up -d mailserver",
		`if [ -n "$rc" ]; then podman-compose up -d roundcube; fi`,
		`if [ -n "$gl" ]; then podman-compose up -d golub; fi`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q: mailserver comes back and each webmail only if it was running, got %q", want, s)
		}
	}
}

func TestCheckWebmailStatusFindsGolub(t *testing.T) {
	orig, origFile := emailsPodmanPsRun, EmailsMailComposeFile
	t.Cleanup(func() { emailsPodmanPsRun, EmailsMailComposeFile = orig, origFile })
	EmailsMailComposeFile = t.TempDir()
	for out, want := range map[string]string{
		"openadmin_mailserver\nopenadmin_golub\n":     "golub",
		"openadmin_mailserver\nopenadmin_roundcube\n": "roundcube",
		"openadmin_mailserver\ngolub-bench-roundcube": "",
	} {
		emailsPodmanPsRun = func(...string) (string, error) { return out, nil }
		_, running := checkWebmailStatus()
		got := ""
		if len(running) > 0 {
			got = running[0]
		}
		if got != want {
			t.Errorf("%q: %v", out, running)
		}
	}
}
