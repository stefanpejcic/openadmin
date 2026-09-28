package activity

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestRecordReadLast(t *testing.T) {
	Dir = t.TempDir()
	if _, ok := Last("bob"); ok {
		t.Fatal("expected no entry before anything was recorded")
	}
	Record("bob", "203.0.113.1", "Added domain 'a.com'")
	Record("bob", "", "Deleted user\n'x'")

	entries := Read("bob")
	if len(entries) != 2 || entries[0].Action != "Deleted user 'x'" || entries[0].IP != "0.0.0.0" || entries[1].IP != "203.0.113.1" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	last, ok := Last("bob")
	if !ok || last.Action != "Deleted user 'x'" {
		t.Fatalf("unexpected last entry: %+v", last)
	}
}

func TestPathRejectsUnsafeNames(t *testing.T) {
	Dir = t.TempDir()
	for _, name := range []string{"", "..", "../etc/passwd", "a/b", "a b"} {
		if Path(name) != "" {
			t.Errorf("expected %q to be rejected", name)
		}
		Record(name, "", "x")
	}
	files, _ := os.ReadDir(Dir)
	if len(files) != 0 {
		t.Fatalf("expected no files for unsafe names, got %d", len(files))
	}
}

func TestTrimKeepsNewest(t *testing.T) {
	Dir = t.TempDir()
	long := strings.Repeat("x", 1000)
	for i := 0; i < 2100; i++ {
		Record("bob", "", long)
	}
	Record("bob", "", "newest")
	info, _ := os.Stat(Path("bob"))
	if info.Size() > maxSize {
		t.Fatalf("expected the log to be trimmed, size %d", info.Size())
	}
	if last, _ := Last("bob"); last.Action != "newest" {
		t.Fatalf("expected the newest entry to survive trimming, got %q", last.Action)
	}
	for _, e := range Read("bob") {
		if e.Action != long && e.Action != "newest" {
			t.Fatalf("trim left a broken line: %+v", e)
		}
	}
}

func TestContextHolder(t *testing.T) {
	ctx := WithRequest(context.Background())
	SetActor(ctx, "api_admin")
	Describe(ctx, "custom")
	Fail(ctx)
	user, action, skip, failed := Result(ctx)
	if user != "api_admin" || action != "custom" || skip || !failed {
		t.Fatalf("unexpected result: %q %q %v %v", user, action, skip, failed)
	}
	// no holder, nothing should panic
	SetActor(context.Background(), "x")
	Describe(context.Background(), "x")
}
