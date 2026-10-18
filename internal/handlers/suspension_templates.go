package handlers

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SuspensionTemplatesDir holds the reason pages admins pick from when suspending a domain.
var SuspensionTemplatesDir = "/etc/openpanel/caddy/templates/suspended"

type suspensionTemplate struct {
	Slug    string
	Name    string
	Content string
}

var (
	suspensionSlugRe     = regexp.MustCompile(`^[a-z0-9_-]+$`)
	suspensionSlugStrip  = regexp.MustCompile(`[^a-z0-9]+`)
	suspensionNameLineRe = regexp.MustCompile(`^<!-- name: (.*) -->$`)
)

func validSuspensionSlug(slug string) bool {
	return len(slug) <= 64 && suspensionSlugRe.MatchString(slug)
}

func suspensionSlugFromName(name string) string {
	slug := strings.Trim(suspensionSlugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 64 {
		slug = strings.TrimRight(slug[:64], "-")
	}
	return slug
}

// cleanSuspensionName keeps the name safe inside the html comment and the vhost header line
func cleanSuspensionName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.ReplaceAll(name, "--", "-"))
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func splitSuspensionTemplate(slug, raw string) suspensionTemplate {
	first, rest, _ := strings.Cut(raw, "\n")
	if m := suspensionNameLineRe.FindStringSubmatch(strings.TrimRight(first, "\r")); m != nil {
		return suspensionTemplate{Slug: slug, Name: m[1], Content: rest}
	}
	return suspensionTemplate{Slug: slug, Name: slug, Content: raw}
}

func listSuspensionTemplates() []suspensionTemplate {
	entries, _ := os.ReadDir(SuspensionTemplatesDir)
	var out []suspensionTemplate
	for _, e := range entries {
		slug, ok := strings.CutSuffix(e.Name(), ".html")
		if e.IsDir() || !ok || !validSuspensionSlug(slug) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(SuspensionTemplatesDir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, splitSuspensionTemplate(slug, string(raw)))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func suspensionTemplateExists(slug string) bool {
	if !validSuspensionSlug(slug) {
		return false
	}
	_, err := os.Stat(filepath.Join(SuspensionTemplatesDir, slug+".html"))
	return err == nil
}

func saveSuspensionTemplate(slug, name, content string) error {
	if err := os.MkdirAll(SuspensionTemplatesDir, 0755); err != nil {
		return err
	}
	data := "<!-- name: " + cleanSuspensionName(name) + " -->\n" + content
	return os.WriteFile(filepath.Join(SuspensionTemplatesDir, slug+".html"), []byte(data), 0644)
}

// suspensionTemplateInUse lists domains currently serving this page, deleting it would 404 them
func suspensionTemplateInUse(slug string) []string {
	needle := "/suspended/" + slug + ".html"
	files, _ := filepath.Glob(filepath.Join(CaddyDomainsConfDir, "*.conf"))
	var domains []string
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), needle) {
			domains = append(domains, strings.TrimSuffix(filepath.Base(f), ".conf"))
		}
	}
	return domains
}

type domainSuspension struct {
	By      string
	Reason  string
	Comment string
}

// readDomainSuspension reads the header lines opencli domains-suspend writes above the caddy block
func readDomainSuspension(domainURL string) domainSuspension {
	f, err := os.Open(filepath.Join(CaddyDomainsConfDir, domainURL+".conf"))
	if err != nil {
		return domainSuspension{}
	}
	defer f.Close()
	return scanDomainSuspension(bufio.NewScanner(f))
}

func parseDomainSuspension(content string) domainSuspension {
	return scanDomainSuspension(bufio.NewScanner(strings.NewReader(content)))
}

func scanDomainSuspension(sc *bufio.Scanner) domainSuspension {
	var s domainSuspension
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "# ") {
			break
		}
		if v, ok := strings.CutPrefix(line, "# suspended_by: "); ok {
			s.By = v
		} else if v, ok := strings.CutPrefix(line, "# reason: "); ok {
			s.Reason = v
		} else if v, ok := strings.CutPrefix(line, "# comment: "); ok {
			s.Comment = v
		}
	}
	return s
}
