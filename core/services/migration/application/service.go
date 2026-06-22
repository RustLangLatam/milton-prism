// Package application contains the migration service's use-case logic.
// It depends only on domain types and driven port interfaces — never on
// infrastructure packages.
package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	billingdomain "milton_prism/core/services/billing/domain"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	applog "milton_prism/pkg/log"
	migsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/utils/pointers"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service orchestrates migration use cases.
type Service struct {
	repo                   ports.MigrationRepository
	tx                     ports.TransactionManager
	identity               ports.IdentityClient
	repoSvc                ports.RepositoryClient
	analysis               ports.AnalysisClient
	artifacts              ports.ArtifactReader
	generationEnqueuer     ports.GenerationJobEnqueuer
	decomposeEnqueuer      ports.DecomposeJobEnqueuer
	generationResultReader ports.GenerationResultReader
	fileArtifactReader     ports.GenerationFileArtifactReader
	migrabilityAssessor    ports.MigrabilityAssessor
	roadmapEnricher        ports.RoadmapEnricher
	blueprintGenerator     ports.BlueprintGenerator
	stackDetector          ports.StackDetector
	billing                ports.BillingClient
	monorepoPath           string // PRISM_MONOREPO_PATH — skeleton root for deliverable assembly
}

// WithBillingClient wires the billing client used for per-month migration quota
// enforcement. When nil, no quota check is performed (enforcement is a no-op) so
// the service degrades open if billing is unavailable.
func (s *Service) WithBillingClient(b ports.BillingClient) *Service {
	s.billing = b
	return s
}

// NewService wires port implementations into the application service.
func NewService(
	repo ports.MigrationRepository,
	tx ports.TransactionManager,
	identity ports.IdentityClient,
	repoSvc ports.RepositoryClient,
	analysis ports.AnalysisClient,
	artifacts ports.ArtifactReader,
	generationEnqueuer ports.GenerationJobEnqueuer,
	decomposeEnqueuer ports.DecomposeJobEnqueuer,
	generationResultReader ports.GenerationResultReader,
	fileArtifactReader ports.GenerationFileArtifactReader,
	migrabilityAssessor ports.MigrabilityAssessor,
	roadmapEnricher ports.RoadmapEnricher,
	blueprintGenerator ports.BlueprintGenerator,
	stackDetector ports.StackDetector,
	monorepoPath string,
) *Service {
	return &Service{
		repo:                   repo,
		tx:                     tx,
		identity:               identity,
		repoSvc:                repoSvc,
		analysis:               analysis,
		artifacts:              artifacts,
		generationEnqueuer:     generationEnqueuer,
		decomposeEnqueuer:      decomposeEnqueuer,
		generationResultReader: generationResultReader,
		fileArtifactReader:     fileArtifactReader,
		migrabilityAssessor:    migrabilityAssessor,
		roadmapEnricher:        roadmapEnricher,
		blueprintGenerator:     blueprintGenerator,
		stackDetector:          stackDetector,
		monorepoPath:           monorepoPath,
	}
}

// CreateMigration validates the payload, confirms the owner and source
// repository exist, and persists the new migration in PENDING state.
func (s *Service) CreateMigration(ctx context.Context, m *domain.Migration) (*domain.Migration, error) {
	if m == nil {
		return nil, domain.ErrMissingPayload
	}
	if m.GetOwnerUserId() == 0 {
		return nil, domain.ErrMissingOwnerUserID
	}
	if m.GetRepositoryId() == 0 {
		return nil, domain.ErrMissingRepositoryID
	}
	if m.GetTarget() == nil ||
		m.GetTarget().GetLanguage() == domain.TargetLanguageUnspecified ||
		m.GetTarget().GetDatabase() == domain.TargetDatabaseUnspecified {
		return nil, domain.ErrInvalidTargetConfig
	}
	// Reject target languages without a real generator profile (Node/Rust are enum
	// values but generator holes). Without this guard CreateMigration would accept
	// them and the generation step would silently fall back to Go (outputProfileLabel
	// defaults to "go"), producing the wrong language with no signal to the user.
	if !domain.IsGenerableLanguage(m.GetTarget().GetLanguage()) {
		return nil, domain.ErrUnsupportedTargetLanguage
	}
	// Validate and canonicalise the monorepo root subdirectory up front so a bad
	// value is rejected at creation instead of failing the async analysis job.
	// Empty = whole repository root (the default).
	normalizedSubdir, subErr := domain.NormalizeRootSubdirectory(m.GetRootSubdirectory())
	if subErr != nil {
		return nil, subErr
	}
	m.RootSubdirectory = normalizedSubdir
	// Default the architectural topology to MICROSERVICES so the persisted
	// TargetConfig is explicit and the decomposition worker never has to infer it.
	// UNSPECIFIED is treated as MICROSERVICES (no break to the existing flow).
	if m.GetTarget().GetTopology() == domain.TargetTopologyUnspecified {
		m.Target.Topology = domain.TargetTopologyMicroservices
	}
	if s.identity != nil {
		if err := s.identity.ValidateUserExists(ctx, m.GetOwnerUserId()); err != nil {
			return nil, err
		}
	}
	if s.repoSvc != nil {
		repoURL, err := s.repoSvc.FetchRepositoryURL(ctx, m.GetRepositoryId())
		if err != nil {
			return nil, err
		}
		m.RepositoryUrl = repoURL
	}

	// Billing quota gate (hard block, count-based, per-month). Runs after all
	// validations and ownership/repo checks, before persisting. Unlimited (-1)
	// plans (enterprise) are never blocked. Skipped when no billing client is
	// wired (degrade open).
	if s.billing != nil {
		if qErr := s.enforceMigrationQuota(ctx, m.GetOwnerUserId()); qErr != nil {
			return nil, qErr
		}
	}

	m.State = domain.MigrationStatePending

	var out *domain.Migration
	err := s.tx.WithTransaction(ctx, func(txCtx context.Context) error {
		var createErr error
		out, createErr = s.repo.Create(txCtx, m)
		return createErr
	})
	if err != nil {
		return nil, fmt.Errorf("create migration: %w", err)
	}
	return out, nil
}

// enforceMigrationQuota resolves the owner's plan via the billing service and
// rejects the operation when the migrations-per-month count limit has been
// reached. Unlimited (-1) plans are never blocked. A plan-resolution or count
// error degrades open (logged, not fatal) so a transient billing/store failure
// never blocks a paying user.
func (s *Service) enforceMigrationQuota(ctx context.Context, ownerUserID uint64) error {
	if ownerUserID == 0 {
		return nil
	}
	plan, err := s.billing.GetUserPlan(ctx, ownerUserID)
	if err != nil {
		applog.Warningf("migration: plan lookup failed owner_user_id=%d: %v — quota check skipped", ownerUserID, err)
		return nil
	}
	limit := plan.GetMaxMigrationsPerMonth()
	if limit == billingdomain.Unlimited {
		return nil
	}
	since := startOfMonthUTC(time.Now())
	count, err := s.repo.CountByOwnerSince(ctx, ownerUserID, since)
	if err != nil {
		applog.Warningf("migration: quota count failed owner_user_id=%d: %v — quota check skipped", ownerUserID, err)
		return nil
	}
	if count >= limit {
		applog.Infof("migration: plan limit reached owner_user_id=%d count=%d limit=%d", ownerUserID, count, limit)
		return domain.NewErrPlanLimitExceeded(limit)
	}
	return nil
}

// startOfMonthUTC returns the first instant of the current calendar month in UTC.
func startOfMonthUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// GetMigration fetches a migration by identifier. For GENERATING, READY, and
// FAILED states it also loads per-service generation records from the worker store.
func (s *Service) GetMigration(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	switch m.GetState() {
	case domain.MigrationStateGenerating, domain.MigrationStateReady, domain.MigrationStateFailed:
		if s.generationResultReader != nil {
			records, readErr := s.generationResultReader.ReadResults(ctx, identifier)
			if readErr != nil {
				applog.Warningf("migration: GetMigration generation records load failed migration_id=%d: %v", identifier, readErr)
			} else {
				m.GenerationRecords = records
			}
		}
	}
	return m, nil
}

// ListMigrations returns a paginated, filtered list of migrations.
func (s *Service) ListMigrations(ctx context.Context, filter *domain.MigrationsFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Migration, *paginationv1.Pagination, error) {
	return s.repo.List(ctx, filter, params)
}

// DeleteMigration soft-deletes a migration by identifier. Only migrations in a
// terminal state (PUSHED, FAILED, CANCELLED) may be deleted.
func (s *Service) DeleteMigration(ctx context.Context, identifier uint64) error {
	if identifier == 0 {
		return domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return err
	}
	if !isTerminalState(m.GetState()) {
		return domain.ErrInvalidStateTransition
	}
	return s.repo.SoftDelete(ctx, identifier)
}

// StartMigration transitions a PENDING migration to ANALYZING and either:
//   - (reuse path) adopts an existing COMPLETED AnalysisSummary referenced by
//     source_analysis_summary_id and transitions directly to DESIGNING, or
//   - (normal path) dispatches a RunAnalysis job to the analysis worker.
//
// On dispatch failure the state is rolled back to PENDING so the migration
// remains retryable.
func (s *Service) StartMigration(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStatePending {
		return nil, domain.ErrInvalidStateTransition
	}

	if m.GetSourceAnalysisSummaryId() != 0 {
		return s.startWithReuse(ctx, m)
	}
	return s.startNormal(ctx, m)
}

// startWithReuse implements the reuse path: validate and adopt an existing
// COMPLETED AnalysisSummary instead of dispatching a new analysis job.
func (s *Service) startWithReuse(ctx context.Context, m *domain.Migration) (*domain.Migration, error) {
	srcID := m.GetSourceAnalysisSummaryId()
	var summary *analysisv1.AnalysisSummary
	if s.analysis != nil {
		var err error
		summary, err = s.analysis.GetAnalysisSummary(ctx, srcID)
		if err != nil {
			applog.Warningf("migration: GetAnalysisSummary failed source_analysis_id=%d migration_id=%d: %v",
				srcID, m.GetIdentifier(), err)
			return nil, domain.ErrSourceAnalysisNotFound
		}
	}
	if summary != nil {
		if summary.GetRepositoryId() != m.GetRepositoryId() {
			return nil, domain.ErrSourceAnalysisInvalid
		}
		if summary.GetState() != analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED {
			return nil, domain.ErrSourceAnalysisInvalid
		}
	}

	// Gate: live connection probe (same as normal path — confirms credentials
	// are valid before committing; the repository URL is unchanged by reuse).
	if s.repoSvc != nil {
		if probeErr := s.repoSvc.ProbeConnection(ctx, m.GetRepositoryId()); probeErr != nil {
			return nil, probeErr
		}
	}

	// Inherit branch from the analysis when the migration was created without one.
	effectiveBranch := m.GetSourceBranch()
	if effectiveBranch == "" && summary != nil {
		effectiveBranch = summary.GetSourceBranch()
	}

	if err := s.repo.UpdateState(ctx, m.GetIdentifier(), domain.MigrationStateAnalyzing); err != nil {
		return nil, err
	}
	if err := s.repo.AdoptAnalysis(ctx, m.GetIdentifier(), srcID, effectiveBranch); err != nil {
		// Roll back to PENDING — the migration is not yet in a useful state.
		_ = s.repo.UpdateState(ctx, m.GetIdentifier(), domain.MigrationStatePending)
		return nil, fmt.Errorf("migration: adopt analysis failed: %w", err)
	}

	applog.Infof("migration: adopted analysis source_analysis_id=%d migration_id=%d branch=%s",
		srcID, m.GetIdentifier(), effectiveBranch)

	// Kick off the decomposition pipeline. The decomposition worker picks this
	// up from the "analysis" queue and transitions the migration to
	// AWAITING_APPROVAL once the restructure plan is ready.
	if s.decomposeEnqueuer != nil {
		if enqErr := s.decomposeEnqueuer.EnqueueDecompose(
			ctx,
			m.GetIdentifier(),
			srcID,
			m.GetRepositoryUrl(),
			effectiveBranch,
		); enqErr != nil {
			applog.Warningf("migration: EnqueueDecompose failed migration_id=%d summary_id=%d: %v — migration stays in DESIGNING",
				m.GetIdentifier(), srcID, enqErr)
		}
	}

	m.State = domain.MigrationStateDesigning
	m.AnalysisSummaryId = srcID
	m.AnalysisReused = true
	if effectiveBranch != "" {
		m.SourceBranch = effectiveBranch
	}
	return m, nil
}

// startNormal implements the normal path: probe the remote and dispatch an
// asynchronous RunAnalysis job to the analysis worker.
func (s *Service) startNormal(ctx context.Context, m *domain.Migration) (*domain.Migration, error) {
	// Gate: live connection probe before committing to ANALYZING.
	// Rejects immediately if the token is invalid or the remote is unreachable,
	// avoiding worker jobs that would fail at clone time. Migration stays PENDING.
	if s.repoSvc != nil {
		if probeErr := s.repoSvc.ProbeConnection(ctx, m.GetRepositoryId()); probeErr != nil {
			return nil, probeErr
		}
	}
	if err := s.repo.UpdateState(ctx, m.GetIdentifier(), domain.MigrationStateAnalyzing); err != nil {
		return nil, err
	}
	if s.analysis != nil {
		if dispatchErr := s.analysis.RunAnalysis(ctx, m.GetRepositoryId(), m.GetIdentifier(), m.GetOwnerUserId(), m.GetSourceBranch(), m.GetRootSubdirectory()); dispatchErr != nil {
			applog.Errorf("migration: RunAnalysis dispatch failed migration_id=%d repository_id=%d: %v — rolling back to PENDING",
				m.GetIdentifier(), m.GetRepositoryId(), dispatchErr)
			_ = s.repo.UpdateState(ctx, m.GetIdentifier(), domain.MigrationStatePending)
			return nil, fmt.Errorf("migration: analysis dispatch failed: %w", dispatchErr)
		}
	}
	m.State = domain.MigrationStateAnalyzing
	return m, nil
}

// RunMigration is the single-shot orchestration trigger: it runs the full
// restructuring roadmap end-to-end from the platform with no intermediate
// manual steps. From the migration's current state it advances the pipeline as
// far as it synchronously can and records an auto-approve intent so the design
// plan is approved automatically the moment decomposition reaches
// AWAITING_APPROVAL (honored by the decomposition worker, which owns that
// transition). The run stops at READY/FAILED — the final publish (git push)
// is NEVER automated; that human gate is preserved.
//
// State-by-state behaviour (idempotent — re-running re-asserts the intent):
//   - PENDING            → persist auto_approve, then StartMigration (kicks off
//     analysis → decomposition asynchronously).
//   - ANALYZING/DESIGNING→ persist auto_approve; the worker will auto-approve
//     when it reaches AWAITING_APPROVAL.
//   - AWAITING_APPROVAL  → persist auto_approve, then approve immediately. The
//     migrability gate still applies: a NOT_MIGRABLE
//     verdict without an override returns MIG212.
//   - GENERATING/TESTING/READY → already past the approval gate; persist the
//     intent and return the current record (no-op advance).
//   - terminal states (PUSHED/FAILED/CANCELLED/RESTRUCTURING_READY) → MIG202.
func (s *Service) RunMigration(ctx context.Context, identifier uint64, serviceFilter []string) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}

	switch m.GetState() {
	case domain.MigrationStatePending:
		if err := s.repo.SetAutoApprove(ctx, identifier, true); err != nil {
			return nil, fmt.Errorf("migration: run set auto_approve: %w", err)
		}
		out, startErr := s.StartMigration(ctx, identifier)
		if startErr != nil {
			return nil, startErr
		}
		out.AutoApprove = true
		applog.Infof("migration: RunMigration started pipeline migration_id=%d (auto_approve set)", identifier)
		return out, nil

	case domain.MigrationStateAnalyzing, domain.MigrationStateDesigning:
		if err := s.repo.SetAutoApprove(ctx, identifier, true); err != nil {
			return nil, fmt.Errorf("migration: run set auto_approve: %w", err)
		}
		m.AutoApprove = true
		applog.Infof("migration: RunMigration auto_approve armed mid-pipeline migration_id=%d state=%s", identifier, m.GetState())
		return m, nil

	case domain.MigrationStateAwaitingApproval:
		if err := s.repo.SetAutoApprove(ctx, identifier, true); err != nil {
			return nil, fmt.Errorf("migration: run set auto_approve: %w", err)
		}
		// Approve right now — the plan is already available. The migrability and
		// no-service-boundaries gates inside ApproveDesign still apply.
		out, approveErr := s.ApproveDesign(ctx, identifier, true, serviceFilter)
		if approveErr != nil {
			return nil, approveErr
		}
		out.AutoApprove = true
		applog.Infof("migration: RunMigration approved plan immediately migration_id=%d → GENERATING", identifier)
		return out, nil

	case domain.MigrationStateGenerating, domain.MigrationStateTesting, domain.MigrationStateReady:
		// Already past the approval gate; re-assert the intent and report current.
		if err := s.repo.SetAutoApprove(ctx, identifier, true); err != nil {
			return nil, fmt.Errorf("migration: run set auto_approve: %w", err)
		}
		m.AutoApprove = true
		return m, nil

	default:
		// Terminal states (PUSHED, FAILED, CANCELLED, RESTRUCTURING_READY).
		return nil, domain.ErrInvalidStateTransition
	}
}

// ApproveDesign transitions a migration from AWAITING_APPROVAL. When approved
// is true the migration advances to GENERATING; when false it is CANCELLED.
//
// Gate (A.5/A.8): if a NOT_MIGRABLE verdict exists without override, Approve is
// blocked (MIG212). The user must call SetMigrabilityOverride first.
// PARTIAL warns but does not block. Absent verdict does not block.
func (s *Service) ApproveDesign(ctx context.Context, identifier uint64, approved bool, serviceFilter []string) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStateAwaitingApproval {
		return nil, domain.ErrInvalidStateTransition
	}
	if approved && m.GetPlan().GetNoServiceBoundaries() {
		return nil, domain.ErrNoServiceBoundaries
	}
	if approved && migrabilityBlocked(m) {
		return nil, domain.ErrNotMigrableBlocked
	}
	nextState := domain.MigrationStateGenerating
	if !approved {
		nextState = domain.MigrationStateCancelled
	}
	if err := s.repo.UpdateState(ctx, identifier, nextState); err != nil {
		return nil, err
	}
	m.State = nextState
	if approved && s.generationEnqueuer != nil {
		if dispatchErr := s.generationEnqueuer.EnqueueGeneration(ctx, identifier, serviceFilter); dispatchErr != nil {
			applog.Warningf("migration: EnqueueGeneration dispatch failed migration_id=%d: %v", identifier, dispatchErr)
		}
	}
	return m, nil
}

// AssessMigrability runs the opt-in LLM migrability assessment for a migration.
// It requires a completed analysis (analysis_summary_id > 0). Persists the verdict
// on the migration record; idempotent — re-running updates the stored verdict.
func (s *Service) AssessMigrability(ctx context.Context, identifier uint64, language string) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetAnalysisSummaryId() == 0 {
		return nil, domain.ErrNoAnalysisSummary
	}
	if s.migrabilityAssessor == nil {
		return nil, fmt.Errorf("migration: migrability assessor not configured")
	}
	assessment, err := s.migrabilityAssessor.Assess(ctx, m.GetAnalysisSummaryId(), language)
	if err != nil {
		return nil, fmt.Errorf("migration: assess migrability: %w", err)
	}
	// Overwrite the score with path A's canonical value stored on AnalysisSummary.
	// Path B re-runs Louvain for the LLM prompt context, which can produce a
	// different cluster partition (map-iteration order in absorbSingletons) and
	// therefore a different score. Using path A's score guarantees one source of
	// truth: the score the user sees on the analysis report equals the score
	// embedded in the assessment.
	// The score is attached only when the AnalysisSummary actually has one. An
	// INCOMPLETE_NO_STRUCTURAL_DATA assessment never does (no deep analysis → no
	// stage-6d score), so migrability_score stays absent (nil) rather than a
	// misleading 0 that would rank as "worst migrable". The ms != nil check is the
	// gate: no score on the summary ⟺ no number on the assessment.
	if s.analysis != nil {
		if summary, fetchErr := s.analysis.GetAnalysisSummary(ctx, m.GetAnalysisSummaryId()); fetchErr == nil {
			if ms := summary.GetMigrabilityScore(); ms != nil {
				assessment.MigrabilityScore = pointers.Int32Ptr(ms.GetValue())
				assessment.ScoreSignals = ms.GetSignals()
			}
		}
	}
	if err := s.repo.SetMigrabilityAssessment(ctx, identifier, assessment); err != nil {
		return nil, fmt.Errorf("migration: persist assessment: %w", err)
	}
	m.MigrabilityAssessment = assessment
	return m, nil
}

// EnrichRoadmap runs the opt-in LLM enrichment of the restructuring roadmap steps.
// It requires the migration to be in RESTRUCTURING_READY state with a non-empty
// action_plan. The enrichment is persisted on the migration; the deterministic
// action_plan is always available as a fallback when enrichment is absent.
// Idempotent — re-running replaces the stored enrichment.
func (s *Service) EnrichRoadmap(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStateRestructuringReady {
		return nil, domain.ErrInvalidStateTransition
	}
	roadmap := m.GetRestructuringRoadmap()
	if roadmap == nil || len(roadmap.GetActionPlan()) == 0 {
		return nil, domain.ErrNoRoadmap
	}
	if s.roadmapEnricher == nil {
		return nil, fmt.Errorf("migration: roadmap enricher not configured")
	}
	enrichment, err := s.roadmapEnricher.Enrich(ctx, roadmap)
	if err != nil {
		return nil, fmt.Errorf("migration: enrich roadmap: %w", err)
	}
	if err := s.repo.SetRoadmapEnrichment(ctx, identifier, enrichment); err != nil {
		return nil, fmt.Errorf("migration: persist enrichment: %w", err)
	}
	m.RestructuringRoadmap.Enrichment = enrichment
	return m, nil
}

// GenerateBlueprint runs the opt-in LLM blueprint generation for a migration.
// It requires RESTRUCTURING_READY state, a completed analysis summary, and a
// non-empty roadmap. The full Distill pipeline (graph → detect → cluster → cards)
// is run inside the adapter; the resulting AnalysisDigest drives the LLM prompt.
// Idempotent — re-running replaces the stored blueprint.
func (s *Service) GenerateBlueprint(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStateRestructuringReady {
		return nil, domain.ErrInvalidStateTransition
	}
	if m.GetAnalysisSummaryId() == 0 {
		return nil, domain.ErrNoBlueprintAnalysis
	}
	roadmap := m.GetRestructuringRoadmap()
	if roadmap == nil || len(roadmap.GetActionPlan()) == 0 {
		return nil, domain.ErrNoRoadmap
	}
	if s.blueprintGenerator == nil {
		return nil, fmt.Errorf("migration: blueprint generator not configured")
	}
	blueprint, err := s.blueprintGenerator.Generate(ctx, m.GetAnalysisSummaryId(), roadmap)
	if err != nil {
		return nil, fmt.Errorf("migration: generate blueprint: %w", err)
	}
	if err := s.repo.SetServiceBlueprint(ctx, identifier, blueprint); err != nil {
		return nil, fmt.Errorf("migration: persist blueprint: %w", err)
	}
	m.RestructuringRoadmap.Blueprint = blueprint
	return m, nil
}

// SetMigrabilityOverride sets or clears the migrability_override flag.
// When override is true, a NOT_MIGRABLE verdict no longer blocks Approve/Generate.
// Idempotent — setting the same value twice is a no-op.
func (s *Service) SetMigrabilityOverride(ctx context.Context, identifier uint64, override bool) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetMigrabilityOverride(ctx, identifier, override); err != nil {
		return nil, fmt.Errorf("migration: set override: %w", err)
	}
	m.MigrabilityOverride = override
	return m, nil
}

// migrabilityBlocked reports whether the migration's migrability gate should block
// an Approve action: true only when verdict is NOT_MIGRABLE and override is false.
func migrabilityBlocked(m *domain.Migration) bool {
	v := m.GetMigrabilityAssessment()
	if v == nil {
		return false // absent verdict → no block
	}
	return v.GetVerdict() == domain.MigrabilityVerdictNotMigrable && !m.GetMigrabilityOverride()
}

// CancelMigration transitions a migration to CANCELLED from any non-terminal state.
func (s *Service) CancelMigration(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if isTerminalState(m.GetState()) {
		return nil, domain.ErrInvalidStateTransition
	}
	if err := s.repo.UpdateState(ctx, identifier, domain.MigrationStateCancelled); err != nil {
		return nil, err
	}
	m.State = domain.MigrationStateCancelled
	return m, nil
}

// GetGenerationPackage assembles the generation package for a GENERATING migration
// by joining the RestructurePlan (for error prefixes) with the persisted design
// artifacts (proto + boundary spec). Only callable in GENERATING state.
func (s *Service) GetGenerationPackage(ctx context.Context, identifier uint64) (*domain.GenerationPackage, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStateGenerating {
		return nil, domain.ErrInvalidStateTransition
	}

	artifacts, err := s.artifacts.ReadArtifacts(ctx, identifier)
	if err != nil {
		return nil, fmt.Errorf("generation-package: read artifacts: %w", err)
	}
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("%w: no design artifacts for migration %d", domain.ErrInternal, identifier)
	}

	// Build error-prefix index from the persisted plan.
	prefixByName := make(map[string]string, len(artifacts))
	for _, svc := range m.GetPlan().GetServices() {
		prefixByName[svc.GetName()] = svc.GetErrorPrefix()
	}

	profile := outputProfileLabel(m.GetTarget())
	promptRef := generatorPromptRef(profile)

	specs := make([]*migrationv1.ServiceGenerationSpec, len(artifacts))
	for i, a := range artifacts {
		specs[i] = &migrationv1.ServiceGenerationSpec{
			Name:               a.ServiceName,
			ErrorPrefix:        prefixByName[a.ServiceName],
			ProtoContent:       a.ProtoContent,
			BoundarySpec:       a.BoundarySpec,
			Incomplete:         a.Incomplete,
			IncompleteReason:   a.IncompleteReason,
			GeneratorPromptRef: promptRef,
		}
	}

	return &migrationv1.GenerationPackage{
		MigrationId:   identifier,
		OutputProfile: profile,
		Services:      specs,
	}, nil
}

// PublishMigration reads all generated file artifacts and pushes them to
// targetURL. Callable from READY or PUSHED state.
//
// On success: state advances to PUSHED and the updated migration is returned.
// On push failure: state is NOT changed (READY remains retryable; PUSHED
// remains valid — "was published at some point"). A typed error is returned
// and writeToken never appears in any log or error string at this layer.
func (s *Service) PublishMigration(ctx context.Context, migrationID uint64, targetURL, writeToken, commitMessage string) (*domain.Migration, string, error) {
	if migrationID == 0 {
		return nil, "", domain.ErrMissingIdentifier
	}
	if targetURL == "" {
		return nil, "", domain.ErrMissingPayload
	}
	m, err := s.repo.GetByID(ctx, migrationID, false)
	if err != nil {
		return nil, "", err
	}
	if m.GetState() != domain.MigrationStateReady && m.GetState() != domain.MigrationStatePushed {
		return nil, "", domain.ErrInvalidStateTransition
	}

	files, err := s.fileArtifactReader.ListArtifacts(ctx, migrationID, "")
	if err != nil {
		return nil, "", fmt.Errorf("publish: read artifacts migration_id=%d: %w", migrationID, err)
	}
	if len(files) == 0 {
		return nil, "", domain.ErrNoArtifacts
	}

	if err := detectPathConflicts(files); err != nil {
		applog.Warningf("migration: PublishMigration artifact conflict migration_id=%d: %v", migrationID, err)
		return nil, "", err
	}

	pushFiles := make([]ports.PushFile, len(files))
	for i, f := range files {
		pushFiles[i] = ports.PushFile{Path: f.Path, Content: f.Content}
	}

	pushedBranch, pushErr := s.repoSvc.PushFiles(ctx, targetURL, writeToken, pushFiles, commitMessage)
	if pushErr != nil {
		// Push failed: state not changed, migration remains retryable.
		applog.Warningf("migration: PublishMigration push failed migration_id=%d: %v", migrationID, pushErr)
		return nil, "", pushErr
	}

	if m.GetState() != domain.MigrationStatePushed {
		if stateErr := s.repo.UpdateState(ctx, migrationID, domain.MigrationStatePushed); stateErr != nil {
			return nil, "", stateErr
		}
		m.State = domain.MigrationStatePushed
	}
	return m, pushedBranch, nil
}

// GetGenerationArtifacts returns the generated source files for a migration,
// grouped by service. When serviceName is non-empty only that service's files
// are returned; when empty all services are included. agent_raw_result is
// populated per-service from the generation_results collection for UI debugging.
func (s *Service) GetGenerationArtifacts(ctx context.Context, migrationID uint64, serviceName string) (*migsvcv1.GetGenerationArtifactsResponse, error) {
	if migrationID == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	if _, err := s.repo.GetByID(ctx, migrationID, false); err != nil {
		return nil, err
	}

	files, err := s.fileArtifactReader.ListArtifacts(ctx, migrationID, serviceName)
	if err != nil {
		return nil, fmt.Errorf("generation-artifacts: read files: %w", err)
	}

	// Index agent_raw_result per service from generation_results.
	rawResults := make(map[string]string)
	if s.generationResultReader != nil {
		if records, readErr := s.generationResultReader.ReadResults(ctx, migrationID); readErr == nil {
			for _, r := range records {
				rawResults[r.GetServiceName()] = r.GetAgentRawResult()
			}
		}
	}

	// Group files by service name preserving sort order (files are pre-sorted).
	byService := make(map[string][]*migrationv1.FileArtifact)
	order := make([]string, 0)
	for _, f := range files {
		if _, seen := byService[f.ServiceName]; !seen {
			order = append(order, f.ServiceName)
		}
		byService[f.ServiceName] = append(byService[f.ServiceName], &migrationv1.FileArtifact{
			Path:    f.Path,
			Content: f.Content,
		})
	}
	sort.Strings(order)

	svcs := make([]*migrationv1.ServiceGenerationArtifacts, len(order))
	for i, name := range order {
		svcs[i] = &migrationv1.ServiceGenerationArtifacts{
			ServiceName:    name,
			Files:          byService[name],
			AgentRawResult: rawResults[name],
		}
	}
	return &migsvcv1.GetGenerationArtifactsResponse{Services: svcs}, nil
}

// outputProfileLabel maps the migration's TargetLanguage to a short profile name.
// Defaults to "go" for unset or unknown languages.
func outputProfileLabel(tc *migrationv1.TargetConfig) string {
	if tc == nil {
		return "go"
	}
	switch tc.GetLanguage() {
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON:
		return "python"
	default:
		return "go"
	}
}

// generatorPromptRef returns the path to the generator prompt document for the
// given profile. The Go profile is the only filled profile in v1.
func generatorPromptRef(profile string) string {
	switch profile {
	case "python":
		return "docs/prism/milton-prism-service-generator-prompt-python.md"
	default:
		return "docs/prism/milton-prism-service-generator-prompt.md"
	}
}

// detectPathConflicts returns MIG211 if any two records share a monorepo path
// but carry different content. This blocks the push before any file is written,
// preventing silent last-write-wins data loss in shared gateway/shared packages.
// Paths with duplicate content across services are benign and are ignored.
func detectPathConflicts(files []ports.GeneratedFile) error {
	type entry struct {
		content  string
		services []string
	}
	byPath := make(map[string]*entry, len(files))
	for _, f := range files {
		e, seen := byPath[f.Path]
		if !seen {
			byPath[f.Path] = &entry{content: f.Content, services: []string{f.ServiceName}}
			continue
		}
		if f.Content != e.content {
			e.services = append(e.services, f.ServiceName)
		}
	}

	var conflicts []string
	for path, e := range byPath {
		if len(e.services) > 1 {
			sort.Strings(e.services)
			conflicts = append(conflicts,
				fmt.Sprintf("%s (services: %s)", path, strings.Join(e.services, ", ")))
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return domain.NewErrArtifactConflict(strings.Join(conflicts, "; "))
}

// BackfillRepositoryURLs resolves and persists the repository_url for any
// migration that was created before the snapshot feature existed. It runs once
// at service startup and is a no-op once all records are populated.
// Safe to run concurrently: each update is a targeted $set by identifier.
func (s *Service) BackfillRepositoryURLs(ctx context.Context) {
	if s.repoSvc == nil {
		return
	}
	const pageSize = 100
	params := &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: pageSize}
	for {
		migrations, pagination, err := s.repo.List(ctx, nil, params)
		if err != nil {
			applog.Warningf("migration: BackfillRepositoryURLs list page=%d failed: %v", params.PageNumber, err)
			return
		}
		// Resolve unique IDs that are missing a URL.
		needed := make(map[uint64]string)
		for _, m := range migrations {
			if m.GetRepositoryUrl() == "" && m.GetRepositoryId() > 0 {
				needed[m.GetRepositoryId()] = ""
			}
		}
		for id := range needed {
			url, fetchErr := s.repoSvc.FetchRepositoryURL(ctx, id)
			if fetchErr != nil {
				applog.Warningf("migration: BackfillRepositoryURLs fetch repo_id=%d: %v", id, fetchErr)
				continue
			}
			needed[id] = url
		}
		// Persist URL for each affected migration.
		for _, m := range migrations {
			if m.GetRepositoryUrl() != "" || m.GetRepositoryId() == 0 {
				continue
			}
			url := needed[m.GetRepositoryId()]
			if url == "" {
				continue
			}
			if setErr := s.repo.SetRepositoryURL(ctx, m.GetIdentifier(), url); setErr != nil {
				applog.Warningf("migration: BackfillRepositoryURLs set id=%d: %v", m.GetIdentifier(), setErr)
			}
		}
		if pagination == nil || uint64(params.PageNumber) >= pagination.GetTotalPages() {
			break
		}
		params.PageNumber++
	}
}

// GenerateRestructuringRoadmap assembles a restructuring roadmap from the
// persisted MigrabilityAssessment and RestructurePlan for a NOT_MIGRABLE or
// no-boundary migration, persists it, and transitions to RESTRUCTURING_READY.
// No additional LLM call is made. RESTRUCTURING_READY is a terminal state.
func (s *Service) GenerateRestructuringRoadmap(ctx context.Context, identifier uint64) (*domain.Migration, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if m.GetState() != domain.MigrationStateAwaitingApproval {
		return nil, domain.ErrInvalidStateTransition
	}
	assessment := m.GetMigrabilityAssessment()
	plan := m.GetPlan()
	isNotMigrable := assessment != nil && assessment.GetVerdict() == domain.MigrabilityVerdictNotMigrable
	isNoServiceBoundaries := plan != nil && plan.GetNoServiceBoundaries()
	if !isNotMigrable && !isNoServiceBoundaries {
		return nil, domain.ErrRoadmapUnavailable
	}
	var infraModules []string
	if s.analysis != nil && m.GetAnalysisSummaryId() != 0 {
		if summary, fetchErr := s.analysis.GetAnalysisSummary(ctx, m.GetAnalysisSummaryId()); fetchErr == nil {
			infraModules = summary.GetModuleClassification().GetInfraModules()
			// When the LLM assessment was not run, fall back to the deterministic
			// score signals persisted on the AnalysisSummary. Those signals are
			// identical to what AssessMigrability would have copied onto the
			// migration — the only difference is the absence of LLM-generated
			// verdict/summary/confidence text.
			if assessment == nil {
				assessment = syntheticAssessmentFromScore(summary.GetMigrabilityScore(), plan)
			}
		}
	}
	// Edge case: analysis service unavailable or summary missing AND no LLM
	// assessment. Produce a diagnosis marker so the roadmap is never silently empty.
	if assessment == nil {
		assessment = &domain.MigrabilityAssessment{
			Verdict: "UNAVAILABLE",
			Summary: "Diagnosis unavailable — the analysis did not produce a score; re-run analysis to populate this section",
		}
	}
	roadmap := assembleRoadmap(assessment, plan, infraModules)
	if err := s.repo.SetRestructuringRoadmap(ctx, identifier, roadmap); err != nil {
		return nil, fmt.Errorf("migration: persist roadmap: %w", err)
	}
	m.RestructuringRoadmap = roadmap
	m.State = domain.MigrationStateRestructuringReady
	return m, nil
}

// syntheticAssessmentFromScore builds a MigrabilityAssessment from the
// deterministic MigrabilityScore when the LLM assessment was never run.
// The score_signals are identical to what AssessMigrability would have copied;
// verdict, summary, confidence, and blockers are absent (LLM-only fields).
func syntheticAssessmentFromScore(ms *commonv1.MigrabilityScore, plan *domain.RestructurePlan) *domain.MigrabilityAssessment {
	if ms == nil || len(ms.GetSignals()) == 0 {
		return nil
	}
	verdict := "NO_SERVICE_BOUNDARIES"
	summary := plan.GetBoundariesExplanation()
	return &domain.MigrabilityAssessment{
		Verdict:          verdict,
		Summary:          summary,
		MigrabilityScore: pointers.Int32Ptr(ms.GetValue()),
		ScoreSignals:     ms.GetSignals(),
	}
}

// assembleRoadmap builds a RestructuringRoadmap from the stored assessment and
// plan data without any external calls. Structural problems are ordered by
// penalty descending. The action plan is one step per active score signal,
// also ordered by penalty descending, with depends_on_step set for signals
// that are downstream of others (cluster_count → domain_presence,
// routing_layout → cluster_count).
func assembleRoadmap(assessment *domain.MigrabilityAssessment, plan *domain.RestructurePlan, infraModules []string) *domain.RestructuringRoadmap {
	roadmap := &domain.RestructuringRoadmap{
		GeneratedTime: timestamppb.Now(),
	}
	if assessment != nil {
		roadmap.Diagnosis = &domain.RoadmapDiagnosis{
			Verdict: assessment.GetVerdict(),
			Summary: assessment.GetSummary(),
			// Copy the optional score pointer so absence is preserved: an INCOMPLETE
			// assessment has a nil score and the diagnosis must stay nil too, never 0.
			MigrabilityScore: assessment.MigrabilityScore,
			Confidence:       assessment.GetConfidence(),
			Blockers:         assessment.GetBlockers(),
		}
		var problems []*domain.StructuralProblem
		for _, sig := range assessment.GetScoreSignals() {
			if sig.GetPenalty() > 0 {
				problems = append(problems, &domain.StructuralProblem{
					Signal:  sig.GetSignal(),
					Penalty: sig.GetPenalty(),
					Detail:  sig.GetDetail(),
				})
			}
		}
		sort.Slice(problems, func(i, j int) bool {
			return problems[i].GetPenalty() > problems[j].GetPenalty()
		})
		roadmap.StructuralProblems = problems
		roadmap.ActionPlan = buildActionPlan(assessment.GetScoreSignals(), infraModules)
	}
	if plan != nil {
		roadmap.BoundariesExplanation = plan.GetBoundariesExplanation()
	}
	return roadmap
}

// buildActionPlan derives one ActionItem per active score signal, ordered by
// penalty descending, with step numbers and dependency links assigned.
func buildActionPlan(signals []*commonv1.ScoreSignal, infraModules []string) []*domain.ActionItem {
	type entry struct {
		item   *domain.ActionItem
		signal string
	}
	var entries []entry
	for _, sig := range signals {
		if sig.GetPenalty() <= 0 {
			continue
		}
		item := signalToActionItem(sig, infraModules)
		if item == nil {
			continue
		}
		item.Impact = sig.GetPenalty()
		entries = append(entries, entry{item: item, signal: sig.GetSignal()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].item.Impact > entries[j].item.Impact
	})
	orderOf := make(map[string]int32, len(entries))
	for i := range entries {
		entries[i].item.Order = int32(i + 1)
		orderOf[entries[i].signal] = int32(i + 1)
	}
	for i := range entries {
		switch entries[i].signal {
		case "cluster_count":
			entries[i].item.DependsOnStep = orderOf["domain_presence"]
		case "routing_layout":
			entries[i].item.DependsOnStep = orderOf["cluster_count"]
		}
	}
	items := make([]*domain.ActionItem, len(entries))
	for i, e := range entries {
		items[i] = e.item
	}
	return items
}

// signalToActionItem maps one ScoreSignal to its concrete ActionItem.
// Returns nil for unknown signals.
func signalToActionItem(sig *commonv1.ScoreSignal, infraModules []string) *domain.ActionItem {
	switch sig.GetSignal() {
	case "domain_presence":
		subject := formatModuleList(infraModules, 5)
		return &domain.ActionItem{
			Kind:     "EXTRACT_DOMAIN",
			Subject:  subject,
			Action:   fmt.Sprintf("Extract domain entities from %s into dedicated domain modules; separate business logic from infrastructure utilities", subject),
			Blocking: true,
		}
	case "hub_severity":
		module := parseHubModule(sig.GetDetail())
		return &domain.ActionItem{
			Kind:     "DECOUPLE_STATE",
			Subject:  module,
			Action:   fmt.Sprintf("Decouple shared state in %s by extracting state management behind a service interface", module),
			Blocking: true,
		}
	case "god_modules":
		modules := parseGodModules(sig.GetDetail())
		subject := strings.Join(modules, ", ")
		if subject == "" {
			subject = sig.GetDetail()
		}
		return &domain.ActionItem{
			Kind:     "SPLIT_GOD_MODULE",
			Subject:  subject,
			Action:   fmt.Sprintf("Split %s by responsibility: extract distinct concerns into focused modules", subject),
			Blocking: false,
		}
	case "cluster_count":
		return &domain.ActionItem{
			Kind:     "DEFINE_BOUNDARIES",
			Subject:  sig.GetDetail(),
			Action:   "Define service boundaries around domain modules once the domain layer exists; group by functional area to form service clusters",
			Blocking: true,
		}
	case "routing_layout":
		return &domain.ActionItem{
			Kind:     "ADD_ROUTING",
			Subject:  sig.GetDetail(),
			Action:   "Add per-domain HTTP routing once service clusters exist: one route file per service boundary",
			Blocking: false,
		}
	}
	return nil
}

// parseHubModule extracts the module name from a hub_severity detail string.
// Format: "<module> fan-in=<N> — ...".
func parseHubModule(detail string) string {
	if idx := strings.Index(detail, " fan-in="); idx > 0 {
		return detail[:idx]
	}
	return detail
}

// parseGodModules extracts module names from a god_modules detail string.
// Format: "N god-module(s): [mod1 mod2 ...] (≥M functions + shared state)".
func parseGodModules(detail string) []string {
	start := strings.Index(detail, "[")
	end := strings.Index(detail, "]")
	if start >= 0 && end > start+1 {
		return strings.Fields(detail[start+1 : end])
	}
	return nil
}

// formatModuleList formats up to max module names for display in an action text.
func formatModuleList(modules []string, max int) string {
	if len(modules) == 0 {
		return "(infra modules)"
	}
	if len(modules) > max {
		modules = modules[:max]
	}
	return strings.Join(modules, ", ")
}

// isTerminalState reports whether state is a terminal node in the state machine.
// Terminal states are PUSHED, FAILED, CANCELLED, and RESTRUCTURING_READY.
func isTerminalState(state domain.MigrationState) bool {
	switch state {
	case domain.MigrationStatePushed,
		domain.MigrationStateFailed,
		domain.MigrationStateCancelled,
		domain.MigrationStateRestructuringReady:
		return true
	default:
		return false
	}
}

// ExportActionPlanPrompt builds a deterministic Markdown document containing
// the restructuring action plan with stack-specific instructions for the
// detected framework. No LLM is invoked. Returns (filename, content, error).
func (s *Service) ExportActionPlanPrompt(ctx context.Context, identifier uint64) (filename string, content []byte, err error) {
	if identifier == 0 {
		return "", nil, domain.ErrMissingIdentifier
	}
	m, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return "", nil, err
	}
	if m.GetState() != domain.MigrationStateRestructuringReady {
		return "", nil, domain.ErrInvalidStateTransition
	}
	roadmap := m.GetRestructuringRoadmap()
	if roadmap == nil || len(roadmap.GetActionPlan()) == 0 {
		return "", nil, domain.ErrNoActionPlan
	}

	var framework string
	var technologies []string
	if s.stackDetector != nil && m.GetAnalysisSummaryId() != 0 {
		framework, technologies, _ = s.stackDetector.Detect(ctx, m.GetAnalysisSummaryId())
		// Detection failure is non-fatal: produce hole export instead of an error.
	}

	profile := resolveProfile(framework, technologies)
	md := BuildActionPlanPrompt(m.GetRepositoryUrl(), identifier, roadmap, framework, technologies, profile)

	repoSlug := repoSlugFromURL(m.GetRepositoryUrl())
	filename = fmt.Sprintf("restructuring-prompt-%s-%d.md", repoSlug, identifier)
	return filename, md, nil
}

// repoSlugFromURL extracts a filesystem-safe slug from a repository URL.
// "https://github.com/org/my-service" → "my-service".
func repoSlugFromURL(repoURL string) string {
	if repoURL == "" {
		return "repo"
	}
	parts := strings.Split(strings.TrimSuffix(repoURL, "/"), "/")
	if len(parts) == 0 {
		return "repo"
	}
	slug := parts[len(parts)-1]
	// Replace any character that is not alphanumeric, hyphen, or underscore.
	var out strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteRune('-')
		}
	}
	if out.Len() == 0 {
		return "repo"
	}
	return out.String()
}
