// Package domain contains the analysis service's domain types and errors.
// All types are aliases of the generated proto types — no parallel structs.
package domain

import (
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

type (
	AnalysisSummary       = analysisv1.AnalysisSummary
	Technology            = analysisv1.Technology
	Vulnerability         = analysisv1.Vulnerability
	DependencyEdge        = analysisv1.DependencyEdge
	AnalysisState         = analysisv1.AnalysisState
	TechnologyStatus      = analysisv1.TechnologyStatus
	Severity              = analysisv1.Severity
	ModuleCard            = analysisv1.ModuleCard
	RouteInfo             = analysisv1.RouteInfo
	BlueprintInfo         = analysisv1.BlueprintInfo
	ModuleClassification  = analysisv1.ModuleClassification
	SharedStateHub        = analysisv1.SharedStateHub
	UnreachableModule     = analysisv1.UnreachableModule
	MigrabilityScore      = commonv1.MigrabilityScore
	MigrabilityAssessment = commonv1.MigrabilityAssessment
	ScoreSignal           = commonv1.ScoreSignal
	DatabaseDetection     = analysisv1.DatabaseDetection
	DatabaseEngine        = analysisv1.DatabaseEngine
	ArchitecturalPattern  = analysisv1.ArchitecturalPattern
	APKind                = analysisv1.ArchitecturalPatternKind
	IntakeAssessment      = analysisv1.IntakeAssessment
	CodebaseKind          = analysisv1.CodebaseKind
	SecurityFinding       = analysisv1.SecurityFinding
	SecurityFindingType   = analysisv1.SecurityFindingType
	SecuritySeverity      = analysisv1.SecuritySeverity
	AuthScheme            = analysisv1.AuthScheme
	AuthSchemeDetection   = analysisv1.AuthSchemeDetection
)

const (
	DatabaseEngineUnspecified = analysisv1.DatabaseEngine_DATABASE_ENGINE_UNSPECIFIED
	DatabaseEnginePostgreSQL  = analysisv1.DatabaseEngine_DATABASE_ENGINE_POSTGRESQL
	DatabaseEngineMySQL       = analysisv1.DatabaseEngine_DATABASE_ENGINE_MYSQL
	DatabaseEngineMongoDB     = analysisv1.DatabaseEngine_DATABASE_ENGINE_MONGODB
	DatabaseEngineSQLite      = analysisv1.DatabaseEngine_DATABASE_ENGINE_SQLITE
	DatabaseEngineSQLServer   = analysisv1.DatabaseEngine_DATABASE_ENGINE_SQLSERVER
	DatabaseEngineOracle      = analysisv1.DatabaseEngine_DATABASE_ENGINE_ORACLE
	DatabaseEngineRedis       = analysisv1.DatabaseEngine_DATABASE_ENGINE_REDIS

	APKindUnspecified     = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_UNSPECIFIED
	APKindClean           = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_CLEAN
	APKindHexagonal       = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_HEXAGONAL
	APKindLayered         = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_LAYERED
	APKindMVC             = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_MVC
	APKindModularMonolith = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_MODULAR_MONOLITH
	APKindSpaghetti       = analysisv1.ArchitecturalPatternKind_ARCHITECTURAL_PATTERN_KIND_SPAGHETTI

	CodebaseKindUnspecified = analysisv1.CodebaseKind_CODEBASE_KIND_UNSPECIFIED
	CodebaseKindBackend     = analysisv1.CodebaseKind_CODEBASE_KIND_BACKEND
	CodebaseKindFrontend    = analysisv1.CodebaseKind_CODEBASE_KIND_FRONTEND
	CodebaseKindLibrary     = analysisv1.CodebaseKind_CODEBASE_KIND_LIBRARY
	CodebaseKindCLI         = analysisv1.CodebaseKind_CODEBASE_KIND_CLI
	CodebaseKindMobile      = analysisv1.CodebaseKind_CODEBASE_KIND_MOBILE

	SecurityFindingTypeUnspecified     = analysisv1.SecurityFindingType_SECURITY_FINDING_TYPE_UNSPECIFIED
	SecurityFindingTypeHardcodedSecret = analysisv1.SecurityFindingType_SECURITY_FINDING_TYPE_HARDCODED_SECRET

	SecuritySeverityUnspecified = analysisv1.SecuritySeverity_SECURITY_SEVERITY_UNSPECIFIED
	SecuritySeverityLow         = analysisv1.SecuritySeverity_SECURITY_SEVERITY_LOW
	SecuritySeverityMedium      = analysisv1.SecuritySeverity_SECURITY_SEVERITY_MEDIUM
	SecuritySeverityHigh        = analysisv1.SecuritySeverity_SECURITY_SEVERITY_HIGH

	AuthSchemeUnspecified   = analysisv1.AuthScheme_AUTH_SCHEME_UNSPECIFIED
	AuthSchemeNone          = analysisv1.AuthScheme_AUTH_SCHEME_NONE
	AuthSchemeJWT           = analysisv1.AuthScheme_AUTH_SCHEME_JWT
	AuthSchemeOAuth2        = analysisv1.AuthScheme_AUTH_SCHEME_OAUTH2
	AuthSchemeSessionCookie = analysisv1.AuthScheme_AUTH_SCHEME_SESSION_COOKIE
	AuthSchemeAPIKey        = analysisv1.AuthScheme_AUTH_SCHEME_API_KEY
	AuthSchemeBasic         = analysisv1.AuthScheme_AUTH_SCHEME_BASIC
)

const (
	AnalysisStateUnspecified    = analysisv1.AnalysisState_ANALYSIS_STATE_UNSPECIFIED
	AnalysisStateRunning        = analysisv1.AnalysisState_ANALYSIS_STATE_RUNNING
	AnalysisStateCompleted      = analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED
	AnalysisStateFailed         = analysisv1.AnalysisState_ANALYSIS_STATE_FAILED
	// AnalysisStateAwaitingRootSelection: a monorepo with multiple detected
	// project roots; the heavy pipeline did not run, root_candidates holds the
	// options, and the user must pick one via SelectRoot to proceed.
	AnalysisStateAwaitingRootSelection = analysisv1.AnalysisState_ANALYSIS_STATE_AWAITING_ROOT_SELECTION
	// AnalysisStateCancelled: the analysis was cancelled by the user (terminal).
	AnalysisStateCancelled      = analysisv1.AnalysisState_ANALYSIS_STATE_CANCELLED
	TechnologyStatusUnspecified = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_UNSPECIFIED
	TechnologyStatusCurrent     = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_CURRENT
	TechnologyStatusOutdated    = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_OUTDATED
	TechnologyStatusEndOfLife   = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_END_OF_LIFE
	SeverityUnspecified         = analysisv1.Severity_SEVERITY_UNSPECIFIED
	SeverityLow                 = analysisv1.Severity_SEVERITY_LOW
	SeverityMedium              = analysisv1.Severity_SEVERITY_MEDIUM
	SeverityHigh                = analysisv1.Severity_SEVERITY_HIGH
	SeverityCritical            = analysisv1.Severity_SEVERITY_CRITICAL
)
