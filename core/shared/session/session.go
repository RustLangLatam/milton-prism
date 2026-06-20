// Package session defines the Session type and validity rules used across
// the authentication and authorization flows.
package session

import (
	"time"
)

// Session represents a user's login session stored in cache.
type Session struct {
	SessionID  string    `json:"session_id"`   // UUID for the session
	UserID     uint64    `json:"user_id"`      // User's unique identifier (typically from token claims)
	SystemUser bool      `json:"system_user"`  // User is a system user
	RefreshJTI string    `json:"refresh_jti"`  // JWT Identifier of the refresh token
	AccessJTI  string    `json:"access_jti"`   // JWT Identifier of the access token
	ExpiresAt  time.Time `json:"expires_at"`   // Refresh token expiration timestamp
	CreatedAt  time.Time `json:"created_at"`   // When the session was created
	LastUsedAt time.Time `json:"last_used_at"` // When the session was last active
	DeviceInfo string    `json:"device_info"`  // Device fingerprint / user-agent
	IPAddress  string    `json:"ip_address"`   // Client IP address
	IsRevoked  bool      `json:"is_revoked"`   // True if session was manually invalidated
}

// IsExpired returns true if the session has expired based on refresh token expiry.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// IsValid returns true if the session is still active and not revoked.
func (s *Session) IsValid() bool {
	return !s.IsRevoked && !s.IsExpired()
}

// Revoke marks the session as invalid.
func (s *Session) Revoke() {
	s.IsRevoked = true
}

// MarkUsed updates the last-used timestamp.
func (s *Session) MarkUsed() {
	s.LastUsedAt = time.Now()
}

// IsSessionValid performs integrity checks for a session
func (s *Session) IsSessionValid() bool {
	if s.IsRevoked {
		return false
	}
	if time.Now().After(s.ExpiresAt) {
		return false
	}
	return true
}
