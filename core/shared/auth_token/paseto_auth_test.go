package auth_token_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"milton_prism/core/shared/auth_token"
	"milton_prism/pkg/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPasetoValidator(t *testing.T) {
	// Generate a valid public key.
	publicKeyBytes := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	c := &config.TokenValidatorConfig{
		PublicKey:         hex.EncodeToString(publicKeyBytes),
		TokenCommonConfig: config.TokenCommonConfig{Blacklist: false},
	}

	// Create a validator.
	validator, err := auth_token.NewPasetoValidator(c, nil)

	assert.NoError(t, err, "Validator creation should not fail")
	assert.NotNil(t, validator, "Validator should be created")
	assert.Equal(t, c.PublicKey, hex.EncodeToString(validator.PublicKey), "Public keys should match")
}

func TestNewPasetoValidatorInvalidKey(t *testing.T) {
	// Invalid public key size.
	c := &config.TokenValidatorConfig{
		PublicKey: "abcd", // Invalid key.
	}

	validator, err := auth_token.NewPasetoValidator(c, nil)

	assert.Error(t, err, "Validator creation should fail for invalid public key")
	assert.Nil(t, validator, "Validator should not be created with an invalid key")
}

func TestNewPasetoGenerator(t *testing.T) {
	// Generate valid keys.
	privateKey := ed25519.NewKeyFromSeed(make([]byte, 32))
	c := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
	}

	// Create a generator.
	generator, err := auth_token.NewPasetoGenerator(c, nil)

	assert.NoError(t, err, "Generator creation should not fail")
	assert.NotNil(t, generator, "Generator should be created")
	assert.Equal(t, c.AccessTokenDuration, uint32(generator.AccessDuration.Seconds()), "Access token duration should match")
	assert.Equal(t, c.RefreshTokenDuration, uint32(generator.RefreshDuration.Seconds()), "Refresh token duration should match")
}

func TestNewPasetoGeneratorInvalidPrivateKey(t *testing.T) {
	// Invalid private key.
	c := &config.TokenGeneratorConfig{
		SignKey: "abcd", // Invalid key.
	}

	generator, err := auth_token.NewPasetoGenerator(c, nil)

	assert.Error(t, err, "Generator creation should fail for invalid private key")
	assert.Nil(t, generator, "Generator should not be created with an invalid key")
}

func TestNewToken(t *testing.T) {
	// Generate valid keys and setup.
	privateKey := ed25519.NewKeyFromSeed(make([]byte, 32))
	c := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
	}

	generator, _ := auth_token.NewPasetoGenerator(c, nil)

	userProperties := map[string]interface{}{
		"email":   "user@example.com",
		"isAdmin": true,
	}

	token, _, err := generator.NewToken(auth_token.GrantTypeAccess, userProperties, nil)

	assert.NoError(t, err, "Token generation should not fail")
	assert.NotEmpty(t, token, "Generated token should not be empty")
}

func TestVerifyToken(t *testing.T) {
	// Generate valid keys and setup.
	privateKey := ed25519.NewKeyFromSeed(make([]byte, 32))
	c := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
	}

	generator, _ := auth_token.NewPasetoGenerator(c, nil)
	token, _, _ := generator.NewToken(auth_token.GrantTypeAccess, nil, nil)

	// Create a validator.
	validatorConfig := c.ToValidatorConfig(hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)))
	validator, _ := auth_token.NewPasetoValidator(validatorConfig, nil)

	var claims auth_token.PasetoClaims
	valid, err := validator.Verify(token.GetValue(), false, &claims)

	assert.NoError(t, err, "Token verification should not fail")
	assert.True(t, valid, "Token should be valid")
	assert.Equal(t, auth_token.GrantTypeAccess, claims.GrantType, "Grant type should match")
}

func TestVerifyInvalidToken(t *testing.T) {
	validator := &auth_token.PasetoValidator{}

	var claims auth_token.PasetoClaims
	valid, err := validator.Verify("invalid-token", false, &claims)

	assert.Error(t, err, "Verification should fail for an invalid token")
	assert.False(t, valid, "Token should be invalid")
}
