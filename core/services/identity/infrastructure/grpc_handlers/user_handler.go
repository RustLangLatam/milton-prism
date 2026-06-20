// Package grpc_handlers exposes the identity application service as a gRPC
// IdentityServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"

	"milton_prism/core/services/identity/application"
	"milton_prism/core/services/identity/domain"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// SessionExtractor validates the access token in ctx and returns the user ID,
// session ID, and raw token string — needed for complete session revocation.
type SessionExtractor func(ctx context.Context) (userID uint64, sessionID string, rawToken string, err error)

// IdentityHandler implements identitysvcv1.IdentityServiceServer.
type IdentityHandler struct {
	identitysvcv1.UnimplementedIdentityServiceServer
	svc            *application.Service
	authExtract    AuthExtractor
	sessionExtract SessionExtractor
}

// NewIdentityHandler builds a handler bound to the application service.
func NewIdentityHandler(svc *application.Service, authExtract AuthExtractor, sessionExtract SessionExtractor) *IdentityHandler {
	return &IdentityHandler{svc: svc, authExtract: authExtract, sessionExtract: sessionExtract}
}

// CreateUser persists a new user account.
func (h *IdentityHandler) CreateUser(ctx context.Context, req *identitysvcv1.CreateUserRequest) (*identityv1.User, error) {
	if req.GetUser() == nil || req.GetUser().GetEmail() == "" {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	if req.GetPassword() == "" {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPassword, domain.ErrMissingPassword.Message)
	}
	out, err := h.svc.CreateUser(ctx, req.GetUser(), req.GetPassword())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

// GetUser returns a user by identifier. Only self or system users may call this.
func (h *IdentityHandler) GetUser(ctx context.Context, req *identitysvcv1.GetUserRequest) (*identityv1.User, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("identity: GetUser authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	if !isSystem && callerID != req.GetIdentifier() {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	out, err := h.svc.GetUser(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

// ListUsers returns a paginated user list. Only system users may call this.
func (h *IdentityHandler) ListUsers(ctx context.Context, req *identitysvcv1.ListUsersRequest) (*identitysvcv1.ListUsersResponse, error) {
	_, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("identity: ListUsers authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	items, p, err := h.svc.ListUsers(ctx, req.GetFilter(), req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &identitysvcv1.ListUsersResponse{Users: items, Pagination: p}, nil
}

// UpdateUser applies a FieldMask-bounded partial update. Only self or system users may call this.
func (h *IdentityHandler) UpdateUser(ctx context.Context, req *identitysvcv1.UpdateUserRequest) (*identityv1.User, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("identity: UpdateUser authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetUser() == nil || req.GetUser().GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	if !isSystem && callerID != req.GetUser().GetIdentifier() {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	out, err := h.svc.UpdateUser(ctx, req.GetUser(), req.GetUpdateMask())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

// DeleteUser soft-deletes a user. Only self or system users may call this.
func (h *IdentityHandler) DeleteUser(ctx context.Context, req *identitysvcv1.DeleteUserRequest) (*emptypb.Empty, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("identity: DeleteUser authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if !isSystem && callerID != req.GetIdentifier() {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	if err := h.svc.DeleteUser(ctx, req.GetIdentifier()); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

// AuthenticateUser validates credentials and returns a fresh token pair.
func (h *IdentityHandler) AuthenticateUser(ctx context.Context, req *identitysvcv1.AuthenticateUserRequest) (*tokenv1.AuthorizationTokens, error) {
	if req.GetEmail() == "" {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingEmail, domain.ErrMissingEmail.Message)
	}
	if req.GetPassword() == "" {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPassword, domain.ErrMissingPassword.Message)
	}
	tokens, err := h.svc.AuthenticateUser(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, h.mapError(err)
	}
	return tokens, nil
}

// RefreshToken issues a new token pair from a valid refresh token.
func (h *IdentityHandler) RefreshToken(ctx context.Context, req *identitysvcv1.RefreshTokenRequest) (*tokenv1.AuthorizationTokens, error) {
	if req.GetRefreshToken() == "" {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	tokens, err := h.svc.RefreshToken(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, h.mapError(err)
	}
	return tokens, nil
}

// Logout invalidates the current session and revokes the access token.
func (h *IdentityHandler) Logout(ctx context.Context, _ *identitysvcv1.LogoutRequest) (*emptypb.Empty, error) {
	_, sessionID, rawToken, err := h.sessionExtract(ctx)
	if err != nil {
		applog.Warningf("identity: Logout authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if err := h.svc.Logout(ctx, sessionID, rawToken); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

// GetCurrentUser returns the profile of the authenticated caller.
func (h *IdentityHandler) GetCurrentUser(ctx context.Context, _ *identitysvcv1.GetCurrentUserRequest) (*identityv1.User, error) {
	callerID, _, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("identity: GetCurrentUser authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	out, err := h.svc.GetUser(ctx, callerID)
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *IdentityHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodeUserNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeEmailAlreadyExists:
			return coreerror.NewAlreadyExistsError(dErr.Code, dErr.Message)
		case domain.ErrCodeUserNotActive, domain.ErrCodeAccountSuspended:
			return coreerror.NewPermissionDeniedError(dErr.Code, dErr.Message)
		case domain.ErrCodeInvalidCredentials, domain.ErrCodeInvalidToken, domain.ErrCodeInvalidSession:
			return coreerror.NewUnauthenticatedError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingIdentifier, domain.ErrCodeMissingPayload,
			domain.ErrCodeInvalidEmail, domain.ErrCodeInvalidPassword,
			domain.ErrCodeMissingEmail, domain.ErrCodeMissingPassword:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		case domain.ErrCodeInternal, domain.ErrCodeTokenGeneration, domain.ErrCodeTokenRefresh:
			applog.Warningf("internal identity error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("unhandled identity error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	applog.Warningf("unhandled identity error: error=%v", err)
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
