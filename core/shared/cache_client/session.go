package cache_client

import (
	"encoding/json"
	"errors"
	applog "milton_prism/pkg/log"
	"milton_prism/core/shared/session"
	paniccontrol "milton_prism/core/shared/utils"
	"strconv"
	"time"

	"github.com/gomodule/redigo/redis"
)

type SessionCache struct {
	*Cache
	ttl time.Duration
}

const (
	userSessionKeyPrefix = "user:sessions:" // Set of session IDs per user
	sessionDataKeyPrefix = "session:data:"  // Session data per session Identifier
)

// NewSessionCache returns a new instance of SessionCache
func NewSessionCache(pool *Cache, ttl time.Duration) *SessionCache {
	return &SessionCache{
		Cache: pool,
		ttl:   ttl,
	}
}

// SaveSession stores a new session
func (sc *SessionCache) SaveSession(s *session.Session) error {
	conn := sc.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	sessionKey := sessionDataKeyPrefix + s.SessionID
	userSetKey := userSessionKeyPrefix + strconv.FormatUint(s.UserID, 10)

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	// Store session data with TTL
	if _, err := conn.Do("SETEX", sessionKey, int(sc.ttl.Seconds()), data); err != nil {
		return err
	}

	// Add session Identifier to the user's session set
	if _, err := conn.Do("SADD", userSetKey, s.SessionID); err != nil {
		return err
	}

	// Set TTL on the user session set (optional, can be removed if you want it permanent)
	if _, err := conn.Do("EXPIRE", userSetKey, int(sc.ttl.Seconds())); err != nil {
		return err
	}

	return nil
}

// GetSessionByID retrieves session data by session Identifier
func (sc *SessionCache) GetSessionByID(sessionID string) (*session.Session, error) {
	conn := sc.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	data, err := redis.Bytes(conn.Do("GET", sessionDataKeyPrefix+sessionID))
	if err != nil {
		if errors.Is(err, redis.ErrNil) {
			return nil, errors.New("session not found")
		}
		return nil, err
	}

	var s session.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// GetAllUserSessions fetches all session objects for a user
func (sc *SessionCache) GetAllUserSessions(userID uint64) ([]*session.Session, error) {
	conn := sc.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	userSetKey := userSessionKeyPrefix + strconv.FormatUint(userID, 10)
	sessionIDs, err := redis.Strings(conn.Do("SMEMBERS", userSetKey))
	if err != nil {
		return nil, err
	}

	var sessions []*session.Session
	for _, sid := range sessionIDs {
		session, err := sc.GetSessionByID(sid)
		if err == nil {
			sessions = append(sessions, session)
		}
	}

	return sessions, nil
}

// RevokeSession marks a session as revoked and updates it
func (sc *SessionCache) RevokeSession(sessionID string) error {
	s, err := sc.GetSessionByID(sessionID)
	if err != nil {
		return err
	}

	s.IsRevoked = true
	s.LastUsedAt = time.Now()
	return sc.SaveSession(s)
}

// DeleteSession completely removes a session
func (sc *SessionCache) DeleteSession(sessionID string, userID string) error {
	conn := sc.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	_, err := conn.Do("DEL", sessionDataKeyPrefix+sessionID)
	if err != nil {
		return err
	}

	_, err = conn.Do("SREM", userSessionKeyPrefix+userID, sessionID)
	return err
}
