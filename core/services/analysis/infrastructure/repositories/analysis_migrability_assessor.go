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
	// Honest-degrade gate. Read the EXPLICIT deep-analysis-availability signal the
	// analysis pipeline set, mirroring the migration assessor twin. When deep
	// analysis was unavailable there is nothing to reason over, so short-circuit
	// BEFORE Detect / Cluster / Distill / Score / LLM with an INCOMPLETE verdict:
	// no score, no prose, no confidence, no token spend. This replaces the prior
	// len(graph.Edges)==0 → ErrNoDeepData short-circuit, which surfaced to the
	// caller as a 400 FailedPrecondition instead of an honest degrade.
	available, err := a.graphLoader.LoadDeepAnalysisAvailable(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: load availability: %w", err)
	}
	if !available {
		lang := language
		if lang == "" {
			lang = "en"
		}
		// Distinguish the CAUSE of the degrade so the UI does not show a generic
		// "no structural data" when the real reason is a known intake limit (guard 5
		// non-backend, or guard 7 unsupported language). Read the intake assessment the
		// pipeline persisted; fall back to the generic reason when it is absent (e.g.
		// summaries produced before the intake gate existed) or when the repo was a
		// backend in a supported language that simply failed to parse.
		verdict := workerdomain.VerdictIncompleteNoStructuralData
		reason := workerdomain.ReasonNoStructuralData
		if stored, loadErr := a.repo.GetByID(ctx, analysisSummaryID, false); loadErr == nil && stored != nil {
			if ia := stored.GetIntakeAssessment(); ia != nil && !ia.GetMigratable() {
				switch {
				case ia.GetCodebaseKind() != domain.CodebaseKindBackend &&
					ia.GetCodebaseKind() != domain.CodebaseKindUnspecified:
					verdict = workerdomain.VerdictNotABackend
					reason = fmt.Sprintf(workerdomain.ReasonNotABackend, codebaseKindPhrase(ia.GetCodebaseKind()))
				case !ia.GetLanguageSupported() && ia.GetPrimaryLanguage() != "":
					verdict = workerdomain.VerdictUnsupportedLanguage
					reason = fmt.Sprintf(workerdomain.ReasonUnsupportedLanguage,
						ia.GetPrimaryLanguage(), joinNonEmpty(ia.GetSupportedLanguages(), ", "))
				}
			}
		}
		return &commonv1.MigrabilityAssessment{
			Verdict:            verdict,
			Reasons:            []string{reason},
			AssessedTime:       timestamppb.New(time.Now().UTC()),
			AssessmentLanguage: lang,
			// No MigrabilityScore, ScoreSignals, Summary, Blockers, Confidence,
			// or CostUsd — this is a degrade, not a judgement.
		}, nil
	}

	graph, err := a.graphLoader.Load(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("analysis migrability assessor: load graph: %w", err)
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

	// Anchor the LLM prose to the deterministic classifiers' output (DB engine,
	// architectural pattern) so the model confirms them instead of inventing its
	// own. These never affect the score — they only enrich the prompt. Loaded
	// best-effort: a read error leaves AnchorFacts empty.
	if stored, loadErr := a.repo.GetByID(ctx, analysisSummaryID, false); loadErr == nil && stored != nil {
		digest.AnchorFacts = buildAnchorFacts(stored)
	}

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

// buildAnchorFacts renders the deterministic database-engine and architectural-pattern
// classifications of a stored summary into compact fact strings for the LLM prompt.
// Returns nil when neither classifier produced a result.
func buildAnchorFacts(s *domain.AnalysisSummary) []string {
	var facts []string
	if dd := s.GetDatabaseDetection(); dd != nil {
		if dd.GetUnknown() || len(dd.GetEngineNames()) == 0 {
			facts = append(facts, "Database engine: unknown (no driver/config signal detected)")
		} else {
			facts = append(facts, "Database engine(s) detected: "+joinNonEmpty(dd.GetEngineNames(), ", "))
		}
	}
	if ap := s.GetArchitecturalPattern(); ap != nil && ap.GetName() != "" {
		facts = append(facts, fmt.Sprintf("Architectural pattern (deterministic classification): %s (confidence %.0f%%)",
			ap.GetName(), ap.GetConfidence()*100))
	}
	return facts
}

// codebaseKindPhrase renders a CodebaseKind as a natural-language noun phrase for
// the degrade reason. Mirrors the worker-side helper of the same name; kept local
// to avoid a cross-layer import from the service repository into the worker app.
func codebaseKindPhrase(k domain.CodebaseKind) string {
	switch k {
	case domain.CodebaseKindFrontend:
		return "frontend-only application (SPA / static site)"
	case domain.CodebaseKindLibrary:
		return "reusable library / package"
	case domain.CodebaseKindCLI:
		return "command-line tool"
	case domain.CodebaseKindMobile:
		return "mobile application"
	default:
		return "non-backend project"
	}
}

// joinNonEmpty joins non-empty strings with sep.
func joinNonEmpty(xs []string, sep string) string {
	out := ""
	for _, x := range xs {
		if x == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += x
	}
	return out
}
