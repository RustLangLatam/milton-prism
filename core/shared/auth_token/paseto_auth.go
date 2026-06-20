package auth_token

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"milton_prism/core/shared/cache_client"
	"milton_prism/core/shared/utils/datetime"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"
	"time"

	"github.com/google/uuid"

	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	pasetov4 "zntr.io/paseto/v4"
)

// PasetoClaims represents a JWT claim set.
type PasetoClaims struct {
	Scopes []string `json:"scopes"`

	UserProperties map[string]interface{} `json:"user_properties"`

	GrantType GrantType `json:"grant_type"`

	// the `iss` (Issuer) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.1
	Issuer string `json:"iss,omitempty"`

	// the `sub` (Subject) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.2
	Subject string `json:"sub,omitempty"`

	// the `aud` (Audience) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.3
	Audience string `json:"aud,omitempty"`

	// the `exp` (Expiration Time) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.4
	ExpiresAt time.Time `json:"exp,omitempty"`

	// the `nbf` (Not Before) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.5
	NotBefore time.Time `json:"nbf,omitempty"`

	// the `iat` (Issued At) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.6
	IssuedAt time.Time `json:"iat,omitempty"`

	// the `jti` (JWT ID) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.7
	ID string `json:"jti,omitempty"`

	// the `sid` (Session ID) claim.
	SessionId *string `json:"sid,omitempty"`
}

func (c *PasetoClaims) VerifyAudience(cmp string, req bool) bool {
	return verifyAud([]string{c.Audience}, cmp, req)
}

func verifyAud(aud []string, cmp string, required bool) bool {
	if len(aud) == 0 {
		return !required
	}
	// use a var here to keep constant time compare when looping over a number of claims
	result := false

	var stringClaims string
	for _, a := range aud {
		if subtle.ConstantTimeCompare([]byte(a), []byte(cmp)) != 0 {
			result = true
		}
		stringClaims = stringClaims + a
	}

	// case where "" is sent in one or many aud claims
	if len(stringClaims) == 0 {
		return !required
	}

	return result
}

// VerifyIssuer compares the iss claim against cmp.
// If required is false, this method will return true if the value matches or is unset
func (c *PasetoClaims) VerifyIssuer(cmp string, req bool) bool {
	return verifyIss(c.Issuer, cmp, req)
}

func verifyIss(iss string, cmp string, required bool) bool {
	if iss == "" {
		return !required
	}
	return subtle.ConstantTimeCompare([]byte(iss), []byte(cmp)) != 0
}

// PasetoValidator validates PASETO tokens.
type PasetoValidator struct {
	config    *config.TokenValidatorConfig
	PublicKey ed25519.PublicKey
	cache     *cache_client.TokenBlacklistCache
}

// PasetoGenerator generates and validates PASETO tokens.
type PasetoGenerator struct {
	PasetoValidator
	AccessDuration  time.Duration
	RefreshDuration time.Duration
	privateKey      ed25519.PrivateKey
}

// NewPasetoValidator initializes a PasetoValidator with a public key.
func NewPasetoValidator(config *config.TokenValidatorConfig, cache *cache_client.TokenBlacklistCache) (*PasetoValidator, error) {
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

	publicKey := ed25519.PublicKey(publicKeyBytes)
	return &PasetoValidator{
		config:    config,
		PublicKey: publicKey,
		cache:     cache,
	}, nil
}

// NewPasetoGenerator initializes a PasetoGenerator with both private and public keys.
func NewPasetoGenerator(config *config.TokenGeneratorConfig, cache *cache_client.TokenBlacklistCache) (*PasetoGenerator, error) {
	privateKeyBytes, err := hex.DecodeString(config.SignKey)
	if err != nil {
		log.Error("Error decoding private key: %v", err)
		return nil, errors.New("failed to decode private key")
	}

	if len(privateKeyBytes)*2 != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key size")
	}
	privateKey := ed25519.NewKeyFromSeed(privateKeyBytes)

	publicKey := privateKey.Public().(ed25519.PublicKey)
	validatorConfig := config.ToValidatorConfig(hex.EncodeToString(publicKey))
	validator, err := NewPasetoValidator(validatorConfig, cache)
	if err != nil {
		return nil, fmt.Errorf("failed to create PasetoValidator: %w", err)
	}

	return &PasetoGenerator{
		PasetoValidator: *validator,
		AccessDuration:  time.Duration(config.AccessTokenDuration) * time.Second,
		RefreshDuration: time.Duration(config.RefreshTokenDuration) * time.Second,
		privateKey:      privateKey,
	}, nil
}

// NewToken generates a new PASETO token with the specified grant type and claims.
func (g *PasetoGenerator) NewToken(grantType GrantType, userProperties map[string]interface{}, sessionId *string) (*tokenv1.Token, *TokenClaimsBase, error) {
	iat := time.Now().UTC()
	exp := iat.Add(g.getDuration(grantType))

	var claims PasetoClaims

	if userProperties != nil {
		claims.UserProperties = userProperties
	}

	// Populate claims
	claims.GrantType = grantType
	claims.Issuer = g.config.Issuer
	claims.Audience = g.config.Audience
	claims.IssuedAt = iat.Truncate(time.Second)
	claims.NotBefore = iat.Truncate(time.Second)
	claims.ExpiresAt = exp.Truncate(time.Second)
	claims.ID = uuid.NewString()
	claims.SessionId = sessionId

	// Serialize claims to JSON
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal claims: %w", err)
	}

	// Sign the token
	token, err := pasetov4.Sign(payloadBytes, g.privateKey, nil, []byte("implicit assertion"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign PASETO token: %w", err)
	}

	return &tokenv1.Token{Value: token, ExpireTime: datetime.ToProtoTimestamp(&exp)}, &TokenClaimsBase{ExpiresIn: g.getDuration(grantType), JTI: claims.ID, SessionID: sessionId}, nil
}

// Verify checks if a PASETO token is valid and performs issuer and audience checks.
func (v *PasetoValidator) Verify(tokenString string, isRefresh bool, claims interface{}) (bool, error) {
	// Implicit assertions
	assertions := []byte("implicit assertion")

	// Verify the token
	payloadBytes, err := pasetov4.Verify(tokenString, v.PublicKey, nil, assertions)
	if err != nil {
		return false, errors.New("invalid token")
	}

	// Check blacklist
	if v.config.Blacklist {
		isBlacklisted, err := ExistBlackList(v.cache, tokenString)
		if err != nil {
			return false, err
		}
		if isBlacklisted {
			return false, errors.New("token is blacklisted")
		}
	}

	// Parse payload
	var tokenClaims PasetoClaims
	if err := json.Unmarshal(payloadBytes, &tokenClaims); err != nil {
		return false, fmt.Errorf("failed to parse token payload %w", err)
	}

	// GrantType validation
	if isRefresh && tokenClaims.GrantType != GrantTypeRefresh {
		return false, errors.New("invalid GrantType: expected refresh token")
	}

	// Validate Issuer and Audience
	if v.config.ValidateIssuer && !tokenClaims.VerifyIssuer(v.config.Issuer, true) {
		return false, errors.New("invalid token issuer")
	}
	if v.config.ValidateAudience && !tokenClaims.VerifyAudience(v.config.Audience, true) {
		return false, errors.New("invalid token audience")
	}

	// external claims
	if claims != nil {
		if err := json.Unmarshal(payloadBytes, claims); err != nil {
			return true, fmt.Errorf("failed to unmarshal external claims: %w", err)
		}
	}

	return true, nil
}

// getDuration returns the expiration duration based on the grant type.
func (g *PasetoGenerator) getDuration(grantType GrantType) time.Duration {
	if grantType == GrantTypeAccess {
		return g.AccessDuration
	}
	return g.RefreshDuration
}
