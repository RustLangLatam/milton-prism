package services

import (
	"encoding/json"
	"milton_prism/core/shared/auth_token"
	paniccontrol "milton_prism/core/shared/utils"
	"milton_prism/pkg/log"

	coreerror "milton_prism/core/shared/error"

	"golang.org/x/net/context"
)

// UserProperties struct holds user-specific properties.
type UserProperties struct {
	Identifier uint64  `json:"identifier,omitempty"`
	Email      *string `json:"email,omitempty"`
	Name       *uint64 `json:"name,omitempty"`
	SystemUser bool    `json:"system_user"`
	SessionId  string  `json:"sid"`
}

// Claims struct embeds UserProperties, simulating a part of a JWT token structure.
type Claims struct {
	UserProperties UserProperties `json:"user_properties"`
	SessionId      string         `json:"sid"`
}

// UnmarshalJSON implements custom unmarshalling for UserProperties.
func (c *UserProperties) UnmarshalJSON(data []byte) error {
	type Alias UserProperties // Avoid infinite recursion by using an alias type.
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	// Unmarshal into the auxiliary struct.
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	return nil
}

// verifyAccessTokenAndSession extracts, validates, and verifies a JWT access token from the request context.
// It performs the following steps:
// Extracts the access token from the context.
// Verifies the token's signature and expiration using the token validator.
// Retrieves the corresponding user session from the cache using the session Identifier from the token's claims.
// Checks if the retrieved session is valid (e.g., not expired or revoked).
// If all checks pass, it returns the user properties, enriched with session details.
//
// Parameters:
//   - ctx: The context.Context from the incoming request, which should contain the access token.
//
// Returns:
//   - A pointer to UserProperties if the token and session are valid.
//   - An error if the token is missing, invalid, or the session is not found or invalid.
func (s *Services) verifyAccessTokenAndSession(ctx context.Context) (*UserProperties, error) {
	// Handle panics
	defer paniccontrol.RecoverFromPanic()

	claims := Claims{}

	// Extract the access token from the context.
	accessToken, err := auth_token.ExtractTokenFromContext(ctx, auth_token.TokenAccessName)
	if err != nil {
		return nil, err // Error if the token is not found.
	}

	// Verify the access token. The 'isRefresh' parameter is set to false.
	if _, err := s.validatorToken.Verify(*accessToken, false, &claims); err != nil {
		log.Warningf("Failed to validate access token: %v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}

	// Get the session from the cache using the SessionId from the token's claims.
	session, err := s.cacheClient.GetSessionByID(claims.SessionId)
	if err != nil {
		log.Warningf("Failed to get session: %v", err)
		return nil, coreerror.UserErrorInvalidSession
	}

	// Check if the session is valid.
	if !session.IsValid() {
		return nil, coreerror.UserErrorInvalidSession
	}

	// Enrich the user properties with details from the valid session.
	claims.UserProperties.SessionId = session.SessionID
	claims.UserProperties.Identifier = session.UserID
	claims.UserProperties.SystemUser = session.SystemUser

	return &claims.UserProperties, nil
}
