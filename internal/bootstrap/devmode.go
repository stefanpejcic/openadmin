package bootstrap

import (
	"bufio"
	"os"
	"strings"

	"openadmin/internal/config"
)

// OpenpanelConfigPath is a var (not const) so tests can point it at a
// scratch fixture instead of the real /etc path. Defaults to the same path
// config.Openpanel() reads, kept as a separate var here (rather than an
// import-time reference) since this line-scan runs before config's cached
// loader is ever touched.
var OpenpanelConfigPath = config.OpenpanelConfigPath

// IsDevMode reports true only if OpenpanelConfigPath contains a line that
// is exactly "dev_mode=on" (case-insensitive, surrounding whitespace
// trimmed). Any read error, including a missing file, is treated as false.
func IsDevMode() bool {
	f, err := os.Open(OpenpanelConfigPath)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.EqualFold(strings.TrimSpace(scanner.Text()), "dev_mode=on") {
			return true
		}
	}
	return false
}
