package handlers

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var errBackupsGotHostKey = errors.New("got host key")

// backupsFetchSSHHostKey reads the server's host key in authorized_keys form without logging in, swapped in tests
var backupsFetchSSHHostKey = func(host, port, user string) (string, error) {
	var found string
	cfg := &ssh.ClientConfig{
		User: user,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			found = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			return errBackupsGotHostKey
		},
		Timeout: 15 * time.Second,
	}
	_, err := ssh.Dial("tcp", net.JoinHostPort(host, port), cfg)
	if found != "" {
		return found, nil
	}
	if err == nil {
		err = errors.New("no host key received")
	}
	return "", err
}

// backupEnvValues is every uncommented KEY=value of backup.env, quotes stripped
func backupEnvValues(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		if m := backupEnvLineRe.FindStringSubmatch(line); m != nil && m[1] == "" {
			values[m[2]] = backupEnvUnquote(m[3])
		}
	}
	return values
}

// setBackupEnvValue sets key on its own line, reusing the uncommented or commented one or going after the last line of the same section
func setBackupEnvValue(content, key, value string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	commented, after := -1, -1
	prefix := key[:strings.Index(key, "_")+1]
	for i, line := range lines {
		m := backupEnvLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[2] == key {
			if m[1] == "" {
				lines[i] = key + "=" + value
				return strings.Join(lines, "\n") + "\n"
			}
			if commented < 0 {
				commented = i
			}
		}
		if strings.HasPrefix(m[2], prefix) {
			after = i
		}
	}
	switch {
	case commented >= 0:
		lines[commented] = key + "=" + value
	case after >= 0:
		lines = append(lines[:after+1], append([]string{key + "=" + value}, lines[after+1:]...)...)
	default:
		lines = append(lines, key+"="+value)
	}
	return strings.Join(lines, "\n") + "\n"
}

// pinBackupSSHHostKey stores the SSH destination's host key when there's none or the host moved, docker-volume-backup won't connect without it
func pinBackupSSHHostKey(before, after string) (string, error) {
	prev, next := backupEnvValues(before), backupEnvValues(after)
	host := next["SSH_HOST_NAME"]
	if host == "" {
		return after, nil
	}
	port := next["SSH_PORT"]
	if port == "" {
		port = "22"
	}
	key := next["SSH_HOST_PUBLIC_KEY"]
	moved := prev["SSH_HOST_NAME"] != host || prev["SSH_PORT"] != next["SSH_PORT"]
	// a key kept for the same host, or one the admin just typed, is left alone
	if key != "" && (!moved || key != prev["SSH_HOST_PUBLIC_KEY"]) {
		return after, nil
	}
	fetched, err := backupsFetchSSHHostKey(host, port, next["SSH_USER"])
	if err != nil {
		return after, fmt.Errorf("the SSH host key of %s couldn't be read (%v), backups to it won't run until SSH_HOST_PUBLIC_KEY is set", net.JoinHostPort(host, port), err)
	}
	return setBackupEnvValue(after, "SSH_HOST_PUBLIC_KEY", `"`+fetched+`"`), nil
}
