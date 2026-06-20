package repositories

import (
	"context"
	"fmt"
	"time"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	analysisconverters "milton_prism/core/worker/analysis/application"
	analysisadapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	workerapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	decompadapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	decompports "milton_prism/core/worker/decomposition/ports"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	"milton_prism/pkg/utils/pointers"

	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ ports.AnalysisMigrabilityAssessor = (*AnalysisMigrabilityAssessorAdapter)(nil)

// AnalysisMigrabilityAssessorAdapter implements ports.AnalysisMigrabilityAssessor
// for the analysis service. It loads the stored graph and cards from MongoDB,
// runs the PHPAware detect → Louvain cluster → guardrail → distill → score
// pipeline, calls the LLM assessor, persists the result, and returns it.
type AnalysisMigrabilityAssessorAdapter struct {
	graphLoader *decompadapters.MongoGraphLoader
	detector    decompports.InfraDetector
	clusterer   *decompadapters.LouvainClusterer
	assessor    *workerapp.Assessor
	repo        *MongoAnalysisSummaryRepository
}

// NewAnalysisMigrabilityAssessorAdapter constructs the adapter.
// analysisDB must be the analysis database (milton_prism_analysis).
// Returns an error only when ANTHROPIC_API_KEY is absent from the environment.
func NewAnalysisMigrabilityAssessorAdapter(
	analysisDB *mongo.Database,
	repo *MongoAnalysisSummaryRepository,
) (*AnalysisMigrabilityAssessorAdapter, error) {
	modelClient, err := analysisadapters.NewAnthropicModelClient(nil)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: model client: %w", err)
	}
	return &AnalysisMigrabilityAssessorAdapter{
		graphLoader: decompadapters.NewMongoGraphLoader(analysisDB),
		detector:    decompadapters.NewPHPAwareInfraDetector(),
		clusterer:   decompadapters.NewLouvainClusterer(),
		assessor:    workerapp.NewAssessor(modelClient),
		repo:        repo,
	}, nil
}

// Assess loads the analysis data, runs the scoring pipeline + LLM assessor,
// persists the result, and returns the MigrabilityAssessment.
func (a *AnalysisMigrabilityAssessorAdapter) Assess(ctx context.Context, analysisSummaryID uint64, language string) (*domain.MigrabilityAssessment, error) {
	graph, err := a.graphLoader.Load(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: load graph: %w", err)
	}
	if len(graph.Edges) == 0 {
		return nil, domain.ErrNoDeepData
	}

	cls, err := a.detector.Detect(ctx, graph)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: detect: %w", err)
	}

	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	clusterResult, err := a.clusterer.Cluster(ctx, decompports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: cluster: %w", err)
	}

	workerapp.ApplyCoherenceGuardrail(cls, clusterResult, domainGraph)

	cards, err := a.graphLoader.LoadCards(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: load cards: %w", err)
	}

	digest := workerapp.Distill(graph, cls, clusterResult, cards, 0)
	scoreResult := workerapp.Score(digest)

	result, err := a.assessor.Assess(ctx, digest, scoreResult, language)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: llm: %w", err)
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

	assessment := &commonv1.MigrabilityAssessment{
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
	}

	// Persist score bytes first so migrability_score_bytes carries the new fields
	// (score_band, structural_findings, typed_blockers) even for analyses that were
	// scored by a pre-contract-extension worker binary.
	if err := a.repo.UpdateMigrabilityScore(ctx, analysisSummaryID, protoScore); err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: persist score: %w", err)
	}

	if err := a.repo.UpdateMigrabilityAssessment(ctx, analysisSummaryID, assessment); err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: persist: %w", err)
	}

	return assessment, nil
}
