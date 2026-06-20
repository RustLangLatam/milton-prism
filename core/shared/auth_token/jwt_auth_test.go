package auth_token_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"milton_prism/core/shared/auth_token"
	"milton_prism/pkg/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type zeroReader struct{}

func (zeroReader) Read(buf []byte) (int, error) {
	clear(buf)
	return len(buf), nil
}

func generateKeyPair() (ed25519.PrivateKey, ed25519.PublicKey) {
	random := rand.Reader
	publicKey, privateKey, _ := ed25519.GenerateKey(random)
	return privateKey, publicKey
}

func TestNewJWTValidator_ValidConfig(t *testing.T) {
	_, publicKey := generateKeyPair()
	validatorConfig := &config.TokenValidatorConfig{
		PublicKey:         hex.EncodeToString(publicKey),
		TokenCommonConfig: config.TokenCommonConfig{Blacklist: false},
	}

	validator, err := auth_token.NewJWTValidator(validatorConfig, nil)

	require.NoError(t, err, "Validator initialization should succeed")
	assert.NotNil(t, validator, "Validator should not be nil")
	assert.Equal(t, publicKey, validator.PublicKey, "Validator public key should match provided key")
}

func TestNewJWTValidator_InvalidPublicKey(t *testing.T) {
	validatorConfig := &config.TokenValidatorConfig{
		PublicKey: "invalid_hex_key",
	}

	validator, err := auth_token.NewJWTValidator(validatorConfig, nil)

	require.Error(t, err, "Validator initialization should fail with invalid public key")
	assert.Nil(t, validator, "Validator should be nil")
}

func TestNewJWTGenerator_ValidConfig(t *testing.T) {
	privateKey, publicKey := generateKeyPair()
	generatorConfig := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
	}

	generator, err := auth_token.NewJWTGenerator(generatorConfig, nil)

	require.NoError(t, err, "Generator initialization should succeed")
	assert.NotNil(t, generator, "Generator should not be nil")
	assert.Equal(t, privateKey, generator.PrivateKey, "Generator private key should match provided key")
	assert.Equal(t, publicKey, generator.PublicKey, "Generator public key should match provided key")
}

func TestNewJWTGenerator_InvalidPrivateKey(t *testing.T) {
	generatorConfig := &config.TokenGeneratorConfig{
		SignKey: "invalid_hex_key",
	}

	generator, err := auth_token.NewJWTGenerator(generatorConfig, nil)

	require.Error(t, err, "Generator initialization should fail with invalid private key")
	assert.Nil(t, generator, "Generator should be nil")
}

func TestJWTGenerator_NewToken(t *testing.T) {
	privateKey, _ := generateKeyPair()
	generatorConfig := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
	}

	generator, _ := auth_token.NewJWTGenerator(generatorConfig, nil)

	userProps := map[string]interface{}{
		"email":    "user@example.com",
		"is_admin": true,
	}

	token, _, err := generator.NewToken(auth_token.GrantTypeAccess, userProps, nil)

	require.NoError(t, err, "Token generation should succeed")
	assert.NotEmpty(t, token, "Generated token should not be empty")
}

func TestJWTValidator_Verify_ValidToken(t *testing.T) {
	privateKey, _ := generateKeyPair()
	generatorConfig := &config.TokenGeneratorConfig{
		SignKey:              hex.EncodeToString(privateKey.Seed()),
		AccessTokenDuration:  3600,
		RefreshTokenDuration: 7200,
		TokenCommonConfig: config.TokenCommonConfig{
			ValidateIssuer:   true,
			ValidateAudience: true,
			Issuer:           "test_issuer",
			Audience:         "test_audience",
		},
	}

	generator, _ := auth_token.NewJWTGenerator(generatorConfig, nil)

	userProps := map[string]interface{}{
		"email":    "user@example.com",
		"is_admin": true,
	}

	token, _, _ := generator.NewToken(auth_token.GrantTypeAccess, userProps, nil)

	valid, err := generator.Verify(token.GetValue(), false, nil)

	require.NoError(t, err, "Token verification should succeed")
	assert.True(t, valid, "Token should be valid")
}

func TestJWTValidator_Verify_InvalidToken(t *testing.T) {
	_, publicKey := generateKeyPair()
	validatorConfig := &config.TokenValidatorConfig{
		PublicKey: hex.EncodeToString(publicKey),
	}

	validator, _ := auth_token.NewJWTValidator(validatorConfig, nil)

	var claims auth_token.JWTClaims
	valid, err := validator.Verify("invalid_token", false, &claims)

	assert.Error(t, err, "Verification should fail with invalid token")
	assert.False(t, valid, "Token should be invalid")
}
