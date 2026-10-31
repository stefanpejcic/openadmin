package cluster

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"openadmin/internal/admindb"
)

func TestClassifyKeepsPerServerValuesLocal(t *testing.T) {
	for key, want := range map[string]string{
		"openpanel:USERS.session_duration":                   GroupCore,
		"openpanel:DEFAULT.enabled_modules":                  GroupCore,
		"openpanel:SMTP.mail_password":                       GroupSecrets,
		"admin:CLOUDFLARE.cf_key":                            GroupSecrets,
		"openpanel:DEFAULT.ns1":                              GroupNameservers,
		"openpanel:LICENSE.key":                              "",
		"openpanel:SMTP.mail_security_token":                 "",
		"openpanel:PANEL.api":                                "",
		"openpanel:PANEL.dev_mode":                           "",
		"admin:SECURITY.basic_auth_password":                 "",
		"admin:EMAIL.email_storage_location":                 "",
		"file:/etc/openpanel/openpanel/features/default.txt": GroupCore,
		"file:/etc/openpanel/openadmin/secret.key":           "",
		"file:/etc/openpanel/openadmin/users.db":             "",
		"file:/etc/openpanel/openpanel/features/../../x.txt": "",
		"file:/root/.ssh/authorized_keys":                    "",
		"nonsense":                                           "",
	} {
		if got := classify(key); got != want {
			t.Errorf("classify(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestDiffSkipsOtherGroupsAndMarksPinned(t *testing.T) {
	local := map[string]string{"openpanel:USERS.a": "1", "openpanel:USERS.b": "same"}
	remote := map[string]string{
		"openpanel:USERS.a":        "2",
		"openpanel:USERS.b":        "same",
		"openpanel:SMTP.mail_port": "25",
		"openpanel:LICENSE.key":    "enterprise-x",
	}
	got := diff(local, remote, []string{GroupCore}, []string{"openpanel:USERS.a"})
	if len(got) != 1 || got[0].Key != "openpanel:USERS.a" || !got[0].Pinned {
		t.Fatalf("unexpected diff: %+v", got)
	}
	if got := diff(local, remote, []string{GroupCore, GroupSecrets}, nil); len(got) != 2 {
		t.Fatalf("expected secret group included, got %+v", got)
	}
}

func TestApplyRefusesKeysOutsideTheAllowlist(t *testing.T) {
	Dir = t.TempDir()
	for _, key := range []string{"file:/root/.ssh/authorized_keys", "openpanel:LICENSE.key"} {
		if err := applyChanges([]Change{{Key: key, New: "evil"}}); err == nil {
			t.Fatalf("applyChanges accepted %q", key)
		}
	}
}

func TestMaskedHidesSecretsAndFileBodies(t *testing.T) {
	got := masked([]Change{
		{Key: "openpanel:SMTP.mail_password", Group: GroupSecrets, Old: "old", New: "new"},
		{Key: "file:/etc/openpanel/openpanel/custom_code/custom.js", Group: GroupCore, Old: "a", New: "bbb"},
	})
	if got[0].Old != "********" || got[0].New != "********" {
		t.Fatalf("secret leaked: %+v", got[0])
	}
	if got[1].New != "(3 bytes)" {
		t.Fatalf("file body leaked: %+v", got[1])
	}
}

func slavePair(t *testing.T) (master, slave *Manager) {
	t.Helper()
	Dir = t.TempDir()
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 7
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	master = &Manager{loaded: true, state: State{Role: RoleMaster, SeedHex: hex.EncodeToString(seed)}}
	slave = &Manager{loaded: true, state: State{Role: RoleSlave, NodeID: "n1", MasterPubHex: hex.EncodeToString(pub)}}
	return master, slave
}

func TestClaimsVerify(t *testing.T) {
	master, slave := slavePair(t)
	good, err := master.sign(Claims{Aud: "n1", Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slave.verify(good); err != nil {
		t.Fatalf("valid claims rejected: %v", err)
	}
	if _, err := slave.verify(good); err == nil {
		t.Fatal("replayed claims accepted")
	}
	other, _ := master.sign(Claims{Aud: "n2", Action: "status"})
	if _, err := slave.verify(other); err == nil {
		t.Fatal("claims for another server accepted")
	}

	var env envelope
	fresh, _ := master.sign(Claims{Aud: "n1", Action: "status"})
	json.Unmarshal(fresh, &env)
	env.Body = []byte(`{"aud":"n1","act":"account_delete","exp":99999999999}`)
	tampered, _ := json.Marshal(env)
	if _, err := slave.verify(tampered); err == nil {
		t.Fatal("tampered claims accepted")
	}

	body, _ := json.Marshal(Claims{Aud: "n1", Action: "status", Iat: time.Now().Add(-time.Hour).Unix(), Exp: time.Now().Add(-time.Minute).Unix(), Jti: "x"})
	sig, _ := signWith(master.state.SeedHex, body)
	expired, _ := json.Marshal(envelope{Body: body, Sig: sig})
	if _, err := slave.verify(expired); err == nil {
		t.Fatal("expired claims accepted")
	}
}

func TestStandaloneServerRefusesCalls(t *testing.T) {
	master, _ := slavePair(t)
	raw, _ := master.sign(Claims{Aud: "n1", Action: "status"})
	standalone := &Manager{loaded: true}
	if _, err := standalone.verify(raw); err == nil {
		t.Fatal("a standalone server accepted a call")
	}
}

func TestJoinCodeRoundTrip(t *testing.T) {
	raw := make([]byte, 48)
	raw[0], raw[47] = 1, 2
	if _, _, err := parseJoinCode("opjoin-short"); err == nil {
		t.Fatal("short code accepted")
	}
}

func TestNeverProxied(t *testing.T) {
	for path, want := range map[string]bool{
		"/login": true, "/login/2fa": true, "/logout": true, "/_cluster/state": true,
		"/login/token/bob": false, "/dashboard": false, "/users": false, "/server/cluster": false,
	} {
		if got := neverProxied(path); got != want {
			t.Errorf("neverProxied(%q) = %v", path, got)
		}
	}
}

func TestSSHAuthorizeRejectsInjection(t *testing.T) {
	for _, a := range []sshAuthorizeArgs{
		{Pub: "ssh-ed25519 AAAA\nssh-rsa evil", From: "1.2.3.4", Tag: "abcd1234"},
		{Pub: "ssh-ed25519 AAAA", From: `1.2.3.4",command="x`, Tag: "abcd1234"},
		{Pub: "ssh-ed25519 AAAA", From: "1.2.3.4", Tag: "../../x"},
	} {
		if err := sshAuthorize(a); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
}

func TestVersionNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"2.0.20", "2.0.19", true}, {"2.0.19", "2.0.19", false}, {"2.0.9", "2.0.19", false}, {"2.1", "2.0.19", true}, {"", "2.0.19", false}, {"x", "2.0.19", false}, {"2.0.19", "", false}} {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q) = %v", c.a, c.b, got)
		}
	}
}

func TestPromotionMovesKeyAndOtherSlavesKeepTrusting(t *testing.T) {
	master, slave := slavePair(t)
	slave.Licensed = func() bool { return true }
	raw, _ := json.Marshal(promoteArgs{Seed: master.state.SeedHex, SelfURL: "https://new:2087", Nodes: []Node{{ID: "old"}}})
	if err := slave.acceptPromotion(raw); err != nil {
		t.Fatal(err)
	}
	st := slave.State()
	if st.Role != RoleMaster || st.SeedHex != master.state.SeedHex || st.MasterPubHex != "" {
		t.Fatalf("unexpected state: %+v", st)
	}

	pub, err := publicFromSeed(st.SeedHex)
	if err != nil {
		t.Fatal(err)
	}
	other := &Manager{loaded: true, state: State{Role: RoleSlave, NodeID: "n2", MasterPubHex: pub}}
	call, _ := slave.sign(Claims{Aud: "n2", Action: "status"})
	if _, err := other.verify(call); err != nil {
		t.Fatalf("other slave stopped trusting the new master: %v", err)
	}
}

func TestPromotionNeedsLicense(t *testing.T) {
	master, slave := slavePair(t)
	slave.Licensed = func() bool { return false }
	raw, _ := json.Marshal(promoteArgs{Seed: master.state.SeedHex, SelfURL: "https://new:2087"})
	if err := slave.acceptPromotion(raw); err == nil {
		t.Fatal("promoted without a license")
	}
}

func TestStatusArgsOnlyApplyOnSlaves(t *testing.T) {
	_, slave := slavePair(t)
	raw, _ := json.Marshal(statusArgs{ClusterSize: 3, MasterURL: "https://m:2087", MasterName: "m", MasterSPKI: "abc"})
	slave.applyStatusArgs(raw)
	if st := slave.State(); st.MasterURL != "https://m:2087" || slave.ClusterCount() != 3 {
		t.Fatalf("slave didn't learn its master: %+v", st)
	}
	standalone := &Manager{loaded: true}
	standalone.applyStatusArgs(raw)
	if standalone.State().MasterURL != "" || standalone.ClusterCount() != 1 {
		t.Fatal("standalone server changed")
	}
}

const testPage = `<html><body><main id="no_margin_for_iframe"><table id="t"><thead><tr><th class="c"><input type="checkbox"></th><th class="h">Time</th><th>Note</th></tr></thead><tbody>%s</tbody></table></main></body></html>`

func row(ts, note string) string {
	return `<tr><td class="c"><input type="checkbox" name="pick" @change="x()"></td><td class="d">` + ts + `</td><td><a id="a1" href="/users/` + note + `" @click="go()">` + note + `</a><button onclick="del()">Del</button></td></tr>`
}

func open(id, href string) string { return "/open/" + id + "?n=" + href }

var testOpts = mergeOptions{sortTime: true, ascLink: "/x?sort=server&direction=asc", descLink: "/x?sort=server&direction=desc"}

func TestMergeSortsByServerColumn(t *testing.T) {
	local := fmt.Sprintf(testPage, row("2026-10-30 09:00:00", "bob"))
	remote := fmt.Sprintf(testPage, row("2026-10-30 10:00:00", "amy"))
	for dir, first := range map[string]string{"asc": "amy", "desc": "bob"} {
		opts := testOpts
		opts.serverDir = dir
		out := string(mergeTables([]byte(local), "stefan", []remotePage{{id: "n1", name: "petar", body: []byte(remote)}}, opts, open))
		other := map[string]string{"amy": "bob", "bob": "amy"}[first]
		if strings.Index(out, first) > strings.Index(out, other) {
			t.Errorf("%s: %s should come first", dir, first)
		}
		if !strings.Contains(out, "/x?sort=server&amp;direction=asc") || !strings.Contains(out, "<span>Server</span>") {
			t.Errorf("sort links missing from the Server header: %s", out)
		}
	}
}

func TestServerFilterHiddenFromHandlers(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/users?server=n&q=a", nil)
	out := takeServerSort(r)
	if out.URL.Query().Get("server") != "" || out.URL.Query().Get("q") != "a" {
		t.Fatalf("query not cleaned: %s", out.URL.RawQuery)
	}
	if only, _ := out.Context().Value(serverFilterKey{}).(string); only != "n" {
		t.Fatalf("filter lost: %q", only)
	}
}

func TestTakeServerSortHidesItFromTheHandlers(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/users?sort=server&direction=desc&q=a", nil)
	out := takeServerSort(r)
	if out.URL.Query().Get("sort") != "" || out.URL.Query().Get("q") != "a" {
		t.Fatalf("query not cleaned: %s", out.URL.RawQuery)
	}
	if dir, _ := out.Context().Value(serverSortKey{}).(string); dir != "desc" {
		t.Fatalf("direction lost: %q", dir)
	}
	other, _ := http.NewRequest(http.MethodGet, "/users?sort=username", nil)
	if takeServerSort(other) != other {
		t.Fatal("other sorts must stay with the handler")
	}
}

func TestMergeAddsSecondColumnSortsAndNeutralizesRemoteRows(t *testing.T) {
	local := fmt.Sprintf(testPage, row("2026-10-30 09:00:00", "bob"))
	remote := fmt.Sprintf(testPage, row("2026-10-30 10:00:00", "amy")+`<tr><td colspan="3">details of amy</td></tr>`)
	out := string(mergeTables([]byte(local), "stefan", []remotePage{{id: "n1", name: "petar", body: []byte(remote)}}, testOpts, open))

	if !strings.Contains(out, `<span>Server</span>`) {
		t.Fatalf("Server header isn't second: %s", out)
	}
	if strings.Index(out, "amy") > strings.Index(out, "bob") {
		t.Fatalf("newer remote row should sort first: %s", out)
	}
	if !strings.Contains(out, "details of amy</td></tr>"+`<tr><td class="c"><input type="checkbox" name="pick"`) {
		t.Fatalf("details row left its row: %s", out)
	}
	amy := out[strings.Index(out, "<tbody>"):strings.Index(out, "details of amy")]
	for _, banned := range []string{"@click", "del()", `id="a1"`, `href="/users/amy"`} {
		if strings.Contains(amy, banned) {
			t.Fatalf("remote row kept %s: %s", banned, amy)
		}
	}
	for _, want := range []string{"window.location.href", `href="/open/n1?n=/users/amy"`, ">petar</a>"} {
		if !strings.Contains(amy, want) {
			t.Fatalf("remote row misses %s: %s", want, amy)
		}
	}
	bob := out[strings.Index(out, "details of amy"):]
	for _, want := range []string{`name="pick"`, `@click="go()"`, `href="/users/bob"`, ">stefan</td>"} {
		if !strings.Contains(bob, want) {
			t.Fatalf("local row changed, missing %s: %s", want, bob)
		}
	}
}

func TestServerColumnGoesAfterTheFirstNamedColumn(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table><thead><tr><th><input type="checkbox"></th><th>ID</th><th><span>Username</span></th><th class="hidden sm:table-cell x">Email</th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(n string) string {
		return `<tr><td><input type="checkbox"></td><td>1</td><td>` + n + `</td><td class="hidden sm:table-cell x">e</td></tr>`
	}
	out := string(mergeTables([]byte(fmt.Sprintf(page, row("bob"))), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(fmt.Sprintf(page, row("amy")))}}, testOpts, open))
	if !strings.Contains(out, `<td>bob</td><td data-cluster-server class="x whitespace-nowrap">stefan</td>`) {
		t.Fatalf("local Server cell isn't after the name or is still hidden: %s", out)
	}
	if !strings.Contains(out, `<span>Username</span></th><th data-cluster-server class="x whitespace-nowrap"><div`) {
		t.Fatalf("header cell isn't after the name: %s", out)
	}
	if !strings.Contains(out, `<td>amy</td><td data-cluster-server class="x whitespace-nowrap"><a href="/open/n?n=">petar</a></td>`) {
		t.Fatalf("remote Server cell isn't after the name: %s", out)
	}
}

func TestServerFilter(t *testing.T) {
	bar := `<html><main id="no_margin_for_iframe"><div class="bar"><div class="box"><input x-model="searchQuery"><div>icon</div></div><button>Status</button></div><table id="t"><thead><tr><th><input type="checkbox"></th><th>Name</th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(n string) string { return `<tr><td><input type="checkbox"></td><td>` + n + `</td></tr>` }
	local := fmt.Sprintf(bar, row("bob"))
	remote := fmt.Sprintf(bar, row("amy"))
	opts := testOpts
	opts.activeID = "local"
	opts.selectFilter = true
	opts.servers = []serverEntry{{ID: "local", Name: "stefan"}, {ID: "n", Name: "petar"}}

	all := string(mergeTables([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, opts, open))
	if !strings.Contains(all, `</div></div><select aria-label="Filter by server"`) || !strings.Contains(all, `</select><button>Status`) {
		t.Fatalf("filter isn't next to the search box: %s", all)
	}
	if !strings.Contains(all, "bob") || !strings.Contains(all, "amy") {
		t.Fatalf("unfiltered page lost rows: %s", all)
	}
	if strings.Contains(all, "new URL(") {
		t.Fatal("URL in an inline handler resolves to document.URL, use window.URL")
	}

	opts.only = "n"
	one := string(mergeTables([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, opts, open))
	if strings.Contains(one, "bob") || !strings.Contains(one, "amy") || !strings.Contains(one, `value="n" selected`) {
		t.Fatalf("filter didn't apply: %s", one)
	}

	empty := string(mergeTables([]byte(local), "stefan", nil, opts, open))
	if strings.Contains(empty, "bob") || !strings.Contains(empty, "Nothing here") {
		t.Fatalf("empty filter result is wrong: %s", empty)
	}
}

func TestMergeLeavesJavascriptTablesAlone(t *testing.T) {
	placeholder := `<tr><td colspan="3">Loading</td></tr>`
	page := []byte(fmt.Sprintf(testPage, placeholder))
	if out := mergeTables(page, "stefan", []remotePage{{id: "n", name: "petar", body: page}}, testOpts, open); out != nil {
		t.Fatalf("js filled table was changed: %s", out)
	}
	tpl := []byte(fmt.Sprintf(testPage, `<template x-for="i in items"><tr><td>x</td></tr></template>`))
	if out := mergeTables(tpl, "stefan", []remotePage{{id: "n", name: "petar", body: tpl}}, testOpts, open); out != nil {
		t.Fatalf("x-for table was changed: %s", out)
	}
}

func TestMergeReplacesLocalPlaceholderWithRemoteRows(t *testing.T) {
	local := fmt.Sprintf(testPage, `<tr><td colspan="3">No users</td></tr>`)
	remote := fmt.Sprintf(testPage, row("2026-10-30 10:00:00", "amy"))
	out := string(mergeTables([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, testOpts, open))
	if strings.Contains(out, "No users") || !strings.Contains(out, "amy") {
		t.Fatalf("placeholder stayed: %s", out)
	}
}

func TestMergeableSkipsConfigAndSessionPages(t *testing.T) {
	for path, want := range map[string]bool{"/users": true, "/notifications": true, "/settings/php": true, "/features/basic": true, "/server/cluster": false, "/dashboard": false, "/login": false, "/logout": false, "/_cluster/state": false} {
		r, _ := http.NewRequest(http.MethodGet, path, nil)
		if got := mergeable(r); got != want {
			t.Errorf("mergeable(%s) = %v", path, got)
		}
	}
	post, _ := http.NewRequest(http.MethodPost, "/users", nil)
	if mergeable(post) {
		t.Error("POST must never be merged")
	}
	jsonReq, _ := http.NewRequest(http.MethodGet, "/users?output=json", nil)
	if mergeable(jsonReq) {
		t.Error("json output must never be merged")
	}
}

func TestServerPickerOpensTheChosenServersForm(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, SeedHex: "aa", Nodes: []Node{{ID: "n1", Name: "petar"}, {ID: "n2", Name: "lazar"}}}}
	m.health = map[string]NodeHealth{"n2": {Online: false}}
	page := []byte(`<main><form id="userForm" class="mt-8"><input name="admin_username"></form></main>`)
	out := string(m.serverPicker(page, "n1", &admindb.User{ID: 1}))
	if !strings.Contains(out, `<form id="userForm" class="mt-8"><div class="mb-4">`) {
		t.Fatalf("picker isn't first in the form: %s", out)
	}
	if !strings.Contains(out, `selected>petar</option>`) || !strings.Contains(out, `disabled>lazar (offline)</option>`) || !strings.Contains(out, `>stefan</option>`) {
		t.Fatalf("wrong options: %s", out)
	}
	if !strings.Contains(out, "/_cluster/open?") || !strings.Contains(out, "sig=") {
		t.Fatalf("options must be signed open links: %s", out)
	}
	if string(m.serverPicker([]byte("<main>no form</main>"), "n1", &admindb.User{ID: 1})) != "<main>no form</main>" {
		t.Fatal("page without the form changed")
	}
}

func TestRowTemplateIsNotAJavascriptTable(t *testing.T) {
	page := []byte(fmt.Sprintf(testPage, `<tr><td><input type="checkbox"></td><td>2026-10-30 09:00:00</td><td><template x-if="a"><a href="/x">m</a></template></td></tr>`))
	if ts := parseTables(page); len(ts) != 1 || ts[0].hasTemplate || !ts[0].realRows() {
		t.Fatalf("row with a template was taken for a js table: %+v", ts)
	}
}

func TestActionablePageKeepsRemoteActionsWorking(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table id="t"><thead><tr><th><input type="checkbox"></th><th>Service</th><th>Actions</th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(tok string) string {
		return `<tr><td><input type="checkbox" name="pick" @change="x()"></td><td>caddy</td><td><form x-data method="POST" action="/services"><input type="hidden" name="csrf_token" value="` + tok + `"><input type="hidden" name="action" value="restart"><button type="button" @click="go($el)">R</button></form></td></tr>`
	}
	opts := testOpts
	opts.actionable = true
	out := string(mergeTables([]byte(fmt.Sprintf(page, row("LOCAL"))), "stefan", []remotePage{{id: "n1", name: "petar", body: []byte(fmt.Sprintf(page, row("REMOTE")))}}, opts, open))

	localAt, remoteAt := strings.Index(out, "LOCAL"), strings.Index(out, "REMOTE")
	if localAt < 0 || remoteAt < 0 {
		t.Fatalf("tokens missing: %s", out)
	}
	remote := out[strings.LastIndex(out[:remoteAt], `<tr data-cluster-node="n1">`):]
	for _, want := range []string{`action="/_cluster/n/n1/services"`, `value="REMOTE"`, `name="action"`, `@click="go($el)"`, `disabled`} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote row misses %s: %s", want, remote)
		}
	}
	if strings.Contains(remote, `name="pick"`) {
		t.Fatalf("bulk checkbox must stay off on remote rows: %s", remote)
	}
	local := out[:strings.Index(out, "petar")]
	if !strings.Contains(local, `action="/services"`) {
		t.Fatalf("local form changed: %s", local)
	}
}

func TestForwardDeniesSessionAndConsoleRoutes(t *testing.T) {
	for _, path := range []string{"/login", "/logout", "/ws/terminal", "/terminal", "/_cluster/rpc", "/api/users"} {
		denied := false
		for _, p := range forwardDenied {
			if strings.HasPrefix(path, p) {
				denied = true
			}
		}
		if !denied {
			t.Errorf("%s must not be forwardable", path)
		}
	}
	if forwardGet["/users"] || forwardGet["/terminal"] {
		t.Error("reads through the forward are an explicit list")
	}
}

func TestBulkSelectionSplitsByServer(t *testing.T) {
	got, remote := bulkTargets([]string{"bob", "cluster:n1:amy", "cluster:n1:a:b", "cluster:local:eve"}, "local")
	if !remote || len(got["local"]) != 2 || len(got["n1"]) != 2 || got["n1"][1] != "a:b" {
		t.Fatalf("got %v", got)
	}
	if _, remote := bulkTargets([]string{"bob", "amy"}, "local"); remote {
		t.Fatal("a selection from one server must stay a normal bulk post")
	}
}

func TestRemoteBulkBoxAndFormsStayUsable(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table id="t"><thead><tr><th><input type="checkbox"></th><th>Name</th><th>Do</th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(n string) string {
		return `<tr><td><input type="checkbox" class="bulk-select-box" value="` + n + `" :checked="!!bulkSelected[&#34;` + n + `&#34;]" @change="bulkToggle($event.target)"></td><td>` + n + `</td><td><form method="POST" action="/users/` + n + `/x"><input type="hidden" name="csrf_token" value="T"><button type="submit" onclick="return confirm('sure?')">Go</button></form><form method="GET" action="/s"></form><button type="button" @click="del('` + n + `')">Del</button></td></tr>`
	}
	out := string(mergeTables([]byte(fmt.Sprintf(page, row("bob"))), "stefan", []remotePage{{id: "n1", name: "petar", body: []byte(fmt.Sprintf(page, row("amy")))}}, testOpts, open))
	remote := out[strings.Index(out, `cluster:n1:amy`)-40:]
	for _, want := range []string{`value="cluster:n1:amy"`, `cluster:n1:amy&#34;]`, `@change="bulkToggle($event.target)"`, `action="/_cluster/n/n1/users/amy/x"`, `name="csrf_token"`, `confirm(`, `action="/s"`} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote row misses %s: %s", want, remote)
		}
	}
	if strings.Contains(remote, `del('amy')`) {
		t.Fatalf("a script calling the page's own server must be off: %s", remote)
	}
}

func TestSaveAnchorOnlyTakesSaveButtons(t *testing.T) {
	help := []byte(`<main id="no_margin_for_iframe"><header><button type="button">Need Help?</button></header></main>`)
	if saveAnchor(help) != -1 {
		t.Fatal("a help button isn't a save button")
	}
	save := []byte(`<main id="no_margin_for_iframe"><header><button class="x">Save settings</button></header></main>`)
	if saveAnchor(save) < 0 {
		t.Fatal("Save text should count")
	}
}

func TestMemberVersionsReplacesInstalledVersion(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", Version: "2.0.19", state: State{Role: RoleMaster, Nodes: []Node{{ID: "n", Name: "petar"}}}}
	m.health = map[string]NodeHealth{"n": {Online: true, Version: "2.0.18"}}
	out := string(m.memberVersions([]byte(`<div id="installed_version" class="x">2.0.19
</div><p>next</p>`)))
	for _, want := range []string{"stefan", "petar", "2.0.18", "<p>next</p>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
}

func TestFilterMatchesThePageFilters(t *testing.T) {
	o := mergeOptions{only: "n", servers: []serverEntry{{ID: "local", Name: "stefan"}, {ID: "n", Name: "petar"}}, filterURL: func(id string) string { return "/users?server=" + id }}
	out := o.filterControl()
	for _, want := range []string{"border-dashed", "Server: petar", `href="/users?server=local"`, "All servers", "cluster-dd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "onchange") {
		t.Fatal("must work without inline js, the terminal csp blocks it")
	}
}

func TestRowsWithoutTheHiddenHeaderCellStayAligned(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table id="p"><thead><tr><th><input type="checkbox"></th><th class="hidden">x</th><th>Pid</th><th>Owner</th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(pid string) string {
		return `<tr><td><input type="checkbox"></td><td>` + pid + `</td><td>root</td></tr>`
	}
	out := string(mergeTables([]byte(fmt.Sprintf(page, row("1"))), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(fmt.Sprintf(page, row("2")))}}, testOpts, open))
	if !strings.Contains(out, `<td>1</td><td data-cluster-server class=" whitespace-nowrap">stefan</td><td>root</td>`) {
		t.Fatalf("Server cell isn't between Pid and Owner: %s", out)
	}
	if !strings.Contains(out, `>petar</a></td><td>root</td>`) {
		t.Fatalf("remote Server cell isn't before Owner: %s", out)
	}
}

func TestPickerPagesAndAnchors(t *testing.T) {
	for path, want := range map[string]bool{"/server/resource-usage": true, "/services/limits": true, "/settings/general": true, "/security/waf": true, "/backups/system": true, "/terminal": true, "/emails/queue": true, "/emails/reports": false, "/user/import": false, "/security/2fa": false, "/users": false, "/domains": false} {
		if got := pickerPage(path); got != want {
			t.Errorf("pickerPage(%s) = %v", path, got)
		}
	}
	term := []byte(`<main id="no_margin_for_iframe"><label>x</label><select id="shell"></select></main>`)
	if at := pickerAnchor(term); !strings.HasPrefix(string(term[at:]), `<select id="shell"`) {
		t.Fatalf("terminal picker isn't before the shell choice: %s", term[at:])
	}
	head := []byte(`<main id="no_margin_for_iframe"><div><h1>T</h1><h2>S</h2></div></main>`)
	if at := pickerAnchor(head); !strings.HasPrefix(string(head[at:]), `</div>`) {
		t.Fatalf("picker isn't under the subtitle: %s", head[at:])
	}
}

func TestMergeEmailsAddsServerDataAndRemoteDomains(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, state: State{Role: RoleMaster, SeedHex: "aa"}}
	page := func(tok, row, dom string) []byte {
		return []byte(`<main id="no_margin_for_iframe"><form method="POST" action="/emails/new"><input type="hidden" name="csrf_token" value="` + tok + `"><select name="domain" required><option value="" disabled>Select a domain</option><option value="` + dom + `">` + dom + `</option></select></form><div><input x-model="searchQuery"></div><script>const EMAIL_DATA = [` + row + `];</script></main>`)
	}
	opts := mergeOptions{activeID: "local", servers: []serverEntry{{ID: "local", Name: "stefan"}, {ID: "n", Name: "petar"}}, filterURL: func(id string) string { return "/x?server=" + id }}
	out := string(m.mergeEmails(page("L", `["a@x.com","1MB",1]`, "x.com"), "local", "stefan", []remotePage{{id: "n", name: "petar", body: page("R", `["b@y.com","2MB",2]`, "y.com")}}, opts, &admindb.User{ID: 1}))
	for _, want := range []string{`["a@x.com","1MB",1,"","stefan",""]`, `"b@y.com","2MB",2,"n","petar","/_cluster/open?`, `<option value="y.com" data-action="/_cluster/n/n/emails/new" data-token="R">y.com (petar)</option>`, "cluster-dd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	opts.only = "n"
	out = string(m.mergeEmails(page("L", `["a@x.com","1MB",1]`, "x.com"), "local", "stefan", []remotePage{{id: "n", name: "petar", body: page("R", `["b@y.com","2MB",2]`, "y.com")}}, opts, &admindb.User{ID: 1}))
	if strings.Contains(out, `"a@x.com"`) || !strings.Contains(out, `"b@y.com"`) {
		t.Fatalf("filter didn't apply: %s", out)
	}
}

func TestApplyAllOnlyCoversSharedSettings(t *testing.T) {
	for path, want := range map[string]bool{
		"/settings/custom-code": true, "/settings/locales": true, "/settings/updates": true, "/features/basic": true, "/plans/new": true, "/plans/12": true, "/plan/delete/x": true, "/api/docker-tags": true,
		"/settings/general": false, "/settings/notifications/test-smtp": false, "/settings/updates/update_now": true, "/settings/defaults/files": false, "/server/reboot": false, "/users": false, "/plans/resellers": false,
	} {
		if got := applyPath(path); got != want {
			t.Errorf("applyPath(%s) = %v", path, got)
		}
	}
	for path, want := range map[string]bool{"/settings/php": true, "/plans/3": true, "/plans": false, "/settings/general": false} {
		if got := applyPage(path); got != want {
			t.Errorf("applyPage(%s) = %v", path, got)
		}
	}
}

func TestReplayRespectsTheFirstServersAnswer(t *testing.T) {
	for _, c := range []struct {
		status   int
		location string
		want     bool
	}{{303, "/settings/php", true}, {200, "", true}, {403, "", false}, {400, "", false}, {303, "/login?next=x", false}, {500, "", false}, {0, "", false}} {
		if got := responseOK(c.status, c.location); got != c.want {
			t.Errorf("responseOK(%d,%q) = %v", c.status, c.location, got)
		}
	}
}

func TestPlanReplayDropsOtherServersIDs(t *testing.T) {
	out := string(withoutUpsell([]byte("name=a&upsell_plan_id=3&ram=1"), "application/x-www-form-urlencoded"))
	if strings.Contains(out, "upsell") || !strings.Contains(out, "ram=1") {
		t.Fatalf("got %s", out)
	}
}

func TestCombineUsageAveragesFromTheNewestSample(t *testing.T) {
	a := []byte(`{"snapshots":[{"ts":"t1","cpu_pct":10,"mem":{"pct":20}},{"ts":"t2","cpu_pct":30,"mem":{"pct":40}}]}`)
	b := []byte(`{"snapshots":[{"ts":"x2","cpu_pct":50,"mem":{"pct":60}}]}`)
	out := combineUsage([]combinedPage{{"local", "s", a}, {"n", "p", b}})
	snaps := out["snapshots"].([]usageSnapshot)
	if len(snaps) != 2 || out["servers"] != 2 {
		t.Fatalf("got %+v", out)
	}
	if snaps[1].CPUPct != 40 || snaps[1].Mem.Pct != 50 || snaps[0].CPUPct != 10 {
		t.Fatalf("wrong averages: %+v", snaps)
	}
}

func TestCombineActivityNamesTheServerAndSortsNewestFirst(t *testing.T) {
	out := combineActivity([]combinedPage{
		{"local", "stefan", []byte(`{"combined_logs":["2026-10-29 10:00:00 x a"]}`)},
		{"n", "petar", []byte(`{"combined_logs":["2026-10-30 10:00:00 x b"]}`)},
	})["combined_logs"]
	if len(out) != 2 || !strings.HasSuffix(out[0], "b (petar)") || !strings.HasSuffix(out[1], "a (stefan)") {
		t.Fatalf("got %v", out)
	}
}

func TestCombineTasksLinksRemoteOnesThroughTheirServer(t *testing.T) {
	out := combineTasks([]combinedPage{
		{"local", "stefan", []byte(`[{"pid":1,"status":"running"}]`)},
		{"n", "petar", []byte(`[{"pid":2,"status":"running"}]`)},
	}, func(id, pid string) string { return "/open/" + id + "/" + pid })
	if out[0]["server"] != "stefan" || out[0]["link"] != nil || out[1]["link"] != "/open/n/2" {
		t.Fatalf("got %v", out)
	}
}

func TestSaveAnchorIsBeforeTheHeadersLastButton(t *testing.T) {
	page := []byte(`<main id="no_margin_for_iframe"><header><a href="/x">Help</a><button>One</button><button type="submit">Save</button></header><button>Other</button></main>`)
	if at := saveAnchor(page); !strings.HasPrefix(string(page[at:]), `<button type="submit">Save`) {
		t.Fatalf("anchor is at: %s", page[at:])
	}
	if at := saveAnchor([]byte(`<main id="no_margin_for_iframe"><header><h1>T</h1></header><button>Other</button></main>`)); at != -1 {
		t.Fatalf("a button outside the header must not count, got %d", at)
	}
}

func TestRightAnchorForPagesWithoutASaveButton(t *testing.T) {
	on := []byte(`<main id="no_margin_for_iframe"><header><div class="grow"><h1>API</h1></div><div class="flex flex-none items-center"><a href="/x">Spec</a><form method="POST"><button>Disable</button></form></div></header></main>`)
	if at := rightAnchor(on); !strings.HasPrefix(string(on[at:]), `<a href="/x">Spec`) {
		t.Fatalf("should open the right block, not sit inside the form: %s", on[at:])
	}
	off := []byte(`<main id="no_margin_for_iframe"><header><div class="grow"><h1>API</h1></div><div class="flex flex-none items-center"></div></header></main>`)
	if at := rightAnchor(off); !strings.HasPrefix(string(off[at:]), `</div></header>`) {
		t.Fatalf("empty right block should take it: %s", off[at:])
	}
	save := []byte(`<main id="no_margin_for_iframe"><header><div class="flex flex-none"><a href="/h">Help</a><button type="submit">Save</button></div></header></main>`)
	if at := rightAnchor(save); !strings.HasPrefix(string(save[at:]), `<button type="submit">Save`) {
		t.Fatalf("save button still comes first: %s", save[at:])
	}
	if rightAnchor([]byte(`<main id="no_margin_for_iframe"><h1>T</h1></main>`)) != -1 {
		t.Fatal("no header means the title fallback")
	}
}

func TestGlobalSortUsesThePagesColumn(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table id="t"><thead><tr><th><input type="checkbox"></th><th><a href="/x?sort=name&direction=asc">Name</a></th><th><a href="/x?sort=disk">Disk</a></th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(n, d string) string {
		return `<tr><td><input type="checkbox"></td><td>` + n + `</td><td>` + d + `</td></tr>`
	}
	opts := testOpts
	opts.sortTime, opts.sortKey, opts.sortDir = false, "disk", "desc"
	out := string(mergeTables([]byte(fmt.Sprintf(page, row("bob", "9"))), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(fmt.Sprintf(page, row("amy", "10")))}}, opts, open))
	if strings.Index(out, "<td>amy") > strings.Index(out, "<td>bob") {
		t.Fatalf("10 should sort above 9 across servers: %s", out)
	}
}

func TestCountsCoverEveryServer(t *testing.T) {
	page := []byte(`<b>3</b> domains hosted on this server. <b>2</b> user accounts on this server. <b>1</b> website in Site Manager across all users. Showing <span class="a" x-text="count"></span> of <span class="a">3</span>`)
	out := string(fixCounts(page, 5, 3))
	for _, want := range []string{"<b>5</b> domains across <b>3</b> servers.", "<b>5</b> user accounts across <b>3</b> servers.", "<b>5</b> website in Site Manager across all users on all servers", `of <span class="a">5</span>`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
}

func TestRemoteScriptButtonOpensItsServer(t *testing.T) {
	r := []byte(`<tr><td><input type="checkbox"></td><td>amy</td><td><button type="button" id="d" @click="del('amy')">Del</button></td></tr>`)
	head := `<table><thead><tr><th>a</th><th>b</th><th>c</th></tr></thead><tbody>`
	spans := parseTables([]byte(head + string(r) + `</tbody></table>`))
	if len(spans) != 1 {
		t.Fatal("no table")
	}
	out := string(remoteRow(r, len(head), spans[0].rows[0], 2, "petar", func(string) string { return "/open/n" }, "n", false))
	if strings.Contains(out, "del(") || !strings.Contains(out, "window.location.href=") || !strings.Contains(out, "open") {
		t.Fatalf("got %s", out)
	}
}

func TestAnswerErrorCatchesRefusalsIn200s(t *testing.T) {
	for body, bad := range map[string]bool{`{"success":false,"message":"no"}`: true, `{"error":"x"}`: true, `{"success":true}`: false, `<html></html>`: false, `{"scheduled":true}`: false} {
		if (answerError([]byte(body)) != nil) != bad {
			t.Errorf("answerError(%s)", body)
		}
	}
}

func TestCombinedHeaderStats(t *testing.T) {
	cpu, _ := json.Marshal(combineCPU([]combinedPage{{"l", "stefan", []byte(`{"cpu_0":10,"cpu_1":30}`)}, {"n", "petar", []byte(`{"cpu_0":50}`)}}))
	if !strings.Contains(string(cpu), `"name":"stefan","avg":20,"cores":2`) || !strings.Contains(string(cpu), `"name":"petar","avg":50,"cores":1`) {
		t.Fatalf("cpu %s", cpu)
	}
	load := combineLoad([]combinedPage{{"l", "s", []byte(`{"load1min":1,"load5min":2,"load15min":3}`)}, {"n", "p", []byte(`{"load1min":3,"load5min":4,"load15min":5}`)}})
	if load["load1min"] != 3.0 {
		t.Fatalf("load %v", load)
	}
	disk := combineDisk([]combinedPage{{"l", "s", []byte(`[{"mountpoint":"/","used":10,"total":100,"percent":10},{"mountpoint":"/mnt/x","used":1,"total":2}]`)}, {"n", "p", []byte(`[{"mountpoint":"/","used":30,"total":100,"percent":30}]`)}})
	if len(disk) != 3 || disk[0].Percent != 30 || disk[1].Device != "s" {
		t.Fatalf("disk %+v", disk)
	}
	mem := combineMemory([]combinedPage{{"l", "s", []byte(`{"human_readable":{"ram":{"percent":"40%"}}}`)}, {"n", "p", []byte(`{"human_readable":{"ram":{"percent":"60%"}}}`)}})
	if mem["human_readable"].(memoryHuman).RAM.Percent != "60.0%" {
		t.Fatalf("mem %v", mem)
	}
}

func TestHostKeyCheckAcceptsCommonTypesOnly(t *testing.T) {
	for key, want := range map[string]bool{"ssh-rsa AAAA": true, "ssh-ed25519 AAAA": true, "ecdsa-sha2-nistp256 AAAA": true, "ssh-rsa AAAA\n1.2.3.4 ssh-rsa EVIL": false, "bad AAAA": false} {
		if hostKeyRe.MatchString(key) != want {
			t.Errorf("hostKeyRe(%q) != %v", key, want)
		}
	}
}

func TestTitleAnchorIsBeforeTheTitle(t *testing.T) {
	page := []byte(`<main id="no_margin_for_iframe"><div><h1 class="x">Swap</h1></div></main>`)
	if at := titleAnchor(page); !strings.HasPrefix(string(page[at:]), "<h1") {
		t.Fatalf("at %s", page[at:])
	}
}

func TestTransferPickerListsOtherMembers(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, Nodes: []Node{{ID: "n1", Name: "petar"}, {ID: "n2", Name: "lazar"}}}}
	m.health = map[string]NodeHealth{"n2": {Online: false}}
	out := string(m.transferPicker([]byte(`<form method="POST" id="transfer-form" action="/import/transfer/"><input name="host"></form>`), "rasa", "n1"))
	for _, want := range []string{`data-account="rasa"`, `data-from="n1"`, "Another server over SSH", `value="local"`, `disabled>lazar (offline)`, `<input name="host">`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, `value="n1"`) {
		t.Fatalf("the account's own server can't be a destination: %s", out)
	}
}

func TestPlainAndHoverButtonsStayOnRemoteRows(t *testing.T) {
	r := []byte(`<tr><td><input type="checkbox"></td><td>amy</td><td><div x-data="{open:false}" @mouseenter="open = true" @mouseleave="open = false"><button type="button" class="menu">Actions</button><a href="/x">Go</a></div><button type="button" @click="del('amy')">D</button></td></tr>`)
	head := `<table><thead><tr><th>a</th><th>b</th><th>c</th></tr></thead><tbody>`
	spans := parseTables([]byte(head + string(r) + `</tbody></table>`))
	out := string(remoteRow(r, len(head), spans[0].rows[0], 2, "petar", func(string) string { return "/open/n" }, "n", false))
	for _, want := range []string{`@mouseenter="open = true"`, `<button type="button" class="menu">Actions</button>`, `href="/open/n"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "Open this server") || strings.Contains(out, "Opens this server") {
		t.Fatalf("no such note anymore: %s", out)
	}
}

func TestServicesShowOnceWithAColumnPerServer(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><div><input x-model="searchQuery"></div><table id="exiting_users"><thead><tr><th><input type="checkbox"></th><th>Service</th><th>Status</th><th>Real Name</th><th></th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(name, status, token string) string {
		return `<tr x-show="a"><td><input type="checkbox" class="bulk-select-box" value="` + name + `"></td><td>` + name + `</td><td class="s">` + status + `</td><td>` + name + `</td><td><form method="POST" action="/services"><input type="hidden" name="csrf_token" value="` + token + `"><button type="button" @click="go($el)">R</button></form></td></tr>`
	}
	local := fmt.Sprintf(page, row("caddy", "up-L", "L")+row("bind", "up-L", "L"))
	remote := fmt.Sprintf(page, row("caddy", "down-R", "R")+row("extra", "up-R", "R"))
	opts := testOpts
	opts.activeID = "local"
	out := string(mergeServices([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, opts, open))
	if c := strings.Count(out, `<td>caddy<span class="block text-xs`); c != 1 {
		t.Fatalf("caddy should show once with its details under the name, got %d: %s", c, out)
	}
	if strings.Contains(out, "<th>Real Name</th>") {
		t.Fatalf("real name folds into the service cell: %s", out)
	}
	for _, want := range []string{">stefan</th>", ">petar</th>", "up-L", "down-R", `action="/_cluster/n/n/services"`, `value="R"`, `action="/services"`, `<td class="s">-</td>`, "<td>extra<span"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "<th>Status</th>") {
		t.Fatalf("the single status column should be gone: %s", out)
	}
	opts.only = "n"
	one := string(mergeServices([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, opts, open))
	if strings.Contains(one, "up-L") || !strings.Contains(one, "down-R") {
		t.Fatalf("filter didn't apply: %s", one)
	}
}

func TestCreateFormPickerUsesTheUsernameForm(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, SeedHex: "aa", Nodes: []Node{{ID: "n1", Name: "petar"}}}}
	page := []byte(`<form method="POST" class="inline"><input name="x"></form><form method="POST"><input name="username"></form>`)
	out := string(m.createFormPicker(page, "/resellers", "local", &admindb.User{ID: 1}))
	if !strings.Contains(out, `<form method="POST"><div class="mb-4">`) || strings.Contains(out, `class="inline"><div`) {
		t.Fatalf("picker isn't in the create form: %s", out)
	}
}

func TestTemplatePagesGetPickerAndApply(t *testing.T) {
	for _, path := range []string{"/domains/file-templates", "/domains/vhost-templates", "/domains/zone-templates"} {
		if !pickerPage(path) || !applyPage(path) || tablesMerge(path) {
			t.Errorf("%s should have a picker and apply box and no merged table", path)
		}
	}
	if !pickerPage("/domains/ratelimit") || !tablesMerge("/domains/ratelimit") {
		t.Error("rate limit keeps its merged list and gets a picker")
	}
}

func TestDecorateAddsControlsInsideForms(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, Nodes: []Node{{ID: "n", Name: "petar"}}}}
	page := []byte(`<form x-show="showScan" method="POST" action="/websites/scan"><input name="username"><button type="submit">Scan</button></form>`)
	out := string(m.decoratePage(page, "/websites", "local", &admindb.User{ID: 1}))
	if !strings.Contains(out, `<option value="">All servers</option>`) || !strings.Contains(out, `</select></div><button type="submit">Scan`) {
		t.Fatalf("got %s", out)
	}
	rate := []byte(`<form method="POST" action="/domains/ratelimit"><input type="hidden" name="csrf_token" value="t"><input type="hidden" name="action" value="default"><button type="submit" class="x">Save</button></form><form method="POST" action="/domains/ratelimit"><input type="hidden" name="csrf_token" value="t"><input type="hidden" name="action" value="set"><button type="submit">One</button></form>`)
	out = string(m.decoratePage(rate, "/domains/ratelimit", "local", &admindb.User{ID: 1}))
	if strings.Count(out, "cluster-apply-all") != 1 || !strings.Contains(out, `checked> Apply on all servers</label></div><button type="submit" class="x">`) {
		t.Fatalf("got %s", out)
	}
}

func TestEmailSettingsFormsGetTheirOwnControls(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, SeedHex: "aa", Nodes: []Node{{ID: "n", Name: "petar"}}}}
	page := []byte(`<form><button type="submit" :disabled="locked" id="save_path_btn">Save</button></form><h2>Enable Services</h2><form><button type="submit" class="a">Save stack</button></form><form id="relay-form"><button type="submit" class="b">Save relay</button></form><form action="/x"><button type="submit" class="c">Other</button></form>`)
	out := string(m.decoratePage(page, "/emails/settings", "local", &admindb.User{ID: 1}))
	if strings.Count(out, "cluster-apply-all") != 3 || strings.Count(out, "cluster-dd") != 3 {
		t.Fatalf("each shared form needs a box and a menu: %s", out)
	}
	if strings.Contains(out, `</div><button type="submit" class="c">`) {
		t.Fatalf("other forms stay without: %s", out)
	}
}

func TestDomainLimitPanelsGetAServerMenu(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, SeedHex: "aa", Nodes: []Node{{ID: "n", Name: "petar"}}}}
	out := string(m.decoratePage([]byte(`<label for="restore_user">a</label><label for="remove_user">b</label>`), "/emails/domain-limits", "n", &admindb.User{ID: 1}))
	if strings.Count(out, "Server: petar") != 2 {
		t.Fatalf("got %s", out)
	}
}

func TestUserLabelNeedsARealServer(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{loaded: true, Hostname: "stefan", state: State{Role: RoleMaster, Nodes: []Node{{ID: "n", Name: "lazar"}}}}
	for label, want := range map[string][2]string{
		"bob (lazar)": {"bob", "n"}, "bob (stefan)": {"bob", LocalNode}, "bob (other)": {"", ""}, "bob": {"", ""},
	} {
		name, id, _ := m.splitUserLabel(label)
		if name != want[0] || id != want[1] {
			t.Errorf("%s -> %s %s", label, name, id)
		}
	}
}

func TestFTPListHasNoServerControls(t *testing.T) {
	if pickerPage("/services/ftp") || !pickerPage("/services/ftp/settings") {
		t.Fatal("ftp list has no picker, its settings do")
	}
	opts := testOpts
	opts.noFilter, opts.forceColumn = true, true
	page := `<html><main id="no_margin_for_iframe"><div><input x-model="searchQuery"></div><table id="t"><thead><tr><th><input type="checkbox"></th><th>Account</th></tr></thead><tbody><tr><td colspan="2">none</td></tr></tbody></table></main></html>`
	out := string(mergeTables([]byte(page), "stefan", nil, opts, open))
	if !strings.Contains(out, "<span>Server</span>") || strings.Contains(out, "cluster-dd") || strings.Contains(out, "Filter by server") {
		t.Fatalf("got %s", out)
	}
}

func TestRemoteMenuButtonKnowsItsServer(t *testing.T) {
	r := []byte(`<tr><td><input type="checkbox"></td><td>amy</td><td><button type="button" class="plan-menu-btn x" data-id="3">m</button></td></tr>`)
	head := `<table><thead><tr><th>a</th><th>b</th><th>c</th></tr></thead><tbody>`
	spans := parseTables([]byte(head + string(r) + `</tbody></table>`))
	out := string(remoteRow(r, len(head), spans[0].rows[0], 2, "petar", func(string) string { return "/open/n" }, "n", false))
	if !strings.Contains(out, `data-cluster-node="n"`) || !strings.Contains(out, `data-id="3"`) {
		t.Fatalf("got %s", out)
	}
}

func TestWholeListNotificationActionsGoEverywhere(t *testing.T) {
	if !alwaysApplyPaths["/notifications/mark_as_read/9999"] || !alwaysApplyPaths["/notifications/delete/9999"] {
		t.Fatal("acknowledge and delete all must reach every server")
	}
	if alwaysApplyPaths["/notifications/mark_as_read/3"] {
		t.Fatal("a single row belongs to its own server")
	}
}

func TestFlashOfReadsTheServersMessage(t *testing.T) {
	page := []byte(`<div id="alert"><span class="sr-only">Notification</span>
  <div class="x">Docroot <b>must</b> start
  </div><button type="button"><span class="sr-only">Dismiss</span></button></div>`)
	if got := flashOf(page); got != "Docroot must start" {
		t.Fatalf("got %q", got)
	}
	if flashOf([]byte("<div>none</div>")) != "" {
		t.Fatal("no flash, no message")
	}
}

func TestRightAnchorPrefersTheHeaderTabs(t *testing.T) {
	page := []byte(`<main id="no_margin_for_iframe"><header><div class="flex items-center"><div class="grow"><h1>Usage</h1></div><div role="tablist" class="x"><a>Load</a></div></div></header></main>`)
	if at := rightAnchor(page); !strings.HasPrefix(string(page[at:]), `<div role="tablist"`) {
		t.Fatalf("at %s", page[at:])
	}
}

func TestCronsAreAPerServerPageWithApply(t *testing.T) {
	if tablesMerge("/server/crons") || !pickerPage("/server/crons") || !applyPage("/server/crons") || !perServerDefault["/server/crons"] {
		t.Fatal("crons: no merged table, a picker and an apply box that starts off")
	}
}

func TestWafFleetRequests(t *testing.T) {
	post := func(path, body string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	rec := httptest.NewRecorder()
	if !goesEverywhere(rec, post("/security/waf", "action=update")) || !goesEverywhere(rec, post("/security/waf/rules", "rule_name=x&action=disable")) || !goesEverywhere(rec, post("/security/waf/rules/bulk", "{}")) {
		t.Error("shared rule sets must reach every server")
	}
	if goesEverywhere(rec, post("/security/waf", "status=off")) || goesEverywhere(rec, post("/users", "")) {
		t.Error("turning the waf off and other pages stay on one server")
	}
	kept := post("/security/waf", "action=update")
	goesEverywhere(rec, kept)
	if raw, _ := io.ReadAll(kept.Body); string(raw) != "action=update" {
		t.Errorf("the handler must still get the body, got %q", raw)
	}
	if tablesMerge("/security/waf") || tablesMerge("/security/waf/rules") || !tablesMerge("/security/waf/domains") {
		t.Error("no server column on shared waf lists")
	}
}

func TestInviteIsSingleUse(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{}
	secret := randomHex(16)
	if err := writeJSONFile(invitesPath(), []joinFile{
		{Hash: hashSecret(secret), Expires: time.Now().Add(time.Hour)},
		{Hash: hashSecret("old"), Expires: time.Now().Add(-time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	if m.takeInvite("old") {
		t.Fatal("expired invite was accepted")
	}
	if m.takeInvite("wrong") {
		t.Fatal("unknown invite was accepted")
	}
	if !m.takeInvite(secret) {
		t.Fatal("valid invite was refused")
	}
	if m.takeInvite(secret) {
		t.Fatal("invite worked twice")
	}
}

func TestEnrollRejectsBadInvite(t *testing.T) {
	for _, code := range []string{"", "opinvite-!!", invitePrefix + "e30"} {
		if _, err := Enroll(code, "2087", "", ""); err == nil || err.Error() != "invalid invite" {
			t.Fatalf("%q: got %v", code, err)
		}
	}
}

func TestServicesRestartBadgeShowsUnderItsServer(t *testing.T) {
	page := `<html><main id="no_margin_for_iframe"><table id="exiting_users"><thead><tr><th>Service</th><th>Status</th><th>Real Name</th><th></th></tr></thead><tbody>%s</tbody></table></main></html>`
	row := func(badge, status string) string {
		return `<tr><td>OpenPanel` + badge + `</td><td class="s">` + status + `</td><td>openpanel</td><td></td></tr>`
	}
	badge := `<span class="b">RESTART NEEDED</span>`
	local := fmt.Sprintf(page, row(badge, "up-L"))
	remote := fmt.Sprintf(page, row("", "up-R"))
	opts := testOpts
	opts.activeID = "local"
	out := string(mergeServices([]byte(local), "stefan", []remotePage{{id: "n", name: "petar", body: []byte(remote)}}, opts, open))
	if c := strings.Count(out, "RESTART NEEDED"); c != 1 {
		t.Fatalf("badge should show once, got %d: %s", c, out)
	}
	if !strings.Contains(out, "up-L"+badge) {
		t.Fatalf("badge should sit in the server's own column: %s", out)
	}
}

func TestPausedServerIsLeftOut(t *testing.T) {
	Dir = t.TempDir()
	m := &Manager{}
	if err := m.update(func(s *State) error {
		s.Role = RoleMaster
		s.Nodes = []Node{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPaused("b", true); err != nil {
		t.Fatal(err)
	}
	if live := m.LiveNodes(); len(live) != 1 || live[0].ID != "a" {
		t.Fatalf("paused server still live: %+v", live)
	}
	if _, ok := m.liveNodeByID("b"); ok {
		t.Fatal("paused server found for an action")
	}
	if n, ok := m.nodeByID("b"); !ok || !n.Paused {
		t.Fatal("paused server should stay in the cluster")
	}
	if err := m.SetPaused("b", false); err != nil || len(m.LiveNodes()) != 2 {
		t.Fatalf("resume failed: %v", err)
	}
}

func TestMain(m *testing.M) {
	Notify = func(string, string, string) {}
	os.Exit(m.Run())
}

func TestVersionProblem(t *testing.T) {
	for _, c := range []struct {
		slave, master string
		ok            bool
	}{
		{"2.0.20", "2.0.20", true},
		{"2.0.20", "2.0.21", true},
		{"2.0.21", "2.0.20", false},
		{"2.0.19", "2.0.20", false},
		{"2.0.20", "2.0.19", false},
		{"2.0.20", "", false},
	} {
		if got := versionProblem(c.slave, c.master) == ""; got != c.ok {
			t.Errorf("slave %s master %s: ok=%v, want %v", c.slave, c.master, got, c.ok)
		}
	}
}

func TestRunOpencliRejectsUnlistedCommands(t *testing.T) {
	for _, args := range [][]string{{}, {"docker-update"}, {"user-list", "a\x00b"}, {"email-manage", "rm", "-rf", "/"}} {
		raw, _ := json.Marshal(args)
		if _, err := runOpencli(raw); err == nil {
			t.Fatalf("runOpencli(%q) should fail", args)
		}
	}
}

func TestPickNodesMatchesShortNameAndSkipsPaused(t *testing.T) {
	m := &Manager{loaded: true, state: State{Role: RoleMaster, Nodes: []Node{
		{ID: "a1", Name: "server2.example.com"},
		{ID: "b2", Name: "server3.example.com", Paused: true},
	}}}
	if n, err := m.pickNodes("server2"); err != nil || len(n) != 1 || n[0].ID != "a1" {
		t.Fatalf("short name: %v %v", n, err)
	}
	if _, err := m.pickNodes("server3.example.com"); err == nil {
		t.Fatal("a paused server should be refused")
	}
	if n, _ := m.pickNodes("all"); len(n) != 1 {
		t.Fatalf("all should skip paused servers, got %v", n)
	}
	if _, err := m.pickNodes("nope"); err == nil {
		t.Fatal("unknown server should fail")
	}
}

func TestSortDirectionReadsDashPrefix(t *testing.T) {
	for q, want := range map[string]string{"sort=-pid": "desc", "sort=pid": "", "sort=name&direction=desc": "desc", "sort=name&direction=asc": "asc"} {
		v, _ := url.ParseQuery(q)
		if got := sortDirection(v); got != want {
			t.Fatalf("%s: got %q want %q", q, got, want)
		}
	}
}

func TestStoreHealthWaitsForRepeatedFailures(t *testing.T) {
	m := &Manager{}
	m.storeHealth(map[string]NodeHealth{"n": {Online: true, Version: "2.0.20"}})
	for i := 1; i < offlineAfter; i++ {
		m.storeHealth(map[string]NodeHealth{"n": {Error: "context deadline exceeded"}})
		if h, _ := m.nodeHealth("n"); !h.Online || h.Version != "2.0.20" {
			t.Fatalf("offline after %d failed polls", i)
		}
	}
	m.storeHealth(map[string]NodeHealth{"n": {Error: "context deadline exceeded"}})
	if h, _ := m.nodeHealth("n"); h.Online {
		t.Fatal("still online after offlineAfter failed polls")
	}
	m.storeHealth(map[string]NodeHealth{"n": {Online: true}})
	m.storeHealth(map[string]NodeHealth{"n": {}})
	if h, _ := m.nodeHealth("n"); !h.Online {
		t.Fatal("a good poll must reset the failure count")
	}
	m2 := &Manager{}
	m2.storeHealth(map[string]NodeHealth{"n": {}})
	if h, _ := m2.nodeHealth("n"); h.Online {
		t.Fatal("a server never seen online must show offline right away")
	}
}
