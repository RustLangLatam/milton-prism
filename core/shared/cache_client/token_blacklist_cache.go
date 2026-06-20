package cache_client

import (
	"milton_prism/pkg/log"
)

const blackListCachePrefix = "milton_prism_token_bl_cf"

type TokenBlacklistCache struct {
	*Cache
}

func NewTokenBlacklistCache(pool *Cache) *TokenBlacklistCache {
	return &TokenBlacklistCache{
		Cache: pool,
	}
}

// AddTokenToBlacklist adds a token to the Cache blacklist set with an optional TTL.
func (r *TokenBlacklistCache) AddTokenToBlacklist(token string, ttl *uint64) error {
	// Use the correct parameter order for SAddEx: (pool, key, member, expiration)
	if err := SAddEx(r.redisPool, blackListCachePrefix, token, ttl); err != nil {
		log.Errorf("Failed to add token to cacheClient blacklist: %v", err)
		return err
	}
	return nil
}

// IsTokenBlacklisted checks if a token is in the Cache blacklist set.
func (r *TokenBlacklistCache) IsTokenBlacklisted(token string) (bool, error) {
	isMember, err := SIsMember(r.redisPool, blackListCachePrefix, token)
	if err != nil {
		log.Errorf("Failed to check token in cacheClient blacklist: %v", err)
		return false, err
	}
	return isMember, nil
}
