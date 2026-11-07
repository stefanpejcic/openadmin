package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"openadmin/internal/paneldb"
)

func TestAddZoneSummaryCountsRecords(t *testing.T) {
	dir := t.TempDir()
	orig := BindZonesDir
	BindZonesDir = dir
	t.Cleanup(func() { BindZonesDir = orig })

	zone := "$TTL 3600\n" +
		"@\tIN\tSOA\tns1.example.com. admin.example.com. ( 1 3600 600 604800 300 )\n" +
		"; a comment IN A line\n" +
		"@\t3600\tIN\tA\t203.0.113.10\n" +
		"\t\tIN\tAAAA\t2001:db8::1\n" +
		"www\t3600\tIN\tCNAME\texample.com.\n" +
		"@\t3600\tIN\tMX\t10 mail.example.com.\n"
	if err := os.WriteFile(filepath.Join(dir, "example.com.zone"), []byte(zone), 0644); err != nil {
		t.Fatal(err)
	}

	row := paneldb.RowMap{}
	addZoneSummary(row, "example.com")
	if row["zone_exists"] != true {
		t.Fatalf("expected the zone to exist, got %v", row["zone_exists"])
	}
	if got := row["zone_records"]; got != 5 {
		t.Fatalf("expected 5 records (SOA, A, AAAA, CNAME, MX), got %v", got)
	}

	missing := paneldb.RowMap{}
	addZoneSummary(missing, "nozone.example")
	if missing["zone_exists"] != false {
		t.Fatalf("expected no zone for a domain without a file, got %v", missing["zone_exists"])
	}

	bad := paneldb.RowMap{}
	addZoneSummary(bad, "../etc/passwd")
	if bad["zone_exists"] != false {
		t.Fatal("expected an invalid domain name to be ignored")
	}
}
