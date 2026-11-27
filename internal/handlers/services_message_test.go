package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// what podman printed when openpanel_mysql was left behind with phpmyadmin depending on it
const podmanMySQLConflict = `Error: removing container 790483c6676f from runtime: container 790483c6676fab0ca66275a50213c739ecf83c526 has dependent containers which must be removed before it: 7cf331cecad67bd69bc7ed4e6de56442c0549d939: container already exists
Error: creating container storage: the container name "openpanel_mysql" is already in use by 790483c6676fab0ca66275a50213c739ecf83c526. You have to remove that container to be able to reuse that name: that name is already in use, or use --replace to instruct Podman to do so.
Error: IPAM error: requested ip address 172.20.0.3 is already allocated to container ID 50d7163f24f1d76f70d39be5c3ac5c4d3a42014f619`

func TestServiceActionMessageKeepsRawOutputInDetails(t *testing.T) {
	msg, details, cat := serviceActionMessage("openpanel_mysql", "docker", "start", false, podmanMySQLConflict+"\n")
	if cat != "error" {
		t.Fatalf("category = %q", cat)
	}
	if details != strings.TrimSpace(podmanMySQLConflict) {
		t.Errorf("details should be the raw output, got %q", details)
	}
	// the toast sentence says what's wrong and how to fix it, without container IDs
	for _, want := range []string{"Failed to start service 'openpanel_mysql'.", "other containers depend on it", "podman rm -f --depend openpanel_mysql && cd /root && podman-compose up -d"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "790483c6676f") || strings.Contains(msg, "172.20.0.3") {
		t.Errorf("raw output leaked into the message: %q", msg)
	}
}

func TestServiceActionMessageHints(t *testing.T) {
	cases := []struct{ name, out, want string }{
		{"openpanel_mysql", `the container name "openpanel_mysql" is already in use by abc`, "same name"},
		{"caddy", "IPAM error: requested ip address 172.20.0.9 is already allocated", "IP address is still held"},
		{"caddy", "Error: rootlessport listen tcp 0.0.0.0:443: bind: address already in use", "port it needs"},
		{"openadmin_roundcube", "has dependent containers which must be removed before it", "cd /usr/local/mail/openmail"},
		{"caddy", "something podman never said before", "Podman returned an error, see the details."},
	}
	for _, c := range cases {
		msg, _, _ := serviceActionMessage(c.name, "docker", "start", false, c.out)
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s / %q: message %q is missing %q", c.name, c.out, msg, c.want)
		}
	}
	msg, details, cat := serviceActionMessage("caddy", "docker", "restart", true, "ok")
	if msg != "Successfully restarted service 'caddy'." || details != "" || cat != "success" {
		t.Errorf("success = %q, %q, %q", msg, details, cat)
	}
	msg, details, _ = serviceActionMessage("nginx", "system", "start", false, "Job for nginx.service failed.")
	if msg != "Failed to start service 'nginx'. Try from terminal: 'systemctl start nginx'" || details != "Job for nginx.service failed." {
		t.Errorf("system = %q, %q", msg, details)
	}
}

func TestServicesAsyncActionReturnsDetails(t *testing.T) {
	withScratchServicesConfig(t, "")
	origRun := controlServiceRun
	controlServiceRun = func(name, typ, action string) (bool, string) { return false, podmanMySQLConflict }
	t.Cleanup(func() { controlServiceRun = origRun })

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	srv, client := newServicesTestServer(t, &Services{MySQL: mysqlDB})

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/services", strings.NewReader(url.Values{
		"real_name": {"openpanel_mysql"}, "action": {"start"}, "container": {"docker"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var res serviceActionResult
	for i := 0; i < 50 && !res.Done; i++ {
		r, err := client.Get(srv.URL + "/services/action-status?real_name=openpanel_mysql")
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(r.Body).Decode(&res)
		r.Body.Close()
		if !res.Done {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !res.Done || res.Success {
		t.Fatalf("expected a finished failure, got %+v", res)
	}
	if !strings.Contains(res.Message, "other containers depend on it") || !strings.Contains(res.Details, "IPAM error") {
		t.Errorf("message/details not split: %+v", res)
	}
}
