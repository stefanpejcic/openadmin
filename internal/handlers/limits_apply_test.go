package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

const limitsTestCompose = `services:
  openpanel_redis:
    image: redis
    container_name: openpanel_redis
    deploy:
      resources:
        limits:
          cpus: "${REDIS_CPUS:-0.5}"
          memory: "${REDIS_RAM:-0.5G}"
  caddy:
    mem_limit: ${CADDY_RAM}
    cpus: ${CADDY_CPUS}
    container_name: caddy
  bind9:
    container_name: openpanel_dns
    cpus: ${BIND9_CPUS}
  nolimits:
    image: busybox
    cpus: ${ORPHAN_CPUS}
`

func TestLimitTargets(t *testing.T) {
	got := limitTargets(limitsTestCompose)
	want := map[string]limitTarget{
		"REDIS_CPUS": {"openpanel_redis", "openpanel_redis", "cpus"},
		"REDIS_RAM":  {"openpanel_redis", "openpanel_redis", "memory"},
		"CADDY_RAM":  {"caddy", "caddy", "memory"},
		"CADDY_CPUS": {"caddy", "caddy", "cpus"},
		"BIND9_CPUS": {"bind9", "openpanel_dns", "cpus"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
}

type limitsStub struct {
	calls   []string
	running map[string]bool
	fail    map[string]error
}

func stubLimits(t *testing.T) *limitsStub {
	t.Helper()
	dir := t.TempDir()
	origCompose, origRun, origRunning, origRecreate := LimitsComposePath, limitsPodmanUpdateRun, limitsContainerRunning, limitsRecreateRun
	t.Cleanup(func() {
		LimitsComposePath, limitsPodmanUpdateRun, limitsContainerRunning, limitsRecreateRun = origCompose, origRun, origRunning, origRecreate
	})
	LimitsComposePath = filepath.Join(dir, "docker-compose.yml")
	os.WriteFile(LimitsComposePath, []byte(limitsTestCompose), 0644)
	st := &limitsStub{running: map[string]bool{"openpanel_redis": true, "caddy": true, "openpanel_dns": true}, fail: map[string]error{}}
	limitsPodmanUpdateRun = func(container, flag string) error {
		st.calls = append(st.calls, container+" "+flag)
		return st.fail[flag]
	}
	limitsContainerRunning = func(c string) bool { return st.running[c] }
	return st
}

func TestApplyLimitsLive(t *testing.T) {
	st := stubLimits(t)
	res := applyLimitsLive(map[string]string{"REDIS_RAM": "2G", "REDIS_CPUS": "1", "CADDY_CPUS": "1.5", "UNKNOWN_CPUS": "1", "BIND_TIMEZONE": "UTC"})
	if !slices.Equal(st.calls, []string{"caddy --cpus=1.5", "openpanel_redis --cpus=1", "openpanel_redis --memory=2g"}) {
		t.Fatalf("calls %v", st.calls)
	}
	// redis shows up once even with two keys changed
	if !slices.Equal(res.Applied, []string{"caddy", "openpanel_redis"}) || len(res.Failed) != 0 || len(res.Recreate) != 0 {
		t.Fatalf("res %+v", res)
	}
	if !slices.Equal(res.Unmapped, []string{"UNKNOWN_CPUS"}) {
		t.Fatalf("unmapped %v", res.Unmapped)
	}
}

func TestApplyLimitsLiveZeroNeedsRestart(t *testing.T) {
	st := stubLimits(t)
	res := applyLimitsLive(map[string]string{"REDIS_RAM": "0", "CADDY_CPUS": "0"})
	if len(st.calls) != 0 {
		t.Fatalf("calls %v", st.calls)
	}
	if !slices.Equal(res.Recreate, []string{"caddy", "openpanel_redis"}) {
		t.Fatalf("recreate %v", res.Recreate)
	}
	if msg := limitsMessage(res); !strings.Contains(msg, "Restart to apply: caddy, openpanel_redis.") {
		t.Fatalf("msg %q", msg)
	}
}

func TestApplyLimitsLiveNotRunningAndFailed(t *testing.T) {
	st := stubLimits(t)
	st.running["caddy"] = false
	st.fail["--memory=2g"] = errors.New("cannot set memory")
	res := applyLimitsLive(map[string]string{"CADDY_CPUS": "2", "CADDY_RAM": "2G", "REDIS_RAM": "2G", "REDIS_CPUS": "1"})
	if !slices.Equal(st.calls, []string{"openpanel_redis --cpus=1", "openpanel_redis --memory=2g"}) {
		t.Fatalf("calls %v", st.calls)
	}
	if !slices.Equal(res.Pending, []string{"caddy"}) {
		t.Fatalf("pending %v", res.Pending)
	}
	if len(res.Failed) != 1 || res.Failed[0].Container != "openpanel_redis" || !strings.Contains(res.Failed[0].Error, "REDIS_RAM: cannot set memory") {
		t.Fatalf("failed %+v", res.Failed)
	}
	// half applied isn't applied
	if len(res.Applied) != 0 {
		t.Fatalf("applied %v", res.Applied)
	}
	msg := limitsMessage(res)
	if !strings.Contains(msg, "Not running, applied when started from Services: caddy.") || !strings.Contains(msg, "Failed to apply to openpanel_redis") {
		t.Fatalf("msg %q", msg)
	}
}

func TestServeRecreate(t *testing.T) {
	stubLimits(t)
	var got limitTarget
	limitsRecreateRun = func(tg limitTarget) (string, []string, error) { got = tg; return "", nil, nil }
	l := &Limits{Sessions: auth.NewManager("test-secret", false)}
	post := func(container string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/services/limits/recreate", strings.NewReader("container="+container))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		l.ServeRecreate(rec, req)
		return rec
	}
	if rec := post("openpanel_dns"); rec.Code != http.StatusSeeOther || got.service != "bind9" {
		t.Fatalf("got %d %+v", rec.Code, got)
	}
	// only containers from the compose file
	got = limitTarget{}
	if post("sshd"); got.service != "" {
		t.Fatalf("recreated %+v", got)
	}
}

func TestServeLimitsZeroOffersRestart(t *testing.T) {
	path := withScratchGlobalEnvPath(t)
	stubLimits(t)
	os.WriteFile(path, []byte("REDIS_RAM=\"1G\"\n"), 0644)
	srv, client := newLimitsTestServer(t, &Limits{})
	resp, err := client.PostForm(srv.URL+"/services/limits", url.Values{"REDIS_RAM": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `action="/services/limits/recreate"`) || !strings.Contains(string(body), "Restart openpanel_redis") {
		t.Fatalf("no restart offer in %s", truncate(string(body)))
	}
}

func TestLimitsRecreateRunTakesDependents(t *testing.T) {
	stubLimits(t)
	compose := limitsTestCompose + `  openpanel:
    container_name: openpanel
    depends_on:
      - openpanel_redis
`
	os.WriteFile(LimitsComposePath, []byte(compose), 0644)
	origOut, origCompose := limitsPodmanOut, limitsComposeRun
	t.Cleanup(func() { limitsPodmanOut, limitsComposeRun = origOut, origCompose })
	var podmanCalls, composeCalls []string
	limitsPodmanOut = func(args ...string) (string, error) {
		podmanCalls = append(podmanCalls, strings.Join(args, " "))
		switch args[0] {
		case "ps":
			return "aaa\nbbb\nccc\n", nil
		case "inspect":
			return "openpanel_redis|aaa|\nopenpanel|bbb|aaa,\ncaddy|ccc|\n", nil
		}
		return "", nil
	}
	limitsComposeRun = func(args ...string) ([]byte, error) {
		composeCalls = append(composeCalls, strings.Join(args, " "))
		return nil, nil
	}
	origFlag := OpenpanelRestartFlagPath
	OpenpanelRestartFlagPath = filepath.Join(t.TempDir(), "flag")
	t.Cleanup(func() { OpenpanelRestartFlagPath = origFlag })

	_, deps, err := limitsRecreateRun(limitTarget{"openpanel_redis", "openpanel_redis", "cpus"})
	if err != nil || !slices.Equal(deps, []string{"openpanel"}) {
		t.Fatalf("deps %v err %v", deps, err)
	}
	if !slices.Contains(podmanCalls, "rm -f --depend openpanel_redis") || !slices.Equal(composeCalls, []string{"up -d --no-deps openpanel_redis openpanel"}) {
		t.Fatalf("podman %v compose %v", podmanCalls, composeCalls)
	}

	// a dependent outside the compose file would be lost, so nothing is removed
	os.WriteFile(LimitsComposePath, []byte(limitsTestCompose), 0644)
	podmanCalls = nil
	if _, _, err := limitsRecreateRun(limitTarget{"openpanel_redis", "openpanel_redis", "cpus"}); err == nil || slices.Contains(podmanCalls, "rm -f --depend openpanel_redis") {
		t.Fatalf("err %v calls %v", err, podmanCalls)
	}
}

func TestApplyLimitsLiveVersionNeedsRestart(t *testing.T) {
	st := stubLimits(t)
	os.WriteFile(LimitsComposePath, []byte(limitsTestCompose+`  phpmyadmin:
    image: phpmyadmin:${PHPMYADMIN_VERSION:-latest}
    container_name: phpmyadmin
`), 0644)
	st.running["phpmyadmin"] = true
	res := applyLimitsLive(map[string]string{"PHPMYADMIN_VERSION": "5.2", "SOMETHING_ELSE": "x"})
	if len(st.calls) != 0 || !slices.Equal(res.Recreate, []string{"phpmyadmin"}) || len(res.Unmapped) != 0 {
		t.Fatalf("calls %v res %+v", st.calls, res)
	}
	if repos := composeImageRepos(limitsTestCompose + "  phpmyadmin:\n    image: phpmyadmin:${PHPMYADMIN_VERSION:-latest}\n"); repos["PHPMYADMIN_VERSION"] != "phpmyadmin" {
		t.Fatalf("repos %v", repos)
	}
}

func TestParseLimitsStats(t *testing.T) {
	got := parseLimitsStats([]byte(`{"Name":"caddy","CPU":12.5,"MemUsage":1024,"MemLimit":2048,"MemPerc":50,"PIDs":7}
{"Name":"openpanel_redis","CPU":0.1,"MemUsage":1,"MemLimit":0,"MemPerc":0,"PIDs":3}
`))
	if got["caddy"].CPU != 12.5 || got["caddy"].PIDs != 7 || got["openpanel_redis"].PIDs != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestLimitsServiceRows(t *testing.T) {
	view := []limitsGroup{
		{Name: "BIND", Rows: []limitsRow{{FieldName: "BIND_TIMEZONE", Label: "TIMEZONE"}}},
		{Name: "BIND9", Rows: []limitsRow{{FieldName: "BIND9_CPUS", Label: "CPUS"}, {FieldName: "BIND9_RAM", Label: "RAM"}, {FieldName: "BIND9_VERSION", Label: "VERSION"}}},
		{Name: "ORPHAN", Rows: []limitsRow{{FieldName: "ORPHAN_X", Label: "X"}}},
	}
	rows := limitsServiceRows(view, map[string]string{"BIND": "openpanel_dns", "BIND9": "openpanel_dns"})
	if len(rows) != 2 || rows[0].Container != "openpanel_dns" || rows[0].CPU.FieldName != "BIND9_CPUS" || rows[0].RAM.FieldName != "BIND9_RAM" || len(rows[0].Other) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[1].Container != "" || rows[1].Groups[0] != "ORPHAN" {
		t.Fatalf("orphan %+v", rows[1])
	}
}
