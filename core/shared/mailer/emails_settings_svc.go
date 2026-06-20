package mailer

import (
	"errors"
	"milton_prism/pkg/log"
)

// EmailsSettingsCfg defines the complete configuration for the email microservice.
// It is designed to be loaded from a TOML file and provides detailed control
// over email providers, templates, operational behavior, and development settings.
type EmailsSettingsCfg struct {
	// Provider selection and configuration.
	Provider ProviderCfg `toml:"provider"`

	TokenExpiry             int    `toml:"tokenExpiry"`             // Expiration time for email verification tokens, in seconds. Defaults to TokenExpiry
	VerificationLinkBaseURL string `toml:"verificationLinkBaseURL"` // Base URL for email verification links sent to users
	ConfirmationLinkBaseURL string `toml:"confirmationLinkBaseURL"` // Base URL for account confirmation links in welcome emails
	OTPLength               int    `toml:"otpLength"`               // Length of OTPs
	CodeLength              int    `toml:"codeLength"`              // Length of generic codes

	// Default identity for outgoing emails.
	DefaultSenderName string `toml:"defaultSenderName"` // e.g., "SaasArg Platform"
	DefaultSenderAddr string `toml:"defaultSenderAddr"` // e.g., "noreply@SaasArg.com"

	// Operational behavior settings.
	RetryAttempts  int            `toml:"retryAttempts"`  // Number of retries on send failure.
	TimeoutSeconds int            `toml:"timeoutSeconds"` // Timeout for a single email send operation.
	RateLimiter    RateLimiterCfg `toml:"rateLimiter"`    // Configuration for rate limiting outgoing emails.

	// Paths and subjects for all email templates.
	Templates TemplatesCfg `toml:"templates"`

	// Settings to facilitate development and testing.
	Development DevelopmentCfg `toml:"development"`

	// Security settings for the service itself.
	Security SecurityCfg `toml:"security"`
}

// --- Nested Configuration Structs ---

// ProviderCfg specifies which email provider to use and its settings.
type ProviderCfg struct {
	// The active email provider. Supported values: "smtp", "ses", "log".
	// "log" will print emails to the console instead of sending them.
	Type string `toml:"type"`

	// Configuration for the SMTP provider.
	SMTP SMTPCfg `toml:"smtp"`

	// Configuration for the AWS SES (Simple Email Service) provider.
	SES SESCfg `toml:"ses"`
}

// SMTPCfg holds all necessary settings for connecting to an SMTP server.
type SMTPCfg struct {
	Host       string `toml:"host"`       // SMTP server hostname (e.g., "smtp.mailgun.org").
	Port       int    `toml:"port"`       // SMTP server port (e.g., 587).
	Username   string `toml:"username"`   // Username for SMTP authentication.
	Password   string `toml:"password"`   // Password for SMTP authentication.
	Encryption string `toml:"encryption"` // "TLS", "SSL", or "None".
}

// SESCfg holds settings for using AWS SES.
type SESCfg struct {
	Region    string `toml:"region"`    // AWS region where SES is configured (e.g., "us-east-1").
	AccessKey string `toml:"accessKey"` // AWS Access Key Identifier.
	SecretKey string `toml:"secretKey"` // AWS Secret Access Key.
}

// RateLimiterCfg configures the rate at which emails are sent.
type RateLimiterCfg struct {
	// The number of emails allowed per period.
	Limit float64 `toml:"limit"`
	// The burst capacity (how many emails can be sent in a quick burst).
	Burst int `toml:"burst"`
}

// TemplatesCfg defines the location and subject for each email template.
type TemplatesCfg struct {
	// Base directory where all template files are stored.
	BaseDir string `toml:"baseDir"`

	Welcome           TemplateDetailCfg `toml:"welcome"`
	TradeConfirmation TemplateDetailCfg `toml:"tradeConfirmation"`
	Invoice           TemplateDetailCfg `toml:"invoice"`
	PasswordReset     TemplateDetailCfg `toml:"passwordReset"`
	EmailVerification TemplateDetailCfg `toml:"emailVerification"`
}

// TemplateDetailCfg contains the subject and file paths for a single email type.
type TemplateDetailCfg struct {
	Subject  string `toml:"subject"`  // The subject line for the email.
	HTMLPath string `toml:"htmlPath"` // Path to the HTML template file, relative to BaseDir.
	TextPath string `toml:"textPath"` // Path to the plain text template file, relative to BaseDir.
}

// DevelopmentCfg provides settings to make development easier and safer.
type DevelopmentCfg struct {
	// If true, the service will log emails instead of sending them. Overrides the provider setting.
	DryRun bool `toml:"dryRun"`
	// If set, all outgoing emails will be redirected to this address, ignoring the original recipient.
	RedirectAllTo string `toml:"redirectAllTo"`
}

// SecurityCfg holds security-related parameters for the service.
type SecurityCfg struct {
	// The API key that clients must provide to use this service.
	RequiredApiKey string `toml:"requiredApiKey"`
}

// Validate checks the configuration, normalizes it, and sets sensible defaults.
func (cfg *EmailsSettingsCfg) Validate() error {
	// --- Provider Defaults ---
	if cfg.Provider.Type == "" {
		cfg.Provider.Type = "log" // Default to safe logging provider.
	}
	if cfg.Provider.Type == "smtp" {
		if cfg.Provider.SMTP.Host == "" {
			return errors.New("provider.smtp.host must be set for smtp provider")
		}
		if cfg.Provider.SMTP.Port == 0 {
			cfg.Provider.SMTP.Port = 587 // Default port for TLS
		}
		if cfg.Provider.SMTP.Encryption == "" {
			cfg.Provider.SMTP.Encryption = "TLS" // Default to modern encryption
		}
	}

	if cfg.TokenExpiry == 0 {
		cfg.TokenExpiry = 300 // Default to 5 minutes
	}

	// --- OTP Defaults ---
	if cfg.OTPLength == 0 {
		cfg.OTPLength = 4
	}

	// --- Code Defaults ---
	if cfg.CodeLength == 0 {
		cfg.CodeLength = 6
	}

	// --- Sender Identity Defaults ---
	if cfg.DefaultSenderName == "" {
		cfg.DefaultSenderName = "Milton Prism Platform"
	}
	if cfg.DefaultSenderAddr == "" {
		cfg.DefaultSenderAddr = "noreply@milton-prism.dev"
	}

	// --- Operational Behavior Defaults ---
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 3
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 10
	}
	if cfg.RateLimiter.Limit <= 0 {
		cfg.RateLimiter.Limit = 5 // 5 emails per second
	}
	if cfg.RateLimiter.Burst <= 0 {
		cfg.RateLimiter.Burst = 5
	}

	// --- Template Defaults ---
	if cfg.Templates.BaseDir == "" {
		cfg.Templates.BaseDir = "templates"
	}
	// It is recommended to explicitly set all template paths and subjects in the config file,
	// but we could add more specific defaults here if desired. Example:
	if cfg.Templates.Welcome.Subject == "" {
		cfg.Templates.Welcome.Subject = "Welcome to Milton Prism!"
	}
	if cfg.Templates.Welcome.HTMLPath == "" {
		cfg.Templates.Welcome.HTMLPath = "welcome.html"
	}
	if cfg.Templates.Welcome.TextPath == "" {
		cfg.Templates.Welcome.TextPath = "welcome.txt"
	}
	if cfg.Templates.PasswordReset.Subject == "" {
		cfg.Templates.PasswordReset.Subject = "Reset Your Milton Prism Password"
	}
	if cfg.Templates.PasswordReset.HTMLPath == "" {
		cfg.Templates.PasswordReset.HTMLPath = "password_reset.html"
	}
	if cfg.Templates.PasswordReset.TextPath == "" {
		cfg.Templates.PasswordReset.TextPath = "password_reset.txt"
	}
	if cfg.Templates.EmailVerification.Subject == "" {
		cfg.Templates.EmailVerification.Subject = "Please Verify Your Email Address"
	}
	if cfg.Templates.EmailVerification.HTMLPath == "" {
		cfg.Templates.EmailVerification.HTMLPath = "email_verification.html"
	}
	if cfg.Templates.EmailVerification.TextPath == "" {
		cfg.Templates.EmailVerification.TextPath = "email_verification.txt"
	}

	// No validation needed for Development settings as their zero-values (false, "") are valid defaults.
	// No validation for Security.RequiredApiKey as an empty key might be valid in a trusted environment.

	log.Info("Email service configuration validated successfully.")
	return nil
}
