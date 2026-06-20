package config

import (
	"errors"
	"time"
)

// MongoDbCfg holds all the parameters for configuring MongoDB client
type MongoDbCfg struct {
	URI                    string        `toml:"uri"`                      // MongoDB URI
	Database               string        `toml:"database"`                 // Database to connect
	ConnectTimeout         time.Duration `toml:"connect_timeout"`          // Connection timeout
	SocketTimeout          time.Duration `toml:"socket_timeout"`           // Timeout for read/write operations
	MaxPoolSize            uint64        `toml:"max_pool_size"`            // Max connections in the pool
	MinPoolSize            uint64        `toml:"min_pool_size"`            // Minimum connections to maintain
	HeartbeatInterval      time.Duration `toml:"heartbeat_interval"`       // Heartbeat frequency
	ServerSelectionTimeout time.Duration `toml:"server_selection_timeout"` // Timeout for server selection
	RetryWrites            bool          `toml:"retry_writes"`             // Enable retryable writes
	RetryReads             bool          `toml:"retry_reads"`              // Enable retryable reads
	Monitor                bool          `toml:"monitor"`                  // Enable basic logging
}

// Validate validates and sets default values for MongoDbCfg
func (cfg *MongoDbCfg) Validate() error {
	if cfg.URI == "" {
		return errors.New("MongoDB URI must not be empty")
	}

	if cfg.Database == "" {
		return errors.New("MongoDB database name must not be empty")
	}

	// Set default durations if not provided
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.SocketTimeout == 0 {
		cfg.SocketTimeout = 5 * time.Second
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.ServerSelectionTimeout == 0 {
		cfg.ServerSelectionTimeout = 5 * time.Second
	}

	// Set default pool sizes if not provided
	if cfg.MaxPoolSize == 0 {
		cfg.MaxPoolSize = 100
	}
	if cfg.MinPoolSize == 0 {
		cfg.MinPoolSize = 5
	}

	// RetryWrites and RetryReads are already bool types and default to false if not set.
	// Monitor defaults to false unless explicitly enabled.

	return nil
}
