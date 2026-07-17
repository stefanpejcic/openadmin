package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/csrf"

	"openadmin/internal/admindb"
	"openadmin/internal/auth"
	"openadmin/internal/webtemplates"
)

// Passkeys bundles the /security/passkeys handlers, mirroring
// modules/security/passkeys.py. Assertion (login-time) verification lives
// in login.go; this file is the registration/management flow.
type Passkeys struct {
	DB       *admindb.DB
	Sessions *auth.Manager
}

const passkeyRegisterChallengeKey = "passkey_register_challenge"

type passkeyRow struct {
	ID        int64
	Name      string
	CreatedAt string
}

type passkeysSettingsPageData struct {
	webtemplates.Chrome
	Passkeys        []passkeyRow
	WebauthnCapable bool
	CSRFToken       string
	Flashes         []auth.Flash
}

// ServeSettings handles GET /security/passkeys, mirroring passkeys_settings().
func (p *Passkeys) ServeSettings(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	creds, err := p.DB.CredentialsByUserID(currentUser.ID)
	if err != nil {
		http.Error(w, "could not load passkeys", http.StatusInternalServerError)
		return
	}

	rows := make([]passkeyRow, 0, len(creds))
	for _, c := range creds {
		name := c.Name.String
		if name == "" {
			name = "Passkey"
		}
		rows = append(rows, passkeyRow{ID: c.ID, Name: name, CreatedAt: c.CreatedAt.Format("2006-01-02 15:04")})
	}

	webtemplates.Render(w, "passkeys.html", passkeysSettingsPageData{
		Chrome:          buildChrome(r, "Passkeys"),
		Passkeys:        rows,
		WebauthnCapable: isWebauthnCapableHost(webauthnRPID(r)),
		CSRFToken:       csrf.Token(r),
		Flashes:         auth.PopFlashes(w, r, p.Sessions),
	})
}

// registrationUser adapts admindb's User + all of their existing
// credentials to go-webauthn's User interface, used to build the
// exclude-credentials list during registration (so a security key already
// registered can't be re-registered) and for FinishRegistration's internal
// checks.
type registrationUser struct {
	user  *admindb.User
	creds []admindb.WebauthnCredential
}

func (u *registrationUser) WebAuthnID() []byte          { return []byte(strconv.FormatInt(u.user.ID, 10)) }
func (u *registrationUser) WebAuthnName() string        { return u.user.Username }
func (u *registrationUser) WebAuthnDisplayName() string { return u.user.Username }
func (u *registrationUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.creds))
	for _, c := range u.creds {
		credID, err := base64.RawURLEncoding.DecodeString(c.CredentialID)
		if err != nil {
			continue
		}
		out = append(out, webauthn.Credential{ID: credID})
	}
	return out
}

// HandleRegisterBegin handles POST /security/passkeys/register/begin,
// mirroring passkeys_register_begin().
func (p *Passkeys) HandleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	rpID := webauthnRPID(r)
	if !isWebauthnCapableHost(rpID) {
		writeJSONError(w, http.StatusBadRequest, webauthnInvalidHostError)
		return
	}

	currentUser := auth.CurrentUser(r)
	creds, err := p.DB.CredentialsByUserID(currentUser.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}

	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "OpenAdmin",
		RPOrigins:     []string{webauthnOrigin(r)},
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}

	regUser := &registrationUser{user: currentUser, creds: creds}
	creation, sessionData, err := wa.BeginRegistration(regUser,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
	)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}

	sess, _ := p.Sessions.Get(r)
	sess.Values[passkeyRegisterChallengeKey] = *sessionData
	sess.Save(r, w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(creation.Response)
}

type registerCompleteBody struct {
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name"`
}

// HandleRegisterComplete handles POST /security/passkeys/register/complete,
// mirroring passkeys_register_complete().
func (p *Passkeys) HandleRegisterComplete(w http.ResponseWriter, r *http.Request) {
	sess, _ := p.Sessions.Get(r)
	sd, ok := sess.Values[passkeyRegisterChallengeKey].(webauthn.SessionData)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Passkey registration session expired, please try again.")
		return
	}
	delete(sess.Values, passkeyRegisterChallengeKey)
	sess.Save(r, w)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	var body registerCompleteBody
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Credential) == 0 {
		writeJSONError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 100 {
		name = name[:100]
	}

	currentUser := auth.CurrentUser(r)
	rpID := webauthnRPID(r)
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "OpenAdmin",
		RPOrigins:     []string{webauthnOrigin(r)},
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not verify passkey")
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(body.Credential)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not verify passkey.")
		return
	}

	regUser := &registrationUser{user: currentUser}
	cred, err := wa.CreateCredential(regUser, sd, parsed)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not verify passkey.")
		return
	}

	credentialID := base64.RawURLEncoding.EncodeToString(cred.ID)
	publicKey := base64.RawURLEncoding.EncodeToString(cred.PublicKey)

	if _, err := p.DB.CreateCredential(currentUser.ID, credentialID, publicKey, uint32(cred.Authenticator.SignCount), name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Could not save passkey.")
		return
	}

	auth.AddFlash(w, r, p.Sessions, "Passkey added.", "success")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// HandleRename handles POST /security/passkeys/rename, mirroring passkeys_rename().
func (p *Passkeys) HandleRename(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	newName := strings.TrimSpace(r.FormValue("name"))
	if len(newName) > 100 {
		newName = newName[:100]
	}

	if id == 0 || newName == "" || !p.DB.CredentialBelongsToUser(id, currentUser.ID) {
		auth.AddFlash(w, r, p.Sessions, "Error: Invalid passkey or name.", "error")
		http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
		return
	}

	if err := p.DB.RenameCredential(id, newName); err != nil {
		auth.AddFlash(w, r, p.Sessions, "Error: Invalid passkey or name.", "error")
		http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
		return
	}

	auth.AddFlash(w, r, p.Sessions, "Passkey renamed.", "success")
	http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
}

// HandleDelete handles POST /security/passkeys/delete, mirroring passkeys_delete().
func (p *Passkeys) HandleDelete(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	if id == 0 || !p.DB.CredentialBelongsToUser(id, currentUser.ID) {
		auth.AddFlash(w, r, p.Sessions, "Error: Invalid passkey.", "error")
		http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
		return
	}

	if err := p.DB.DeleteCredentialByID(id); err != nil {
		auth.AddFlash(w, r, p.Sessions, fmt.Sprintf("Error: could not delete passkey: %v", err), "error")
		http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
		return
	}

	auth.AddFlash(w, r, p.Sessions, "Passkey removed.", "success")
	http.Redirect(w, r, "/security/passkeys", http.StatusSeeOther)
}
