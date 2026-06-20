package cache_client

import (
	"fmt"
	paniccontrol "milton_prism/core/shared/utils"
	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"
	"time"

	"github.com/gomodule/redigo/redis"
)

// NewPool initializes a Redis connection pool with configuration settings.
func NewPool(config *config.CacheCfg) (*redis.Pool, error) {
	pool := &redis.Pool{
		MaxIdle:     int(config.MaxIdle),
		MaxActive:   int(config.ConnectionPoolCount),
		IdleTimeout: time.Duration(config.IdleTimeoutInSec) * time.Second,
		Dial: func() (redis.Conn, error) {
			rawUrl, err := config.BuildCacheURL()

			if err != nil {
				return nil, err
			}

			c, err := redis.DialURL(rawUrl,
				redis.DialReadTimeout(time.Duration(config.ConnectionTimeoutInSec)*time.Second),
				redis.DialWriteTimeout(time.Duration(config.ConnectionTimeoutInSec)*time.Second))
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			// Test connection with PING whenever a connection is borrowed
			_, err := c.Do("PING")
			return err
		},
	}

	// Test the connection immediately after creating the pool
	if err := testRedisConnection(pool); err != nil {
		return nil, fmt.Errorf("connection failed: %v", err)
	}

	return pool, nil
}

// testRedisConnection attempts to get a connection from the pool and PING the Redis server.
func testRedisConnection(pool *redis.Pool) error {
	conn := pool.Get()
	defer func(conn redis.Conn) {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}(conn)

	_, err := conn.Do("PING")
	return err
}

// Redis Set Operations

// SIsMember checks if a member exists in a Redis set for a given key.
func SIsMember(pool *redis.Pool, key, member string) (bool, error) {
	conn := pool.Get()
	defer func(conn redis.Conn) {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}(conn)

	memberBytes := []byte(member)
	isMember, err := redis.Bool(conn.Do("SISMEMBER", key, memberBytes))
	if err != nil {
		return false, err
	}
	return isMember, nil
}

// SAddEx adds a member to a Redis set with an optional expiration time.
//   - key: The Redis set key.
//   - member: The member to add to the set.
//   - expiration: Optional expiration time in seconds for the set.
//
// If expiration is provided, the function will set the TTL on the set after adding the member.
func SAddEx(pool *redis.Pool, key, member string, expiration *uint64) error {
	conn := pool.Get()
	defer func(conn redis.Conn) {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}(conn)

	// Convert member to bytes
	memberBytes := []byte(member)

	// Add the member to the set
	_, err := conn.Do("SADD", key, memberBytes)
	if err != nil {
		return err
	}

	// If expiration is set, apply it to the set
	if expiration != nil {
		_, err := conn.Do("EXPIRE", key, *expiration)
		if err != nil {
			return err
		}
	}

	return nil
}
