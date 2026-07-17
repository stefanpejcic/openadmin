package bootstrap

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// ErrorLogPath and AccessLogPath are vars (not consts) so tests can point
// them at a scratch fixture instead of the real /var/log path.
var (
	ErrorLogPath  = "/var/log/openpanel/admin/error.log"
	AccessLogPath = "/var/log/openpanel/admin/access.log"
)

// SetupLogging configures the process-wide app logger: in dev mode every
// log line is timestamped and appended to ErrorLogPath; otherwise app-level
// logging is discarded entirely. Access logging is separate (see
// server.NewAccessLogger) and is not gated by dev mode.
func SetupLogging(devMode bool) (*log.Logger, error) {
	if err := EnsureLogDirs(); err != nil {
		return nil, err
	}

	if !devMode {
		return log.New(io.Discard, "", 0), nil
	}

	f, err := os.OpenFile(ErrorLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return log.New(f, "", log.Ldate|log.Ltime), nil
}

// EnsureLogDirs ensures the parent directories of ErrorLogPath and
// AccessLogPath exist.
func EnsureLogDirs() error {
	for _, p := range []string{ErrorLogPath, AccessLogPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
	}
	return nil
}
