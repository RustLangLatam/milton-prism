package repositories

import (
	"context"
	"strconv"
	"time"

	"milton_prism/core/services/identity/ports"
	"milton_prism/core/shared/cache_client"
	"milton_prism/core/shared/session"
)

var _ ports.SessionStore = (*SessionStoreAdapter)(nil)

// SessionStoreAdapter adapts cache_client.CacheClient to ports.SessionStore.
type SessionStoreAdapter struct {
	cache *cache_client.CacheClient
	ttl   time.Duration
}

func NewSessionStoreAdapter(c *cache_client.CacheClient) *SessionStoreAdapter {
	return &SessionStoreAdapter{cache: c, ttl: 24 * time.Hour}
}

func (a *SessionStoreAdapter) Save(_ context.Context, sessionID string, userID uint64, systemUser bool) error {
	s := &session.Session{
		SessionID:  sessionID,
		UserID:     userID,
		SystemUser: systemUser,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(a.ttl),
	}
	return a.cache.SaveSession(s)
}

func (a *SessionStoreAdapter) Get(_ context.Context, sessionID string) (uint64, bool, bool, error) {
	s, err := a.cache.GetSessionByID(sessionID)
	if err != nil {
		return 0, false, false, err
	}
	return s.UserID, s.SystemUser, s.IsValid(), nil
}

func (a *SessionStoreAdapter) Delete(_ context.Context, sessionID string) error {
	s, err := a.cache.GetSessionByID(sessionID)
	if err != nil {
		return nil
	}
	return a.cache.DeleteSession(sessionID, strconv.FormatUint(s.UserID, 10))
}
