package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncImunifyPanelGUI(t *testing.T) {
	oldUnit, oldExec, oldAvail := imunifyPanelGUIUnitPath, imunifyPanelGUIExec, imunifyCommandAvailableRun
	defer func() {
		imunifyPanelGUIUnitPath, imunifyPanelGUIExec, imunifyCommandAvailableRun = oldUnit, oldExec, oldAvail
	}()

	var ran []string
	imunifyPanelGUIExec = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}

	cases := []struct {
		name      string
		modules   string
		installed bool
		hasUnit   bool
		want      string
	}{
		{"on with unit", "dns,imunifyav,malware_scan", true, true, "systemctl enable --now imunify-gui-panel"},
		{"on without unit", "imunifyav", true, false, "opencli imunify start"},
		{"on but not installed", "imunifyav", false, false, ""},
		{"off with unit", "dns,malware_scan", true, true, "systemctl disable --now imunify-gui-panel"},
		{"off without unit", "dns", true, false, ""},
		{"substring is not the module", "imunifyav_extra", true, true, "systemctl disable --now imunify-gui-panel"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ran = nil
			imunifyCommandAvailableRun = func(string) bool { return c.installed }
			imunifyPanelGUIUnitPath = filepath.Join(t.TempDir(), "imunify-gui-panel.service")
			if c.hasUnit {
				os.WriteFile(imunifyPanelGUIUnitPath, []byte("[Service]\n"), 0644)
			}
			syncImunifyPanelGUI(c.modules)
			if got := strings.Join(ran, ";"); got != c.want {
				t.Fatalf("ran %q, want %q", got, c.want)
			}
		})
	}
}
