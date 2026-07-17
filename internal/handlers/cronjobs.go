package handlers

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Cronjobs bundles /server/crons.
type Cronjobs struct {
	Sessions *auth.Manager
}

// CronFilePath is the crontab file managed by this handler.
var CronFilePath = "/etc/cron.d/openpanel"

// CronJob is a single parsed line of CronFilePath.
type CronJob struct {
	LineNumber int    `json:"line_number"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	Log        bool   `json:"log"`
}

func isValidCronLine(line string) bool {
	stripped := strings.TrimSpace(line)
	return stripped != "" && !strings.HasPrefix(stripped, "#") && !strings.HasPrefix(stripped, "@")
}

var cronLoggingSplitRe = regexp.MustCompile(`\s*(?:#)?&&\s*`)

// splitCronLine parses a single crontab line. A valid line is
// "<5 schedule fields> root <command> [#]&& echo cron executed >> ...".
func splitCronLine(line string) (CronJob, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 6 {
		return CronJob{}, false
	}
	schedule := strings.Join(fields[0:5], " ")
	commandPart := strings.Join(fields[6:], " ") // skip 5 schedule fields + "root"

	loggingEnabled := strings.Contains(commandPart, "&&") && !strings.Contains(commandPart, "#&&")

	command := commandPart
	if loc := cronLoggingSplitRe.FindStringIndex(commandPart); loc != nil {
		command = strings.TrimSpace(commandPart[:loc[0]])
	} else {
		command = strings.TrimSpace(commandPart)
	}

	if strings.HasPrefix(command, "/usr/local/bin/opencli") {
		command = strings.Replace(command, "/usr/local/bin/opencli", "opencli", 1)
	}

	return CronJob{Schedule: schedule, Command: command, Log: loggingEnabled}, true
}

// addOrUpdateCron rewrites a single numbered line of CronFilePath in
// place, restoring the opencli/sentinel command prefixes that
// splitCronLine strips for display.
func addOrUpdateCron(lineNumber int, schedule string, loggingEnabled bool) error {
	raw, err := os.ReadFile(CronFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // silent no-op when the file doesn't exist
		}
		return err
	}

	lines := strings.Split(string(raw), "\n")
	// strings.Split on a trailing-newline file yields a final "" element;
	// drop it so line numbering matches the file's actual line count,
	// then restore it on write.
	trailingNewline := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}

	found := false
	for idx, line := range lines {
		if idx+1 != lineNumber {
			continue
		}
		parsed, ok := splitCronLine(line)
		if !ok {
			continue
		}

		// Checking the more specific "opencli sentinel" prefix before the
		// plain "opencli" prefix matters: since every "opencli sentinel"
		// command also starts with "opencli", checking the generic prefix
		// first would silently corrupt a sentinel cron entry's command to
		// "/usr/local/bin/opencli sentinel" instead of the intended
		// "/bin/bash .../notifications.sh" whenever its schedule is edited.
		command := parsed.Command
		switch {
		case strings.HasPrefix(command, "opencli sentinel"):
			command = strings.Replace(command, "opencli sentinel", "/bin/bash /usr/local/admin/service/notifications.sh", 1)
		case strings.HasPrefix(command, "opencli"):
			command = strings.Replace(command, "opencli", "/usr/local/bin/opencli", 1)
		}

		if loggingEnabled {
			command += " && echo cron executed >> /var/log/openpanel-cron.log"
		} else {
			command += " #&& echo cron executed >> /var/log/openpanel-cron.log"
		}

		lines[idx] = schedule + " root " + command
		found = true
	}

	if !found {
		return nil
	}

	out := strings.Join(lines, "\n")
	if trailingNewline {
		out += "\n"
	}
	return os.WriteFile(CronFilePath, []byte(out), 0644)
}

type cronsPageData struct {
	webtemplates.Chrome
	CronJobs    []CronJob
	FileMissing bool
	CSRFToken   string
	Flashes     []auth.Flash
}

// ServeCrons handles GET/POST /server/crons.
func (c *Cronjobs) ServeCrons(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		c.handlePost(w, r)
		return
	}

	jobs, fileMissing := readCronJobs()

	if r.URL.Query().Get("output") == "json" {
		if fileMissing {
			writeJSON(w, nil)
			return
		}
		writeJSON(w, jobs)
		return
	}

	webtemplates.Render(w, "crons.html", cronsPageData{
		Chrome:      buildChrome(r, "Cronjobs"),
		CronJobs:    jobs,
		FileMissing: fileMissing,
		CSRFToken:   csrf.Token(r),
		Flashes:     auth.PopFlashes(w, r, c.Sessions),
	})
}

func readCronJobs() (jobs []CronJob, fileMissing bool) {
	raw, err := os.ReadFile(CronFilePath)
	if err != nil {
		return nil, true
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if !isValidCronLine(line) {
			continue
		}
		if parsed, ok := splitCronLine(line); ok {
			parsed.LineNumber = i + 1
			jobs = append(jobs, parsed)
		}
	}
	return jobs, false
}

var (
	cronScheduleFieldRe = regexp.MustCompile(`^(\d+)_schedule_(\d+)$`)
	cronLoggingFieldRe  = regexp.MustCompile(`^(\d+)_logging$`)
)

func (c *Cronjobs) handlePost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	type cronEdit struct {
		scheduleParts [5]string
		logging       bool
	}
	edits := map[string]*cronEdit{}

	for key, values := range r.PostForm {
		if len(values) == 0 {
			continue
		}
		value := values[0]

		if m := cronScheduleFieldRe.FindStringSubmatch(key); m != nil {
			id, idx := m[1], m[2]
			e := edits[id]
			if e == nil {
				e = &cronEdit{}
				edits[id] = e
			}
			i, err := strconv.Atoi(idx)
			if err == nil && i >= 0 && i < 5 {
				e.scheduleParts[i] = strings.TrimSpace(value)
			}
			continue
		}
		if m := cronLoggingFieldRe.FindStringSubmatch(key); m != nil {
			id := m[1]
			e := edits[id]
			if e == nil {
				e = &cronEdit{}
				edits[id] = e
			}
			e.logging = true
		}
	}

	// deterministic order for tests/logs; nothing depends on any
	// particular ordering here beyond that
	ids := make([]string, 0, len(edits))
	for id := range edits {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		e := edits[id]
		lineNumber, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		schedule := strings.Join(e.scheduleParts[:], " ")
		addOrUpdateCron(lineNumber, schedule, e.logging)
	}

	auth.AddFlash(w, r, c.Sessions, "Cron jobs updated successfully", "success")
	http.Redirect(w, r, "/server/crons", http.StatusSeeOther)
}
