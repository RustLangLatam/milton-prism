package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/ports"
	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/cache_client"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"
)

var _ ports.TokenManager = (*TokenManagerAdapter)(nil)

// TokenManagerAdapter adapts auth_token.TokenManager to the identity ports.TokenManager.
type TokenManagerAdapter struct {
	manager   auth_token.TokenManager
	blacklist *cache_client.TokenBlacklistCache
}

func NewTokenManagerAdapter(m auth_token.TokenManager, bl *cache_client.TokenBlacklistCache) *TokenManagerAdapter {
	return &TokenManagerAdapter{manager: m, blacklist: bl}
}

func (a *TokenManagerAdapter) NewTokens(_ context.Context, userID uint64, systemUser bool, sessionID string) (*domain.AuthorizationTokens, error) {
	userProperties := map[string]interface{}{
		"user_id":     userID,
		"system_user": systemUser,
	}
	accessToken, _, err := a.manager.NewToken(auth_token.GrantTypeAccess, userProperties, &sessionID)
	if err != nil {
		return nil, fmt.Errorf("token manager: access token: %w", err)
	}
	refreshToken, _, err := a.manager.NewToken(auth_token.GrantTypeRefresh, nil, &sessionID)
	if err != nil {
		return nil, fmt.Errorf("token manager: refresh token: %w", err)
	}
	return &tokenv1.AuthorizationTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// refreshClaims is a minimal claims structure used to extract the session ID
// embedded in a refresh token's claims payload.
type refreshClaims struct {
	SessionId string `json:"sid"`
}

func (a *TokenManagerAdapter) ExtractSessionID(refreshToken string) (string, error) {
	var c refreshClaims
	ok, err := a.manager.Verify(refreshToken, true, &c)
	if err != nil || !ok {
		return "", fmt.Errorf("token manager: invalid refresh token")
	}
	return c.SessionId, nil
}

func (a *TokenManagerAdapter) Revoke(_ context.Context, token string) error {
	return a.blacklist.AddTokenToBlacklist(token, nil)
}
