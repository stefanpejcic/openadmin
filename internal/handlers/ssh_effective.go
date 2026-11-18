package handlers

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sshd keeps the first value it reads and Ubuntu's sshd_config includes sshd_config.d/*.conf at the top,
// so a cloud-init drop-in used to win over what OpenAdmin wrote, ours sorts first
var SSHDDropInPath = "/etc/ssh/sshd_config.d/00-openadmin.conf"

// sshEffectiveRun is what sshd actually uses, from sshd -T
var sshEffectiveRun = func() (sshSettings, bool) {
	out, err := exec.Command("sshd", "-T").Output()
	if err != nil {
		return sshSettings{}, false
	}
	s := sshSettings{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "port":
			if s.Port == "" {
				s.Port = f[1]
			}
		case "passwordauthentication":
			s.PasswordAuth = f[1]
		case "pubkeyauthentication":
			s.PubkeyAuth = f[1]
		case "permitrootlogin":
			s.PermitRootLogin = f[1]
		}
	}
	return s, s.Port != ""
}

// sshValidateRun checks the config before a restart, a broken sshd_config would lock the server out
var sshValidateRun = func() error {
	return exec.Command("sshd", "-t").Run()
}

// sshCurrentSettings prefers sshd's effective values, the file parse is only a fallback
func sshCurrentSettings(raw string) sshSettings {
	if s, ok := sshEffectiveRun(); ok {
		return s
	}
	return sshParseSettings(raw)
}

// sshUsesDropIns is whether sshd_config pulls in the sshd_config.d directory our drop-in lives in
func sshUsesDropIns(raw string) bool {
	dir := filepath.Dir(SSHDDropInPath)
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && strings.EqualFold(f[0], "Include") && strings.HasPrefix(f[1], dir) {
			return true
		}
	}
	return false
}

func sshDropInContent(s sshSettings) string {
	return "# managed by OpenAdmin > Server > SSH\n" +
		"Port " + s.Port + "\n" +
		"PasswordAuthentication " + s.PasswordAuth + "\n" +
		"PubkeyAuthentication " + s.PubkeyAuth + "\n" +
		"PermitRootLogin " + s.PermitRootLogin + "\n"
}

// restoreFile puts back what was there before, or removes it if there was nothing
func restoreFile(path string, old []byte, existed bool) {
	if existed {
		_ = os.WriteFile(path, old, 0644)
	} else {
		_ = os.Remove(path)
	}
}
