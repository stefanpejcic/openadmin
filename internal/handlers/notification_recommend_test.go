package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openadmin/internal/auth"
)

func writeSnapshots(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sentinel_snapshots.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	return p
}

func TestReadTrafficPeaks(t *testing.T) {
	p := writeSnapshots(t,
		`{"ts":"2026-10-01 00:00:00","web_conn":40}`,
		`{"ts":"2026-10-01 12:00:00","web_conn":310}`,
		`not json`,
		`{"ts":"2026-10-02 00:00:00","web_conn":90,"web_conn_ip":12}`,
		`{"ts":"2026-10-03 06:00:00","web_conn":120,"web_conn_ip":35}`,
		`{"ts":"2026-10-03 12:00:00","web_conn":100,"web_conn_ip":20}`,
	)
	got := readTrafficPeaks(p)
	if got.Total != 310 || got.TotalAt.Format(snapshotTimeLayout) != "2026-10-01 12:00:00" {
		t.Fatalf("total peak = %d at %v", got.Total, got.TotalAt)
	}
	if got.PerIP != 35 || got.PerIPAt.Format(snapshotTimeLayout) != "2026-10-03 06:00:00" {
		t.Fatalf("per-IP peak = %d at %v", got.PerIP, got.PerIPAt)
	}
	if got.TotalSpan != 60*time.Hour || got.PerIPSpan != 36*time.Hour {
		t.Fatalf("spans = %v / %v", got.TotalSpan, got.PerIPSpan)
	}
	if empty := readTrafficPeaks(filepath.Join(t.TempDir(), "missing")); empty.Total != 0 || empty.TotalSpan != 0 {
		t.Fatalf("expected zero peaks for a missing file, got %+v", empty)
	}
}

func TestNiceCeil(t *testing.T) {
	for in, want := range map[int]int{1: 10, 47: 50, 100: 100, 101: 150, 620: 650, 1001: 1100} {
		if got := niceCeil(in); got != want {
			t.Fatalf("niceCeil(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestTrafficAdvice(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if a := trafficAdvice(310, at, 72*time.Hour, 500, 0, "total connections"); a.Recommended != 0 || !strings.Contains(a.Hint, "No domains") {
		t.Fatalf("expected no recommendation without domains, got %+v", a)
	}
	if a := trafficAdvice(310, at, 5*time.Hour, 500, 3, "total connections"); a.Recommended != 0 || !strings.Contains(a.Hint, "5h so far") {
		t.Fatalf("expected a wait message under 24h, got %+v", a)
	}
	a := trafficAdvice(310, at, 72*time.Hour, 500, 3, "total connections")
	if a.Recommended != 650 || a.Peak != 310 || !strings.Contains(a.Hint, "last 3 days: 310 on Oct 1 12:00 UTC") {
		t.Fatalf("expected 2x the peak rounded up, got %+v", a)
	}
	if a := trafficAdvice(4, at, 48*time.Hour, 50, 3, "connections from one IP"); a.Recommended != 50 {
		t.Fatalf("expected the floor on quiet servers, got %+v", a)
	}
	if a := trafficAdvice(0, at, 48*time.Hour, 500, 3, "total connections"); a.Peak != 0 || a.WarnText != "" {
		t.Fatalf("expected no warning when nothing was recorded, got %+v", a)
	}
}

func TestServiceAdvice(t *testing.T) {
	dir := t.TempDir()
	orig := csfBinaryPath
	t.Cleanup(func() { csfBinaryPath = orig })

	if off, _ := serviceAdvice("mysql", nil); !strings.Contains(off, "Recommended: on") {
		t.Fatalf("expected core services recommended on, got %q", off)
	}
	if off, on := serviceAdvice("named", []string{"ftp", "dns"}); off == "" || on != "" {
		t.Fatalf("expected BIND9 recommended when the dns module is on, got %q / %q", off, on)
	}
	if off, on := serviceAdvice("named", []string{"ftp"}); off != "" || !strings.Contains(on, "disabled") {
		t.Fatalf("expected a note when BIND9 is on without the dns module, got %q / %q", off, on)
	}

	csfBinaryPath = filepath.Join(dir, "csf")
	if off, on := serviceAdvice("csf", nil); off != "" || !strings.Contains(on, "isn't installed") {
		t.Fatalf("expected a note when CSF is missing, got %q / %q", off, on)
	}
	os.WriteFile(csfBinaryPath, []byte("#!/bin/sh\n"), 0755)
	if off, _ := serviceAdvice("csf", nil); !strings.Contains(off, "installed") {
		t.Fatalf("expected CSF recommended when installed, got %q", off)
	}
}

func TestGroupUserActions(t *testing.T) {
	var actions []notifToggleView
	for _, a := range notifUserActionOrder {
		actions = append(actions, notifToggleView{Key: a.Key})
	}
	groups := groupUserActions(actions)
	var names []string
	total := 0
	for _, g := range groups {
		names = append(names, g.Name)
		total += len(g.Actions)
	}
	if got := strings.Join(names, ","); got != "OpenAdmin,Web Firewall,Users,FTP,Domains,ImunifyAV" {
		t.Fatalf("groups = %s", got)
	}
	if total != len(notifUserActionOrder) {
		t.Fatalf("expected every action in a group, got %d of %d", total, len(notifUserActionOrder))
	}
}

func TestWhitelistSuggestions(t *testing.T) {
	log := filepath.Join(t.TempDir(), "login.log")
	os.WriteFile(log, []byte(strings.Join([]string{
		"2026-10-01 10:00:00 stefan 198.51.100.7",
		"2026-10-02 10:00:00 alice 198.51.100.7",
		"2026-10-03 10:00:00 stefan 192.0.2.50",
		"2026-10-04 10:00:00 stefan 203.0.113.9 (sso)",
		"2026-10-05 10:00:00 bob 10.0.0.5",
		"2026-10-05 11:00:00 bob <script>",
		"garbage",
	}, "\n")), 0644)

	got := whitelistSuggestions("203.0.113.9", log, []string{"10.0.0.0/8"})
	var lines []string
	for _, s := range got {
		lines = append(lines, s.IP+" "+s.Note)
	}
	want := []string{
		"203.0.113.9 your current IP",
		"192.0.2.50 stefan, last login 2026-10-03",
		"198.51.100.7 alice, stefan, last login 2026-10-02",
	}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("suggestions =\n%q\nwant\n%q", lines, want)
	}

	if got := whitelistSuggestions("127.0.0.1", log, []string{"0.0.0.0/0"}); len(got) != 0 {
		t.Fatalf("expected nothing when everything is already covered, got %v", got)
	}
}

func TestNotificationSettingsRendersRecommendations(t *testing.T) {
	_, notifPath, _ := withScratchNotificationConfig(t)
	os.WriteFile(notifPath, []byte("[DEFAULT]\nservices=panel\nattack=yes\n[HTTP]\nmax_total_conn=300\n"), 0644)

	origSnap := SentinelSnapshotsPath
	SentinelSnapshotsPath = writeSnapshots(t,
		`{"ts":"2026-10-01 00:00:00","web_conn":40,"web_conn_ip":3}`,
		`{"ts":"2026-10-02 06:00:00","web_conn":310,"web_conn_ip":12}`,
	)
	origDomains := notifDomainCountRun
	notifDomainCountRun = func(_ *sql.DB) int { return 2 }
	origLogin := LoginLogPath
	LoginLogPath = filepath.Join(t.TempDir(), "login.log")
	os.WriteFile(LoginLogPath, []byte("2026-10-01 10:00:00 stefan 198.51.100.7\n"), 0644)
	t.Cleanup(func() { SentinelSnapshotsPath, notifDomainCountRun, LoginLogPath = origSnap, origDomains, origLogin })

	ns := &NotificationSettings{Sessions: auth.NewManager("test-secret", false)}
	srv := httptest.NewServer(newNotificationSettingsMux(ns))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/settings/notifications", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{
		"Highest total connections in the last day: 310 on Oct 2 06:00 UTC. Recommended: 650.",
		"Number(v) <= 310",
		"Highest connections from one IP in the last day: 12",
		"Recommended: on, OpenPanel depends on this service.",
		`x-data="actionGroup" data-group="Web Firewall"`,
		"+ 203.0.113.4",
		"+ 198.51.100.7",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in the page", want)
		}
	}
}
