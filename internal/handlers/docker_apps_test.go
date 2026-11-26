package handlers

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withScratchDockerApps(t *testing.T, yml string) {
	t.Helper()
	orig := DockerAppsTemplatePath
	DockerAppsTemplatePath = filepath.Join(t.TempDir(), "applications.yml")
	t.Cleanup(func() { DockerAppsTemplatePath = orig })
	if yml != "" {
		os.WriteFile(DockerAppsTemplatePath, []byte(yml), 0o644)
	}
}

const testDockerApps = `applications:
  - id: uptime-kuma
    name: Uptime Kuma
  - id: grafana
    name: Grafana
  - id: Bad ID
    name: Broken
  - id: grafana
    name: Duplicate
`

func TestLoadDockerAppChoices(t *testing.T) {
	withScratchDockerApps(t, testDockerApps)
	got := loadDockerAppChoices()
	if len(got) != 2 || got[0].ID != "uptime-kuma" || got[1].Name != "Grafana" {
		t.Fatalf("expected the two valid apps in file order, got %+v", got)
	}
	withScratchDockerApps(t, "")
	if got := loadDockerAppChoices(); got != nil {
		t.Fatalf("a missing file should give no apps, got %+v", got)
	}
}

func TestOpenpanelSettingsHiddenDockerApps(t *testing.T) {
	withScratchOpenpanelSettingsPaths(t)
	withScratchDockerApps(t, testDockerApps)
	os.WriteFile(OpenpanelSettingsConfigPath, []byte("[PANEL]\nhidden_docker_apps=\n"), 0o644)
	srv, client := newOpenpanelSettingsTestServer(t, &OpenpanelSettings{})

	resp, err := client.Get(srv.URL + "/settings/open-panel")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"Docker apps on the Auto Installer", `name="hidden_docker_apps"`, "Uptime Kuma", "grafana"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("page missing %q", want)
		}
	}

	form := validOpenpanelForm()
	form.Set("hidden_docker_apps", "grafana uptime-kuma")
	if _, err := client.PostForm(srv.URL+"/settings/open-panel", form); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "[PANEL]") || !strings.Contains(string(saved), "hidden_docker_apps=grafana uptime-kuma") {
		t.Fatalf("hidden apps not saved under [PANEL]:\n%s", saved)
	}

	form.Set("hidden_docker_apps", "grafana; rm -rf /")
	resp, err = client.PostForm(srv.URL+"/settings/open-panel", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	saved, _ = os.ReadFile(OpenpanelSettingsConfigPath)
	if !strings.Contains(string(saved), "hidden_docker_apps=grafana uptime-kuma") || !strings.Contains(string(body), "must be a space-separated list of valid names for hidden_docker_apps") {
		t.Fatalf("a bad value should be refused and the old one kept:\n%s", saved)
	}
}
