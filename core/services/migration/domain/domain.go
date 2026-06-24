// Package domain contains the migration service's domain types and errors.
// All types are aliases of the generated proto types — no parallel structs.
package domain

import (
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

type (
	Migration             = migrationv1.Migration
	MigrationsFilter      = migrationv1.MigrationsFilter
	MigrationState        = migrationv1.MigrationState
	TargetConfig          = migrationv1.TargetConfig
	RestructurePlan       = migrationv1.RestructurePlan
	ProposedService       = migrationv1.ProposedService
	MigrationOutput       = migrationv1.MigrationOutput
	TargetLanguage        = migrationv1.TargetLanguage
	TargetDatabase        = migrationv1.TargetDatabase
	Transport             = migrationv1.Transport
	OutputTarget          = migrationv1.OutputTarget
	GenerationPackage     = migrationv1.GenerationPackage
	ServiceGenerationSpec = migrationv1.ServiceGenerationSpec
	MigrabilityAssessment = commonv1.MigrabilityAssessment
	RestructuringRoadmap  = migrationv1.RestructuringRoadmap
	RoadmapDiagnosis      = migrationv1.RoadmapDiagnosis
	StructuralProblem     = migrationv1.StructuralProblem
	ActionItem            = migrationv1.ActionItem
	RoadmapEnrichment     = migrationv1.RoadmapEnrichment
	EnrichedStep          = migrationv1.EnrichedStep
	ServiceBlueprint      = migrationv1.ServiceBlueprint
	BlueprintService      = migrationv1.BlueprintService
)

const (
	MigrabilityVerdictMigrable    = "MIGRABLE"
	MigrabilityVerdictPartial     = "PARTIAL"
	MigrabilityVerdictNotMigrable = "NOT_MIGRABLE"
	// MigrabilityVerdictIncompleteNoStructuralData is an honest degrade, not a
	// migrability judgement: deep structural analysis produced no graph or module
	// cards, so there are no score signals to build structural problems or an
	// action plan from. A roadmap therefore has no current-verdict content to
	// carry; any roadmap present on such a migration is a stale blob from an
	// earlier verdict generation and MUST NOT be served.
	MigrabilityVerdictIncompleteNoStructuralData = "INCOMPLETE_NO_STRUCTURAL_DATA"
)

// ServiceArtifact holds the raw text artifacts persisted by the decomposition
// engine for a single service. It has no proto equivalent — it is an internal
// read model for the design_artifacts collection.
type ServiceArtifact struct {
	ServiceName      string
	ProtoContent     string
	BoundarySpec     string
	Incomplete       bool
	IncompleteReason string
}

const (
	MigrationStateUnspecified        = migrationv1.MigrationState_MIGRATION_STATE_UNSPECIFIED
	MigrationStatePending            = migrationv1.MigrationState_MIGRATION_STATE_PENDING
	MigrationStateAnalyzing          = migrationv1.MigrationState_MIGRATION_STATE_ANALYZING
	MigrationStateDesigning          = migrationv1.MigrationState_MIGRATION_STATE_DESIGNING
	MigrationStateAwaitingApproval   = migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL
	MigrationStateGenerating         = migrationv1.MigrationState_MIGRATION_STATE_GENERATING
	MigrationStateTesting            = migrationv1.MigrationState_MIGRATION_STATE_TESTING
	MigrationStateReady              = migrationv1.MigrationState_MIGRATION_STATE_READY
	MigrationStatePushed             = migrationv1.MigrationState_MIGRATION_STATE_PUSHED
	MigrationStateFailed             = migrationv1.MigrationState_MIGRATION_STATE_FAILED
	MigrationStateCancelled          = migrationv1.MigrationState_MIGRATION_STATE_CANCELLED
	MigrationStateRestructuringReady = migrationv1.MigrationState_MIGRATION_STATE_RESTRUCTURING_READY
	TargetLanguageUnspecified        = migrationv1.TargetLanguage_TARGET_LANGUAGE_UNSPECIFIED
	TargetLanguageGo                 = migrationv1.TargetLanguage_TARGET_LANGUAGE_GO
	TargetLanguageRust               = migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST
	TargetLanguagePython             = migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON
	TargetLanguageNode               = migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE
	TargetDatabaseUnspecified        = migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED
	TargetDatabaseMongoDB            = migrationv1.TargetDatabase_TARGET_DATABASE_MONGODB
	TargetDatabasePostgres           = migrationv1.TargetDatabase_TARGET_DATABASE_POSTGRES
	TargetDatabaseMariaDB            = migrationv1.TargetDatabase_TARGET_DATABASE_MARIADB
	TransportUnspecified             = migrationv1.Transport_TRANSPORT_UNSPECIFIED
	TransportGRPC                    = migrationv1.Transport_TRANSPORT_GRPC
	TransportHTTP                    = migrationv1.Transport_TRANSPORT_HTTP
	OutputTargetUnspecified          = migrationv1.OutputTarget_OUTPUT_TARGET_UNSPECIFIED
	OutputTargetNewBranch            = migrationv1.OutputTarget_OUTPUT_TARGET_NEW_BRANCH
	OutputTargetNewRepository        = migrationv1.OutputTarget_OUTPUT_TARGET_NEW_REPOSITORY
	TargetTopologyUnspecified        = migrationv1.TargetTopology_TARGET_TOPOLOGY_UNSPECIFIED
	TargetTopologyMicroservices      = migrationv1.TargetTopology_TARGET_TOPOLOGY_MICROSERVICES
	TargetTopologyMonolith           = migrationv1.TargetTopology_TARGET_TOPOLOGY_MONOLITH
)

// generableTargetLanguages is the set of target languages that have a real code
// generator profile (profile doc + generator prompt + reference monorepo). It is
// the single source of truth for the CreateMigration guard and must stay in
// lockstep with outputProfileLabel/generatorPromptRef in the application layer.
// Node (TypeScript + gRPC) and Rust (Tonic + gRPC) are filled profiles: profile
// doc + generator prompt + assembler skeleton/rename, each certified by a real
// containerised run. Any future enum value without a real generator profile must
// be left out of this map so a migration targeting it is rejected rather than
// silently emitting Go.
var generableTargetLanguages = map[TargetLanguage]struct{}{
	TargetLanguageGo:     {},
	TargetLanguagePython: {},
	TargetLanguageNode:   {},
	TargetLanguageRust:   {},
}

// IsGenerableLanguage reports whether lang has a code generator profile today.
func IsGenerableLanguage(lang TargetLanguage) bool {
	_, ok := generableTargetLanguages[lang]
	return ok
}

// supportedProtocolByLanguage is the single source of truth for the (language,
// transport) generation matrix — the PROTOCOL axis, orthogonal to language and
// topology. A cell present here means the generator can emit that language over
// that transport (profile doc + generator prompt + assembler behaviour exist and
// are certified). It MUST stay in lockstep with generatorPromptRef /
// promptProfileBindings in the application + worker layers (each transport that
// is enabled here needs a prompt selected by (profile, transport)).
//
// State: every generable language supports BOTH gRPC and HTTP — the HTTP matrix
// is complete (Go + HTTP, Python + HTTP FastAPI-native, Node + HTTP Fastify-native
// and Rust + HTTP axum-native), each a certified cell (profile doc + generator
// prompt + assembler behaviour + real containerised run). Any new cell must be
// added here AND given a prompt + assembler handling in lockstep.
var supportedProtocolByLanguage = map[TargetLanguage]map[Transport]struct{}{
	TargetLanguageGo: {
		TransportGRPC: {},
		TransportHTTP: {},
	},
	TargetLanguagePython: {
		TransportGRPC: {},
		TransportHTTP: {},
	},
	TargetLanguageNode: {
		TransportGRPC: {},
		TransportHTTP: {},
	},
	TargetLanguageRust: {
		TransportGRPC: {},
		TransportHTTP: {},
	},
}

// IsGenerableProtocol reports whether the generator can emit lang over transport
// today. The caller is expected to canonicalise TRANSPORT_UNSPECIFIED to
// TRANSPORT_GRPC before calling (mirror of how topology is canonicalised). A
// non-generable language always returns false regardless of transport.
func IsGenerableProtocol(lang TargetLanguage, transport Transport) bool {
	transports, ok := supportedProtocolByLanguage[lang]
	if !ok {
		return false
	}
	_, ok = transports[transport]
	return ok
}

// generableDatabaseByLanguage is the single source of truth for the (language,
// database) persistence matrix — the DATABASE axis, orthogonal to protocol and
// topology. A cell present here means the generator can emit that language's
// persistence layer against that database engine (profile doc + generator prompt
// + worker storeSection + assembler config behaviour exist and are certified).
//
// v1 state: ALL FOUR generable languages have a real SQL persistence profile —
// the DB axis is complete (4 languages × {MongoDB, PostgreSQL, MySQL/MariaDB}).
// The certified SQL engines for Go are PostgreSQL AND MariaDB (= the
// MySQL/MariaDB family, slot 3) — both via the SAME GORM models/repos (gorm.io/gorm
// with gorm.io/driver/postgres or gorm.io/driver/mysql selected by the store; the
// GORM models live in infrastructure/repositories and map to/from the domain types,
// schema applied by AutoMigrate). Python mirrors this with SQLAlchemy 2.0 (async):
// one set of DeclarativeBase models/repos in infrastructure/repositories serves
// PostgreSQL (asyncpg) AND MySQL/MariaDB (aiomysql), the async engine/URL selected
// by the store, schema applied by create_all. Node mirrors this with Prisma: ONE
// schema.prisma (datasource provider postgresql|mysql + DATABASE_URL selected by
// store) plus the @prisma/client live in infrastructure/repositories, repos
// implement the same ports mapping Prisma model↔domain, schema applied by Prisma
// Migrate / db push. Rust mirrors this with SeaORM (async, sqlx-backed): one set of
// SeaORM entities/repos in infrastructure/repositories serves PostgreSQL
// (sqlx-postgres) AND MySQL/MariaDB (sqlx-mysql), the driver/feature + DATABASE_URL
// selected by the store, schema applied by sea-orm-migration. Every generable
// language keeps MongoDB (the original path, unchanged — Node+Mongo stays on the
// native `mongodb` driver NOT Prisma, Rust+Mongo stays on the native `mongodb`
// crate NOT SeaORM). Any new cell (a new SQL engine, or SQL for another language)
// must be added here AND given a storeSection prompt + assembler config + a
// certified run.
var generableDatabaseByLanguage = map[TargetLanguage]map[TargetDatabase]struct{}{
	TargetLanguageGo: {
		TargetDatabaseMongoDB:  {},
		TargetDatabasePostgres: {},
		TargetDatabaseMariaDB:  {}, // MySQL/MariaDB family (wire-compatible single target)
	},
	TargetLanguagePython: {
		TargetDatabaseMongoDB:  {},
		TargetDatabasePostgres: {}, // SQLAlchemy 2.0 async + asyncpg
		TargetDatabaseMariaDB:  {}, // SQLAlchemy 2.0 async + aiomysql (MySQL/MariaDB family)
	},
	TargetLanguageNode: {
		TargetDatabaseMongoDB:  {},
		TargetDatabasePostgres: {}, // Prisma (provider postgresql) + @prisma/client
		TargetDatabaseMariaDB:  {}, // Prisma (provider mysql), MySQL/MariaDB family
	},
	TargetLanguageRust: {
		TargetDatabaseMongoDB:  {},
		TargetDatabasePostgres: {}, // SeaORM (sqlx-postgres) + sea-orm-migration
		TargetDatabaseMariaDB:  {}, // SeaORM (sqlx-mysql), MySQL/MariaDB family
	},
}

// IsGenerableDatabase reports whether the generator can emit lang's persistence
// layer for the database engine db today. The caller is expected to canonicalise
// TARGET_DATABASE_UNSPECIFIED to a concrete engine (Auto → database_detection,
// else MONGODB) before calling, mirroring how topology/transport are
// canonicalised. A non-generable language always returns false regardless of db.
func IsGenerableDatabase(lang TargetLanguage, db TargetDatabase) bool {
	databases, ok := generableDatabaseByLanguage[lang]
	if !ok {
		return false
	}
	_, ok = databases[db]
	return ok
}
