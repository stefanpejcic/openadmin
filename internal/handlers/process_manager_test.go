package handlers

import (
	"bufio"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func newProcessManagerTestServer(t *testing.T, p *ProcessManager) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	origPath := admindb.Path
	admindb.Path = filepath.Join(dir, "users.db")
	t.Cleanup(func() { admindb.Path = origPath })

	db, err := admindb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	hash, _ := auth.GeneratePasswordHash("pw")
	db.CreateUser("caller", hash, "admin")
	caller, err := db.UserByUsername("caller")
	if err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewManager("test-secret", false)
	p.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /server/processes", p.ServeProcesses)
	mux.HandleFunc("GET /server/processes/{pid}/{action}", p.ServeProcessAction)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})

	handler := auth.WithUserLoader(sessions, db)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.Get(srv.URL + "/login-as"); err != nil {
		t.Fatal(err)
	}
	return srv, client
}

func TestReadTotalMemKB(t *testing.T) {
	if got := readTotalMemKB(); got == 0 {
		t.Fatal("expected a non-zero total memory reading on Linux")
	}
}

func TestReadProcessInfoForSelf(t *testing.T) {
	info, ok := readProcessInfo(os.Getpid(), readTotalMemKB())
	if !ok {
		t.Fatal("expected to read info for our own pid")
	}
	if info.PID != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), info.PID)
	}
	if info.Owner == "" {
		t.Fatal("expected a resolved owner")
	}
}

func TestReadProcessInfoMissingPidFails(t *testing.T) {
	if _, ok := readProcessInfo(1<<30, 0); ok {
		t.Fatal("expected a bogus pid to fail")
	}
}

func TestListAllProcessesIncludesSelf(t *testing.T) {
	list := listAllProcesses()
	found := false
	for _, p := range list {
		if p.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Fatal("expected our own process to appear in the listing")
	}
}

func TestListAllProcessesCPUPercentRoundedToThreeDecimals(t *testing.T) {
	listAllProcesses() // seeds processCPUCache with a first sample
	time.Sleep(50 * time.Millisecond)
	list := listAllProcesses() // second call can now compute a real delta

	for _, p := range list {
		rounded := math.Round(p.CPUPercent*1000) / 1000
		if math.Abs(p.CPUPercent-rounded) > 1e-9 {
			t.Fatalf("expected CPUPercent rounded to at most 3 decimals, pid %d got %v", p.PID, p.CPUPercent)
		}
	}
}

func TestSortProcessesByNameAndDefault(t *testing.T) {
	// Matches the original's own sort_criteria dict exactly: like cpu/
	// memory/priority/owner/command, the plain "name" key sorts
	// DESCENDING (consistent with "positive key = descending, '-'-prefixed
	// = ascending" used throughout that dict for every field except pid,
	// which is inverted). So plain "name" is Z-to-A, not A-to-Z.
	list := []processInfo{
		{PID: 1, Name: "a", CPUPercent: 5},
		{PID: 2, Name: "b", CPUPercent: 10},
	}
	sortProcesses(list, "name")
	if list[0].Name != "b" || list[1].Name != "a" {
		t.Fatalf("expected descending name sort, got %+v", list)
	}

	list2 := []processInfo{
		{PID: 1, Name: "a", CPUPercent: 5},
		{PID: 2, Name: "b", CPUPercent: 10},
	}
	sortProcesses(list2, "-name")
	if list2[0].Name != "a" || list2[1].Name != "b" {
		t.Fatalf("expected ascending name sort for '-name', got %+v", list2)
	}

	list3 := []processInfo{
		{PID: 1, Name: "b", CPUPercent: 5},
		{PID: 2, Name: "a", CPUPercent: 10},
	}
	sortProcesses(list3, "unknown-sort-key")
	if list3[0].CPUPercent != 10 {
		t.Fatalf("expected default cpu-descending sort for an unrecognized key, got %+v", list3)
	}
}

func TestServeProcessesJSON(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)

	resp, err := client.Get(srv.URL + "/server/processes?output=json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"pid"`) {
		t.Fatalf("expected process JSON array, got %s", truncate(string(body)))
	}
}

func TestServeProcessesHTMLTitleReflectsSort(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)

	resp, err := client.Get(srv.URL + "/server/processes?sort=name")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "sort by name") {
		t.Fatalf("expected sort-aware title, got %s", truncate(string(body)))
	}
}

func TestServeProcessesRendersHTML(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)

	resp, err := client.Get(srv.URL + "/server/processes")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Process Manager", "Pid", "Owner", "Priority", "CPU %", "Memory %", "Command", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeProcessActionInvalidAction(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.Get(srv.URL + "/server/processes/1/bogus")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid action: bogus") {
		t.Fatalf("expected invalid-action flash, got %s", truncate(string(body)))
	}
}

func TestServeProcessActionKillFailureForBogusPid(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.Get(srv.URL + "/server/processes/999999999/kill")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Error killing process") {
		t.Fatalf("expected kill-failure flash for a nonexistent pid, got %s", truncate(string(body)))
	}
}

func TestServeProcessActionKillSuccess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a scratch process to kill: %v", err)
	}
	defer cmd.Wait()

	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }

	resp, err := client.Get(srv.URL + "/server/processes/" + strconv.Itoa(cmd.Process.Pid) + "/kill")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "killed successfully") {
		t.Fatalf("expected kill success flash, got %s", truncate(string(body)))
	}
}

func TestServeProcessActionStraceNonStreamRendersPage(t *testing.T) {
	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)

	resp, err := client.Get(srv.URL + "/server/processes/1234/strace")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"strace -p", "1234", "Process Manager", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServeProcessActionStraceStreamsRawLines(t *testing.T) {
	origRun := straceRun
	straceRun = func(pid int) (*exec.Cmd, error) {
		// A stand-in for `strace -p PID` that just emits a couple of lines,
		// avoiding a dependency on strace actually being installed/permitted.
		return exec.Command("printf", "line one\nline two\n"), nil
	}
	t.Cleanup(func() { straceRun = origRun })

	p := &ProcessManager{}
	srv, client := newProcessManagerTestServer(t, p)

	resp, err := client.Get(srv.URL + "/server/processes/1234/strace?output=stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
		t.Fatalf("expected the two raw lines streamed, got %+v", lines)
	}
}
