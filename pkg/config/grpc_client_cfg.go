package config

import (
	"errors"
	"fmt"
	"milton_prism/pkg/utils/pointers"
)

type GRPCClientsCfg struct {
	IdentityClientConfig        *GrpcClientCfg `toml:"identityServices"`
	RepositoryClientConfig      *GrpcClientCfg `toml:"repositoryServices"`
	AnalysisClientConfig        *GrpcClientCfg `toml:"analysisServices"`
	UserClientConfig            *GrpcClientCfg `toml:"usersServices"`
	CompaniesClientConfig       *GrpcClientCfg `toml:"companiesServices"`
	ProfilesClientConfig        *GrpcClientCfg `toml:"profilesServices"`
	EmailsClientConfig          *GrpcClientCfg `toml:"emailsServices"`
	MasterDataClientConfig      *GrpcClientCfg `toml:"masterDataServices"`
	CommoditiesClientConfig     *GrpcClientCfg `toml:"commoditiesServices"`
	ConsignmentNoteClientConfig *GrpcClientCfg `toml:"consignmentNoteServices"`
	TradersClientConfig         *GrpcClientCfg `toml:"tradersServices"`
	ContractsClientConfig       *GrpcClientCfg `toml:"contractsServices"`
	QuotasClientConfig          *GrpcClientCfg `toml:"quotasServices"`
}

type GrpcClientCfg struct {
	Name              string  `toml:"name"`           // Name of the microservice
	Port              *uint32 `toml:"port"`           // Port to connect to the microservice
	Host              string  `toml:"host"`           // Hostname or IP address of the microservice
	Enabled           bool    `toml:"enabled"`        // Enable or disable the client connection
	EnableHealthCheck *bool   `toml:"healthCheck"`    // Optional: Enable health check
	LazyConnection    *bool   `toml:"lazyConnection"` // Optional: Enable lazy connection
	*ServerOptionCgf          // Connection-specific options for the client
}

// IsHealthCheckEnabled checks if EnableHealthCheck is enabled or unset (defaults to true).
func (m *GrpcClientCfg) IsHealthCheckEnabled() bool {
	return m.EnableHealthCheck == nil || *m.EnableHealthCheck
}

func (m *GrpcClientCfg) IsLazyConnection() bool {
	return m.LazyConnection == nil || *m.LazyConnection
}

// Validate the GrpcClientCfg struct
func (m *GrpcClientCfg) Validate() error {
	if m.Host == "" {
		return errors.New("microservice Host is required")
	}
	if m.Port == nil {
		m.Port = pointers.Uint32Ptr(0)
	}

	if *m.Port == 0 {
		return errors.New("microservice port is required")
	}

	if m.EnableHealthCheck == nil {
		m.EnableHealthCheck = pointers.BoolPtr(true)
	}

	if m.ServerOptionCgf == nil {
		m.ServerOptionCgf = &ServerOptionCgf{MaxRecvMsgSizeMB: 0, MaxSendMsgSizeMB: 0}
	}

	if m.LazyConnection == nil {
		m.LazyConnection = pointers.BoolPtr(false)
	}

	err := m.ServerOptionCgf.Validate()
	if err != nil {
		return fmt.Errorf("invalid transport configuration: %s", err)
	}

	return nil
}

// Endpoint the GrpcClientCfg struct
func (m *GrpcClientCfg) Endpoint() string {
	return fmt.Sprintf("%s:%d", m.Host, *m.Port)
}
