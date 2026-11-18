package handlers

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSummaryWAFFor(t *testing.T) {
	dir := t.TempDir()
	oa, od := wafAuditDir, domainLogsDir
	t.Cleanup(func() { wafAuditDir, domainLogsDir = oa, od })
	wafAuditDir, domainLogsDir = filepath.Join(dir, "waf"), filepath.Join(dir, "domlogs")
	os.MkdirAll(wafAuditDir, 0o755)
	os.MkdirAll(filepath.Join(domainLogsDir, "a.com"), 0o755)

	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	ns := strconv.FormatInt(at.UnixNano(), 10)
	rce := `{"error_message":"[client \"172.70.1.1\"] Coraza: Warning. RCE [id \"932235\"] [msg \"Remote Command Execution: Unix Command Injection\"] [severity \"critical\"]"}`
	score := `{"error_message":"[id \"949110\"] [msg \"Inbound Anomaly Score Exceeded\"]"}`
	audit := `{"transaction":{"unix_timestamp":` + ns + `,"client_ip":"172.70.1.1","server_id":"a.com","request":{"uri":"/x.php"},"is_interrupted":true},"messages":[` + rce + `,` + score + `]}
{"transaction":{"unix_timestamp":` + ns + `,"client_ip":"172.70.1.1","server_id":"a.com","request":{"uri":"/ok"},"is_interrupted":false}}
`
	os.WriteFile(filepath.Join(wafAuditDir, "a.com.log.1"), []byte(audit), 0o644)
	ts := strconv.FormatFloat(float64(at.UnixNano())/1e9+0.3, 'f', 3, 64)
	access := `{"ts":` + ts + `,"request":{"remote_ip":"172.70.1.1","client_ip":"172.70.1.1","host":"a.com","uri":"/x.php","headers":{"Cf-Connecting-Ip":["43.165.5.32"],"Cf-Ipcountry":["DE"]}},"status":403}
`
	os.WriteFile(filepath.Join(domainLogsDir, "a.com", "access.log.1"), []byte(access), 0o644)

	w := summaryWAFFor(nil, "2026-11-18")
	if w == nil || w.Blocked != 1 {
		t.Fatalf("waf: %+v", w)
	}
	if len(w.Rules) != 1 || w.Rules[0].ID != "932235" {
		t.Errorf("rules, the anomaly score rule doesn't count: %+v", w.Rules)
	}
	if len(w.IPs) != 1 || w.IPs[0].IP != "43.165.5.32" || w.IPs[0].Country != "DE" {
		t.Errorf("the visitor behind Cloudflare: %+v", w.IPs)
	}
	if len(w.Domains) != 1 || w.Domains[0].Name != "a.com" || w.Domains[0].Count != 1 {
		t.Errorf("domains: %+v", w.Domains)
	}
	if summaryWAFFor(nil, "2026-11-17") != nil {
		t.Error("another day blocked nothing")
	}
	c := combinedWAF([]summaryServerDay{{Name: "s1", Day: &summaryDay{WAF: w}}, {Name: "s2", Day: &summaryDay{WAF: w}}})
	if c.Blocked != 2 || c.Rules[0].Count != 2 || c.IPs[0].Count != 2 || len(c.Domains) != 2 || c.Domains[0].Server == "" {
		t.Errorf("combined: %+v", c)
	}
}
