package config

import (
	"errors"
	"fmt"
)

// Default values for PoolCfg settings
const (
	DefaultMaxOpenConns    = 10
	DefaultMaxIdleConns    = 5
	DefaultConnMaxIdleTime = 300  // 5 minutes
	DefaultConnMaxLifetime = 1800 // 30 minutes
)

// DefaultTimeout is the default timeout in seconds if not set in the settings_config
const DefaultTimeout = 10 // seconds

// DefaultLevelLogger is the default logger level for the database
const DefaultLevelLogger = "silent"

// PoolCfg represents the connection pool configuration.
type PoolCfg struct {
	// Maximum number of open connections to the server
	MaxOpenConns *int `toml:"maxOpenConns"`
	// Maximum number of idle connections to the server
	MaxIdleConns *int `toml:"maxIdleConns"`
	// Maximum idle time for a connection (in seconds)
	ConnMaxIdleTime *int `toml:"connMaxIdleTime"`
	// Maximum lifetime of a connection (in seconds)
	ConnMaxLifetime *int `toml:"connMaxLifetime"`
}

// Validate checks the PoolCfg configuration for valid settings and applies defaults.
func (p *PoolCfg) Validate() error {
	if p.MaxOpenConns == nil {
		p.MaxOpenConns = intPtr(DefaultMaxOpenConns)
	} else if *p.MaxOpenConns < 0 {
		return errors.New("MaxOpenConns must be a non-negative integer")
	}

	if p.MaxIdleConns == nil {
		p.MaxIdleConns = intPtr(DefaultMaxIdleConns)
	} else if *p.MaxIdleConns < 0 {
		return errors.New("MaxIdleConns must be a non-negative integer")
	}

	if p.ConnMaxIdleTime == nil {
		p.ConnMaxIdleTime = intPtr(DefaultConnMaxIdleTime)
	} else if *p.ConnMaxIdleTime < 0 {
		return errors.New("ConnMaxIdleTime must be a non-negative integer")
	}

	if p.ConnMaxLifetime == nil {
		p.ConnMaxLifetime = intPtr(DefaultConnMaxLifetime)
	} else if *p.ConnMaxLifetime < 0 {
		return errors.New("ConnMaxLifetime must be a non-negative integer")
	}

	return nil
}

// DatabaseCfg holds database configuration, specifically MySQL.
type DatabaseCfg struct {
	// db server Host
	Host string `toml:"host"`
	// db server port
	Port int `toml:"port"`
	// Username for db authentication
	User string `toml:"user"`
	// Password for db authentication
	Pass string `toml:"pass"`
	// Name of the database
	DBName string `toml:"dbname"`
	// Timeout for db connection
	Timeout uint64 `toml:"connectionTimeout"`
	// Level of logging for database. Default: silent. Accept values: silent, error, warn, info
	LevelLogger string `toml:"levelLogger"`
	// db connection pool configuration
	Pool *PoolCfg `toml:"pool"`
}

// Validate checks the DatabaseCfg configuration for valid settings.
func (db *DatabaseCfg) Validate() error {
	// Host must be set
	if db.Host == "" {
		return errors.New("Host must be specified")
	}

	// Port should be a positive integer
	if db.Port <= 0 || db.Port > 65535 {
		return errors.New("port must be a positive integer between 1 and 65535")
	}

	// User and Pass are required fields
	if db.User == "" {
		return errors.New("user must be specified")
	}
	if db.Pass == "" {
		return errors.New("password must be specified")
	}

	// DBName must be set
	if db.DBName == "" {
		return errors.New("DBName must be specified")
	}

	// Set default timeout if not specified
	if db.Timeout == 0 {
		db.Timeout = DefaultTimeout
	}
	//Set default logger if not specified
	if db.LevelLogger == "" {
		db.LevelLogger = DefaultLevelLogger
	}

	// Verify PoolCfg configuration if it exists
	if db.Pool != nil {
		if err := db.Pool.Validate(); err != nil {
			return fmt.Errorf("invalid pool configuration: %s", err)
		}
	} else {
		db.Pool = &PoolCfg{}
		if err := db.Pool.Validate(); err != nil {
			return fmt.Errorf("invalid pool configuration: %s", err)
		}
	}

	return nil
}
