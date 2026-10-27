package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBackupsHostKey makes every host key fetch return key or err and counts the calls
func stubBackupsHostKey(t *testing.T, key string, err error) *[]string {
	t.Helper()
	var calls []string
	orig := backupsFetchSSHHostKey
	backupsFetchSSHHostKey = func(host, port, user string) (string, error) {
		calls = append(calls, host+":"+port+"@"+user)
		return key, err
	}
	t.Cleanup(func() { backupsFetchSSHHostKey = orig })
	return &calls
}

const sshEnvBefore = "########### SSH/SFTP STORAGE\nSSH_HOST_NAME=box.example.com\nSSH_PORT=22\nSSH_USER=u1\n# SSH_IDENTITY_PASSPHRASE=\n\nNOTIFICATION_LEVEL=error\n"

func TestPinBackupSSHHostKey(t *testing.T) {
	pinned := strings.Replace(sshEnvBefore, "SSH_USER=u1\n", "SSH_USER=u1\nSSH_HOST_PUBLIC_KEY=\"ssh-ed25519 AAAAold\"\n", 1)
	cases := []struct {
		name, before, after string
		wantFetch           bool
		wantKey             string
	}{
		{"not ssh", "", "AWS_S3_BUCKET_NAME=b\n# SSH_HOST_NAME=box.example.com\n", false, ""},
		{"first save fetches", "", sshEnvBefore, true, "ssh-ed25519 AAAAnew"},
		{"same host keeps the key", pinned, pinned, false, "ssh-ed25519 AAAAold"},
		{"new host refetches", pinned, strings.Replace(pinned, "box.example.com", "other.example.com", 1), true, "ssh-ed25519 AAAAnew"},
		{"new port refetches", pinned, strings.Replace(pinned, "SSH_PORT=22", "SSH_PORT=2222", 1), true, "ssh-ed25519 AAAAnew"},
		{"typed key is kept", pinned, strings.Replace(strings.Replace(pinned, "box.example.com", "other.example.com", 1), "AAAAold", "AAAAtyped", 1), false, "ssh-ed25519 AAAAtyped"},
		{"cleared key fetches again", pinned, strings.Replace(pinned, "SSH_HOST_PUBLIC_KEY=\"ssh-ed25519 AAAAold\"", "# SSH_HOST_PUBLIC_KEY=", 1), true, "ssh-ed25519 AAAAnew"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := stubBackupsHostKey(t, "ssh-ed25519 AAAAnew", nil)
			out, err := pinBackupSSHHostKey(c.before, c.after)
			if err != nil {
				t.Fatal(err)
			}
			if fetched := len(*calls) > 0; fetched != c.wantFetch {
				t.Fatalf("fetch called = %v, want %v (%v)", fetched, c.wantFetch, *calls)
			}
			if got := backupEnvValues(out)["SSH_HOST_PUBLIC_KEY"]; got != c.wantKey {
				t.Fatalf("SSH_HOST_PUBLIC_KEY = %q, want %q in:\n%s", got, c.wantKey, out)
			}
			if strings.Count(out, "SSH_HOST_PUBLIC_KEY") > 1 {
				t.Fatalf("key written twice:\n%s", out)
			}
		})
	}
}

func TestPinBackupSSHHostKeyPlacementAndDefaults(t *testing.T) {
	calls := stubBackupsHostKey(t, "ecdsa-sha2-nistp256 AAAAkey", nil)
	out, err := pinBackupSSHHostKey("", "SSH_HOST_NAME=10.0.0.5\nSSH_USER=bk\n# SSH_IDENTITY_PASSPHRASE=\n\nNOTIFICATION_LEVEL=error\n")
	if err != nil {
		t.Fatal(err)
	}
	if (*calls)[0] != "10.0.0.5:22@bk" {
		t.Fatalf("expected the default port 22 and the SSH user, got %v", *calls)
	}
	if !strings.Contains(out, "# SSH_IDENTITY_PASSPHRASE=\nSSH_HOST_PUBLIC_KEY=\"ecdsa-sha2-nistp256 AAAAkey\"\n\nNOTIFICATION_LEVEL=error\n") {
		t.Fatalf("expected the key right after the SSH section, got:\n%s", out)
	}
}

func TestPinBackupSSHHostKeyFetchError(t *testing.T) {
	stubBackupsHostKey(t, "", errors.New("connection refused"))
	out, err := pinBackupSSHHostKey("", sshEnvBefore)
	if err == nil || !strings.Contains(err.Error(), "box.example.com:22") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected an error naming the host, got %v", err)
	}
	if out != sshEnvBefore {
		t.Fatalf("content must be saved unchanged when the key can't be read, got:\n%s", out)
	}
}

func TestAPIUserBackupsConfigurationPinsHostKey(t *testing.T) {
	dir := t.TempDir()
	orig := BackupEnvPath
	BackupEnvPath = filepath.Join(dir, "backup.env")
	t.Cleanup(func() { BackupEnvPath = orig })
	stubBackupsHostKey(t, "", errors.New("timeout"))

	body := `{"backup_env": "SSH_HOST_NAME=box.example.com\nSSH_USER=u1\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/backups/user/configuration", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	(&APIBackups{}).ServeUserBackupsConfiguration(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"warning"`) || !strings.Contains(rec.Body.String(), "timeout") {
		t.Fatalf("expected a saved response with the host key warning, got %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(BackupEnvPath); string(got) != "SSH_HOST_NAME=box.example.com\nSSH_USER=u1\n" {
		t.Fatalf("expected the file saved as sent, got %q", got)
	}
}
