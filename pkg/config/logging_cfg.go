package config

// LoggingCfg contains the configuration for logging, including core and file logging.
type LoggingCfg struct {
	// Core logging configuration
	Core *CoreCfg
	// File logging configuration
	File *FileCfg
	// LoggingCfg level (e.g., INFO, DEBUG)
	Level string
}

// CoreCfg represents the core logging configuration.
type CoreCfg struct {
	// Enable or disable colorized output in the core logs
	Colorize bool
	// LoggingCfg level for core output
	Level string
	// Enable exception handling in core logs
	HandleExceptions bool
}

// FileCfg represents the configuration for file-based logging.
type FileCfg struct {
	// LoggingCfg level for file output
	Level string
	// Handle exceptions in file logs
	HandleExceptions bool
	// Enable pretty-print formatting for logs
	PrettyPrint bool
	// Include timestamp in the logs
	Timestamp bool
	// Name of the log file
	Filename string
	// Maximum size (in MB) for log files
	Maxsize int
	// Maximum number of log files to retain
	MaxFiles int
}
