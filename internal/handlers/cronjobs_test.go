package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

func withScratchCronFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	orig := CronFilePath
	CronFilePath = filepath.Join(dir, "openpanel")
	t.Cleanup(func() { CronFilePath = orig })
	if content != "" {
		os.WriteFile(CronFilePath, []byte(content), 0644)
	}
	return CronFilePath
}

func TestIsValidCronLine(t *testing.T) {
	cases := map[string]bool{
		"*/5 * * * * root opencli sentinel": true,
		"# a comment":                       false,
		"@reboot root something":            false,
		"   ":                               false,
		"":                                  false,
	}
	for line, want := range cases {
		if got := isValidCronLine(line); got != want {
			t.Errorf("isValidCronLine(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestSplitCronLineWithLogging(t *testing.T) {
	line := "*/5 * * * * root /usr/local/bin/opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log"
	job, ok := splitCronLine(line)
	if !ok {
		t.Fatal("expected a valid parse")
	}
	if job.Schedule != "*/5 * * * *" {
		t.Fatalf("unexpected schedule: %q", job.Schedule)
	}
	if job.Command != "opencli sentinel --check" {
		t.Fatalf("unexpected command: %q", job.Command)
	}
	if !job.Log {
		t.Fatal("expected logging to be detected as enabled")
	}
}

func TestSplitCronLineWithLoggingDisabled(t *testing.T) {
	line := "0 3 * * * root opencli backup --full #&& echo cron executed >> /var/log/openpanel-cron.log"
	job, ok := splitCronLine(line)
	if !ok {
		t.Fatal("expected a valid parse")
	}
	if job.Log {
		t.Fatal("expected logging to be detected as disabled when the marker is commented out (#&&)")
	}
	if job.Command != "opencli backup --full" {
		t.Fatalf("unexpected command: %q", job.Command)
	}
}

func TestSplitCronLineTooFewFields(t *testing.T) {
	if _, ok := splitCronLine("* * * * *"); ok {
		t.Fatal("expected a line with no command to fail to parse")
	}
}

func TestReadCronJobsSkipsCommentsAndInvalid(t *testing.T) {
	withScratchCronFile(t, strings.Join([]string{
		"# a comment",
		"*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log",
		"@reboot root something",
		"0 3 * * * root opencli backup #&& echo cron executed >> /var/log/openpanel-cron.log",
		"",
	}, "\n"))

	jobs, missing := readCronJobs()
	if missing {
		t.Fatal("expected the file to be found")
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 valid jobs, got %d: %+v", len(jobs), jobs)
	}
	// line numbers are 1-indexed positions in the raw file, matching Python's enumerate(file, start=1)
	if jobs[0].LineNumber != 2 || jobs[1].LineNumber != 4 {
		t.Fatalf("unexpected line numbers: %d, %d", jobs[0].LineNumber, jobs[1].LineNumber)
	}
}

func TestReadCronJobsMissingFile(t *testing.T) {
	dir := t.TempDir()
	orig := CronFilePath
	CronFilePath = filepath.Join(dir, "does-not-exist")
	t.Cleanup(func() { CronFilePath = orig })

	jobs, missing := readCronJobs()
	if !missing || jobs != nil {
		t.Fatalf("expected missing=true and nil jobs, got missing=%v jobs=%v", missing, jobs)
	}
}

func TestAddOrUpdateCronRewritesScheduleAndRestoresPrefix(t *testing.T) {
	path := withScratchCronFile(t, "*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log\n")

	if err := addOrUpdateCron(1, "0 4 * * *", true); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(written)
	if !strings.HasPrefix(got, "0 4 * * * root ") {
		t.Fatalf("expected the schedule to be rewritten, got %q", got)
	}
	// this is the key regression check: a sentinel command must round-trip
	// through the bash-wrapper path, not get mangled into
	// "/usr/local/bin/opencli sentinel" by the startswith('opencli') branch
	// firing before startswith('opencli sentinel') (see the doc comment in
	// cronjobs.go)
	if !strings.Contains(got, "/bin/bash /usr/local/admin/service/notifications.sh --check") {
		t.Fatalf("expected the sentinel command to be restored to its bash-wrapper form, got %q", got)
	}
	if strings.Contains(got, "/usr/local/bin/opencli sentinel") {
		t.Fatalf("did not expect the sentinel command to be mangled into an opencli invocation, got %q", got)
	}
}

func TestAddOrUpdateCronRestoresPlainOpenCLIPrefix(t *testing.T) {
	path := withScratchCronFile(t, "0 3 * * * root opencli backup --full && echo cron executed >> /var/log/openpanel-cron.log\n")

	if err := addOrUpdateCron(1, "0 5 * * *", false); err != nil {
		t.Fatal(err)
	}

	written, _ := os.ReadFile(path)
	got := string(written)
	if !strings.Contains(got, "/usr/local/bin/opencli backup --full") {
		t.Fatalf("expected opencli prefix to be restored, got %q", got)
	}
	if !strings.Contains(got, "#&& echo cron executed") {
		t.Fatalf("expected logging to be commented out, got %q", got)
	}
}

func TestAddOrUpdateCronMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	orig := CronFilePath
	CronFilePath = filepath.Join(dir, "does-not-exist")
	t.Cleanup(func() { CronFilePath = orig })

	if err := addOrUpdateCron(1, "0 5 * * *", false); err != nil {
		t.Fatalf("expected a missing file to be a silent no-op, got %v", err)
	}
}

func TestServeCronsListJSON(t *testing.T) {
	withScratchCronFile(t, "*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log\n")

	c := &Cronjobs{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/crons", c.ServeCrons)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/server/crons?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"schedule":"*/5 * * * *"`) {
		t.Fatalf("expected schedule in JSON, got %s", truncate(string(body)))
	}
}

func TestServeCronsRendersHTML(t *testing.T) {
	withScratchCronFile(t, "*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log\n")

	c := &Cronjobs{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/crons", c.ServeCrons)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/server/crons")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	got := string(body)
	for _, want := range []string{"Scheduler", "opencli sentinel --check", "getDescription", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected page to contain %q (page may have been truncated by a template execution error), got %s", want, truncate(got))
		}
	}
}

func TestServeCronsPostUpdatesMultipleEntries(t *testing.T) {
	path := withScratchCronFile(t, strings.Join([]string{
		"*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log",
		"0 3 * * * root opencli backup #&& echo cron executed >> /var/log/openpanel-cron.log",
		"",
	}, "\n"))

	c := &Cronjobs{Sessions: auth.NewManager("test-secret", false)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /server/crons", c.ServeCrons)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	form := strings.NewReader(
		"1_schedule_0=0&1_schedule_1=6&1_schedule_2=*&1_schedule_3=*&1_schedule_4=*&1_logging=on" +
			"&2_schedule_0=30&2_schedule_1=2&2_schedule_2=*&2_schedule_3=*&2_schedule_4=*",
	)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/server/crons", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	written, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(written), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "0 6 * * * root") {
		t.Fatalf("expected line 1's schedule to be updated, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "30 2 * * * root") {
		t.Fatalf("expected line 2's schedule to be updated, got %q", lines[1])
	}
	// line 2 didn't have "N_logging=on" submitted -> logging should be off
	if !strings.Contains(lines[1], "#&& echo cron executed") {
		t.Fatalf("expected line 2's logging to remain disabled, got %q", lines[1])
	}
}
