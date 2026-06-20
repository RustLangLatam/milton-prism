package auth_token

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"milton_prism/core/shared/cache_client"
	"milton_prism/core/shared/utils/datetime"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"
	"time"

	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
)

// JWTClaims represents the custom claims structure for JWT tokens.
// It extends the standard JWT registered claims with custom fields.
type JWTClaims struct {
	// Scopes contains the permissions/scopes granted to the token holder
	Scopes []string `json:"scopes,omitempty"`

	// UserProperties contains additional user-specific properties
	UserProperties map[string]interface{} `json:"user_properties,omitempty"`

	// GrantType specifies the type of token (access or refresh)
	GrantType GrantType `json:"grant_type"`

	// The identifier for a session at the relying party. See https://openid.net/specs/openid-connect-frontchannel-1_0.html#OPLogout
	SessionId *string `json:"sid,omitempty"`

	// RegisteredClaims represents the standard JWT claims (iat, exp, iss, etc.)
	jwt.RegisteredClaims
}

// JWTValidator is responsible for validating JWT tokens.
// It provides methods to verify token signatures, scopes, and blacklists.
type JWTValidator struct {
	// config contains the token validation configuration
	config *config.TokenValidatorConfig

	// PublicKey is the ED25519 public key used for token verification
	PublicKey ed25519.PublicKey

	// cache is used for token blacklisting (optional)
	cache *cache_client.TokenBlacklistCache
}

// JWTGenerator is responsible for generating and validating JWT tokens.
// It extends JWTValidator with token generation capabilities.
type JWTGenerator struct {
	// JWTValidator embeds the validator functionality
	JWTValidator

	// AccessDuration specifies the duration for access tokens
	AccessDuration time.Duration

	// RefreshDuration specifies the duration for refresh tokens
	RefreshDuration time.Duration

	// PrivateKey is the ED25519 private key used for signing tokens
	PrivateKey ed25519.PrivateKey
}

// NewJWTValidator creates a new JWTValidator instance with the given configuration.
// It initializes the validator with the provided public key and optional cache.
// If blacklist is disabled in settings_config, the cache will be set to nil.
func NewJWTValidator(config *config.TokenValidatorConfig, cache *cache_client.TokenBlacklistCache) (*JWTValidator, error) {
	publicKeyBytes, err := hex.DecodeString(config.PublicKey)
	if err != nil {
		log.Error("Error decoding public key: %v", err)
		return nil, errors.New("failed to decode public key")
	}

	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key size")
	}

	if !config.Blacklist {
		cache = nil
	}

	return &JWTValidator{
		config:    config,
		PublicKey: publicKeyBytes,
		cache:     cache,
	}, nil
}

// NewJWTGenerator creates a new JWTGenerator instance with both public and private keys.
// It initializes the generator with the provided private key and creates a corresponding validator.
// The generator is responsible for both generating and validating tokens.
func NewJWTGenerator(config *config.TokenGeneratorConfig, cache *cache_client.TokenBlacklistCache) (*JWTGenerator, error) {
	seedBytes, err := hex.DecodeString(config.SignKey)
	if err != nil {
		log.Errorf("Error decoding private key: %v", err)
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}

	if len(seedBytes) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid private key size, expected %d bytes, got %d",
			ed25519.PrivateKeySize, len(seedBytes))
	}

	privateKey := ed25519.NewKeyFromSeed(seedBytes)

	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("failed to cast public key to ed25519.PublicKey")
	}

	validatorConfig := config.ToValidatorConfig(hex.EncodeToString(publicKey))

	validator, err := NewJWTValidator(validatorConfig, cache)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWTValidator: %w", err)
	}

	return &JWTGenerator{
		JWTValidator:    *validator,
		AccessDuration:  time.Second * time.Duration(config.AccessTokenDuration),
		RefreshDuration: time.Second * time.Duration(config.RefreshTokenDuration),
		PrivateKey:      privateKey,
	}, nil
}

// Verify validates a JWT token against multiple criteria:
// Token signature using ED25519
// 2. Grant type (access vs refresh)
// 3. Token blacklist status (if enabled)
// 4. Token issuer and audience (if validation is enabled)
// Returns true if all validations pass, otherwise returns an error.
func (v *JWTValidator) Verify(tokenString string, isRefresh bool, claims interface{}) (bool, error) {
	var tokeClaims JWTClaims

	// Parse the JWT token using ED25519 signing method
	token, err := jwt.ParseWithClaims(tokenString, &tokeClaims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return v.PublicKey, nil
	})

	if err != nil || !token.Valid {
		return false, errors.New("invalid JWT token")
	}

	// Verify the GrantType if this is a refresh token check
	if isRefresh && tokeClaims.GrantType != GrantTypeRefresh {
		return false, errors.New("invalid GrantType: expected refresh token")
	}

	// Check if the token is blacklisted (if blacklist feature is enabled)
	if v.config.Blacklist {
		isBlacklisted, err := ExistBlackList(v.cache, tokenString)
		if err != nil {
			return false, fmt.Errorf("failed to check blacklist: %w", err)
		}
		if isBlacklisted {
			return false, errors.New("token is blacklisted")
		}
	}

	// Validate Issuer and Audience if enabled in settings_config
	if v.config.ValidateIssuer && !tokeClaims.VerifyIssuer(v.config.Issuer, true) {
		return false, errors.New("invalid token issuer")
	}
	if v.config.ValidateAudience && !tokeClaims.VerifyAudience(v.config.Audience, true) {
		return false, errors.New("invalid token audience")
	}

	// external claims
	if claims != nil {
		claimsJSON, err := json.Marshal(tokeClaims)
		if err != nil {
			return false, fmt.Errorf("failed to marshal claims: %w", err)
		}
		if err := json.Unmarshal(claimsJSON, claims); err != nil {
			return false, fmt.Errorf("failed to unmarshal claims: %w", err)
		}
	}

	return true, nil
}

// NewToken generates a new JWT token with the specified grant type and user properties.
// It creates either an access or refresh token based on the grant type.
func (g *JWTGenerator) NewToken(grantType GrantType, userProperties map[string]interface{}, sessionId *string) (*tokenv1.Token, *TokenClaimsBase, error) {
	var claims JWTClaims

	if userProperties != nil {
		claims.UserProperties = userProperties
	}

	iat := time.Now().UTC()
	exp := iat.Add(g.getDuration(grantType))

	if err := g.populateClaims(grantType, &claims, iat, exp); err != nil {
		return nil, nil, fmt.Errorf("failed to populate claims: %w", err)
	}

	if sessionId != nil {
		claims.SessionId = sessionId
	}

	token, err := g.signToken(&claims)
	if err != nil {
		return nil, nil, err
	}
	return &tokenv1.Token{Value: token, ExpireTime: datetime.ToProtoTimestamp(&exp)}, &TokenClaimsBase{SessionID: sessionId, JTI: claims.ID, ExpiresIn: g.getDuration(grantType)}, nil
}

// getDuration returns the appropriate expiration duration based on the token type.
// Access tokens use AccessDuration while refresh tokens use RefreshDuration.
func (g *JWTGenerator) getDuration(grantType GrantType) time.Duration {
	if grantType == GrantTypeAccess {
		return g.AccessDuration
	}
	return g.RefreshDuration
}

// populateClaims sets all required JWT claims for the token.
// This includes standard claims like issuer, audience, timestamps, and custom claims.
func (g *JWTGenerator) populateClaims(grantType GrantType, claims *JWTClaims, iat, exp time.Time) error {

	claims.GrantType = grantType
	claims.Issuer = g.config.Issuer
	claims.Audience = []string{g.config.Audience}
	claims.IssuedAt = jwt.NewNumericDate(iat)
	claims.NotBefore = jwt.NewNumericDate(iat)
	claims.ExpiresAt = jwt.NewNumericDate(exp)
	claims.ID = uuid.NewString()

	return nil
}

// signToken creates and signs a JWT token using ED25519.
// It takes the populated claims and generates a signed token string.
func (g *JWTGenerator) signToken(claims *JWTClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenStr, err := token.SignedString(g.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenStr, nil
}
