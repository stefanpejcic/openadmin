package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ServeEmailsConfiguration handles GET /emails/configuration/{type}/{email}: the same Apple Mail, Outlook and Thunderbird files the user panel hands out
func (e *Emails) ServeEmailsConfiguration(w http.ResponseWriter, r *http.Request) {
	account := r.PathValue("email")
	if !emailsRegex.MatchString(account) {
		http.Error(w, "Invalid email format", http.StatusBadRequest)
		return
	}
	if !scopeFor(r, e.MySQL).AllowsEmail(account) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	content, filename, mimetype, ok := emailConfigurationFile(strings.ToLower(r.PathValue("type")), account, emailConnectionInfo(account, e.PublicIP))
	if !ok {
		http.Error(w, "Invalid configuration type", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", mimetype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte(content))
}

// emailConfigurationFile builds one client's settings file, word for word what the user panel's handleEmailConfiguration writes
func emailConfigurationFile(configType, account string, conn emailConnInfo) (content, filename, mimetype string, ok bool) {
	username, domain, _ := strings.Cut(account, "@")
	mailHost, encryption := conn.Host, conn.Security
	sslTrueFalse := "<false />"
	if conn.SSL {
		sslTrueFalse = "<true />"
	}
	switch configType {
	case "thunderbird":
		return fmt.Sprintf(
			"[InternetEmail]\nEmailAddress=%s\nUserName=%s\nIncomingServer=%s\nIncomingProtocol=IMAP\nIncomingPort=%d\nIncomingSecurity=%s\nOutgoingServer=%s\nOutgoingPort=%d\nOutgoingSecurity=%s\nAuthentication=normal password\nDescription=Thunderbird Profile\nNote=Password will be requested on first login.\n",
			account, account, mailHost, conn.IMAPPort, encryption, mailHost, conn.SMTPPort, encryption,
		), username + "_thunderbird_profile.txt", "text/plain", true
	case "outlook":
		return fmt.Sprintf(
			"Outlook Email Configuration:\n\nEmail Address: %s\nUsername: %s\n\nIncoming Server (IMAP): %s\nPort: %d\nEncryption: %s\n\nOutgoing Server (SMTP): %s\nPort: %d\nEncryption: %s\n\nAuthentication: Normal Password\nNote: Password is not stored. You will be prompted on first send/receive.\n",
			account, account, mailHost, conn.IMAPPort, encryption, mailHost, conn.SMTPPort, encryption,
		), username + "_outlook_profile.txt", "text/plain", true
	case "apple":
	default:
		return "", "", "", false
	}
	payloadUUID, innerUUID, outerUUID := uuid.New().String(), uuid.New().String(), uuid.New().String()
	payloadIdentifierBase := mailHost + "." + domain
	description := account + " Email Setup"
	content = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
    <dict>
        <key>PayloadContent</key>
        <array>
            <dict>
                <key>EmailAccountDescription</key>
                <string>%s</string>
                <key>EmailAccountName</key>
                <string>%s</string>
                <key>EmailAccountType</key>
                <string>EmailTypeIMAP</string>
                <key>EmailAddress</key>
                <string>%s</string>
                <key>IncomingMailServerAuthentication</key>
                <string>EmailAuthPassword</string>
                <key>IncomingMailServerHostName</key>
                <string>%s</string>
                <key>IncomingMailServerPortNumber</key>
                <integer>%d</integer>
                <key>IncomingMailServerUseSSL</key>
                %s
                <key>IncomingMailServerUsername</key>
                <string>%s</string>
                <key>OutgoingMailServerAuthentication</key>
                <string>EmailAuthPassword</string>
                <key>OutgoingMailServerHostName</key>
                <string>%s</string>
                <key>OutgoingMailServerPortNumber</key>
                <integer>%d</integer>
                <key>OutgoingMailServerUseSSL</key>
                %s
                <key>OutgoingMailServerUsername</key>
                <string>%s</string>
                <key>OutgoingPasswordSameAsIncomingPassword</key>
                <true />
                <key>PayloadDescription</key>
                <string>%s</string>
                <key>PayloadDisplayName</key>
                <string>%s</string>
                <key>PayloadIdentifier</key>
                <string>%s.%s</string>
                <key>PayloadOrganization</key>
                <string>%s</string>
                <key>PayloadType</key>
                <string>com.apple.mail.managed</string>
                <key>PayloadUUID</key>
                <string>%s</string>
                <key>PayloadVersion</key>
                <integer>1</integer>
                <key>IncomingMailServerIMAPPathPrefix</key>
                <string>INBOX</string>
            </dict>
        </array>
        <key>PayloadDescription</key>
        <string>%s</string>
        <key>PayloadDisplayName</key>
        <string>%s</string>
        <key>PayloadIdentifier</key>
        <string>%s.%s</string>
        <key>PayloadOrganization</key>
        <string>%s</string>
        <key>PayloadType</key>
        <string>Configuration</string>
        <key>PayloadUUID</key>
        <string>%s</string>
        <key>PayloadVersion</key>
        <integer>1</integer>
    </dict>
</plist>`,
		account, username, account, mailHost, conn.IMAPPort, sslTrueFalse, account,
		mailHost, conn.SMTPPort, sslTrueFalse, account,
		description, description, payloadIdentifierBase, innerUUID, domain, innerUUID,
		description, description, payloadIdentifierBase, outerUUID, domain, payloadUUID,
	)
	return content, username + "_apple_mail.mobileconfig", "application/x-apple-aspen-config", true
}
