package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newAPIServerCronsMux(c *APIServerCrons) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/crons", c.ServeCrons)
	mux.HandleFunc("POST /api/server/crons", c.ServeCrons)
	return mux
}

func TestAPIServeCronsGetMissingFileReturnsNull(t *testing.T) {
	withScratchCronFile(t, "")
	os.Remove(CronFilePath)

	c := &APIServerCrons{}
	srv := httptest.NewServer(newAPIServerCronsMux(c))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/crons")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body != nil {
		t.Fatalf("expected null body for a missing cron file, got %v", body)
	}
}

func TestAPIServeCronsGetListsParsedJobs(t *testing.T) {
	withScratchCronFile(t, "*/5 * * * * root opencli sentinel --check && echo cron executed >> /var/log/openpanel-cron.log\n")

	c := &APIServerCrons{}
	srv := httptest.NewServer(newAPIServerCronsMux(c))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/server/crons")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var jobs []CronJob
	json.NewDecoder(resp.Body).Decode(&jobs)
	if len(jobs) != 1 || jobs[0].LineNumber != 1 || jobs[0].Schedule != "*/5 * * * *" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
}

func TestAPIServeCronsPostInvalidJSON(t *testing.T) {
	withScratchCronFile(t, "")

	c := &APIServerCrons{}
	srv := httptest.NewServer(newAPIServerCronsMux(c))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/crons", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIServeCronsPostEmptyJobsIsRejected(t *testing.T) {
	withScratchCronFile(t, "")

	c := &APIServerCrons{}
	srv := httptest.NewServer(newAPIServerCronsMux(c))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/crons", "application/json", strings.NewReader(`{"jobs":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "jobs must be a non-empty list") {
		t.Fatalf("unexpected error message: %s", body)
	}
}

func TestAPIServeCronsPostUpdatesEntry(t *testing.T) {
	path := withScratchCronFile(t, "*/5 * * * * root opencli backup && echo cron executed >> /var/log/openpanel-cron.log\n")

	c := &APIServerCrons{}
	srv := httptest.NewServer(newAPIServerCronsMux(c))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/server/crons", "application/json",
		strings.NewReader(`{"jobs":[{"line_number":1,"schedule":"0 4 * * *","logging":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var respBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&respBody)
	if respBody["success"] != true {
		t.Fatalf("expected success=true, got %+v", respBody)
	}

	written, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(written), "0 4 * * * root ") {
		t.Fatalf("expected the schedule to be rewritten, got %q", written)
	}
}
