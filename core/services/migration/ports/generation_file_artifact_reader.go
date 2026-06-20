package ports

import "context"

// GeneratedFile carries the path and UTF-8 source content of one generated
// source file produced by the autonomous generation agent.
type GeneratedFile struct {
	ServiceName string
	Path        string
	Content     string
}

// GenerationFileArtifactReader reads persisted generated source files from
// the generation_file_artifacts collection written by the generation worker.
type GenerationFileArtifactReader interface {
	// ListArtifacts returns generated files for the given migration.
	// If serviceName is non-empty, only that service's files are returned.
	// If serviceName is empty, files for all services are returned.
	ListArtifacts(ctx context.Context, migrationID uint64, serviceName string) ([]GeneratedFile, error)
}
