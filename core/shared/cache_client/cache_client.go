// Package cache_client provides a Redis-backed client for session management,
// token blacklisting, and email-verification caching.
package cache_client

import "time"

type CacheClient struct {
	*SessionCache
	TokenBlacklist *TokenBlacklistCache
	EmailVerify    *EmailVerifyCache
}

func NewCacheClient(redisPool *Cache) *CacheClient {
	return &CacheClient{
		SessionCache:   NewSessionCache(redisPool, 24*time.Hour),
		TokenBlacklist: NewTokenBlacklistCache(redisPool),
		EmailVerify:    NewEmailVerifyCache(redisPool),
	}
}
