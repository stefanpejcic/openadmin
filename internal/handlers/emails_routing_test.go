package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/config"
)

func TestEmailFilterCount(t *testing.T) {
	mail := t.TempDir()
	home := filepath.Join(mail, "info", "home")
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, ".dovecot.sieve"), []byte("require [\"fileinto\"];\n# spam\nif header :contains \"subject\" \"x\" {\n  fileinto \"Junk\";\n}\nelsif true {\n stop;\n}\n"), 0o644)
	if n := emailFilterCount(mail, "info@x.com"); n != 2 {
		t.Fatalf("want 2, got %d", n)
	}
	os.MkdirAll(filepath.Join(mail, "fwd", "home"), 0o755)
	os.WriteFile(filepath.Join(mail, "fwd", "home", ".dovecot.sieve"), []byte("redirect \"a@b.com\";\n"), 0o644)
	if n := emailFilterCount(mail, "fwd@x.com"); n != 1 {
		t.Fatalf("plain redirect want 1, got %d", n)
	}
	for _, a := range []string{"none@x.com", "../info@x.com", ".info@x.com", "a/b@x.com"} {
		if n := emailFilterCount(mail, a); n != 0 {
			t.Fatalf("%s want 0, got %d", a, n)
		}
	}
	if d := emailMailDir("/var/mail", nil, "x.com"); d != "/var/mail/x.com" {
		t.Fatal(d)
	}
	if d := emailMailDir("user_dir", map[string]string{"x.com": "stefan"}, "x.com"); d != "/home/stefan/mail/x.com" {
		t.Fatal(d)
	}
}

func TestNormalizeEmailQuota(t *testing.T) {
	for in, want := range map[string]string{"0": "0", "2G": "2G", "2g": "2G", "512m": "512M", "100K": "100k", " 1T ": "1T", "10B": "10B"} {
		if got, ok := normalizeEmailQuota(in); !ok || got != want {
			t.Fatalf("%q: got %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "2", "1.5G", "2GB", "-1G", "G", "00", "2 G"} {
		if _, ok := normalizeEmailQuota(in); ok {
			t.Fatalf("%q should be rejected", in)
		}
	}
}

func TestEmailRestricted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "postfix-send-access.cf")
	os.WriteFile(p, []byte("# x\nInfo@X.com REJECT\n\nb@y.com REJECT\n"), 0o644)
	got := emailRestricted(p)
	if !got["info@x.com"] || !got["b@y.com"] || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if len(emailRestricted(p+".missing")) != 0 {
		t.Fatal("missing file should be empty")
	}
}

// emailRoutingFixture points the account file and admin.ini at a temp dir with info@x.com, mail stored under <dir>/mail
func emailRoutingFixture(t *testing.T) (mail string) {
	dir := t.TempDir()
	mail = filepath.Join(dir, "mail")
	origAcc, origCfg := EmailsAccountsFile, config.AdminConfigPath
	t.Cleanup(func() { EmailsAccountsFile, config.AdminConfigPath = origAcc, origCfg })
	EmailsAccountsFile = filepath.Join(dir, "postfix-accounts.cf")
	config.AdminConfigPath = filepath.Join(dir, "admin.ini")
	os.WriteFile(EmailsAccountsFile, []byte("info@x.com|{SHA512-CRYPT}x\n"), 0o644)
	os.WriteFile(config.AdminConfigPath, []byte("[EMAIL]\nemail_storage_location="+mail+"\n"), 0o644)
	return mail
}

func TestServeFilterReadAndSave(t *testing.T) {
	mail := emailRoutingFixture(t)
	e := &Emails{}

	w := httptest.NewRecorder()
	e.ServeFilter(w, httptest.NewRequest("GET", "/emails/api/filter?email=info@x.com", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"filters":[]`) {
		t.Fatalf("empty read: %d %s", w.Code, w.Body)
	}

	script := "require [\"fileinto\"];\n\n# spam\nif header :contains \"Subject\" \"x\" {\n  fileinto \"Junk\";\n}\n"
	body, _ := json.Marshal(map[string]string{"email": "info@x.com", "content": script})
	w = httptest.NewRecorder()
	e.ServeFilter(w, httptest.NewRequest("POST", "/emails/api/filter", bytes.NewReader(body)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"count":1`) || !strings.Contains(w.Body.String(), `"name":"spam"`) {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if got, _ := os.ReadFile(filepath.Join(mail, "x.com", "info", "home", ".dovecot.sieve")); string(got) != script {
		t.Fatalf("file: %q", got)
	}

	for _, q := range []string{"nobody@x.com", "../x@x.com"} {
		w = httptest.NewRecorder()
		e.ServeFilter(w, httptest.NewRequest("GET", "/emails/api/filter?email="+q, nil))
		if w.Code == 200 {
			t.Fatalf("%s should be refused", q)
		}
	}
}

func TestSieveRuleCount(t *testing.T) {
	for in, want := range map[string]int{"": 0, "# No filters defined.\n": 0, "require [\"fileinto\"];\n": 0, "redirect \"a@b.c\";\n": 1, "if true { stop; }\nelsif false { stop; }\n": 2} {
		if got := sieveRuleCount([]byte(in)); got != want {
			t.Fatalf("%q: got %d want %d", in, got, want)
		}
	}
}
