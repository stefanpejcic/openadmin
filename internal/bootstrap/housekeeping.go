package bootstrap

import (
	"log"
	"os"
)

// executablePaths lists files that must be made executable at startup.
var executablePaths = []string{
	"/etc/openpanel/wordpress/wp-cli.phar",     // wpcli for php containers
	"/usr/local/admin/modules/security/csf.pl", // csf gui
	"/etc/openpanel/services/watcher.sh",       // reload dns zones
	"/etc/openpanel/mysql/scripts/dump.sh",     // mysql export script for backups
	"/etc/openpanel/openlitespeed/start.sh",    // overwrites ols entrypoint
}

// cacheDir is a var (not a const) so tests can point it at a scratch
// fixture.
var cacheDir = "/tmp/openadmin_cache"

// RunStartupHousekeeping performs a set of fixed startup tasks: ensuring
// known scripts are executable and clearing the on-disk cache directory.
func RunStartupHousekeeping(logger *log.Logger) {
	for _, p := range executablePaths {
		makeExecutableIfExists(logger, p)
	}
	clearCache(logger, cacheDir)
}

func makeExecutableIfExists(logger *log.Logger, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mode := info.Mode()
	if mode&0111 == 0111 {
		return
	}
	if err := os.Chmod(path, mode|0111); err != nil {
		logger.Printf("Failed to set +x on %s: %v", path, err)
		return
	}
	logger.Printf("Made %s executable (+x)", path)
}

func clearCache(logger *log.Logger, dir string) {
	if err := os.RemoveAll(dir); err != nil {
		logger.Printf("Failed to clear cache in %s: %v", dir, err)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Printf("Failed to clear cache in %s: %v", dir, err)
		return
	}
	logger.Printf("Recreated empty cache directory %s", dir)
}
