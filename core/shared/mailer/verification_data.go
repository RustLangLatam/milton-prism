package mailer

import "time"

// VerificationData stores information about a pending email verification.
type VerificationData struct {
	UserID     uint64    `json:"user_id"`
	Email      string    `json:"email"`
	Method     string    `json:"method"`     // "link", "otp", "code"
	Credential string    `json:"credential"` // The actual token, OTP, or code
	ExpiresAt  time.Time `json:"expires_at"`
	Verified   bool      `json:"verified"`
}
