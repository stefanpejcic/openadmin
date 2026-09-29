package webtemplates

import (
	"testing"
	"time"
)

func TestTimeAgo(t *testing.T) {
	timeAgo := funcMap["timeAgo"].(func(interface{}) string)
	day := 24 * time.Hour
	cases := map[time.Duration]string{time.Hour: "today", 5 * day: "5d ago", 90 * day: "3mo ago", 800 * day: "2y ago"}
	for ago, want := range cases {
		if got := timeAgo(time.Now().Add(-ago)); got != want {
			t.Fatalf("%v ago: got %q, want %q", ago, got, want)
		}
	}
	if got := timeAgo(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}
