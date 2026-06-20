package mailer

import "milton_prism/pkg/log"

// --- Decorator for Redirection ---

type redirectMailer struct {
	wrappedMailer   Mailer
	redirectAddress string
}

func newRedirectMailer(wrapped Mailer, address string) Mailer {
	return &redirectMailer{wrappedMailer: wrapped, redirectAddress: address}
}

func (m *redirectMailer) Send(recipient, templateName string, data TemplateData, attachments ...string) error {
	// Add a note to the data about the redirection, which can be useful in the template.
	if data == nil {
		data = make(TemplateData)
	}
	data["_originalRecipient"] = recipient
	data["_isRedirected"] = true

	log.Infof("Redirecting email originally for %s to %s", recipient, m.redirectAddress)
	return m.wrappedMailer.Send(m.redirectAddress, templateName, data)
}
