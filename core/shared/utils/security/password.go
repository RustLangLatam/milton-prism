// Package security provides cryptographic utilities for password hashing
// and verification used in user authentication flows.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2Params contains the parameters for Argon2id hashing
type Argon2Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams provides secure default parameters for Argon2id
var DefaultParams = Argon2Params{
	Memory:      64 * 1024, // 64MB
	Iterations:  3,         // 3 passes
	Parallelism: 2,         // 2 threads
	SaltLength:  16,        // 16 bytes salt
	KeyLength:   32,        // 32 bytes key
}

// GeneratePassword creates a new Argon2id hash of the password
func GeneratePassword(password string) (string, error) {
	return GeneratePasswordWithParams(password, &DefaultParams)
}

// GeneratePasswordWithParams creates a hash with custom parameters
func GeneratePasswordWithParams(password string, params *Argon2Params) (string, error) {
	// Generate a cryptographically secure random salt
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %v", err)
	}

	// Generate the hash using Argon2id
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		params.KeyLength,
	)

	// Base64 encode the salt and hashed password
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	// Return a standardized string representation
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.Memory,
		params.Iterations,
		params.Parallelism,
		b64Salt,
		b64Hash,
	)

	return encoded, nil
}

// VerifyPassword compares a password with an Argon2id hash
func VerifyPassword(password, encodedHash string) (bool, error) {
	// Extract the parameters, salt and hash from the encoded string
	params, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	// Generate the hash using the same parameters
	testHash := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		params.KeyLength,
	)

	// Constant time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare(hash, testHash) == 1 {
		return true, nil
	}
	return false, nil
}

// decodeHash extracts parameters from encoded hash
func decodeHash(encodedHash string) (*Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return nil, nil, nil, errors.New("invalid hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, nil, err
	}
	if version != argon2.Version {
		return nil, nil, nil, errors.New("incompatible argon2 version")
	}

	params := &Argon2Params{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &params.Parallelism); err != nil {
		return nil, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, err
	}
	params.SaltLength = uint32(len(salt))

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, err
	}
	params.KeyLength = uint32(len(hash))

	return params, salt, hash, nil
}

var passwordRegex = regexp.MustCompile(`^[A-Za-z\d._%+\-@$!%*?&]{8,64}$`)

// IsValidPassword validates the password meets complexity requirements.
// Requirements:
// - 8 to 64 characters long
// - At least one lowercase letter
// - At least one uppercase letter
// - At least one digit
// - At least one special character (._%+-@$!%*?&)
func IsValidPassword(password string, min, max int) bool {
	password = strings.TrimSpace(password)

	// Length check
	if len(password) < min || len(password) > max {
		return false
	}

	// Check regex pattern (allowed characters)
	if !passwordRegex.MatchString(password) {
		return false
	}

	// Individual requirement checks
	var (
		hasLower   = false
		hasUpper   = false
		hasNumber  = false
		hasSpecial = false
		specials   = "._%+-@$!%*?&"
	)

	for _, char := range password {
		switch {
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsNumber(char):
			hasNumber = true
		case strings.ContainsRune(specials, char):
			hasSpecial = true
		}

		// Early exit if all requirements met
		if hasLower && hasUpper && hasNumber && hasSpecial {
			return true
		}
	}

	return hasLower && hasUpper && hasNumber && hasSpecial
}
