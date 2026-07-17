package bootstrap

import (
	"log"
	"os"
)

// RestartFlagPath and DisabledFlagPath are vars (not consts) so tests can
// point them at a scratch fixture instead of the real /root path.
var (
	RestartFlagPath  = "/root/openadmin_restart_needed"
	DisabledFlagPath = "/root/openadmin_is_disabled"
)

// RemoveRestartFlag removes the restart-needed flag file if present.
// Best-effort: logs on failure but never aborts startup.
func RemoveRestartFlag(logger *log.Logger) {
	if _, err := os.Stat(RestartFlagPath); err != nil {
		return
	}
	if err := os.Remove(RestartFlagPath); err != nil {
		logger.Printf("Error removing %s: %v", RestartFlagPath, err)
		return
	}
	logger.Println("Removed the restart-needed flag for OpenAdmin panel.")
}

// ExitIfDisabled checks for the disabled-flag file: if present, log and
// exit(1) before anything binds a socket.
func ExitIfDisabled(logger *log.Logger) {
	if _, err := os.Stat(DisabledFlagPath); err != nil {
		return
	}
	logger.Printf("OpenAdmin is disabled! enable it from terminal with 'opencli admin on' or by removing the flag file: %s", DisabledFlagPath)
	os.Exit(1)
}
