package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/auth"
)

const sampleBackupEnv = `########### BACKUP SCHEDULE
BACKUP_CRON_EXPRESSION="0 0 5 31 2 ?"
# BACKUP_COMPRESSION='gz'
GZIP_PARALLELISM='0'
BACKUP_SOURCES='/backup'

########### S3 COMPATIBLE STORAGE
# AWS_S3_BUCKET_NAME=''
# AWS_ENDPOINT='s3.amazonaws.com'
# AWS_PART_SIZE='16'

########### SSH/SFTP STORAGE
SSH_HOST_NAME='box.example.com'
SSH_PORT='22'
SSH_USER='u1'
SSH_PASSWORD='s3cret'
# SSH_IDENTITY_FILE='/var/www/html/id_rsa'

NOTIFICATION_LEVEL='error'
`

func fieldValue(fields []backupEnvField, key string) (string, bool) {
	for _, f := range fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

func TestParseBackupEnvFormDetectsDestinationAndUnquotes(t *testing.T) {
	form := parseBackupEnvForm(sampleBackupEnv)
	if form.Destination != "ssh" || form.MultipleActive {
		t.Fatalf("expected single ssh destination, got %+v", form)
	}
	if v, _ := fieldValue(form.Settings, "GZIP_PARALLELISM"); v != "0" {
		t.Fatalf("expected unquoted GZIP_PARALLELISM 0, got %q", v)
	}
	if _, ok := fieldValue(form.Settings, "BACKUP_COMPRESSION"); ok {
		t.Fatal("commented settings must not show up in the form")
	}
	var s3, ssh backupEnvSection
	for _, s := range form.Sections {
		switch s.Name {
		case "s3":
			s3 = s
		case "ssh":
			ssh = s
		}
	}
	if v, _ := fieldValue(s3.Fields, "AWS_ENDPOINT"); v != "s3.amazonaws.com" {
		t.Fatalf("expected commented example value for inactive section, got %q", v)
	}
	if v, _ := fieldValue(ssh.Fields, "SSH_PASSWORD"); v != "s3cret" {
		t.Fatalf("expected ssh password, got %q", v)
	}
	for _, f := range ssh.Fields {
		if f.Key == "SSH_PASSWORD" && !f.Secret {
			t.Fatal("SSH_PASSWORD should be a secret field")
		}
	}
}

func TestRenderBackupEnvFormSwitchesDestination(t *testing.T) {
	out := renderBackupEnvForm(sampleBackupEnv, "s3",
		map[string]string{"AWS_S3_BUCKET_NAME": "my-bucket", "AWS_ENDPOINT": "s3.eu.example.com", "AWS_PART_SIZE": ""},
		map[string]string{"GZIP_PARALLELISM": "2", "NOTIFICATION_LEVEL": "info"})

	for _, want := range []string{
		"AWS_S3_BUCKET_NAME=my-bucket\n",
		"AWS_ENDPOINT=s3.eu.example.com\n",
		"# AWS_PART_SIZE=\n",
		"# SSH_HOST_NAME=box.example.com\n",
		"# SSH_PASSWORD=s3cret\n",
		"# SSH_IDENTITY_FILE='/var/www/html/id_rsa'\n",
		"GZIP_PARALLELISM=2\n",
		"NOTIFICATION_LEVEL=info\n",
		"BACKUP_CRON_EXPRESSION=\"0 0 5 31 2 ?\"\n",
		"# BACKUP_COMPRESSION='gz'\n",
		"########### SSH/SFTP STORAGE\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
	if f := parseBackupEnvForm(out); f.Destination != "s3" || f.MultipleActive {
		t.Fatalf("expected only s3 active after switching, got %+v", f)
	}
}

func TestRenderBackupEnvFormAppendsMissingDestinationKeys(t *testing.T) {
	out := renderBackupEnvForm("GZIP_PARALLELISM=0\n", "webdav", map[string]string{"WEBDAV_URL": "https://dav.example.com", "WEBDAV_PATH": ""}, nil)
	if !strings.Contains(out, "WEBDAV_URL=https://dav.example.com\n") || strings.Contains(out, "WEBDAV_PATH") {
		t.Fatalf("expected only non-empty missing keys appended, got:\n%s", out)
	}
}

func TestServeUserBackupsConfigurationFormMode(t *testing.T) {
	dir := t.TempDir()
	orig := BackupEnvPath
	BackupEnvPath = filepath.Join(dir, "backup.env")
	t.Cleanup(func() { BackupEnvPath = orig })
	os.WriteFile(BackupEnvPath, []byte(sampleBackupEnv), 0644)

	form := url.Values{
		"mode":                       {"form"},
		"destination":                {"ssh"},
		"values[SSH_HOST_NAME]":      {"new.example.com\nEVIL=1"},
		"values[SSH_PASSWORD]":       {"pw2"},
		"settings[GZIP_PARALLELISM]": {"4"},
		"settings[NOT_A_SETTING]":    {"x"},
	}
	req := httptest.NewRequest(http.MethodPost, "/backups/user/configuration", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	(&Backups{Sessions: auth.NewManager("test-secret", false)}).ServeUserBackupsConfiguration(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	got, _ := os.ReadFile(BackupEnvPath)
	s := string(got)
	for _, want := range []string{"SSH_HOST_NAME=new.example.comEVIL=1\n", "SSH_PASSWORD=pw2\n", "GZIP_PARALLELISM=4\n"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q in saved file:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\nEVIL=1") || strings.Contains(s, "NOT_A_SETTING") {
		t.Fatalf("injected lines/keys must not be written:\n%s", s)
	}
}

func TestServeUserBackupsConfigurationAdvancedMode(t *testing.T) {
	dir := t.TempDir()
	orig := BackupEnvPath
	BackupEnvPath = filepath.Join(dir, "backup.env")
	t.Cleanup(func() { BackupEnvPath = orig })

	form := url.Values{"mode": {"advanced"}, "backup_env": {"RAW=1\n"}}
	req := httptest.NewRequest(http.MethodPost, "/backups/user/configuration", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	(&Backups{Sessions: auth.NewManager("test-secret", false)}).ServeUserBackupsConfiguration(httptest.NewRecorder(), req)

	if got, _ := os.ReadFile(BackupEnvPath); string(got) != "RAW=1\n" {
		t.Fatalf("advanced mode should save the textarea as-is, got %q", got)
	}
}
