// Package config reads OpenPanel's flat INI-style config files
// ([section]\nkey=value\n...).
package config

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"sync"
)

// Data is a parsed config file: section -> key -> value.
type Data map[string]map[string]string

func (d Data) Get(section, key, fallback string) string {
	if d == nil {
		return fallback
	}
	sec, ok := d[section]
	if !ok {
		return fallback
	}
	v, ok := sec[key]
	if !ok {
		return fallback
	}
	return v
}

// Set assigns config[section][key] = value, creating the section if it
// doesn't already exist.
func (d Data) Set(section, key, value string) {
	if d[section] == nil {
		d[section] = map[string]string{}
	}
	d[section][key] = value
}

// Load parses path into Data. A missing file returns an empty Data (not an
// error), since config files here are optional.
func Load(path string) Data {
	data := Data{}

	f, err := os.Open(path)
	if err != nil {
		return data
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if data[section] == nil {
			data[section] = map[string]string{}
		}
		data[section][strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return data
}

// Save writes data back to path in the same "[section]\nkey=value\n\n"
// format Load reads (no spaces around "="). Section and key order is
// sorted for deterministic output; nothing reads this file in a way
// that's sensitive to key ordering, only to section/key presence.
func Save(path string, data Data) error {
	var b strings.Builder

	sections := make([]string, 0, len(data))
	for s := range data {
		sections = append(sections, s)
	}
	sort.Strings(sections)

	for _, s := range sections {
		b.WriteString("[" + s + "]\n")
		keys := make([]string, 0, len(data[s]))
		for k := range data[s] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k + "=" + data[s][k] + "\n")
		}
		b.WriteString("\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// OpenpanelConfigPath and AdminConfigPath are vars (not consts) so tests can
// point them at scratch fixtures instead of the real /etc paths.
var (
	OpenpanelConfigPath = "/etc/openpanel/openpanel/conf/openpanel.config"
	AdminConfigPath     = "/etc/openpanel/openadmin/config/admin.ini"
)

var (
	openpanelOnce sync.Once
	openpanelData Data
	adminOnce     sync.Once
	adminData     Data
)

// Openpanel returns the parsed /etc/openpanel/openpanel/conf/openpanel.config,
// cached for the life of the process: this config rarely changes without a
// restart already being required.
func Openpanel() Data {
	openpanelOnce.Do(func() { openpanelData = Load(OpenpanelConfigPath) })
	return openpanelData
}

// Admin returns the parsed /etc/openpanel/openadmin/config/admin.ini.
func Admin() Data {
	adminOnce.Do(func() { adminData = Load(AdminConfigPath) })
	return adminData
}
