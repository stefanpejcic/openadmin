package handlers

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// a fake opencli on PATH that writes its arguments, one call per line
func fakeOpencliOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencli"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestDomainsDNSDeleteAndDefaultPassYes(t *testing.T) {
	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	withScratchCaddyDomainsDir(t)
	log := fakeOpencliOnPath(t)

	d := &Domains{MySQL: mysqlDB}
	srv, client := newDomainsTestServer(t, d, "admin")
	for _, action := range []string{"delete", "default", "reload"} {
		resp, err := client.PostForm(srv.URL+"/domains/dns/toggle", url.Values{"domain_name": {"example.com"}, "dns_action": {action}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	b, _ := os.ReadFile(log)
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{"domains-dns delete example.com -y", "domains-dns default example.com -y", "domains-dns reload example.com"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("opencli calls = %q, want %q", got, want)
	}
}
