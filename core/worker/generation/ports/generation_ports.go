package ports

import (
	"context"

	workerdomain "milton_prism/core/worker/generation/domain"
)

// ServiceSpec bundles everything needed to generate one microservice.
type ServiceSpec struct {
	Name               string
	ErrorPrefix        string
	ProtoContent       string
	BoundarySpec       string
	Incomplete         bool
	IncompleteReason   string
	GeneratorPromptRef string
	// Protocol is the transport the generated service speaks ("grpc" | "http").
	// Orthogonal to OutputProfile (the language). It selects the generator prompt
	// per (profile, protocol) and the transport section injected into the prompt.
	// Empty is treated as "grpc" for backward compatibility.
	Protocol string
	// AuthScheme is the effective authentication scheme the generated service must
	// implement ("jwt"/"none"/…), resolved as override ?? detected. v1 generates
	// "jwt" and "none". Empty is treated as "none".
	AuthScheme string
	// AuthSignatureAlg is the JWT signature algorithm family the generated
	// validation accepts when AuthScheme is "jwt" (HS256/RS256/ES256/EdDSA). Empty
	// for non-JWT or undetermined.
	AuthSignatureAlg string
	// Store is the persistence engine the generated service must target
	// ("mongodb" | "postgres" | "mysql"), resolved as the migration's
	// TargetConfig.database override, or — for Auto (UNSPECIFIED) — the engine
	// detected in the linked analysis summary. Orthogonal to OutputProfile and
	// Protocol. Selects the store section injected into the prompt (Mongo client +
	// repos vs Postgres pool + SQL repos + migrations). Empty is treated as "mongodb"
	// (the original path, unchanged).
	Store string
}

// GenerationPackage is the worker-internal view of the assembled generation specs.
type GenerationPackage struct {
	MigrationID   uint64
	OutputProfile string
	// Protocol is the transport every service in this package speaks
	// ("grpc" | "http"). Read from the migration's TargetConfig
	// inter_service_transport. Empty is treated as "grpc".
	Protocol string
	// Store is the persistence engine every service in this package targets
	// ("mongodb" | "postgres" | "mysql"), resolved by the reader as the
	// TargetConfig.database override or — for Auto (UNSPECIFIED) — the engine
	// detected in the linked analysis summary. Empty is treated as "mongodb".
	Store    string
	Services []ServiceSpec
}

// GenerationPackageReader reads the assembled generation specs for a GENERATING migration.
type GenerationPackageReader interface {
	ReadPackage(ctx context.Context, migrationID uint64) (*GenerationPackage, error)
}

// GenerationStore persists per-service generation records and file artifacts
// for one migration. Artifact methods use (migration_id, service_name, path)
// as the natural key — re-upserting overwrites, never duplicates.
type GenerationStore interface {
	UpsertRecord(ctx context.Context, rec workerdomain.ServiceGenerationRecord) error
	ListRecords(ctx context.Context, migrationID uint64) ([]workerdomain.ServiceGenerationRecord, error)
	// UpsertArtifacts persists the generated file contents for one service.
	// Each artifact is keyed by (migration_id, service_name, path); calling
	// this more than once for the same key overwrites the stored content.
	UpsertArtifacts(ctx context.Context, migrationID uint64, serviceName string, artifacts []workerdomain.FileArtifact) error
	// ListArtifacts returns all persisted file artifacts for one service within
	// a migration, sorted by path.
	ListArtifacts(ctx context.Context, migrationID uint64, serviceName string) ([]workerdomain.FileArtifact, error)
}

// MigrationStateUpdater writes the final migration state once all services have
// been processed.
//   - MarkReady — all services done; migration is safe to inspect, test, and push.
//   - MarkFailed — at least one service failed; per-service detail is in the
//     generation_results collection. A FAILED migration is terminal; retrying
//     requires an explicit FAILED→GENERATING transition (future operation).
type MigrationStateUpdater interface {
	MarkReady(ctx context.Context, migrationID uint64) error
	MarkFailed(ctx context.Context, migrationID uint64) error
}
