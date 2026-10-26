package handlers

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ImportUploadDir holds uploaded backups, one folder per OpenAdmin account, a reseller can only restore from their own folder
var ImportUploadDir = "/home/reseller_imports"

var importBackupNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}\.tar\.gz$`)

// opencli user-restore writes this line near the top of the log for reseller restores
var importLogResellerRe = regexp.MustCompile(`^\[[^\]]+\] Reseller: (\S+)$`)

func importUploadDirFor(username string) string {
	return filepath.Join(ImportUploadDir, filepath.Base(username))
}

// resolveResellerBackup follows symlinks so a reseller can't point the restore at a file outside their upload folder
func resolveResellerBackup(reseller, path string) (string, error) {
	dir, err := filepath.EvalSymlinks(importUploadDirFor(reseller))
	if err != nil {
		return "", errors.New("You haven't uploaded any backups yet.")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("Backup file not found.")
	}
	if !strings.HasPrefix(real, dir+string(os.PathSeparator)) {
		return "", errors.New("Resellers can only restore backups they uploaded.")
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Backup file not found.")
	}
	return real, nil
}

// listUploadedBackups lists .tar.gz files in a user's upload folder
func listUploadedBackups(username string) []string {
	dir := importUploadDirFor(username)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		if e.Type().IsRegular() && importBackupNameRe.MatchString(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// importLogReseller returns the reseller a restore log belongs to, "" for admin restores
func importLogReseller(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 15 && sc.Scan(); i++ {
		if m := importLogResellerRe.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			return m[1]
		}
	}
	return ""
}

// importLogsFor lists restore logs, a reseller only gets the ones of their own restores
func importLogsFor(r *http.Request) []importLogStatus {
	logs := listLogFilesWithStatus(ImporterOpenPanelImportLogDir)
	reseller := actingReseller(r)
	if reseller == "" {
		return logs
	}
	out := []importLogStatus{}
	for _, l := range logs {
		if importLogReseller(filepath.Join(ImporterOpenPanelImportLogDir, l.Filename)) == reseller {
			out = append(out, l)
		}
	}
	return out
}

// importLogAllowed is false when a reseller asks for a log that isn't from their restore
func importLogAllowed(r *http.Request, logFilename string) bool {
	reseller := actingReseller(r)
	if reseller == "" {
		return true
	}
	path, ok := safeJoinOr400(ImporterOpenPanelImportLogDir, logFilename)
	return ok && importLogReseller(path) == reseller
}

// saveImportUpload streams the "file" form field into the acting user's upload folder, so multi GB backups never sit in memory
func saveImportUpload(r *http.Request, username string) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", errors.New("Upload the backup as multipart/form-data with a 'file' field.")
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", errors.New("No file was uploaded.")
		}
		if err != nil {
			return "", fmt.Errorf("Upload failed: %v", err)
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		name := filepath.Base(part.FileName())
		if !importBackupNameRe.MatchString(name) {
			part.Close()
			return "", errors.New("The backup has to be an OpenPanel .tar.gz file, with only letters, numbers, dots, dashes and underscores in its name.")
		}
		dir := importUploadDirFor(username)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("Can't create upload folder: %v", err)
		}
		dst := filepath.Join(dir, name)
		tmp, err := os.CreateTemp(dir, ".upload-*")
		if err != nil {
			return "", fmt.Errorf("Can't save upload: %v", err)
		}
		_, copyErr := io.Copy(tmp, part)
		closeErr := tmp.Close()
		part.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(tmp.Name())
			return "", errors.New("Upload was interrupted, try again.")
		}
		if err := os.Rename(tmp.Name(), dst); err != nil {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("Can't save upload: %v", err)
		}
		return dst, nil
	}
}
