// This file supplies every page's shared sidebar/header/footer chrome
// (server-wide values plus per-request session/role state). Chrome is
// embedded into each page's own render-data struct so templates can read
// both sets of fields through one dot.
package handlers

import (
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/csrf"

	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// chromeSite holds server-wide values computed once at process startup,
// set once from main.go via InitChromeSiteInfo.
var chromeSite struct {
	PublicIP         string
	ServerHostname   string
	ForceDomainValue string
	PanelVersion     string
	LicenseType      string
	DevMode          bool
	ModulesConfig    string
	CustomCSSEnabled bool
}

// ChromeCustomCSSPath is checked once at startup (see InitChromeSiteInfo):
// if present, every page links it as an extra stylesheet. Picking up a new
// file, or one placed after startup, needs a restart -- same tradeoff as
// the other InitChromeSiteInfo values.
var ChromeCustomCSSPath = "/usr/local/admin/custom.css"

// ChromeTourSkipFilePath marks that the onboarding tour has been dismissed.
var ChromeTourSkipFilePath = "/etc/openpanel/openadmin/tour.skip"

// ChromeQuickStartSkipFilePath marks that the post-login "Quick Start"
// onboarding page has been dismissed.
var ChromeQuickStartSkipFilePath = "/etc/openpanel/openadmin/config/quick_start.dismissed"

// quickStartDismissed reports whether the Quick Start page has been
// permanently dismissed.
func quickStartDismissed() bool {
	_, err := os.Stat(ChromeQuickStartSkipFilePath)
	return err == nil
}

// ChromeOpenpanelRestartFlagPath / ChromeOpenadminRestartFlagPath are the
// two restart-flag files checked when building the chrome's
// restart-pending banner.
var (
	ChromeOpenpanelRestartFlagPath = "/root/openpanel_restart_needed"
	ChromeOpenadminRestartFlagPath = "/root/openadmin_restart_needed"
)

// InitChromeSiteInfo records the server-wide values computed once at
// startup. modulesConfigPath is the openpanel.config path passed to
// modulesEnabledList() on each request -- the enabled-modules list is read
// fresh every request, not cached at startup.
func InitChromeSiteInfo(publicIP, serverHostname, forceDomainValue, panelVersion, licenseType string, devMode bool, modulesConfigPath string) {
	chromeSite.PublicIP = publicIP
	chromeSite.ServerHostname = serverHostname
	chromeSite.ForceDomainValue = forceDomainValue
	chromeSite.PanelVersion = panelVersion
	chromeSite.LicenseType = licenseType
	chromeSite.DevMode = devMode
	chromeSite.ModulesConfig = modulesConfigPath
	chromeSite.CustomCSSEnabled = isRegularFile(ChromeCustomCSSPath)
}

// buildChrome computes the per-request chrome fields from the current
// session/role plus the startup globals recorded above.
func buildChrome(r *http.Request, title string) webtemplates.Chrome {
	user := auth.CurrentUser(r)
	role := ""
	username := ""
	if user != nil {
		role = user.Role
		username = user.Username
	}
	isReseller := role == "reseller"
	isAdmin := role == "admin"
	isUser := role == "user"

	unread := 0
	var restartMessages []string
	if !isReseller {
		// Counts every unread notification in the log, not just the last 5
		// lines -- capping at 5 made the sidebar badge stop moving sensibly
		// once there were more than 5 unread, since dismissing one only
		// ever nudged the count within that narrow window. This behavior is
		// intentional per an explicit product request; the display itself
		// still caps at "20+" for anything above 30 (see chrome_sidebar's
		// badge in _layout.html).
		lines, _ := readNotificationLines()
		for _, l := range lines {
			if strings.Contains(l, "UNREAD") {
				unread++
			}
		}

		openpanelRestart := false
		if raw, err := os.ReadFile(ChromeOpenpanelRestartFlagPath); err == nil {
			if strings.TrimSpace(string(raw)) != "" {
				openpanelRestart = true
			}
		}
		openadminRestart := false
		if _, err := os.Stat(ChromeOpenadminRestartFlagPath); err == nil {
			openadminRestart = true
		}
		switch {
		case openpanelRestart && openadminRestart:
			restartMessages = []string{"Pending changes in both OpenPanel and OpenAdmin. Restart both services."}
		case openpanelRestart:
			restartMessages = []string{"Pending changes for OpenPanel UI. Restart the 'openpanel' container."}
		case openadminRestart:
			restartMessages = []string{"Pending changes for OpenAdmin. Restart the 'admin' service."}
		}
	}

	_, tourSkipErr := os.Stat(ChromeTourSkipFilePath)
	tourShow := user != nil && !isReseller && os.IsNotExist(tourSkipErr)

	return webtemplates.Chrome{
		Title:               title,
		CurrentPath:         r.URL.Path,
		CurrentUser:         username,
		CSRFToken:           csrf.Token(r),
		IsReseller:          isReseller,
		IsAdmin:             isAdmin,
		IsUser:              isUser,
		PanelVersion:        chromeSite.PanelVersion,
		PublicIP:            chromeSite.PublicIP,
		ServerHostname:      chromeSite.ServerHostname,
		ForceDomainValue:    chromeSite.ForceDomainValue,
		LicenseType:         chromeSite.LicenseType,
		DevMode:             chromeSite.DevMode,
		EnabledModules:      modulesEnabledList(chromeSite.ModulesConfig),
		UnreadNotifications: unread,
		RestartMessages:     restartMessages,
		TourShow:            tourShow,
		CustomCSSEnabled:    chromeSite.CustomCSSEnabled,
	}
}

// mergeChrome flattens buildChrome's fields directly into a map-based
// render-data value (rather than embedding, which only works for typed
// structs), so map[string]interface{}-shaped handlers can use the same
// chrome_* templates as the typed ones.
func mergeChrome(data map[string]interface{}, r *http.Request, title string) map[string]interface{} {
	c := buildChrome(r, title)
	data["Title"] = c.Title
	data["CurrentPath"] = c.CurrentPath
	data["CurrentUser"] = c.CurrentUser
	data["CSRFToken"] = c.CSRFToken
	data["IsReseller"] = c.IsReseller
	data["IsAdmin"] = c.IsAdmin
	data["IsUser"] = c.IsUser
	data["PanelVersion"] = c.PanelVersion
	data["PublicIP"] = c.PublicIP
	data["ServerHostname"] = c.ServerHostname
	data["ForceDomainValue"] = c.ForceDomainValue
	data["LicenseType"] = c.LicenseType
	data["DevMode"] = c.DevMode
	data["EnabledModules"] = c.EnabledModules
	data["UnreadNotifications"] = c.UnreadNotifications
	data["RestartMessages"] = c.RestartMessages
	data["TourShow"] = c.TourShow
	data["CustomCSSEnabled"] = c.CustomCSSEnabled
	return data
}
