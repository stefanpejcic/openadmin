package handlers

import (
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// DockerAppsTemplatePath is the local copy of the one-click Docker apps the OpenPanel Auto Installer lists
var DockerAppsTemplatePath = "/etc/openpanel/docker/templates/applications.yml"

type dockerAppChoice struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// same id rule the user panel uses, anything else it drops anyway
var dockerAppIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,40}$`)

// loadDockerAppChoices lists the apps admins can hide from the Auto Installer, nil when the file is missing or broken
func loadDockerAppChoices() []dockerAppChoice {
	raw, err := os.ReadFile(DockerAppsTemplatePath)
	if err != nil {
		return nil
	}
	var f struct {
		Applications []dockerAppChoice `yaml:"applications"`
	}
	if yaml.Unmarshal(raw, &f) != nil {
		return nil
	}
	var out []dockerAppChoice
	seen := map[string]bool{}
	for _, a := range f.Applications {
		if !dockerAppIDRe.MatchString(a.ID) || seen[a.ID] {
			continue
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	return out
}
