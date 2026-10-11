package handlers

import (
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"
)

func adviceFor(groups []limitsGroup, group, label string) limitAdvice {
	for _, g := range groups {
		if g.Name != group {
			continue
		}
		for _, r := range g.Rows {
			if r.Label == label {
				return r.Advice
			}
		}
	}
	return limitAdvice{}
}

func TestAdviseLimits(t *testing.T) {
	raw := map[string]map[string]string{
		"OPENPANEL":  {"RAM": "6g", "CPUS": "3.0"},
		"CADDY":      {"RAM": "8g", "CPUS": "6.0"},
		"MYSQL":      {"RAM": "1g", "CPUS": "1.0"},
		"CLAMAV":     {"RAM": "2g", "CPUS": "2.0"},
		"CLAMD":      {"STARTUP_DELAY": "30"},
		"BIND9":      {"RAM": "0.1g", "CPUS": "0.1"},
		"FTP":        {"RAM": "0.5g", "CPUS": "0.5"},
		"PHPMYADMIN": {"RAM": "1.0G", "CPU": "1.0", "MEMORY_LIMIT": "2048M", "UPLOAD_LIMIT": "3G"},
	}
	big := limitsHost{Cores: 18, RAMGB: 94, Modules: []string{"dns", "phpmyadmin"}, Users: 22, Domains: 22}
	g := adviseLimits(buildLimitsView(raw), raw, big)

	if a := adviceFor(g, "OPENPANEL", "RAM"); a.Low != 2 {
		t.Fatalf("openpanel ram = %+v", a)
	}
	if a := adviceFor(g, "BIND9", "CPUS"); a.Low != 0.1 {
		t.Fatalf("bind9 cpu = %+v", a)
	}
	if a := adviceFor(g, "CADDY", "RAM"); a.Low != 2 || a.High != 8 || !strings.Contains(a.LowText, "22 domains") {
		t.Fatalf("caddy ram on a big server should scale by domains, got %+v", a)
	}
	if a := adviceFor(g, "CADDY", "CPUS"); a.High != 6 {
		t.Fatalf("caddy cpu max = %+v", a)
	}
	if a := adviceFor(g, "MYSQL", "RAM"); a.Low != 1.1 || a.High != 2 {
		t.Fatalf("mysql ram = %+v", a)
	}
	if a := adviceFor(g, "MYSQL", "CPUS"); a.Low != 1 || a.High != 1 {
		t.Fatalf("mysql cpu should be capped at a third of openpanel, got %+v", a)
	}
	if a := adviceFor(g, "PHPMYADMIN", "RAM"); a.Low != 3 || !strings.Contains(a.LowText, "UPLOAD_LIMIT") {
		t.Fatalf("phpmyadmin ram = %+v", a)
	}
	if a := adviceFor(g, "PHPMYADMIN", "CPU"); a.Low != 2 {
		t.Fatalf("phpmyadmin cpu = %+v", a)
	}
	for _, skip := range [][2]string{{"CLAMAV", "RAM"}, {"CLAMD", "STARTUP_DELAY"}, {"FTP", "RAM"}} {
		if a := adviceFor(g, skip[0], skip[1]); a.Low != 0 || a.High != 0 {
			t.Fatalf("expected no advice for %v without its module, got %+v", skip, a)
		}
	}

	small := limitsHost{Cores: 3, RAMGB: 4, Modules: []string{"malware_scan", "ftp"}, Users: 35, Domains: 5}
	g = adviseLimits(buildLimitsView(raw), raw, small)
	if a := adviceFor(g, "OPENPANEL", "CPUS"); a.Low != 1 {
		t.Fatalf("openpanel on a 4 GB server = %+v", a)
	}
	if a := adviceFor(g, "CADDY", "RAM"); a.Low != 1.4 || !strings.Contains(a.LowText, "a third of the server") {
		t.Fatalf("caddy on a small server should take a third of RAM, got %+v", a)
	}
	if a := adviceFor(g, "CLAMAV", "CPUS"); a.Low != 4 || a.High != 6 {
		t.Fatalf("clamav = %+v", a)
	}
	if a := adviceFor(g, "CLAMD", "STARTUP_DELAY"); a.Low != 30 || a.High != 600 {
		t.Fatalf("clamd delay = %+v", a)
	}
	if a := adviceFor(g, "FTP", "RAM"); a.Low != 0.8 || a.High != 3 {
		t.Fatalf("ftp = %+v", a)
	}
	if a := adviceFor(g, "BIND9", "RAM"); a.Low != 0 {
		t.Fatalf("expected no bind9 advice without the dns module, got %+v", a)
	}
}

func TestServeLimitsRendersAdvice(t *testing.T) {
	withScratchGlobalEnvPath(t)
	os.WriteFile(GlobalEnvPath, []byte("OPENPANEL_RAM=\"1g\"\nOPENPANEL_CPUS=\"1.0\"\n"), 0644)
	orig := limitsHostRun
	limitsHostRun = func(_ *sql.DB) limitsHost { return limitsHost{Cores: 8, RAMGB: 16} }
	t.Cleanup(func() { limitsHostRun = orig })

	srv, client := newLimitsTestServer(t, &Limits{})
	resp, err := client.Get(srv.URL + "/services/limits")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Number(v) > 0 && Number(v) < 2") || !strings.Contains(string(body), "on servers with over 4 GB RAM") {
		t.Fatal("expected the OpenPanel recommendation in the page")
	}
}
