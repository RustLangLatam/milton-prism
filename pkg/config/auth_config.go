package config

import (
	"errors"
	"fmt"
	"milton_prism/pkg/utils/pointers"
)

// Constants for algorithm values
const (
	AlgorithmEd25519     = 1
	AlgorithmSirius      = 2
	AlgorithmEcdsaSha256 = 3
	AlgorithmRs256       = 4
)

// TokenRole represents the role of the token configuration.
type TokenRole string

const (
	TokenRoleValidator TokenRole = "validator"
	TokenRoleGenerator TokenRole = "generator"
)

// AuthCfg contains authentication-related configuration.
type AuthCfg struct {
	Algorithm            int                   `toml:"algorithm"`
	TokenValidatorConfig *TokenValidatorConfig `toml:"tokenValidator"`
	TokenGeneratorConfig *TokenGeneratorConfig `toml:"tokenGenerator"`
}

// Validate checks AuthCfg fields and sets defaults for optional fields.
// - role: Specifies whether to validate for "validator" or "generator".
func (a *AuthCfg) Validate(role TokenRole) error {
	// Set default for Algorithm if not specified
	if a.Algorithm == 0 {
		a.Algorithm = AlgorithmEd25519 // Default to Ed25519
	}

	// Verify Algorithm value
	if a.Algorithm != AlgorithmEd25519 && a.Algorithm != AlgorithmSirius &&
		a.Algorithm != AlgorithmEcdsaSha256 && a.Algorithm != AlgorithmRs256 {
		return fmt.Errorf("unsupported algorithm: %d; must be one of %d, %d, %d, %d",
			a.Algorithm, AlgorithmEd25519, AlgorithmSirius, AlgorithmEcdsaSha256, AlgorithmRs256)
	}

	// Role-specific validation
	switch role {
	case TokenRoleValidator:
		if a.TokenValidatorConfig == nil {
			return errors.New("TokenValidatorConfig is required for validator role")
		}
		if err := a.TokenValidatorConfig.Validate(); err != nil {
			return fmt.Errorf("tokenValidatorConfig validation failed: %s", err)
		}

	case TokenRoleGenerator:
		if a.TokenGeneratorConfig == nil {
			return errors.New("TokenGeneratorConfig is required for generator role")
		}
		if err := a.TokenGeneratorConfig.Validate(); err != nil {
			return fmt.Errorf("tokenGeneratorConfig validation failed: %s", err)
		}

	default:
		return fmt.Errorf("unknown token role: %s", role)
	}

	return nil
}

// TokenCommonConfig defines the configuration structure for token validation.
type TokenCommonConfig struct {
	Enabled          bool    `toml:"enabled"`
	SchemaType       *string `toml:"schemaType"`
	Issuer           string  `toml:"issuer"`
	Audience         string  `toml:"audience"`
	Storage          bool    `toml:"storage"`
	Blacklist        bool    `toml:"blacklist"`
	ValidateIssuer   bool    `toml:"validateIssuer"`
	ValidateAudience bool    `toml:"validateAudience"`
}

// Validate checks TokenCommonConfig fields and sets defaults for optional fields.
func (a *TokenCommonConfig) Validate() error {
	// Verify SchemaType value
	if a.SchemaType != nil {
		if *a.SchemaType != "JWT" && *a.SchemaType != "Paseto" {
			return fmt.Errorf("unsupported SchemaType: %s; must be 'JWT' or 'Paseto'", *a.SchemaType)
		}
	} else {
		// Set default for SchemaType if empty
		a.SchemaType = pointers.StringPtr("JWT") // Default to "JWT"
	}

	// Check that Issuer is provided if ValidateIssuer is true
	if a.ValidateIssuer && a.Issuer == "" {
		return errors.New("issuer is required when ValidateIssuer is true")
	}

	// Check that Audience is provided if ValidateAudience is true
	if a.ValidateAudience && a.Audience == "" {
		return errors.New("audience is required when ValidateAudience is true")
	}

	return nil
}

type TokenValidatorConfig struct {
	TokenCommonConfig        // Embeds TokenCommonConfig for validation fields
	PublicKey         string `toml:"publicKey"`
}

// Validate checks TokenGeneratorConfig fields and ensures all required fields for token generation are set.
func (g *TokenValidatorConfig) Validate() error {
	// First validate the embedded TokenCommonConfig fields
	if err := g.TokenCommonConfig.Validate(); err != nil {
		return fmt.Errorf("token settings_config validation failed: %w", err)
	}

	// Verify PublicKey length and format
	if g.PublicKey == "" {
		return errors.New("publicKey must be specified")
	}
	if len(g.PublicKey) != 64 {
		return errors.New("publicKey must be exactly 64 characters")
	}
	if !isHex(g.PublicKey) {
		return errors.New("publicKey must be a 64-character hexadecimal string")
	}

	return nil
}

// UserClaims Payload inclusion flags
type UserClaims struct {
	IncludeState        *bool `toml:"includeState"`        // Whether to include state
	IncludeUserKind     *bool `toml:"includeUserKind"`     // Whether to include user kind
	IncludeUserID       bool  `toml:"includeUserId"`       // Whether to include user identifier
	IncludeUsername     bool  `toml:"includeUsername"`     // Whether to include username
	IncludeUserEmail    bool  `toml:"includeUserEmail"`    // Whether to include email
	IncludeUserProfiles bool  `toml:"includeUserProfiles"` // Whether to include profiles
	IncludeCustomClaims bool  `toml:"includeCustomClaims"` // Whether to allow custom claims
}

// Validate ensures UserClaims configuration is correct and sets default values
func (u *UserClaims) Validate() error {
	// Set defaults for nil boolean pointers
	if u.IncludeState == nil {
		defaultState := true
		u.IncludeState = &defaultState
	}
	if u.IncludeUserKind == nil {
		defaultKind := true
		u.IncludeUserKind = &defaultKind
	}

	// Validate other fields if needed
	// (Add any additional validation rules for other fields here)

	return nil
}

// TokenGeneratorConfig extends TokenCommonConfig to include fields specific to token generation.
type TokenGeneratorConfig struct {
	TokenCommonConfig // Validation settings

	// Signing configuration
	SignKey              string `toml:"signKey"`
	AccessTokenDuration  uint32 `toml:"accessTokenDuration"`  // in seconds
	RefreshTokenDuration uint32 `toml:"refreshTokenDuration"` // in seconds
	TtlCache             uint64 `toml:"ttlCache"`

	// User claims
	UserClaims UserClaims `toml:"userClaims"`
}

// Validate checks TokenGeneratorConfig fields and ensures all required fields for token generation are set.
func (g *TokenGeneratorConfig) Validate() error {
	// First validate the embedded TokenCommonConfig fields
	if err := g.TokenCommonConfig.Validate(); err != nil {
		return fmt.Errorf("token settings_config validation failed: %w", err)
	}

	// Validate user claims (this will set defaults for IncludeState/IncludeUserKind)
	if err := g.UserClaims.Validate(); err != nil {
		return fmt.Errorf("user claims validation failed: %w", err)
	}

	// Check that SignKey is provided and correctly formatted
	if g.SignKey == "" {
		return errors.New("privateKey must be specified for token generation")
	}
	if len(g.SignKey) != 64 {
		return errors.New("privateKey must be exactly 64 characters")
	}

	if !isHex(g.SignKey) {
		return errors.New("privateKey must be a 64-character hexadecimal string")
	}

	// Check AccessTokenDuration is set and is a positive duration
	if g.AccessTokenDuration <= 0 {
		return errors.New("accessTokenDuration must be a positive integer representing seconds")
	}

	// Check RefreshTokenDuration is set and is a positive duration
	if g.RefreshTokenDuration <= 0 {
		return errors.New("refreshTokenDuration must be a positive integer representing seconds")
	}

	// Optionally, check that refresh token duration is longer than access token duration
	if g.RefreshTokenDuration < g.AccessTokenDuration {
		return errors.New("refreshTokenDuration should be greater than or equal to accessTokenDuration")
	}

	return nil
}

// ToValidatorConfig converts TokenGeneratorConfig to TokenValidatorConfig using only the PublicKey.
func (g *TokenGeneratorConfig) ToValidatorConfig(publicKey string) *TokenValidatorConfig {
	return &TokenValidatorConfig{
		TokenCommonConfig: g.TokenCommonConfig, // Copy common settings_config fields
		PublicKey:         publicKey,           // Set the provided public key
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
