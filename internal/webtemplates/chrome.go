package webtemplates

import "html/template"

// Chrome holds the data every authenticated page needs to render the shared
// sidebar/header/footer chrome, plus per-request user/session context.
// Page-specific data structs embed this so templates can access both the
// page's own fields and the chrome fields through the same dot.
type Chrome struct {
	// SummaryTab shows the Daily Summary tab, SummaryAttention its orange dot
	SummaryTab       bool
	SummaryAttention bool

	Title       string
	CurrentPath string
	CurrentUser string
	CSRFToken   string

	IsReseller bool
	IsAdmin    bool
	IsUser     bool

	PanelVersion        string
	PublicIP            string
	ServerHostname      string
	ForceDomainValue    string
	LicenseType         string
	DevMode             bool
	EnabledModules      []string
	UnreadNotifications int
	RestartMessages     []string
	TourShow            bool
	CustomCSSEnabled    bool
	ClusterSwitcher     template.HTML
	ClusterCount        int

	// MenuStyle is "classic" (collapsible groups) or "modern" (one link per area, pages as tabs), from openpanel.config's menu_style
	MenuStyle   string
	NavItems    []NavItem // modern sidebar
	PageTabs    []NavLink // modern tab bar above the page content
	AreaLabel   string    // modern breadcrumb's first crumb, the active sidebar area
	AreaHref    string
	HelpDoc     string       // docs page behind the tab bar's Documentation button, e.g. "admin/settings/general"
	HelpPart    string       // optional part of HelpDoc to show, see helpSections
	BulkActions []BulkAction // buttons for _bulk.html, set by pages that have bulk selection
}
