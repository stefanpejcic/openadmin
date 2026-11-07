package webtemplates

import (
	"strings"
	"unicode"
)

// names that stay capitalized wherever they appear in a title
var properNouns = map[string]bool{
	"Docker": true, "Roundcube": true, "Coraza": true, "Python": true, "Ruby": true, "Java": true,
	"Varnish": true, "Node": true, "Joomla": true, "Drupal": true, "Nextcloud": true, "Redis": true,
	"Memcached": true, "Elasticsearch": true, "Apache": true, "Nginx": true, "Caddy": true,
	"Imunify": true, "Mautic": true, "Moodle": true, "Grav": true, "Cloudflare": true, "Opencart": true,
	"Laravel": true, "Django": true, "Flask": true, "Ghost": true, "Pandoc": true, "Matomo": true,
	"Linux": true, "Git": true, "Composer": true, "Cron": true, "Roundcubemail": true, "Snappymail": true,
}

// sentenceCase turns "Settings (Email & Password Change)" into "Settings (email & password change)",
// keeping acronyms, words with inner capitals (MySQL, ClamAV) and known product names
func sentenceCase(s string) string {
	words := strings.Split(s, " ")
	first := true
	for i, w := range words {
		lead := strings.TrimLeft(w, "([\"'")
		if lead == "" {
			continue
		}
		if first {
			first = false
			continue
		}
		core := strings.TrimRight(lead, ")]\"',.:;")
		if keepCase(core) {
			continue
		}
		words[i] = strings.ToLower(w)
	}
	return strings.Join(words, " ")
}

func keepCase(w string) bool {
	if w == "" || properNouns[w] {
		return true
	}
	upper, inner := 0, false
	for i, r := range w {
		if unicode.IsUpper(r) {
			upper++
			if i > 0 {
				inner = true
			}
		}
		if unicode.IsDigit(r) {
			return true
		}
	}
	// DNS, WAF, MySQL, ClamAV, VirtualHosts, WebServer
	return inner || (upper > 1)
}
