package webtemplates

import (
	"strings"
	"testing"
)

func TestSiteTypeIcon(t *testing.T) {
	if got := SiteTypeLabel("WordPress"); got != "WordPress" {
		t.Errorf("label = %q", got)
	}
	if !strings.Contains(string(SiteTypeIcon("wordpress")), "bi-wordpress") {
		t.Error("expected the wordpress logo")
	}
	// docker-based types carry a suffix, like nodejs-20
	if got := SiteTypeLabel("nodejs-20"); got != "Node.js" {
		t.Errorf("label = %q", got)
	}
	if SiteTypeIcon("something") != siteTypeFallbackIcon || SiteTypeLabel("something") != "Something" {
		t.Error("expected the fallback for unknown types")
	}
}

func TestVulnSourceSprite(t *testing.T) {
	sprite := string(VulnSourceSprite())
	if strings.Count(sprite, "<symbol ") != len(vulnSourceLogos) || strings.Contains(sprite, `viewBox=""`) {
		t.Errorf("bad sprite: %.300s", sprite)
	}
	logo := string(VulnSourceLogo("https://www.wordfence.com/threat-intel/x"))
	if !strings.Contains(logo, `href="#vuln-src-1"`) || !strings.Contains(logo, `width="76"`) || !strings.Contains(logo, `fill="none"`) {
		t.Errorf("logo = %s", logo)
	}
	if VulnSourceLogo("https://example.com/x") != "" {
		t.Error("expected no logo for unknown sources")
	}
}

func TestSiteTypeSprite(t *testing.T) {
	sprite := string(SiteTypeSprite([]string{"wordpress", "WordPress", "nodejs-20", "something"}))
	if strings.Count(sprite, "<symbol ") != 3 {
		t.Errorf("expected one symbol per logo: %.200s", sprite)
	}
	use := string(SiteTypeUse("java"))
	if !strings.Contains(use, `href="#site-type-java"`) || !strings.Contains(use, `stroke="currentColor"`) || !strings.Contains(use, `fill="none"`) {
		t.Errorf("use = %s", use)
	}
}
