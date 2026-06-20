package config

import (
	"flag"
	"fmt"
	"os"
	"milton_prism/pkg/log"

	"github.com/BurntSushi/toml"
)

var (
	Version   string
	BuildTime string
)

// loadCfg loads a TOML configuration file and decodes it into the provided target struct.
// It accepts a pointer to the target configuration structure (e.g., MicroserviceServerCfg or GatewayServerCfg).
//
// Returns:
//   - An error if there's a failure in reading, decoding, or validating the configuration
func loadCfg(target interface{}) {
	// Define a command-line flag for specifying the configuration file path.
	// Usage:
	//   ./service --config-file-path=/path/to/config.toml
	// If no path is provided, it defaults to "config.toml".
	configFilePath := flag.String("config-file-path", "config.toml", "Path to the configuration TOML file")
	flag.Parse()

	// Check if the configFilePath pointer is nil (shouldn't happen with `flag.String`)
	if configFilePath == nil {
		log.Fatal("configuration file path pointer is nil: failed to initialize `--config-file-path` flag")
	}

	// Verify that the configuration file exists at the specified path
	fileInfo, err := os.Stat(*configFilePath)
	if os.IsNotExist(err) {
		log.Fatalf("configuration file not found: no file exists at the specified path '%s'", *configFilePath)
	} else if err != nil {
		log.Fatalf("error accessing configuration file: could not access '%s' due to error: %v", *configFilePath, err)
	}

	// Additional check: confirm the specified path is a regular file and not a directory
	if fileInfo.IsDir() {
		log.Fatalf("invalid configuration file path: '%s' is a directory, not a file", *configFilePath)
	}

	// Log the configuration file path being used
	log.Infof("Loading configuration from file: %s", *configFilePath)

	// Decode the TOML configuration into the provided target struct
	if _, err := toml.DecodeFile(*configFilePath, target); err != nil {
		log.Fatalf("TOML decoding error: failed to parse configuration file '%s' - syntax or formatting issue may exist. Details: %v", *configFilePath, err)
	}
}

// LoadMicroserviceCfg loads configuration specifically into a GrpcClientCfg struct
// and performs validation on the loaded config.
func LoadMicroserviceCfg(role TokenRole, customConfig SettingConfig) (*MicroserviceServerCfg, error) {
	conf := &MicroserviceServerCfg{
		tokenRole: role,
	}

	if customConfig != nil {
		conf.SettingsConfig = customConfig
	}

	// Use the generic loadCfg function to load into the MicroserviceServerCfg struct
	loadCfg(conf)

	// Decode the service-specific settings_config if provided
	if conf.SettingsConfig != nil && conf.RawSettingsConfig != nil {
		// Convert map to TOML string and decode into custom settings_config
		data, err := toml.Marshal(conf.RawSettingsConfig)
		if err != nil {
			return nil, err
		}

		if _, err := toml.Decode(string(data), conf.SettingsConfig); err != nil {
			return nil, err
		}
	}

	// Verify the loaded configuration
	if err := conf.Validate(); err != nil {
		log.Fatalf("configuration validation error: %v. Please check the configuration file", err)
	}

	// Log basic service information
	log.Info(conf.Server.Name)
	log.Infof("BuildTime: %s", BuildTime)
	log.Infof("Starting service: %s - version %s", conf.Server.Name, Version)

	if conf.Auth != nil {
		err := conf.Auth.Validate(role)
		if err != nil {
			return nil, err
		}

		log.Infof("Algorithm: %#v", conf.Auth.Algorithm)

		if role == TokenRoleValidator {
			log.Infof("Token validator role, schemaType: %#v", *conf.Auth.TokenValidatorConfig.SchemaType)
		} else if role == TokenRoleGenerator {
			log.Infof("Token Generator role, schemaType: %#v", *conf.Auth.TokenGeneratorConfig.SchemaType)
		}
	}

	if conf.Server.ServerOptionCgf != nil {
		log.Infof("MaxRecvMsgSizeMB: %d", conf.Server.MaxRecvMsgSizeMB/(1024*1024))
		log.Infof("MaxSendMsgSizeMB: %d", conf.Server.MaxSendMsgSizeMB/(1024*1024))
	}

	return conf, nil
}

// LoadGatewayCfg loads configuration specifically into a GatewayCfg struct
// and performs validation on the loaded config.
func LoadGatewayCfg() (*GatewayServerCfg, error) {
	var conf GatewayServerCfg

	// Use the generic loadCfg function to load into the GatewayCfg struct
	loadCfg(&conf)

	// Perform any necessary validation on the loaded GatewayServerCfg
	// (Assuming GatewayServerCfg has its own Verify method)
	if err := conf.Validate(); err != nil {
		log.Fatalf("configuration validation error: %v. Please check the configuration file", err)
	}

	// Log basic service information
	log.Info(conf.Server.Name)
	log.Infof("BuildTime: %s", BuildTime)
	log.Infof("Starting service: %s - version %s", conf.Server.Name, Version)

	if conf.Server.ServerOptionCgf != nil {
		log.Infof("MaxRecvMsgSizeMB: %d", conf.Server.MaxRecvMsgSizeMB)
		log.Infof("MaxSendMsgSizeMB: %d", conf.Server.MaxSendMsgSizeMB)
	}

	log.Infof("Cors Enabled: %v", conf.Cors.Enabled)
	if conf.Cors.Enabled {
		log.Infof("Cors AllowedOrigin: %v", conf.Cors.AllowOrigin)
		log.Infof("Cors AllowedMethods: %v", conf.Cors.AllowedMethods)
		log.Infof("Cors AllowedHeaders: %v", conf.Cors.AllowedHeaders)
		log.Infof("Cors ExposeHeaders: %v", conf.Cors.ExposeHeaders)
	}

	return &conf, nil
}

type RequiredFields struct {
	RequireCache              bool
	RequireAuth               bool
	RequireDatabase           bool
	RequireMongoDb            bool
	RequireProfilesSvc        bool
	RequireUsersSvc           bool
	RequireCompaniesSvc       bool
	RequireEmailsSvc          bool
	RequireCommoditiesSvc     bool
	RequireConsignmentNoteSvc bool
	RequireMasterDataSvc      bool
	RequireTradersSvc         bool
}

// SettingConfig defines the interface for service-specific configurations
type SettingConfig interface {
	Validate() error
}

func (c *MicroserviceServerCfg) ValidateWithFlags(flags RequiredFields) error {
	if flags.RequireCache {
		if c.Cache == nil {
			return fmt.Errorf("cache is required")
		}
		if err := c.Cache.Validate(); err != nil {
			return fmt.Errorf("server validation failed: %w", err)
		}
	}

	if flags.RequireAuth {
		if c.Auth == nil {
			return fmt.Errorf("auth is required")
		}
		if err := c.Auth.Validate(c.tokenRole); err != nil {
			return fmt.Errorf("auth validation failed: %w", err)
		}
	}

	if flags.RequireDatabase {
		if c.Database == nil {
			return fmt.Errorf("database is required")
		}
		if err := c.Database.Validate(); err != nil {
			return fmt.Errorf("database validation failed: %w", err)
		}
	}

	if flags.RequireMongoDb {
		if c.Mongo == nil {
			return fmt.Errorf("mongodb is required")
		}
		if err := c.Mongo.Validate(); err != nil {
			return fmt.Errorf("mongodb validation failed: %w", err)
		}
	}

	if flags.RequireProfilesSvc {
		if c.GrpcServices == nil || c.GrpcServices.ProfilesClientConfig == nil {
			return fmt.Errorf("grpc profiles services is required")
		}
		if err := c.GrpcServices.ProfilesClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc profiles services validation failed: %w", err)
		}
	}

	if flags.RequireUsersSvc {
		if c.GrpcServices == nil || c.GrpcServices.UserClientConfig == nil {
			return fmt.Errorf("grpc users services is required")
		}
		if err := c.GrpcServices.UserClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc users services validation failed: %w", err)
		}
	}

	if flags.RequireCompaniesSvc {
		if c.GrpcServices == nil || c.GrpcServices.CompaniesClientConfig == nil {
			return fmt.Errorf("grpc companies services is required")
		}
		if err := c.GrpcServices.CompaniesClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc companies services validation failed: %w", err)
		}
	}

	if flags.RequireEmailsSvc {
		if c.GrpcServices == nil || c.GrpcServices.EmailsClientConfig == nil {
			return fmt.Errorf("grpc emails services is required")
		}
		if err := c.GrpcServices.EmailsClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc emails services validation failed: %w", err)
		}
	}

	if flags.RequireCommoditiesSvc {
		if c.GrpcServices == nil || c.GrpcServices.CommoditiesClientConfig == nil {
			return fmt.Errorf("grpc commodities services is required")
		}
		if err := c.GrpcServices.CommoditiesClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc commodities services validation failed: %w", err)
		}
	}

	if flags.RequireMasterDataSvc {
		if c.GrpcServices == nil || c.GrpcServices.MasterDataClientConfig == nil {
			return fmt.Errorf("grpc master data services is required")
		}
		if err := c.GrpcServices.MasterDataClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc master data services validation failed: %w", err)
		}
	}

	if flags.RequireTradersSvc {
		if c.GrpcServices == nil || c.GrpcServices.TradersClientConfig == nil {
			return fmt.Errorf("grpc traders services is required")
		}
		if err := c.GrpcServices.TradersClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc traders services validation failed: %w", err)
		}
	}

	if flags.RequireConsignmentNoteSvc {
		if c.GrpcServices == nil || c.GrpcServices.ConsignmentNoteClientConfig == nil {
			return fmt.Errorf("grpc consignment note services is required")
		}
		if err := c.GrpcServices.ConsignmentNoteClientConfig.Validate(); err != nil {
			return fmt.Errorf("grpc consignment note services validation failed: %w", err)
		}
	}

	return nil
}

// MicroserviceServerCfg represents the configuration data stored in the TOML file.
type MicroserviceServerCfg struct {
	Server        *GrpcServerCfg  `toml:"microservice"`
	HttpListen    *HttpListenCfg  `toml:"http"`
	MetricsListen *HttpListenCfg  `toml:"metrics"`
	Cache         *CacheCfg       `toml:"cache"`
	Logging       LoggingCfg      `toml:"logging"`
	Auth          *AuthCfg        `toml:"auth"`
	Database      *DatabaseCfg    `toml:"db"`
	Mongo         *MongoDbCfg     `toml:"mongo"`
	GrpcServices  *GRPCClientsCfg `toml:"grpcServices"`
	HTTPServices  *HTTPClientsCfg `toml:"httpServices"`
	tokenRole     TokenRole

	// RawSettingsConfig will hold the raw TOML data for service-specific settings_config
	RawSettingsConfig map[string]interface{} `toml:"settings"`

	// SettingsConfig will be populated after decoding
	SettingsConfig SettingConfig `toml:"-"`
}

// Validate checks if the required fields are present and validates optional fields if they are not nil.
func (c *MicroserviceServerCfg) Validate() error {
	// Validate required fields.
	if c.Server == nil {
		return fmt.Errorf("microservice configuration (GrpcServerCfg) is required")
	}

	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("microservice validation failed: %w", err)
	}

	if c.SettingsConfig != nil {
		if err := c.SettingsConfig.Validate(); err != nil {
			return fmt.Errorf("settings settings_config: %w", err)
		}
	}

	if c.HTTPServices != nil {
		if err := c.HTTPServices.Validate(); err != nil {
			return fmt.Errorf("http services validation failed: %w", err)
		}
	}

	return nil
}

// HttpEnabled checks if the HTTP gateway is properly configured and enabled.
// Returns true only if:
// - HttpListen configuration exists
// - HttpListen is explicitly enabled
// - Required fields are properly set
func (c *MicroserviceServerCfg) HttpEnabled() bool {
	if c.HttpListen == nil {
		return false
	}

	// Additional validation can be added here if needed
	// For example:
	// if c.HttpListen.Port == nil || *c.HttpListen.Port == 0 {
	//     return false
	// }

	return c.HttpListen.Enabled
}

func (c *MicroserviceServerCfg) MetricsEnabled() bool {
	if c.MetricsListen == nil {
		return false
	}

	return c.MetricsListen.Enabled
}

type GatewayServerCfg struct {
	Server       *HttpGateWayServerCfg `toml:"gateway"`
	Cors         *CORSCfg              `toml:"cors"`
	Metrics      MetricsCfg            `toml:"metrics"`
	Logging      LoggingCfg            `toml:"logging"`
	GrpcServices []*GrpcClientCfg      `toml:"grpcServices"`
}

// Validate checks the GatewayServerCfg struct for valid configuration values.
func (c *GatewayServerCfg) Validate() error {
	// Verify GrpcServerCfg
	if c.Server == nil {
		return fmt.Errorf("gateway server configuration is required")
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server validation failed: %w", err)
	}

	// Verify CORSCfg
	if c.Cors != nil {
		if err := c.Cors.Validate(); err != nil {
			return fmt.Errorf("cors validation failed: %w", err)
		}
	} else {
		c.Cors = &CORSCfg{}
		_ = c.Cors.Validate()
	}

	// Check that at least one GrpcClientCfg is not nil
	if !c.hasAtLeastOneMicroservice() {
		return fmt.Errorf("at least one microservice configuration is required")
	}

	// Validate each GrpcClientCfg in the slice
	for i, svc := range c.GrpcServices {
		if svc == nil {
			return fmt.Errorf("grpc service at index %d is nil", i)
		}
		if err := svc.Validate(); err != nil {
			return fmt.Errorf("grpc service at index %d validation failed: %w", i, err)
		}
	}

	// Optionally validate MetricsCfg and LoggingCfg fields here if required
	return nil
}

// hasAtLeastOneMicroservice checks if there's at least one configured gRPC service
func (c *GatewayServerCfg) hasAtLeastOneMicroservice() bool {
	if len(c.GrpcServices) == 0 {
		return false
	}
	for _, svc := range c.GrpcServices {
		if svc != nil {
			return true
		}
	}
	return false
}

// intPtr is a helper function to get a pointer to an int value.
func intPtr(i int) *int {
	return &i
}
