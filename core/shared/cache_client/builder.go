package cache_client

import (
	"regexp"

	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"

	"github.com/gomodule/redigo/redis"
)

// Cache represents a Redis cache.
type Cache struct {
	// redisPool is the Redis connection pool.
	redisPool *redis.Pool
}

// CreatePoolCache creates a new Redis cache instance.
//
// Args:
//
//	redisConfig: A pointer to a config.cacheClient instance.
//
// Returns:
//
//	A pointer to a cacheClient instance.
func CreatePoolCache(config *config.CacheCfg) *Cache {
	applog.Info("Initializing pool cache")
	defer applog.Info("Initializing pool cache - completed")

	url, _ := config.BuildCacheURL()

	applog.Infof("cacheClient protectedMode: %v", config.ProtectedMode)
	applog.Infof("cacheClient PoolCount: %d", config.ConnectionPoolCount)
	applog.Infof("cacheClient host: %s", removePassword(url))

	redisPool, err := NewPool(config)
	if err != nil {
		applog.Fatalf("Failed to create cacheClient pool: %v", err)
	}

	return &Cache{
		redisPool: redisPool,
	}
}

// GetConn returns a Redis connection from the pool.
func (r *Cache) GetConn() redis.Conn {
	conn := r.redisPool.Get()
	return conn
}

// Close closes the Redis connection pool.
func (r *Cache) Close() {
	err := r.redisPool.Close()
	if err != nil {
		return
	}
}

func removePassword(uri string) string {
	// Regular expression to match the password segment in the Redis URI
	re := regexp.MustCompile(`://.*?@`)
	return re.ReplaceAllString(uri, "://")
}
