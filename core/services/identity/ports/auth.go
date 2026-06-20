package ports

import (
	"context"

	"milton_prism/core/services/identity/domain"
)

// PasswordHasher hashes and verifies user passwords.
type PasswordHasher interface {
	Hash(plain string) (string, error)
	Verify(hash, plain string) error
}

// TokenManager generates and validates session tokens.
// The application depends on this port; the auth_token package is wired behind it.
type TokenManager interface {
	NewTokens(ctx context.Context, userID uint64, systemUser bool, sessionID string) (*domain.AuthorizationTokens, error)
	// ExtractSessionID validates a refresh token and returns the embedded session ID.
	ExtractSessionID(refreshToken string) (string, error)
	Revoke(ctx context.Context, token string) error
}

// SessionStore persists user sessions in a cache backend.
type SessionStore interface {
	Save(ctx context.Context, sessionID string, userID uint64, systemUser bool) error
	Get(ctx context.Context, sessionID string) (userID uint64, systemUser bool, valid bool, err error)
	Delete(ctx context.Context, sessionID string) error
}
