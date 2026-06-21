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
	TargetDatabaseUnspecified        = migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED
	TargetDatabaseMongoDB            = migrationv1.TargetDatabase_TARGET_DATABASE_MONGODB
	TransportUnspecified             = migrationv1.Transport_TRANSPORT_UNSPECIFIED
	TransportGRPC                    = migrationv1.Transport_TRANSPORT_GRPC
	OutputTargetUnspecified          = migrationv1.OutputTarget_OUTPUT_TARGET_UNSPECIFIED
	OutputTargetNewBranch            = migrationv1.OutputTarget_OUTPUT_TARGET_NEW_BRANCH
	OutputTargetNewRepository        = migrationv1.OutputTarget_OUTPUT_TARGET_NEW_REPOSITORY
)
