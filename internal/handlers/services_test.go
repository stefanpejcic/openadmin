package handlers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
)

func withScratchServicesConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	orig := ServicesConfigPath
	ServicesConfigPath = filepath.Join(dir, "services.json")
	t.Cleanup(func() { ServicesConfigPath = orig })
	if content != "" {
		os.WriteFile(ServicesConfigPath, []byte(content), 0644)
	}
	return ServicesConfigPath
}

func newServicesTestServer(t *testing.T, s *Services) (*httptest.Server, *http.Client) {
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
	s.Sessions = sessions

	mux := http.NewServeMux()
	mux.HandleFunc("GET /services", s.ServeStatus)
	mux.HandleFunc("POST /services", s.ServeStatus)
	mux.HandleFunc("GET /services/admin/status", s.ServeAdminStatus)
	mux.HandleFunc("GET /services/monitored", s.ServeMonitored)
	mux.HandleFunc("GET /services/edit", s.ServeEdit)
	mux.HandleFunc("POST /services/edit", s.ServeEdit)
	mux.HandleFunc("GET /service/{action}/{service_name}", s.HandleManageService)
	mux.HandleFunc("POST /service/{action}/{service_name}", s.HandleManageService)
	mux.HandleFunc("/login-as", func(w http.ResponseWriter, r *http.Request) {
		auth.LoginUser(w, r, sessions, caller, "203.0.113.1")
	})
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range auth.PopFlashes(w, r, sessions) {
			w.Write([]byte(f.Category + ": " + f.Message + "\n"))
		}
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

func TestGetServiceStatusFromCacheDockerKnown(t *testing.T) {
	svc := map[string]interface{}{"real_name": "caddy", "type": "docker"}
	up := getServiceStatusFromCache(svc, map[string]bool{"caddy": true}, nil, 5)
	if up == nil || !*up {
		t.Fatalf("expected up=true, got %v", up)
	}
}

func TestGetServiceStatusFromCacheDockerUnknownCoreServiceNoUsersYet(t *testing.T) {
	svc := map[string]interface{}{"real_name": "openpanel_mysql", "type": "docker"}
	status := getServiceStatusFromCache(svc, map[string]bool{}, nil, 0)
	if status != nil {
		t.Fatalf("expected nil (not-yet-initialized) when no users exist yet, got %v", *status)
	}
}

func TestGetServiceStatusFromCacheDockerUnknownWithUsersIsDown(t *testing.T) {
	svc := map[string]interface{}{"real_name": "openpanel_mysql", "type": "docker"}
	status := getServiceStatusFromCache(svc, map[string]bool{}, nil, 5)
	if status == nil || *status {
		t.Fatalf("expected false (down) when users exist but container is gone, got %v", status)
	}
}

func TestGetServiceStatusFromCacheDockerUnknownNonCoreService(t *testing.T) {
	svc := map[string]interface{}{"real_name": "netdata", "type": "docker"}
	status := getServiceStatusFromCache(svc, map[string]bool{}, nil, 0)
	if status == nil || *status {
		t.Fatalf("expected false for a non-core docker service missing from the cache, got %v", status)
	}
}

func TestGetServiceStatusFromCacheSystemd(t *testing.T) {
	svc := map[string]interface{}{"real_name": "sshd", "type": "system"}
	status := getServiceStatusFromCache(svc, nil, map[string]bool{"sshd": true}, 5)
	if status == nil || !*status {
		t.Fatalf("expected true, got %v", status)
	}
}

func TestServicesEditRoundTrip(t *testing.T) {
	path := withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services/edit?json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "caddy") {
		t.Fatalf("expected existing config in response, got %s", truncate(string(body)))
	}

	newData := `[{"name":"MySQL","real_name":"openpanel_mysql","type":"docker"}]`
	resp, err = client.PostForm(srv.URL+"/services/edit", url.Values{"data": {newData}})
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(respBody), "Config file updated successfully.") {
		t.Fatalf("expected success flash, got %s", truncate(string(respBody)))
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "openpanel_mysql") {
		t.Fatalf("expected the new config to be written, got %s", written)
	}
}

func TestServicesEditRendersHTML(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services/edit")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Edit Services", "caddy", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestServicesEditRejectsInvalidJSON(t *testing.T) {
	withScratchServicesConfig(t, "")

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.PostForm(srv.URL+"/services/edit", url.Values{"data": {"not valid json"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid JSON data") {
		t.Fatalf("expected invalid-JSON flash, got %s", truncate(string(body)))
	}
}

func TestServicesMonitoredReadsFromNotificationsIni(t *testing.T) {
	dir := t.TempDir()
	origNotif := NotificationsConfigPath
	NotificationsConfigPath = filepath.Join(dir, "notifications.ini")
	t.Cleanup(func() { NotificationsConfigPath = origNotif })
	os.WriteFile(NotificationsConfigPath, []byte("[DEFAULT]\nservices=caddy,openpanel_mysql\n"), 0644)

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services/monitored")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "caddy") || !strings.Contains(string(body), "openpanel_mysql") {
		t.Fatalf("expected both monitored services, got %s", truncate(string(body)))
	}
}

func TestServicesMonitoredMissingConfigReturns404(t *testing.T) {
	dir := t.TempDir()
	origNotif := NotificationsConfigPath
	NotificationsConfigPath = filepath.Join(dir, "does-not-exist.ini")
	t.Cleanup(func() { NotificationsConfigPath = origNotif })

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services/monitored")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServicesAdminStatus(t *testing.T) {
	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services/admin/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"up"`) {
		t.Fatalf("expected status up, got %s", truncate(string(body)))
	}
}

func TestServicesStatusPostControlsService(t *testing.T) {
	withScratchServicesConfig(t, "")

	var gotName, gotType, gotAction string
	origRun := controlServiceRun
	controlServiceRun = func(name, typ, action string) (bool, string) {
		gotName, gotType, gotAction = name, typ, action
		return true, "ok"
	}
	t.Cleanup(func() { controlServiceRun = origRun })

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"user_count", "plan_count"}).AddRow(1, 1))

	s := &Services{MySQL: mysqlDB}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.PostForm(srv.URL+"/services", url.Values{
		"real_name": {"caddy"}, "action": {"restart"}, "container": {"docker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if gotName != "caddy" || gotType != "docker" || gotAction != "restart" {
		t.Fatalf("expected controlServiceRun to be called with (caddy, docker, restart), got (%s, %s, %s)", gotName, gotType, gotAction)
	}
	if !strings.Contains(string(body), "Successfully restarted service &#39;caddy&#39;.") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
}

func TestServeStatusRendersHTML(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"},{"name":"MySQL","real_name":"openpanel_mysql","type":"docker"}]`)

	mysqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"user_count", "plan_count"}).AddRow(1, 1))

	s := &Services{MySQL: mysqlDB}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.Get(srv.URL + "/services")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(string(body)))
	}
	for _, want := range []string{"Services", "Caddy", "MySQL", "Edit services", "Monitoring", "</html>"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected body to contain %q, got %s", want, truncate(string(body)))
		}
	}
}

func TestManageServiceRejectsUnknownService(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	called := false
	origRun := manageServiceRun
	manageServiceRun = func(name, action string) (bool, string) { called = true; return true, "" }
	t.Cleanup(func() { manageServiceRun = origRun })

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.PostForm(srv.URL+"/service/start/not-a-real-service", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid service") {
		t.Fatalf("expected invalid-service flash, got %s", truncate(string(body)))
	}
	if called {
		t.Fatal("expected manageServiceRun to never be invoked for an unlisted service")
	}
}

func TestManageServiceRejectsInvalidAction(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.PostForm(srv.URL+"/service/bogus/caddy", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Invalid action") {
		t.Fatalf("expected invalid-action flash, got %s", truncate(string(body)))
	}
}

func TestManageServiceSuccessCallsRunner(t *testing.T) {
	withScratchServicesConfig(t, `[{"name":"Caddy","real_name":"caddy","type":"docker"}]`)

	var gotName, gotAction string
	origRun := manageServiceRun
	manageServiceRun = func(name, action string) (bool, string) {
		gotName, gotAction = name, action
		return true, "ok"
	}
	t.Cleanup(func() { manageServiceRun = origRun })

	s := &Services{}
	srv, client := newServicesTestServer(t, s)

	resp, err := client.PostForm(srv.URL+"/service/start/caddy", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if gotName != "caddy" || gotAction != "start" {
		t.Fatalf("expected (caddy, start), got (%s, %s)", gotName, gotAction)
	}
	if !strings.Contains(string(body), "Caddy start") {
		t.Fatalf("expected success flash, got %s", truncate(string(body)))
	}
}
