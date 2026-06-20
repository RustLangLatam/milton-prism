// Package application implements the use cases of the identity service.
//
// Service depends only on driven ports (repository, hasher, token manager,
// session store). All authentication side effects are behind those ports so
// the core is testable and free of MongoDB / Redis / gRPC client knowledge.
package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/ports"
	applog "milton_prism/pkg/log"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Service orchestrates identity use cases.
type Service struct {
	repo     ports.UserRepository
	tx       ports.TransactionManager
	hasher   ports.PasswordHasher
	tokens   ports.TokenManager
	sessions ports.SessionStore
}

// NewService wires the identity application service.
func NewService(
	repo ports.UserRepository,
	tx ports.TransactionManager,
	hasher ports.PasswordHasher,
	tokens ports.TokenManager,
	sessions ports.SessionStore,
) *Service {
	return &Service{repo: repo, tx: tx, hasher: hasher, tokens: tokens, sessions: sessions}
}

// CreateUser persists a new user account with a hashed password.
func (s *Service) CreateUser(ctx context.Context, u *domain.User, plainPassword string) (*domain.User, error) {
	if u == nil || u.GetEmail() == "" {
		return nil, domain.ErrMissingPayload
	}
	if plainPassword == "" {
		return nil, domain.ErrMissingPassword
	}
	hash := plainPassword
	if s.hasher != nil {
		h, err := s.hasher.Hash(plainPassword)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
		}
		hash = h
	}
	out, err := s.repo.Create(ctx, u, hash)
	if err != nil {
		if errors.Is(err, domain.ErrEmailAlreadyExists) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return out, nil
}

// GetUser returns a user by identifier.
func (s *Service) GetUser(ctx context.Context, identifier uint64) (*domain.User, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	out, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return out, nil
}

// ListUsers returns a paginated list of users.
func (s *Service) ListUsers(ctx context.Context, filter *domain.UsersFilter, params *queryparamsv1.PageQueryParams) ([]*domain.User, *paginationv1.Pagination, error) {
	if params == nil {
		params = impl.PageQueryParamsDefault()
	}
	impl.NormalizePaginationParams(params)
	items, p, err := s.repo.List(ctx, filter, params)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return items, p, nil
}

// UpdateUser applies a FieldMask-bounded partial update to a user.
// A nil or empty mask means "update all mutable fields".
func (s *Service) UpdateUser(ctx context.Context, u *domain.User, mask *fieldmaskpb.FieldMask) (*domain.User, error) {
	if u == nil || u.GetIdentifier() == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	existing, err := s.repo.GetByID(ctx, u.GetIdentifier(), false)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	paths := mask.GetPaths()
	if len(paths) == 0 {
		paths = []string{"email", "display_name", "system_user", "state"}
	}
	for _, p := range paths {
		switch p {
		case "email":
			existing.Email = u.GetEmail()
		case "display_name":
			existing.DisplayName = u.GetDisplayName()
		case "system_user":
			existing.SystemUser = u.GetSystemUser()
		case "state":
			existing.State = u.GetState()
		}
	}
	if err := s.repo.Update(ctx, existing); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return existing, nil
}

// DeleteUser soft-deletes a user by setting delete_time.
func (s *Service) DeleteUser(ctx context.Context, identifier uint64) error {
	if identifier == 0 {
		return domain.ErrMissingIdentifier
	}
	if err := s.repo.SoftDelete(ctx, identifier); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return err
		}
		return fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return nil
}

// AuthenticateUser validates credentials and returns fresh auth tokens.
// Password is never returned or persisted in plaintext.
func (s *Service) AuthenticateUser(ctx context.Context, email, plainPassword string) (*domain.AuthorizationTokens, error) {
	if email == "" {
		return nil, domain.ErrMissingEmail
	}
	if plainPassword == "" {
		return nil, domain.ErrMissingPassword
	}
	user, hash, err := s.repo.GetCredentialsByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	if user.GetState() == domain.UserStateDeleted {
		return nil, domain.ErrUserNotActive
	}
	if user.GetState() == domain.UserStateSuspended {
		return nil, domain.ErrAccountSuspended
	}
	if s.hasher != nil {
		if err := s.hasher.Verify(hash, plainPassword); err != nil {
			return nil, domain.ErrInvalidCredentials
		}
	}
	if s.tokens == nil {
		return nil, domain.ErrTokenGeneration
	}
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	tokens, err := s.tokens.NewTokens(ctx, user.GetIdentifier(), user.GetSystemUser(), sessionID)
	if err != nil {
		return nil, domain.ErrTokenGeneration
	}
	if s.sessions != nil {
		if err := s.sessions.Save(ctx, sessionID, user.GetIdentifier(), user.GetSystemUser()); err != nil {
			return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
		}
	}
	return tokens, nil
}

// RefreshToken validates the supplied refresh token and returns a new token pair.
func (s *Service) RefreshToken(ctx context.Context, rawRefreshToken string) (*domain.AuthorizationTokens, error) {
	if rawRefreshToken == "" {
		return nil, domain.ErrInvalidToken
	}
	if s.tokens == nil {
		return nil, domain.ErrTokenRefresh
	}
	sessionID, err := s.tokens.ExtractSessionID(rawRefreshToken)
	if err != nil {
		return nil, domain.ErrInvalidToken
	}
	userID, _, valid, err := s.sessions.Get(ctx, sessionID)
	if err != nil || !valid {
		return nil, domain.ErrInvalidSession
	}
	user, err := s.repo.GetByID(ctx, userID, false)
	if err != nil {
		return nil, domain.ErrUserNotFound
	}
	if err := s.tokens.Revoke(ctx, rawRefreshToken); err != nil {
		applog.Warningf("identity.RefreshToken: revoke old token: code=%s error=%v", domain.ErrCodeInternal, err)
	}
	tokens, err := s.tokens.NewTokens(ctx, user.GetIdentifier(), user.GetSystemUser(), sessionID)
	if err != nil {
		return nil, domain.ErrTokenRefresh
	}
	return tokens, nil
}

// Logout invalidates the user session and revokes the access token.
func (s *Service) Logout(ctx context.Context, sessionID, accessToken string) error {
	if s.sessions != nil && sessionID != "" {
		if err := s.sessions.Delete(ctx, sessionID); err != nil {
			return fmt.Errorf("%w: %v", domain.ErrInternal, err)
		}
	}
	if s.tokens != nil && accessToken != "" {
		if err := s.tokens.Revoke(ctx, accessToken); err != nil {
			return fmt.Errorf("%w: %v", domain.ErrInternal, err)
		}
	}
	return nil
}

// generateSessionID returns a 16-byte cryptographically random hex string used as session ID.
func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
