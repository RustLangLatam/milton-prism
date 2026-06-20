package repositories

import (
	"context"
	"fmt"
	"time"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	analysisconverters "milton_prism/core/worker/analysis/application"
	analysisadapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	workerapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	workeradapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	workerports "milton_prism/core/worker/decomposition/ports"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	"milton_prism/pkg/utils/pointers"

	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ ports.MigrabilityAssessor = (*MigrabilityAssessorAdapter)(nil)

// MigrabilityAssessorAdapter implements ports.MigrabilityAssessor by running
// the M1 distiller + M2 LLM assessor over the stored analysis summary.
// It reads from the analysis database (milton_prism_analysis) and calls the
// Anthropic API via ANTHROPIC_API_KEY read from the runtime environment.
type MigrabilityAssessorAdapter struct {
	graphLoader *workeradapters.MongoGraphLoader
	detector    workerports.InfraDetector
	clusterer   *workeradapters.LouvainClusterer
	assessor    *workerapp.Assessor
}

// NewMigrabilityAssessorAdapter constructs the adapter.
// analysisDB must be the analysis database (milton_prism_analysis).
// Returns an error only when ANTHROPIC_API_KEY is absent from the environment.
func NewMigrabilityAssessorAdapter(analysisDB *mongo.Database) (*MigrabilityAssessorAdapter, error) {
	modelClient, err := analysisadapters.NewAnthropicModelClient(nil)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: model client: %w", err)
	}
	return &MigrabilityAssessorAdapter{
		graphLoader: workeradapters.NewMongoGraphLoader(analysisDB),
		detector:    workeradapters.NewPHPAwareInfraDetector(),
		clusterer:   workeradapters.NewLouvainClusterer(),
		assessor:    workerapp.NewAssessor(modelClient),
	}, nil
}

// Assess loads the analysis summary, distills the structural digest (M1), calls
// the LLM assessor (M2), and returns the verdict as a MigrabilityAssessment proto.
func (a *MigrabilityAssessorAdapter) Assess(ctx context.Context, analysisSummaryID uint64, language string) (*domain.MigrabilityAssessment, error) {
	// Honest-degrade gate. Read the EXPLICIT deep-analysis-availability signal the
	// analysis pipeline set (not derived from an empty graph / DomainEmpty here).
	// When deep analysis was unavailable there is nothing to reason over, so
	// short-circuit BEFORE Distill / Score / LLM: no score, no prose, no
	// confidence, no token spend. The DomainEmpty → NOT_MIGRABLE guardrail below
	// stays intact; it is simply never reached for these repos.
	available, err := a.graphLoader.LoadDeepAnalysisAvailable(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: load availability: %w", err)
	}
	if !available {
		lang := language
		if lang == "" {
			lang = "en"
		}
		return &commonv1.MigrabilityAssessment{
			Verdict:            workerdomain.VerdictIncompleteNoStructuralData,
			Reasons:            []string{workerdomain.ReasonNoStructuralData},
			AssessedTime:       timestamppb.New(time.Now().UTC()),
			AssessmentLanguage: lang,
			// No MigrabilityScore, ScoreSignals, Summary, Blockers, Confidence,
			// or CostUsd — this is a degrade, not a judgement.
		}, nil
	}

	graph, err := a.graphLoader.Load(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: load graph: %w", err)
	}

	cls, err := a.detector.Detect(ctx, graph)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: detect: %w", err)
	}

	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	clusterResult, err := a.clusterer.Cluster(ctx, workerports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: cluster: %w", err)
	}

	// Apply structural-fallback flag + coherence guardrail before distilling.
	// When the guardrail fires it resets cls.Domain and clusterResult.Clusters
	// so the digest carries DomainEmpty=true / NoServiceBoundaries=true, which
	// leads the assessor to emit NOT_MIGRABLE instead of a false-positive PARTIAL.
	workerapp.ApplyCoherenceGuardrail(cls, clusterResult, domainGraph)

	cards, err := a.graphLoader.LoadCards(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: load cards: %w", err)
	}

	digest := workerapp.Distill(graph, cls, clusterResult, cards, 0)

	scoreResult := workerapp.Score(digest)

	result, err := a.assessor.Assess(ctx, digest, scoreResult, language)
	if err != nil {
		return nil, fmt.Errorf("migrability assessor: llm: %w", err)
	}

	protoScore := analysisconverters.ToProtoMigrabilityScore(scoreResult)

	typedRecs := workerapp.ComputeTypedRecommendations(result.Verdict.Verdict, scoreResult)
	var protoRecs []*commonv1.TypedRecommendation
	for _, r := range typedRecs {
		protoRecs = append(protoRecs, &commonv1.TypedRecommendation{
			RecKey: r.RecKey,
			Params: r.Params,
		})
	}

	return &commonv1.MigrabilityAssessment{
		Verdict:              result.Verdict.Verdict,
		Summary:              result.Verdict.Summary,
		Reasons:              result.Verdict.Reasons,
		Blockers:             result.Verdict.Blockers,
		Confidence:           result.Verdict.Confidence,
		CostUsd:              result.CostUSD,
		AssessedTime:         timestamppb.New(time.Now().UTC()),
		MigrabilityScore:     pointers.Int32Ptr(int32(scoreResult.Value)),
		ScoreSignals:         protoScore.Signals,
		TypedRecommendations: protoRecs,
		AssessmentLanguage: func() string {
			if language == "" {
				return "en"
			}
			return language
		}(),
	}, nil
}
