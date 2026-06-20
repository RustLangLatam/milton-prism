package repositories

import (
	"errors"

	"milton_prism/core/services/identity/ports"
	"milton_prism/core/shared/utils/security"
)

var _ ports.PasswordHasher = (*Argon2Hasher)(nil)

// Argon2Hasher implements ports.PasswordHasher using Argon2id.
type Argon2Hasher struct{}

func NewArgon2Hasher() *Argon2Hasher { return &Argon2Hasher{} }

func (h *Argon2Hasher) Hash(plain string) (string, error) {
	return security.GeneratePassword(plain)
}

func (h *Argon2Hasher) Verify(hash, plain string) error {
	ok, err := security.VerifyPassword(plain, hash)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("password mismatch")
	}
	return nil
}
