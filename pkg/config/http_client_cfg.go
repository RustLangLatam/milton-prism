package config

import (
	"fmt"
	"milton_prism/pkg/utils/pointers"
	"time"
)

type HTTPClientCfg struct {
	Host    string        `toml:"host"`
	Timeout time.Duration `toml:"timeout"`
	Enabled *bool         `toml:"enabled"`
}

func (c *HTTPClientCfg) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("baseURL is required")
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Enabled == nil {
		c.Enabled = pointers.BoolPtr(true)
	}
	return nil
}

type HTTPClientsCfg struct {
	Contracts *HTTPClientCfg `toml:"contractsServices"`
	Quotas    *HTTPClientCfg `toml:"quotasServices"`
	Arca      *HTTPClientCfg `toml:"arcaServices"`
	Templates *HTTPClientCfg `toml:"templatesServices"`
}

func (c *HTTPClientsCfg) Validate() error {
	if c.Contracts != nil {
		if err := c.Contracts.Validate(); err != nil {
			return fmt.Errorf("contractsServices: %w", err)
		}
	}
	if c.Quotas != nil {
		if err := c.Quotas.Validate(); err != nil {
			return fmt.Errorf("quotasServices: %w", err)
		}
	}
	if c.Arca != nil {
		if err := c.Arca.Validate(); err != nil {
			return fmt.Errorf("arcaServices: %w", err)
		}
	}
	if c.Templates != nil {
		if err := c.Templates.Validate(); err != nil {
			return fmt.Errorf("templatesServices: %w", err)
		}
	}
	return nil
}
