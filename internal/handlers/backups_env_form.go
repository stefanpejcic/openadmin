package handlers

import (
	"regexp"
	"strings"
)

// same destination sections as OpenPanel's backups module, order matters for the dropdown
var backupEnvSectionOrder = []string{"s3", "webdav", "ssh", "azure", "dropbox"}

var backupEnvSectionLabels = map[string]string{
	"s3":      "S3 compatible storage",
	"webdav":  "WebDAV",
	"ssh":     "SSH / SFTP",
	"azure":   "Azure Blob Storage",
	"dropbox": "Dropbox",
}

var backupEnvSectionKeys = map[string][]string{
	"s3": {
		"AWS_S3_BUCKET_NAME", "AWS_S3_PATH", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_IAM_ROLE_ENDPOINT", "AWS_ENDPOINT", "AWS_ENDPOINT_PROTO", "AWS_ENDPOINT_INSECURE",
		"AWS_ENDPOINT_CA_CERT", "AWS_STORAGE_CLASS", "AWS_PART_SIZE",
	},
	"webdav": {"WEBDAV_URL", "WEBDAV_USERNAME", "WEBDAV_PATH", "WEBDAV_PASSWORD", "WEBDAV_URL_INSECURE"},
	"ssh": {
		"SSH_HOST_NAME", "SSH_PORT", "SSH_REMOTE_PATH", "SSH_USER", "SSH_PASSWORD",
		"SSH_IDENTITY_FILE", "SSH_IDENTITY_PASSPHRASE", "SSH_HOST_PUBLIC_KEY",
	},
	"azure": {
		"AZURE_STORAGE_ACCOUNT_NAME", "AZURE_STORAGE_PRIMARY_ACCOUNT_KEY", "AZURE_STORAGE_CONNECTION_STRING",
		"AZURE_STORAGE_CONTAINER_NAME", "AZURE_STORAGE_ENDPOINT", "AZURE_STORAGE_ACCESS_TIER",
	},
	"dropbox": {"DROPBOX_REMOTE_PATH", "DROPBOX_APP_KEY", "DROPBOX_APP_SECRET", "DROPBOX_CONCURRENCY_LEVEL", "DROPBOX_REFRESH_TOKEN"},
}

// matches "KEY=value" and "# KEY=value", group 1 is the comment marker
var backupEnvLineRe = regexp.MustCompile(`^\s*(#\s*)?([A-Z][A-Z0-9_]*)=(.*)$`)

var backupEnvSecretRe = regexp.MustCompile(`PASSWORD|SECRET|PASSPHRASE|TOKEN|ACCOUNT_KEY|CONNECTION_STRING|APP_KEY`)

type backupEnvField struct {
	Key, Value string
	Secret     bool
}

type backupEnvSection struct {
	Name, Label string
	Fields      []backupEnvField
}

type backupEnvForm struct {
	Destination string
	// more than one section uncommented, the form shows the first and saving fixes it
	MultipleActive bool
	Sections       []backupEnvSection
	Settings       []backupEnvField
}

func backupEnvSectionOf(key string) string {
	for _, s := range backupEnvSectionOrder {
		for _, k := range backupEnvSectionKeys[s] {
			if k == key {
				return s
			}
		}
	}
	return ""
}

// strip one pair of wrapping quotes, podman-compose would otherwise pass them through literally
func backupEnvUnquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// parseBackupEnvForm reads destination fields (commented or not) and the active general settings out of backup.env
func parseBackupEnvForm(content string) backupEnvForm {
	values := map[string]string{}
	active := map[string]bool{}
	var settings []backupEnvField

	for _, line := range strings.Split(content, "\n") {
		m := backupEnvLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		commented, key, value := m[1] != "", m[2], backupEnvUnquote(m[3])
		section := backupEnvSectionOf(key)
		switch {
		case section != "":
			// an uncommented value wins over a commented example
			if _, seen := values[key]; !seen || !commented {
				values[key] = value
			}
			if !commented {
				active[section] = true
			}
		case !commented:
			settings = append(settings, backupEnvField{Key: key, Value: value, Secret: backupEnvSecretRe.MatchString(key)})
		}
	}

	form := backupEnvForm{Settings: settings}
	activeCount := 0
	for _, s := range backupEnvSectionOrder {
		sec := backupEnvSection{Name: s, Label: backupEnvSectionLabels[s]}
		for _, k := range backupEnvSectionKeys[s] {
			sec.Fields = append(sec.Fields, backupEnvField{Key: k, Value: values[k], Secret: backupEnvSecretRe.MatchString(k)})
		}
		form.Sections = append(form.Sections, sec)
		if active[s] {
			activeCount++
			if form.Destination == "" {
				form.Destination = s
			}
		}
	}
	form.MultipleActive = activeCount > 1
	return form
}

// renderBackupEnvForm uncomments the chosen destination with the posted values, comments every other destination and updates the general settings in place
func renderBackupEnvForm(content, destination string, destValues, settings map[string]string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	written := map[string]bool{}

	// empty destination values stay commented, an empty-but-set number would break the backup container
	destLine := func(key, value string) string {
		if value == "" {
			return "# " + key + "="
		}
		return key + "=" + value
	}

	for i, line := range lines {
		m := backupEnvLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		commented, key, value := m[1] != "", m[2], backupEnvUnquote(m[3])
		section := backupEnvSectionOf(key)
		switch {
		case section == destination && section != "":
			if written[key] {
				lines[i] = "# " + key + "=" + value
				continue
			}
			if v, ok := destValues[key]; ok {
				value = v
			}
			lines[i] = destLine(key, value)
			written[key] = true
		case section != "":
			if !commented {
				lines[i] = "# " + key + "=" + value
			}
		case !commented:
			if v, ok := settings[key]; ok {
				lines[i] = key + "=" + v
			}
		}
	}

	// destination keys the file didn't have yet
	for _, k := range backupEnvSectionKeys[destination] {
		if !written[k] && destValues[k] != "" {
			lines = append(lines, k+"="+destValues[k])
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
