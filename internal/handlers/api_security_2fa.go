// This file implements the JSON REST API's caller-owns-the-account security
// surface: 2FA enrollment/enable/disable and passkey management for
// whichever user the bearer token identifies. Unlike security_toggles.go's
// admin-picks-a-target-user pages, these routes never take a username
// parameter -- they always act on the caller's own account, resolved via
// APIAuth.ActingAPIUserOr404.
package handlers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"openadmin/internal/auth"
)

// APISecurity2FA bundles the /api/security/2fa* and /api/security/passkeys
// handlers.
type APISecurity2FA struct {
	Auth *APIAuth
}

// ServeStatus handles GET /api/security/2fa. Unlike the HTML enrollment
// page (TwoFA.ServeSettings), this never caches the pending secret in a
// session -- the JSON API is stateless, so every call while 2FA is
// disabled mints and returns a brand new secret/QR code.
func (a *APISecurity2FA) ServeStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := a.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	if user.TOTPEnabled {
		writeJSON(w, map[string]bool{"totp_enabled": true})
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "OpenAdmin", AccountName: user.Username})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not generate 2FA secret")
		return
	}
	secret := key.Secret()

	qrURI, err := qrDataURIForSecret(secret, user.Username)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not generate 2FA secret")
		return
	}

	writeJSON(w, map[string]interface{}{
		"totp_enabled": false,
		"secret":       secret,
		"qr_data_uri":  qrURI,
	})
}

// HandleEnable handles POST /api/security/2fa/enable. The secret/code are
// validated before the acting user's row is even looked up -- an invalid
// code is reported as a 400 even for a token whose user has since been
// deleted.
func (a *APISecurity2FA) HandleEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	code := strings.TrimSpace(body.Code)
	if body.Secret == "" || code == "" {
		writeJSONError(w, http.StatusBadRequest, "secret and code are required")
		return
	}

	valid, _ := totp.ValidateCustom(code, body.Secret, time.Now(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "Invalid authentication code.")
		return
	}

	user, ok := a.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	if err := a.Auth.DB.SetTOTP(user.Username, body.Secret, true); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "Two-factor authentication has been enabled.",
	})
}

// HandleDisable handles POST /api/security/2fa/disable.
func (a *APISecurity2FA) HandleDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	user, ok := a.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	if !auth.CheckPasswordHash(user.PasswordHash, body.Password) {
		writeJSONError(w, http.StatusBadRequest, "Incorrect password.")
		return
	}

	if err := a.Auth.DB.SetTOTP(user.Username, "", false); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "Two-factor authentication has been disabled.",
	})
}

// ServePasskeys handles GET/POST/DELETE /api/security/passkeys: listing,
// renaming, or deleting the caller's own passkeys. Registration itself
// isn't exposed here -- it requires a browser/authenticator round trip
// (see passkeys.go's HandleRegisterBegin/Complete) that has no equivalent
// over a bare JSON API.
func (a *APISecurity2FA) ServePasskeys(w http.ResponseWriter, r *http.Request) {
	user, ok := a.Auth.ActingAPIUserOr404(w, r)
	if !ok {
		return
	}

	if r.Method == http.MethodGet {
		creds, err := a.Auth.DB.CredentialsByUserID(user.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Newest first, matching order_by(created_at.desc()).
		sort.Slice(creds, func(i, j int) bool { return creds[i].CreatedAt.After(creds[j].CreatedAt) })

		out := make([]map[string]interface{}, 0, len(creds))
		for _, c := range creds {
			var name interface{}
			if c.Name.Valid {
				name = c.Name.String
			}
			var createdAt interface{}
			if !c.CreatedAt.IsZero() {
				createdAt = c.CreatedAt.Format(time.RFC3339)
			}
			out = append(out, map[string]interface{}{
				"id":         c.ID,
				"name":       name,
				"created_at": createdAt,
			})
		}
		writeJSON(w, map[string]interface{}{"passkeys": out})
		return
	}

	var body struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if !apiDecodeJSONBody(r, &body) {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	if !a.Auth.DB.CredentialBelongsToUser(body.ID, user.ID) {
		writeJSONError(w, http.StatusNotFound, "Invalid passkey.")
		return
	}

	if r.Method == http.MethodDelete {
		if err := a.Auth.DB.DeleteCredentialByID(body.ID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "Passkey removed."})
		return
	}

	newName := strings.TrimSpace(body.Name)
	if len(newName) > 100 {
		newName = newName[:100]
	}
	if newName == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required.")
		return
	}
	if err := a.Auth.DB.RenameCredential(body.ID, newName); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "message": "Passkey renamed."})
}
