package services

import (
	"fmt"
	"sync"
	"time"

	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/session"

	"github.com/google/uuid"
	"golang.org/x/net/context"
)

// SystemUserID is the RESERVED user id used for internal, service-to-service
// system calls (e.g. recording GENERATION/MIGRATION spend in billing). Human
// user ids are issued from a Mongo sequence that starts at 10001 and only ever
// increments (see identity/.../identifier.go), so any value below that floor can
// never collide with a real user. 1 is the documented system sentinel.
const SystemUserID uint64 = 1

// systemTokenTTL is the lifetime of a minted system access token AND of the
// backing cache session. Kept deliberately short so a leaked token is useless
// within minutes. The token is re-minted when fewer than systemTokenRenewBefore
// remain (see SystemAccessToken).
const systemTokenTTL = 5 * time.Minute

// systemTokenRenewBefore is the slack window before expiry at which the cached
// system token is proactively re-minted, so a caller never receives a token
// that is about to expire mid-flight.
const systemTokenRenewBefore = 60 * time.Second

// systemTokenState is the in-process cache of the current system token and the
// session id it is bound to. Guarded by mu. Encapsulated in this package so the
// signing key (creatorToken) is reachable from exactly one place — only this
// binary mints system tokens.
type systemTokenState struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

var sysTokenState systemTokenState

// SystemAccessToken returns a short-lived PASETO access token that authenticates
// as the reserved system user with system_user:true. It is intended for exactly
// one purpose: authorizing internal RecordUsage calls to the billing service,
// which require a system caller (BIL101 Failure_System_User_Required otherwise).
//
// The billing handler reads the system flag from the CACHED SESSION, not from
// the token claim, so this helper does two things atomically per mint:
//  1. seeds/refreshes a system session in the shared session cache
//     (SystemUser:true, fresh sid, short ExpiresAt, IsRevoked:false), and
//  2. mints a PASETO token bound to that sid carrying user_properties
//     {user_id: SystemUserID, system_user: true}.
//
// The token is cached in-process and reused until fewer than
// systemTokenRenewBefore remain, then re-minted. The returned string is a raw
// secret: callers MUST NOT log it.
//
// Returns an error when the binary has no token creator configured (validator-
// only role) — in that case no system token can be produced.
func (s *Services) SystemAccessToken(ctx context.Context) (string, error) {
	if s.creatorToken == nil {
		return "", fmt.Errorf("system token: no token creator configured (validator-only role)")
	}
	if s.cacheClient == nil {
		return "", fmt.Errorf("system token: no session cache configured")
	}

	sysTokenState.mu.Lock()
	defer sysTokenState.mu.Unlock()

	// Reuse the cached token while it still has comfortable headroom.
	if sysTokenState.token != "" && time.Until(sysTokenState.expiresAt) > systemTokenRenewBefore {
		return sysTokenState.token, nil
	}

	now := time.Now().UTC()
	exp := now.Add(systemTokenTTL)
	sid := uuid.NewString()

	// (a) Seed a system session in the shared cache. The billing handler resolves
	// system_user from this session (verifyAccessTokenAndSession reads
	// session.SystemUser), so the token claim alone is not enough. ExpiresAt is
	// short — IsValid() checks it independently of the Redis key TTL — so the
	// session stops authorizing within systemTokenTTL even though the cache layer
	// keeps the key longer.
	sess := &session.Session{
		SessionID:  sid,
		UserID:     SystemUserID,
		SystemUser: true,
		AccessJTI:  "",
		ExpiresAt:  exp,
		CreatedAt:  now,
		LastUsedAt: now,
		IsRevoked:  false,
	}
	if err := s.cacheClient.SaveSession(sess); err != nil {
		return "", fmt.Errorf("system token: seed session: %w", err)
	}

	// (b) Mint a PASETO access token bound to that sid. user_properties carries
	// the reserved system user id and the system flag; sid links to the cached
	// system session above.
	userProperties := map[string]interface{}{
		"user_id":     SystemUserID,
		"system_user": true,
	}
	tok, _, err := s.creatorToken.NewToken(auth_token.GrantTypeAccess, userProperties, &sid)
	if err != nil {
		return "", fmt.Errorf("system token: mint: %w", err)
	}

	// Renew slightly before the real expiry so callers never get a token within
	// the renew window. The minted token's own exp follows the generator's
	// configured access duration; we cap our renewal clock to systemTokenTTL so
	// the session and token stay aligned.
	sysTokenState.token = tok.GetValue()
	sysTokenState.expiresAt = exp
	return sysTokenState.token, nil
}
