// Package domain contains the analysis service's domain types and errors.
// All types are aliases of the generated proto types — no parallel structs.
package domain

import (
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
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
)

const (
	AnalysisStateUnspecified  = analysisv1.AnalysisState_ANALYSIS_STATE_UNSPECIFIED
	AnalysisStateRunning      = analysisv1.AnalysisState_ANALYSIS_STATE_RUNNING
	AnalysisStateCompleted    = analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED
	AnalysisStateFailed       = analysisv1.AnalysisState_ANALYSIS_STATE_FAILED
	TechnologyStatusUnspecified = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_UNSPECIFIED
	TechnologyStatusCurrent   = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_CURRENT
	TechnologyStatusOutdated  = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_OUTDATED
	TechnologyStatusEndOfLife = analysisv1.TechnologyStatus_TECHNOLOGY_STATUS_END_OF_LIFE
	SeverityUnspecified       = analysisv1.Severity_SEVERITY_UNSPECIFIED
	SeverityLow               = analysisv1.Severity_SEVERITY_LOW
	SeverityMedium            = analysisv1.Severity_SEVERITY_MEDIUM
	SeverityHigh              = analysisv1.Severity_SEVERITY_HIGH
	SeverityCritical          = analysisv1.Severity_SEVERITY_CRITICAL
)
