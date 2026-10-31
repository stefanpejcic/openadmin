package cluster

import "os/exec"

// Notify writes a cluster event to this server's notifications through sentinel, a var so tests don't run opencli
var Notify = func(title, message, severity string) {
	args := []string{"sentinel", "--action=cluster", "--title=" + title, "--message=" + message, "--severity=" + severity}
	// the admin did these on purpose, only problems go out by email or webhook
	if severity == "info" {
		args = append(args, "--no-send")
	}
	go exec.Command("opencli", args...).Run()
}
