// Package mailer defines the Mailer interface and provides SMTP, redirect,
// and no-op log implementations for sending transactional emails.
package mailer

import (
	"bytes"
	"fmt"
	"html/template"
	"net/smtp"
	"os"
	"path/filepath"
	"milton_prism/pkg/log"

	"golang.org/x/time/rate"
)

// TemplateData is an alias for a map to pass data to templates.
type TemplateData map[string]interface{}

// Mailer defines the interface for an email sending service.
// This allows us to swap providers (SMTP, SES, etc.) without changing the code that uses it.
type Mailer interface {
	Send(recipient, templateName string, data TemplateData, attachments ...string) error
}

// New creates and returns a Mailer instance based on the provided configuration.
// This is the factory that chooses the correct implementation.
func New(cfg *EmailsSettingsCfg) (Mailer, error) {
	log.Infof("Creating mailer with provider: %s, host: %s, port: %d, username: %s", cfg.Provider.Type, cfg.Provider.SMTP.Host, cfg.Provider.SMTP.Port, cfg.Provider.SMTP.Username)
	var mailer Mailer

	// Choose the provider based on the configuration
	switch cfg.Provider.Type {
	case "smtp":
		auth := smtp.PlainAuth("", cfg.Provider.SMTP.Username, cfg.Provider.SMTP.Password, cfg.Provider.SMTP.Host)
		limiter := rate.NewLimiter(rate.Limit(cfg.RateLimiter.Limit), cfg.RateLimiter.Burst)
		mailer = &smtpMailer{cfg: cfg, auth: auth, rateLimiter: limiter}
	case "log":
		mailer = &logMailer{cfg: cfg}
	// case "ses":
	// 	// Logic to initialize the AWS SES client would go here
	// 	mailer, err = newSesMailer(cfg)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to create SES mailer: %w", err)
	// 	}
	default:
		return nil, fmt.Errorf("unsupported mailer provider: %s", cfg.Provider.Type)
	}

	// Apply Decorators for additional functionality like development modes.
	// The Decorator pattern allows us to wrap a mailer with extra features.
	if cfg.Development.DryRun {
		log.Infof("Email sending is in DryRun mode. Using logMailer.")
		// If DryRun is enabled, it always overrides the provider with the logMailer.
		mailer = &logMailer{cfg: cfg}
	} else if cfg.Development.RedirectAllTo != "" {
		log.Infof("Email redirection is active. All emails will be sent to %s", cfg.Development.RedirectAllTo)
		mailer = newRedirectMailer(mailer, cfg.Development.RedirectAllTo)
	}

	return mailer, nil
}

// parseTemplates is a helper function to process the HTML and text templates.
// Note: This implementation focuses on the HTML template for simplicity.
func parseTemplates(cfg *EmailsSettingsCfg, templateName string, data TemplateData) (string, string, error) {
	// Get the specific template details from the configuration.
	tplDetail, err := getTemplateDetail(cfg, templateName)
	if err != nil {
		return "", "", err // This error means the template name itself is invalid.
	}

	var body string
	var parseErr error

	// --- Attempt to parse the HTML template first ---
	if tplDetail.HTMLPath != "" {
		htmlPath := filepath.Join(cfg.Templates.BaseDir, tplDetail.HTMLPath)
		// Check if the file actually exists before trying to parse it.
		if _, err := os.Stat(htmlPath); err == nil {
			// File exists, so we use it.
			body, parseErr = parseAndExecuteTemplate(htmlPath, data)
			if parseErr != nil {
				// This is a critical error (e.g., malformed template), so we return immediately.
				return "", "", fmt.Errorf("failed to parse/execute HTML template %s: %w", htmlPath, parseErr)
			}
			// If successful, we have our body and can return.
			return tplDetail.Subject, body, nil
		}
		// If os.Stat returned an error, it's likely "file not found".
		// We can safely ignore it and proceed to the fallback.
		log.Infof("HTML template not found at %s. Attempting to fall back to plain text.", htmlPath)
	}

	// --- 2. Fallback to the plain text template ---
	if tplDetail.TextPath != "" {
		textPath := filepath.Join(cfg.Templates.BaseDir, tplDetail.TextPath)
		if _, err := os.Stat(textPath); err == nil {
			// Text file exists, so we use it as the fallback.
			body, parseErr = parseAndExecuteTemplate(textPath, data)
			if parseErr != nil {
				return "", "", fmt.Errorf("failed to parse/execute Text template %s: %w", textPath, parseErr)
			}
			// Success with the text template.
			return tplDetail.Subject, body, nil
		}
	}

	// --- 3. If neither template was found, return an error ---
	return "", "", fmt.Errorf("no valid template file could be found for '%s' (checked for %s and %s)",
		templateName, tplDetail.HTMLPath, tplDetail.TextPath)
}

// Helper function to avoid code repetition for parsing and executing.
func parseAndExecuteTemplate(templatePath string, data TemplateData) (string, error) {
	// template.New() needs a name for the template, using the filename is a good practice.
	t, err := template.New(filepath.Base(templatePath)).ParseFiles(templatePath)
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	// Execute the template into a buffer.
	buf := new(bytes.Buffer)
	if err = t.Execute(buf, data); err != nil {
		return "", fmt.Errorf("execute error: %w", err)
	}

	return buf.String(), nil
}

// getTemplateDetail function remains the same as before.
func getTemplateDetail(cfg *EmailsSettingsCfg, name string) (TemplateDetailCfg, error) {
	switch name {
	case "welcome":
		return cfg.Templates.Welcome, nil
	case "tradeConfirmation":
		return cfg.Templates.TradeConfirmation, nil
	case "invoice":
		return cfg.Templates.Invoice, nil
	case "passwordReset":
		return cfg.Templates.PasswordReset, nil
	case "emailVerification":
		return cfg.Templates.EmailVerification, nil
	default:
		return TemplateDetailCfg{}, fmt.Errorf("template '%s' not found in configuration", name)
	}
}
