package handlers

import "testing"

func TestBackupsUserScheduleChoice(t *testing.T) {
	cases := map[string]string{
		"59 23 31 2 *": "disabled",
		"0 3 * * *":    "daily",
		"0 3 * * 0":    "weekly",
		"0 3 1 * *":    "monthly",
		"0 22 * * *":   "custom",
		"30 1 * * *":   "custom",
	}
	for schedule, want := range cases {
		if got := backupsUserScheduleChoice(schedule); got != want {
			t.Errorf("backupsUserScheduleChoice(%q) = %q, want %q", schedule, got, want)
		}
	}
	if got := backupScheduleLabel("0 22 * * *"); got != "Custom (0 22 * * *)" {
		t.Errorf("label = %q", got)
	}
}
