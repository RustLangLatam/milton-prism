package config

import (
	"fmt"
	"milton_prism/pkg/utils/pointers"
)

// GrpcServerCfg struct contains basic server configuration details.
type GrpcServerCfg struct {
	// Name of the server
	Name string `toml:"name"`
	// Port on which the server is running
	Port *uint32 `toml:"port"`
	// Host address of the server
	Host string `toml:"host"`

	// Protocol of the server
	UseSsl bool `toml:"useSsl"`

	Timeout uint64 `toml:"timeout"`

	// PanicLimit is the maximum number of goroutines that can panic
	PanicLimit uint16 `toml:"panicLimit"`

	// ServerOptionCgf contains the transport configuration for the server
	*ServerOptionCgf
}

func (c *GrpcServerCfg) ToClient() *GrpcClientCfg {
	return &GrpcClientCfg{
		Name:              c.Name,
		Port:              c.Port,
		Host:              c.Host,
		Enabled:           true,
		EnableHealthCheck: pointers.BoolPtr(false),
		LazyConnection:    pointers.BoolPtr(false),
		ServerOptionCgf:   c.ServerOptionCgf,
	}
}

// Validate the GrpcServerCfg struct
func (c *GrpcServerCfg) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server name is required")
	}

	if c.Host == "" {
		c.Host = "localhost"
	}

	if c.Port == nil || *c.Port == 0 {
		c.Port = pointers.Uint32Ptr(50051)
	}

	if c.PanicLimit == 0 {
		c.PanicLimit = 5
	}

	if c.ServerOptionCgf != nil {
		err := c.ServerOptionCgf.Validate()
		if err != nil {
			return fmt.Errorf("invalid transport configuration: %s", err)
		}
	} else {
		c.ServerOptionCgf = &ServerOptionCgf{}
		_ = c.ServerOptionCgf.Validate()
	}

	return nil
}

// FullHost returns the full Host string in the format "Host:port".
func (c *GrpcServerCfg) FullHost() string {
	return fmt.Sprintf("%s:%d", c.Host, *c.Port)
}

// FullURL returns the full URL in the format "protocol://Host:port".
func (c *GrpcServerCfg) FullURL() string {
	return fmt.Sprintf("tcp://%s", c.FullHost())
}

const defaultProtocol = "http"

// HttpListenCfg struct contains basic server configuration details.
type HttpListenCfg struct {
	// Port on which the server is running
	Port *uint32 `toml:"port"`
	// Host address of the server
	Host *string `toml:"host"`
	// Protocol of the server
	UseSsl  bool       `toml:"useSsl"`
	ApiKey  *string    `toml:"apiKey"`
	Cors    *CORSCfg   `toml:"cors"`
	Enabled bool       `toml:"enabled"` // Enable or disable the client connection
	Metrics MetricsCfg `toml:"metrics"`
	// ServerOptionCgf contains the transport configuration for the server
	*ServerOptionCgf
}

// Validate the HttpGateWayServerCfg struct
func (s *HttpListenCfg) Validate() error {

	if s.Port == nil || *s.Port == 0 {
		s.Port = pointers.Uint32Ptr(8080)
	}

	if s.Host == nil || *s.Host == "" {
		s.Host = pointers.StringPtr("localhost")
	}

	if *s.Host == "" {
		return fmt.Errorf("server Host is required")
	}

	if s.ApiKey != nil {
		if *s.ApiKey == "" {
			return fmt.Errorf("api key cannot be empty")
		}
	}

	// Verify CORSCfg
	if s.Cors != nil {
		if err := s.Cors.Validate(); err != nil {
			return fmt.Errorf("cors validation failed: %w", err)
		}
	} else {
		s.Cors = &CORSCfg{}
		_ = s.Cors.Validate()
	}

	if s.ServerOptionCgf != nil {
		err := s.ServerOptionCgf.Validate()
		if err != nil {
			return fmt.Errorf("invalid transport configuration: %s", err)
		}
	} else {
		s.ServerOptionCgf = &ServerOptionCgf{}
		_ = s.ServerOptionCgf.Validate()
	}

	return nil
}

// FullHost returns the full Host string in the format "Host:port".
func (s *HttpListenCfg) FullHost() string {
	return fmt.Sprintf("%s:%d", *s.Host, *s.Port)
}

// FullURL returns the full URL in the format "protocol://Host:port".
func (s *HttpListenCfg) FullURL() string {
	protocol := defaultProtocol

	if s != nil && s.UseSsl {
		protocol = "https"
	}

	port := ""
	if s.Port != nil {
		port = fmt.Sprintf(":%d", *s.Port)
	}
	return fmt.Sprintf("%s://%s%s", protocol, *s.Host, port)
}

// HttpGateWayServerCfg struct contains basic server configuration details.
type HttpGateWayServerCfg struct {
	// Name of the server
	Name string `toml:"name"`
	// Port on which the server is running
	Port *uint32 `toml:"port"`
	// Host address of the server
	Host string `toml:"host"`

	// Protocol of the server
	UseSsl bool `toml:"useSsl"`

	Timeout uint64 `toml:"timeout"`

	ApiKey *string `toml:"apiKey"`

	// PanicLimit is the maximum number of goroutines that can panic
	PanicLimit uint16 `toml:"panicLimit"`

	// ServerOptionCgf contains the transport configuration for the server
	*ServerOptionCgf
}

// Validate the HttpGateWayServerCfg struct
func (s *HttpGateWayServerCfg) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("server name is required")
	}

	if s.Host == "" {
		return fmt.Errorf("server Host is required")
	}

	if s.ApiKey != nil {
		if *s.ApiKey == "" {
			return fmt.Errorf("api key cannot be empty")
		}
	}

	if s.PanicLimit == 0 {
		s.PanicLimit = 5
	}

	if s.ServerOptionCgf != nil {
		err := s.ServerOptionCgf.Validate()
		if err != nil {
			return fmt.Errorf("invalid transport configuration: %s", err)
		}
	} else {
		s.ServerOptionCgf = &ServerOptionCgf{}
		_ = s.ServerOptionCgf.Validate()
	}

	return nil
}

// FullHost returns the full Host string in the format "Host:port".
func (s *HttpGateWayServerCfg) FullHost() string {
	return fmt.Sprintf("%s:%d", s.Host, *s.Port)
}

// FullURL returns the full URL in the format "protocol://Host:port".
func (s *HttpGateWayServerCfg) FullURL() string {
	protocol := defaultProtocol

	if s != nil && s.UseSsl {
		protocol = "https"
	}

	port := ""
	if s.Port != nil {
		port = fmt.Sprintf(":%d", *s.Port)
	}
	return fmt.Sprintf("%s://%s%s", protocol, s.Host, port)
}
