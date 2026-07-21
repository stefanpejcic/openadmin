package handlers

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"image/png"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// TwoFA bundles the /security/2fa self-enrollment handlers, mirroring
// modules/security/twofa.py. Verification of an already-enabled user's TOTP
// code at login time lives in login.go (HandleTwoFASubmit) -- this file is
// the setup/enrollment flow.
type TwoFA struct {
	DB       *admindb.DB
	Sessions *auth.Manager
}

const pendingTOTPSecretKey = "pending_totp_secret"

type twoFASettingsPageData struct {
	webtemplates.Chrome
	TOTPEnabled bool
	// QRDataURI is template.URL (not string): html/template's contextual
	// autoescaper treats a plain string with a "data:" scheme in a src=""
	// attribute as unsafe and silently replaces it with a "#ZgotmplZ"
	// placeholder. This is self-generated PNG data (see qrDataURIForSecret),
	// not user input, so it's safe to mark pre-vetted.
	QRDataURI template.URL
	Secret    string
	CSRFToken string
	Flashes   []auth.Flash
}

// ServeSettings handles GET /security/2fa, mirroring twofa_settings().
func (t *TwoFA) ServeSettings(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	user, err := t.DB.UserByID(currentUser.ID)
	if err != nil {
		http.Error(w, "user not found", http.StatusInternalServerError)
		return
	}

	data := twoFASettingsPageData{
		Chrome:      buildChrome(r, "Two-Factor Authentication"),
		TOTPEnabled: user.TOTPEnabled,
		CSRFToken:   csrf.Token(r),
		Flashes:     auth.PopFlashes(w, r, t.Sessions),
	}

	if !user.TOTPEnabled {
		sess, _ := t.Sessions.Get(r)
		secret, _ := sess.Values[pendingTOTPSecretKey].(string)
		if secret == "" {
			key, err := totp.Generate(totp.GenerateOpts{Issuer: "OpenAdmin", AccountName: user.Username})
			if err != nil {
				http.Error(w, "could not generate 2FA secret", http.StatusInternalServerError)
				return
			}
			secret = key.Secret()
			sess.Values[pendingTOTPSecretKey] = secret
			_ = sess.Save(r, w)
		}

		qrURI, err := qrDataURIForSecret(secret, user.Username)
		if err == nil {
			data.QRDataURI = template.URL(qrURI)
			data.Secret = secret
		}
	}

	webtemplates.Render(w, "twofa.html", data)
}

// qrDataURIForSecret rebuilds the same otpauth:// URL pyotp's
// provisioning_uri() would (Issuer=OpenAdmin, default SHA1/6-digit/30s,
// matching the ValidateOpts login.go's HandleTwoFASubmit already verifies
// against) and renders it as a PNG data URI, mirroring make_qr_data_uri().
func qrDataURIForSecret(secret, accountName string) (string, error) {
	key, err := otp.NewKeyFromURL("otpauth://totp/OpenAdmin:" + accountName + "?secret=" + secret + "&issuer=OpenAdmin")
	if err != nil {
		return "", err
	}
	img, err := key.Image(256, 256)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// HandleEnable handles POST /security/2fa/enable, mirroring twofa_enable().
func (t *TwoFA) HandleEnable(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	sess, _ := t.Sessions.Get(r)
	secret, _ := sess.Values[pendingTOTPSecretKey].(string)
	code := strings.TrimSpace(r.FormValue("code"))

	if secret == "" {
		auth.AddFlash(w, r, t.Sessions, "Error: 2FA setup session expired, please try again.", "error")
		http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
		return
	}

	valid, _ := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if !valid {
		auth.AddFlash(w, r, t.Sessions, "Error: Invalid authentication code. Please try again.", "error")
		http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
		return
	}

	if err := t.DB.SetTOTP(currentUser.Username, secret, true); err != nil {
		auth.AddFlash(w, r, t.Sessions, "Error: Could not enable two-factor authentication.", "error")
		http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
		return
	}
	delete(sess.Values, pendingTOTPSecretKey)
	_ = sess.Save(r, w)

	auth.AddFlash(w, r, t.Sessions, "Two-factor authentication has been enabled.", "success")
	http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
}

// HandleDisable handles POST /security/2fa/disable, mirroring twofa_disable().
func (t *TwoFA) HandleDisable(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	user, err := t.DB.UserByID(currentUser.ID)
	if err != nil {
		http.Error(w, "user not found", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPasswordHash(user.PasswordHash, r.FormValue("password")) {
		auth.AddFlash(w, r, t.Sessions, "Error: Incorrect password.", "error")
		http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
		return
	}

	if err := t.DB.SetTOTP(user.Username, "", false); err != nil {
		auth.AddFlash(w, r, t.Sessions, "Error: Could not disable two-factor authentication.", "error")
		http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
		return
	}

	auth.AddFlash(w, r, t.Sessions, "Two-factor authentication has been disabled.", "success")
	http.Redirect(w, r, "/security/2fa", http.StatusSeeOther)
}
