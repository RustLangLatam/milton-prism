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
	// HTTPFramework is the HTTP web framework the generated router/handlers are built
	// on ("net_http" | "gin" | …), derived from the migration's
	// TargetConfig.http_framework (canonicalised). Orthogonal to OutputProfile/Protocol.
	// Only meaningful when Protocol == "http"; empty/"net_http" keeps the language's
	// native HTTP default. Selects the HTTP-framework section injected into the prompt.
	HTTPFramework string
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
	// repos vs a GORM persistence layer + AutoMigrate, driver chosen by store).
	// Empty is treated as "mongodb" (the original path, unchanged).
	Store string
	// SourceToPort is the bounded, per-service ORIGINAL source captured by the
	// decomposition stage (persisted in the design_artifact as source_files). It
	// carries the domain files (the business logic to TRANSLATE faithfully into the
	// target language) and the input test files (the behaviour oracle). The reader
	// populates it from the design_artifact; the agent invoker injects it into the
	// combined prompt so the generator PORTS the logic instead of inventing CRUD.
	// Empty when the decomposition pre-dates source capture (degrades to the old
	// contract-only behaviour — no regression).
	SourceToPort []SourceFile
}

// SourceFile is one bounded original source file captured for a service during
// decomposition (stage 7b). It mirrors the decomposition domain SourceFile but is
// redeclared here so the generation ports stay decoupled from the decomposition
// domain. Role is "domain" (a service-owned source file whose logic must be
// ported) or "test" (an input test that exercises the service — the behaviour
// oracle to port or to derive characterization tests from).
type SourceFile struct {
	// Path is the original workspace-relative path (e.g. "conduit/articles/views.py").
	Path string
	// Lang is the inferred SOURCE language label (e.g. "python", "php").
	Lang string
	// Role is "domain" or "test".
	Role string
	// Content is the full file text (bounded by the decomposition capture cap).
	Content string
	// Symbols are the classes and functions declared in the module (from the
	// analysis card); empty when no card was available.
	Symbols []string
}

// GenerationPackage is the worker-internal view of the assembled generation specs.
type GenerationPackage struct {
	MigrationID   uint64
	OutputProfile string
	// Protocol is the transport every service in this package speaks
	// ("grpc" | "http"). Read from the migration's TargetConfig
	// inter_service_transport. Empty is treated as "grpc".
	Protocol string
	// HTTPFramework is the HTTP web framework every service in this package is built
	// on ("net_http" | "gin" | …), derived from the migration's
	// TargetConfig.http_framework (canonicalised). Only meaningful when Protocol ==
	// "http"; empty/"net_http" keeps the language's native HTTP default.
	HTTPFramework string
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
