package webtemplates

// Chrome holds the data every authenticated page needs to render the shared
// sidebar/header/footer chrome, plus per-request user/session context.
// Page-specific data structs embed this so templates can access both the
// page's own fields and the chrome fields through the same dot.
type Chrome struct {
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
}
