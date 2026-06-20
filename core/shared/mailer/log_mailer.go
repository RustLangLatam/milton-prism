package mailer

import (
	"milton_prism/pkg/log"
)

// logMailer implements the Mailer interface to print emails to the console.
type logMailer struct {
	cfg *EmailsSettingsCfg
}

// Send for logMailer
func (m *logMailer) Send(recipient, templateName string, data TemplateData, attachments ...string) error {
	subject, _, err := parseTemplates(m.cfg, templateName, data)
	if err != nil {
		log.Infof("ERROR: [logMailer] could not parse template %s: %v", templateName, err)
		return err
	}

	// Print the email to the console instead of sending it.
	log.Infof("--- NEW EMAIL (LOG MAILER) ---")
	log.Infof("Recipient: %s", recipient)
	log.Infof("From: %s <%s>", m.cfg.DefaultSenderName, m.cfg.DefaultSenderAddr)
	log.Infof("Subject: %s", subject)
	log.Infof("Body: %s", attachments)
	log.Infof("------------------------------")

	return nil
}
