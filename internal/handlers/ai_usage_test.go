package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAIUsageShowsNewestError(t *testing.T) {
	orig := AIUsageUsersDir
	t.Cleanup(func() { AIUsageUsersDir = orig })
	AIUsageUsersDir = t.TempDir()
	month := time.Now().UTC().Format("2006-01")
	write := func(user, body string) {
		_ = os.MkdirAll(filepath.Join(AIUsageUsersDir, user), 0o755)
		if err := os.WriteFile(filepath.Join(AIUsageUsersDir, user, "ai_usage.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("alice", `{"months":{"`+month+`":{"requests":2}},"last_error":"old","last_error_at":"2026-01-01T10:00:00Z"}`)
	// a user whose failed questions were all refunded still reports the error
	write("bob", `{"months":{"`+month+`":{"requests":0}},"last_error":"provider returned 400: Unsupported parameter: 'max_tokens'","last_error_at":"2026-01-02T10:00:00Z"}`)

	r := loadAIUsage()
	if r.LastErrorUser != "bob" || r.LastError != "provider returned 400: Unsupported parameter: 'max_tokens'" || r.LastErrorAt == "" {
		t.Fatalf("got %+v", r)
	}
	if len(r.Rows) != 1 || r.Rows[0].Username != "alice" {
		t.Fatalf("rows: %+v", r.Rows)
	}
}
