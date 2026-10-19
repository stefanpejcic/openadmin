package handlers

import (
	"context"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
	"openadmin/internal/webtemplates"
)

// same files the user panel's /emails/deliverability compares DNS against
var (
	DKIMKeysBasePath = "/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys"
	ZoneTemplatePath = "/etc/openpanel/bind9/zone_template.txt"
)

const (
	deliverabilityWorkers  = 16
	deliverabilityTimeout  = 4 * time.Second
	deliverabilityCacheTTL = 5 * time.Minute
)

var quotedChunkRe = regexp.MustCompile(`"([^"]*)"`)

// lookupTXT is swapped in tests so they never hit real DNS
var lookupTXT = func(ctx context.Context, name string) []string {
	ctx, cancel := context.WithTimeout(ctx, deliverabilityTimeout)
	defer cancel()
	records, _ := net.DefaultResolver.LookupTXT(ctx, name)
	return records
}

type deliverabilityRecord struct {
	Status   string `json:"status"` // ok, missing, mismatch or wrong_ip
	Current  string `json:"current"`
	Expected string `json:"expected"`
}

type deliverabilityRow struct {
	Domain   string               `json:"domain"`
	Owner    string               `json:"owner"`
	ServerIP string               `json:"server_ip"`
	OK       bool                 `json:"ok"`
	DKIM     deliverabilityRecord `json:"dkim"`
	SPF      deliverabilityRecord `json:"spf"`
	DMARC    deliverabilityRecord `json:"dmarc"`
}

// expectedDKIM joins the quoted chunks of the TXT record the mailserver generated for domain
func expectedDKIM(domain string) string {
	raw, err := os.ReadFile(DKIMKeysBasePath + "/" + domain + "/mail.txt")
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, m := range quotedChunkRe.FindAllStringSubmatch(string(raw), -1) {
		sb.WriteString(m[1])
	}
	return sb.String()
}

// zoneTemplateRecord pulls the quoted value of the first zone template line containing marker
func zoneTemplateRecord(template, marker string) string {
	for _, line := range strings.Split(template, "\n") {
		if strings.Contains(line, marker) {
			if m := quotedChunkRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func firstWithPrefix(records []string, prefix string) string {
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(r), prefix) {
			return r
		}
	}
	return ""
}

// checkDeliverability mirrors the user panel's checkDomainDeliverability
func checkDeliverability(ctx context.Context, domain, serverIP, zoneTemplate string) deliverabilityRow {
	row := deliverabilityRow{Domain: domain, ServerIP: serverIP}

	row.DKIM.Expected = expectedDKIM(domain)
	if txt := lookupTXT(ctx, "mail._domainkey."+domain); len(txt) > 0 {
		row.DKIM.Current = txt[0]
	}
	switch {
	case row.DKIM.Current == "":
		row.DKIM.Status = "missing"
	case row.DKIM.Expected != "" && strings.TrimSpace(row.DKIM.Current) == strings.TrimSpace(row.DKIM.Expected):
		row.DKIM.Status = "ok"
	default:
		row.DKIM.Status = "mismatch"
	}

	row.SPF.Expected = "v=spf1 ip4:" + serverIP + " +a +mx ~all"
	if t := zoneTemplateRecord(zoneTemplate, "spf1"); t != "" {
		row.SPF.Expected = strings.ReplaceAll(t, "{server_ip}", serverIP)
	}
	row.SPF.Current = firstWithPrefix(lookupTXT(ctx, domain), "v=spf1")
	switch {
	case row.SPF.Current == "":
		row.SPF.Status = "missing"
	case serverIP != "" && strings.Contains(row.SPF.Current, "ip4:"+serverIP):
		row.SPF.Status = "ok"
	default:
		row.SPF.Status = "wrong_ip"
	}

	row.DMARC.Expected = "v=DMARC1; p=none;"
	if t := zoneTemplateRecord(zoneTemplate, "_dmarc"); t != "" {
		row.DMARC.Expected = t
	}
	row.DMARC.Current = firstWithPrefix(lookupTXT(ctx, "_dmarc."+domain), "v=dmarc1")
	row.DMARC.Status = "missing"
	if row.DMARC.Current != "" {
		row.DMARC.Status = "ok"
	}

	row.OK = row.DKIM.Status == "ok" && row.SPF.Status == "ok" && row.DMARC.Status == "ok"
	return row
}

type deliverabilityResult struct {
	at   time.Time
	rows []deliverabilityRow
}

var (
	deliverabilityCacheMu sync.Mutex
	deliverabilityCache   deliverabilityResult
)

// allDeliverability checks every domain on the server, a few at a time, cached for a few minutes since it's all DNS
func (e *Emails) allDeliverability(refresh bool) []deliverabilityRow {
	deliverabilityCacheMu.Lock()
	cached := deliverabilityCache
	deliverabilityCacheMu.Unlock()
	if !refresh && !cached.at.IsZero() && time.Since(cached.at) < deliverabilityCacheTTL {
		return cached.rows
	}
	if e.MySQL == nil {
		return nil
	}
	domains, err := paneldb.GetAllDomains(e.MySQL)
	if err != nil {
		return nil
	}
	zoneTemplate := ""
	if raw, err := os.ReadFile(ZoneTemplatePath); err == nil {
		zoneTemplate = string(raw)
	}

	rows := make([]deliverabilityRow, len(domains))
	ips := map[string]string{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, deliverabilityWorkers)
	for i, d := range domains {
		domain, _ := d["domain_url"].(string)
		owner, _ := d["username"].(string)
		ip, ok := ips[owner]
		if !ok {
			// a dedicated IP is what the user's SPF has to allow, otherwise the server's
			if ip = firstIPFor(owner); ip == "" {
				ip = e.PublicIP
			}
			ips[owner] = ip
		}
		wg.Add(1)
		go func(i int, domain, owner, ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = checkDeliverability(context.Background(), strings.ToLower(domain), ip, zoneTemplate)
			rows[i].Owner = owner
		}(i, domain, owner, ip)
	}
	wg.Wait()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Domain < rows[j].Domain })

	deliverabilityCacheMu.Lock()
	deliverabilityCache = deliverabilityResult{at: time.Now(), rows: rows}
	deliverabilityCacheMu.Unlock()
	return rows
}

type deliverabilityPageData struct {
	webtemplates.Chrome
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeDeliverability handles GET /emails/deliverability, the rows come from ?output=json so the page itself opens right away
func (e *Emails) ServeDeliverability(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("output") == "json" {
		scope := scopeFor(r, e.MySQL)
		rows := filterRows(scope, e.allDeliverability(r.URL.Query().Get("refresh") != ""), func(d deliverabilityRow) bool { return scope.AllowsDomain(d.Domain) })
		if rows == nil {
			rows = []deliverabilityRow{}
		}
		writeJSON(w, map[string]interface{}{"domains": rows})
		return
	}
	webtemplates.Render(w, "emails_deliverability.html", deliverabilityPageData{
		Chrome:    buildChrome(r, "Email Deliverability"),
		CSRFToken: csrf.Token(r),
		Flashes:   auth.PopFlashes(w, r, e.Sessions),
	})
}
