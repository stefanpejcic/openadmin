package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/auth"
)

// stubImunify fakes an installed agent, replies are keyed by the first three args
func stubImunify(t *testing.T, replies map[string]string) *[][]string {
	t.Helper()
	var calls [][]string
	origAvail, origRun, origScan := imunifyCommandAvailableRun, imunifyAgentRun, imunifyScanRun
	imunifyCommandAvailableRun = func(string) bool { return true }
	imunifyAgentRun = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(replies[strings.Join(args[:3], " ")]), nil
	}
	imunifyScanRun = func(ctx string) error {
		calls = append(calls, []string{"scan", ctx})
		return nil
	}
	imunifyUsersCache, imunifyUsersUntil = nil, time.Time{}
	t.Cleanup(func() {
		imunifyCommandAvailableRun, imunifyAgentRun, imunifyScanRun = origAvail, origRun, origScan
		imunifyUsersCache, imunifyUsersUntil = nil, time.Time{}
	})
	return &calls
}

func newImunifyScanTestServer(t *testing.T, ir *ImunifyScan) (*httptest.Server, *http.Client) {
	t.Helper()
	return newAdminTestServer(t, func(mux *http.ServeMux, sessions *auth.Manager) {
		ir.Sessions = sessions
		mux.HandleFunc("POST /security/imunify/{user}", ir.HandleScanUser)
	})
}

var imunifyTestReplies = map[string]string{
	"malware user list": `{"items":[{"user":"bobctx","home":"/home/bobctx","infected":2,"infected_db":1,"scan_date":1791712446,"scan_status":"stopped"},{"user":"alice","home":"/home/alice","infected":0,"scan_date":1791712456,"scan_status":"running"}]}`,
}

func TestImunifyScanUser(t *testing.T) {
	calls := stubImunify(t, imunifyTestReplies)
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("SELECT server FROM users").WithArgs("bob").WillReturnRows(sqlmock.NewRows([]string{"server"}).AddRow("bobctx"))

	ir := &ImunifyScan{MySQL: db}
	srv, client := newImunifyScanTestServer(t, ir)
	resp, err := client.PostForm(srv.URL+"/security/imunify/bob", url.Values{"back": {"/websites"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/websites" {
		t.Errorf("redirected to %q", loc)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "scan bobctx" {
		t.Errorf("calls = %v", *calls)
	}

	resp, _ = client.PostForm(srv.URL+"/security/imunify/..%2Fetc", url.Values{"back": {"https://evil.example"}})
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/websites" || len(*calls) != 1 {
		t.Errorf("bad username should be rejected, got %q and %v", loc, *calls)
	}
}

func TestWebsitesImunifyColumn(t *testing.T) {
	stubImunify(t, imunifyTestReplies)
	stubLatestWordPress(t, "")
	withWebsiteReportDirs(t)
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols).
		AddRow(1, "a.com", "nodejs", "", "", "", "a.com", "/var/www/html/a.com", "carol", "carol").
		AddRow(2, "b.com", "wordpress", "", "", "", "b.com", "/var/www/html/b.com", "bob", "bobctx"))
	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, err := client.Get(srv.URL + "/websites")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	for _, want := range []string{"ImunifyAV", `href="/security/imunify/"`, "3 infected", "ImunifyAV hasn't scanned"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestWebsitesNoImunifyColumn(t *testing.T) {
	stubLatestWordPress(t, "")
	withWebsiteReportDirs(t)
	orig := imunifyCommandAvailableRun
	imunifyCommandAvailableRun = func(string) bool { return false }
	t.Cleanup(func() { imunifyCommandAvailableRun = orig })
	db, m, _ := sqlmock.New()
	defer db.Close()
	m.ExpectQuery("FROM sites s").WillReturnRows(sqlmock.NewRows(websitesCols))
	srv, client := newWebsitesTestServer(t, &Websites{MySQL: db})
	resp, _ := client.Get(srv.URL + "/websites")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), `"Label" "ImunifyAV"`) || strings.Contains(string(body), "Scan with ImunifyAV") {
		t.Error("the ImunifyAV column needs imunify installed")
	}
}
