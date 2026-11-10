package handlers

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

// written by opencli imunify install/start, serves imunify's ui to the user panel on the panel network gateway
var imunifyPanelGUIUnitPath = "/etc/systemd/system/imunify-gui-panel.service"

var imunifyPanelGUIExec = func(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		log.Printf("imunify panel gui: %s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return err
}

// syncImunifyPanelGUI keeps the user panel's imunify ui running only while the imunifyav module is on, like clamav follows malware_scan
func syncImunifyPanelGUI(enabledModules string) {
	_, statErr := os.Stat(imunifyPanelGUIUnitPath)
	hasUnit := statErr == nil
	if !hasModule(strings.Split(enabledModules, ","), "imunifyav") {
		if hasUnit {
			_ = imunifyPanelGUIExec("systemctl", "disable", "--now", "imunify-gui-panel")
		}
		return
	}
	if !imunifyInstalled() {
		return
	}
	if hasUnit {
		_ = imunifyPanelGUIExec("systemctl", "enable", "--now", "imunify-gui-panel")
		return
	}
	// installed before the user panel page existed, start writes the unit with its key and router
	_ = imunifyPanelGUIExec("opencli", "imunify", "start")
}
