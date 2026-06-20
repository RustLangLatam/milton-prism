package mailer

import (
	"fmt"
	"milton_prism/pkg/log"
	"net/smtp"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/time/rate"
)

// smtpMailer implements the Mailer interface to send emails via SMTP.
type smtpMailer struct {
	cfg         *EmailsSettingsCfg
	auth        smtp.Auth
	rateLimiter *rate.Limiter
}

// Send for smtpMailer
func (m *smtpMailer) Send(recipient, templateName string, data TemplateData, attachments ...string) error {
	// Wait for the rate limiter if necessary.
	// A real implementation should use a context that can be cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	err := m.rateLimiter.Wait(ctx)
	if err != nil {
		return err
	}

	subject, body, err := parseTemplates(m.cfg, templateName, data)
	if err != nil {
		return err
	}

	// Build the email message.
	msg := "From: " + m.cfg.DefaultSenderName + " <" + m.cfg.DefaultSenderAddr + ">\r\n" +
		"To: " + recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\r\n" +
		"\r\n" +
		body

	addr := fmt.Sprintf("%s:%d", m.cfg.Provider.SMTP.Host, m.cfg.Provider.SMTP.Port)

	// Retry logic
	var sendErr error
	for i := 0; i < m.cfg.RetryAttempts; i++ {
		sendErr = smtp.SendMail(addr, m.auth, m.cfg.DefaultSenderAddr, []string{recipient}, []byte(msg))
		if sendErr == nil {
			return nil // Success
		}
		log.Warningf("Attempt %d failed to send email to %s: %v", i+1, recipient, sendErr)
		time.Sleep(2 * time.Second) // Wait before retrying
	}

	return fmt.Errorf("failed to send email after %d attempts: %w", m.cfg.RetryAttempts, sendErr)
}
