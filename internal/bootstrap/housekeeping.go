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

// vars (not consts) so tests can point them at scratch fixtures.
var (
	csfSymlinkTarget = "/etc/csf/ui/images/"
	csfSymlinkName   = "/usr/local/admin/static/configservercsf"
	cacheDir         = "/tmp/openadmin_cache"
)

// RunStartupHousekeeping performs a set of fixed startup tasks: ensuring
// known scripts are executable, recreating a symlink, and clearing the
// on-disk cache directory.
func RunStartupHousekeeping(logger *log.Logger) {
	for _, p := range executablePaths {
		makeExecutableIfExists(logger, p)
	}
	symlinkForce(logger, csfSymlinkTarget, csfSymlinkName)
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

// symlinkForce removes whatever is at linkName (broken symlink, live
// symlink, or regular file) and recreates it pointing at target. os.Lstat
// (unlike os.Stat) succeeds for a broken symlink too, so a single check
// covers both cases.
func symlinkForce(logger *log.Logger, target, linkName string) {
	if _, err := os.Lstat(linkName); err == nil {
		if err := os.Remove(linkName); err != nil {
			logger.Printf("Failed to create symlink %s -> %s: %v", linkName, target, err)
			return
		}
	}
	if err := os.Symlink(target, linkName); err != nil {
		logger.Printf("Failed to create symlink %s -> %s: %v", linkName, target, err)
		return
	}
	logger.Printf("Created symlink: %s -> %s", linkName, target)
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
