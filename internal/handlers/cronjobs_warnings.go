package handlers

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type cronWarning struct {
	Command, Schedule, Problem string
}

// cronWarnings flags jobs whose schedule is invalid or can never fire, docker-backup on its "Disabled" schedule is the intended default
func cronWarnings(jobs []CronJob, now time.Time) []cronWarning {
	var out []cronWarning
	for _, j := range jobs {
		if strings.HasPrefix(j.Command, "opencli docker-backup") && j.Schedule == backupScheduleChoices["disabled"] {
			continue
		}
		sched, err := cron.ParseStandard(j.Schedule)
		switch {
		case err != nil:
			out = append(out, cronWarning{j.Command, j.Schedule, "invalid schedule"})
		case sched.Next(now).IsZero():
			out = append(out, cronWarning{j.Command, j.Schedule, "never runs, so it's effectively disabled"})
		}
	}
	return out
}
