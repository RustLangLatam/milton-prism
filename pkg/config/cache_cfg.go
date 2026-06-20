package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CacheCfg represents the cache configuration.
// It holds the necessary fields to establish a connection to a Redis cache.
type CacheCfg struct {
	// Host is the hostname or IP address of the Redis cache.
	Host string `toml:"host"`
	// Port is the port number of the Redis cache.
	Port string `toml:"port"`
	// ProtectedMode indicates whether the Redis cache is in protected mode.
	ProtectedMode bool `toml:"protectedMode"`
	// RequirePass is the password required to connect to the Redis cache.
	RequirePass string `toml:"requirePass"`
	// ConnectionPoolCount is the maximum number of connections in the pool.
	ConnectionPoolCount uint64 `toml:"connectionPoolCount"`
	// IdleTimeoutInSec is the idle timeout in seconds for connections in the pool.
	IdleTimeoutInSec uint64 `toml:"idleTimeoutInSec"`
	// ConnectionTimeoutInSec is the connection timeout in seconds for connections in the pool.
	ConnectionTimeoutInSec uint64 `toml:"connectionTimeoutInSec"`
	// MaxIdle is the maximum number of idle connections in the pool.
	MaxIdle uint64 `toml:"maxIdle"`
}

// setDefaults sets default values for fields with no value.
// It is called by the Validate method to ensure that all fields have a value.
func (c *CacheCfg) setDefaults() {
	// Set default port to 6379 if not provided
	if c.Port == "" {
		c.Port = "6379" // Default Redis port
	}
	// Set default connection pool count to 10 if not provided
	if c.ConnectionPoolCount == 0 {
		c.ConnectionPoolCount = 10
	}
	// Set default idle timeout to 300 seconds if not provided
	if c.IdleTimeoutInSec == 0 {
		c.IdleTimeoutInSec = 300
	}
	// Set default connection timeout to 10 seconds if not provided
	if c.ConnectionTimeoutInSec == 0 {
		c.ConnectionTimeoutInSec = 10
	}
	// Set default max idle connections to 5 if not provided
	if c.MaxIdle == 0 {
		c.MaxIdle = 5
	}
}

// Validate checks if the CacheCfg fields are valid.
// It sets default values for fields with no value and checks for required fields.
func (c *CacheCfg) Validate() error {
	// Set default values for fields with no value
	c.setDefaults()

	// Check if Host is required
	if c.Host == "" {
		return errors.New("cache Host is required")
	}

	// Build the Redis URL from the configuration
	_, err := c.BuildCacheURL()
	if err != nil {
		return err
	}

	return nil
}

// BuildCacheURL constructs the Redis URL from the configuration.
// It checks for password requirements in protected mode, prepends the scheme if missing,
// sets the correct port, and adds the password if provided.
func (c *CacheCfg) BuildCacheURL() (string, error) {
	// Check for password in protected mode
	if c.ProtectedMode && c.RequirePass == "" {
		return "", errors.New("password is required when protected mode is enabled")
	}

	// Prepend scheme if missing
	scheme := "redis://"
	if strings.Contains(c.Host, "://") {
		scheme = ""
	}

	// Parse the URL to add scheme and check validity
	parsedURL, err := url.Parse(scheme + c.Host)
	if err != nil {
		return "", err
	}

	// Set the correct port in the URL
	parsedURL.Host = fmt.Sprintf("%s:%s", parsedURL.Hostname(), c.Port)

	// Add password if provided in protected mode.
	if c.ProtectedMode {
		// Set the password in the URL
		parsedURL.User = url.UserPassword(parsedURL.User.Username(), c.RequirePass)
	}

	// Return the constructed Redis URL
	return parsedURL.String(), nil
}
