// Package auth_token provides token generation and validation for JWT and PASETO tokens,
// along with context helpers for propagating token claims across service boundaries.
package auth_token

import (
	"errors"
	"fmt"
	"milton_prism/core/shared/cache_client"
	"milton_prism/pkg/config"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"
)

// GrantType represents the type of grant in a token.
type GrantType string

const (
	TokenAccessName  string = "authorization"
	TokenRefreshName string = "x-refresh-token"
	CtxIdName        string = "x-ctx-id"
	ForwardedName    string = "x-forwarded"
	InternalName     string = "x-internal"
)

const (
	GrantTypeAccess  GrantType = "access"  // Represents an access grant
	GrantTypeRefresh GrantType = "refresh" // Represents a refresh grant
)

// TokenClaimsBase Represents critical metadata for a JWT token
type TokenClaimsBase struct {
	ExpiresIn time.Duration `json:"expires_in"` // Token validity duration
	JTI       string        `json:"jti"`        // Unique token identifier (RFC 7519)
	SessionID *string       `json:"session_id"` // Associated session
}

type AuthTokenPackage struct {
	*tokenv1.AuthorizationTokens                  // Generated tokens (access/refresh)
	AccessTokenClaimsBase        *TokenClaimsBase `json:"access_token_claims_base"`
	RefreshTokenClaimsBase       *TokenClaimsBase `json:"refresh_token_claims_base"`
}

// IsValid checks if the GrantType is valid.
func (g GrantType) IsValid() bool {
	switch g {
	case GrantTypeAccess, GrantTypeRefresh:
		return true
	default:
		return false
	}
}

// String returns the string representation of the GrantType.
func (g GrantType) String() string {
	return string(g)
}

// TokenValidator interface for token validation.
type TokenValidator interface {
	// Verify checks if a token is valid, optionally validating if it's a refresh token.
	// - token: the token string to validate.
	// - isRefresh: whether to check for a GrantTypeRefresh token.
	// - claims: a reference to populate with the token's claims.
	Verify(token string, isRefresh bool, claims interface{}) (bool, error)
}

// TokenManager interface for token creation and validation.
type TokenManager interface {
	TokenValidator

	// NewToken generates a new token with the specified grant type and claims.
	// - grantType: the type of grant to include in the token.
	// - claims: the claims to include in the token.
	NewToken(grantType GrantType, additionalInfo map[string]interface{}, sessionId *string) (*tokenv1.Token, *TokenClaimsBase, error)
}

// NewTokenValidator initializes a TokenValidator based on the schema type (e.g., JWT or Paseto).
func NewTokenValidator(config *config.TokenValidatorConfig, cache *cache_client.TokenBlacklistCache) (TokenValidator, error) {
	switch *config.SchemaType {
	case "JWT":
		validator, err := NewJWTValidator(config, cache)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT validator: %w", err)
		}
		return validator, nil
	case "Paseto":
		validator, err := NewPasetoValidator(config, cache)
		if err != nil {
			return nil, fmt.Errorf("failed to create Paseto validator: %w", err)
		}
		return validator, nil
	default:
		return nil, errors.New("unsupported schema type")
	}
}

// NewTokenCreator initializes a TokenManager based on the schema type (e.g., JWT or Paseto).
func NewTokenCreator(config *config.TokenGeneratorConfig, cache *cache_client.TokenBlacklistCache) (TokenManager, error) {
	switch *config.SchemaType {
	case "JWT":
		manager, err := NewJWTGenerator(config, cache)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT manager: %w", err)
		}
		return manager, nil
	case "Paseto":
		manager, err := NewPasetoGenerator(config, cache)
		if err != nil {
			return nil, fmt.Errorf("failed to create Paseto manager: %w", err)
		}
		return manager, nil
	default:
		return nil, errors.New("unsupported schema type")
	}
}

// ExistBlackList checks if a token is blacklisted.
// - cache: the cache instance to check against.
// - token: the token string to check.
func ExistBlackList(cache *cache_client.TokenBlacklistCache, token string) (bool, error) {
	isBlacklisted, err := cache.IsTokenBlacklisted(token)
	if err != nil {
		return false, fmt.Errorf("failed to check blacklist: %w", err)
	}
	return isBlacklisted, nil
}

// ExtractTokenFromContext safely extracts a token from gRPC metadata context.
//
// Parameters:
//   - ctx: context.Context containing the metadata
//   - key: string metadata key to look for (e.g., "authorization")
//
// Returns:
//   - *string: extracted token without "Bearer " prefix
//   - error: nil on success, or TokenValidationErrorInvalid if token is missing/malformed
func ExtractTokenFromContext(ctx context.Context, key string) (*string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("metadata not found in context")
	}

	// Retrieve token from metadata
	values := md.Get(key)
	if len(values) == 0 {
		return nil, fmt.Errorf("token not found for key: %s", key)
	}

	// Remove leading and trailing whitespace
	token := strings.TrimSpace(values[0])
	if token == "" {
		return nil, fmt.Errorf("empty token for key: %s", key)
	}

	// Remove "Bearer " prefix if present (case insensitive)
	token = strings.TrimPrefix(strings.TrimPrefix(token, "Bearer "), "bearer ")

	return &token, nil
}

func CtxIdFromContext(ctx context.Context) *string {
	if value, ok := ctx.Value(CtxIdName).(string); ok && value != "" {
		// If the request Identifier is in the context, return it.
		return &value
	}

	return nil
}
