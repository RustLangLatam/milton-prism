// Package application contains the analysis service's use-case logic.
// It depends only on domain types and driven port interfaces — never on
// infrastructure packages.
package application

import (
	"context"
	"time"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	billingdomain "milton_prism/core/services/billing/domain"
	applog "milton_prism/pkg/log"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// Service orchestrates analysis use cases.
type Service struct {
	repo       ports.AnalysisSummaryRepository
	repoSvc    ports.RepositoryClient
	enqueuer   ports.JobEnqueuer
	assessor   ports.AnalysisMigrabilityAssessor
	plans      ports.PlanProvider
	migrations ports.MigrationClient
}

// NewService wires port implementations into the application service.
func NewService(
	repo ports.AnalysisSummaryRepository,
	repoSvc ports.RepositoryClient,
	enqueuer ports.JobEnqueuer,
) *Service {
	return &Service{repo: repo, repoSvc: repoSvc, enqueuer: enqueuer}
}

// WithMigrabilityAssessor wires the optional LLM migrability assessor.
// When nil, AssessMigrability returns an internal error.
func (s *Service) WithMigrabilityAssessor(a ports.AnalysisMigrabilityAssessor) *Service {
	s.assessor = a
	return s
}

// WithPlanProvider wires the billing plan provider used for per-month analysis
// quota enforcement. When nil, no quota check is performed (enforcement is a
// no-op) so the service degrades open if billing is unavailable.
func (s *Service) WithPlanProvider(p ports.PlanProvider) *Service {
	s.plans = p
	return s
}

// WithMigrationClient wires the cross-service migration client used by
// DeleteAnalysisSummary to count active migrations referencing an analysis.
// When nil, the live-migration guard degrades CLOSED (delete is refused with
// ErrInternal) so a misconfigured deployment never silently orphans a migration.
func (s *Service) WithMigrationClient(m ports.MigrationClient) *Service {
	s.migrations = m
	return s
}

// GetAnalysisSummary fetches an analysis summary by identifier.
func (s *Service) GetAnalysisSummary(ctx context.Context, identifier uint64) (*domain.AnalysisSummary, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	return s.repo.GetByID(ctx, identifier, false)
}

// ListAnalysisSummaries returns a paginated, filtered list of analysis summaries.
func (s *Service) ListAnalysisSummaries(ctx context.Context, filter *analysissvcv1.AnalysisSummariesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.AnalysisSummary, *paginationv1.Pagination, error) {
	return s.repo.List(ctx, filter, params)
}

// AssessMigrability runs the opt-in LLM migrability assessment for a completed
// analysis summary identified by identifier. The assessor loads the dependency
// graph, runs Louvain clustering, calls the LLM, persists the result, and
// returns the MigrabilityAssessment.
func (s *Service) AssessMigrability(ctx context.Context, identifier uint64, language string) (*domain.MigrabilityAssessment, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	if s.assessor == nil {
		return nil, domain.ErrInternal
	}
	return s.assessor.Assess(ctx, identifier, language)
}

// RunAnalysisResult is the return value of RunAnalysis.
// Exactly one of Summary or Duplicate is non-nil.
//   - When Duplicate is non-nil: the same commit is already covered by an existing
//     COMPLETED analysis; the caller should surface it to the user and offer force=true.
//   - When Summary is non-nil: a new analysis run was created and enqueued.
type RunAnalysisResult struct {
	Summary   *domain.AnalysisSummary
	Duplicate *domain.AnalysisSummary
}

// RunAnalysis validates the request, optionally deduplicates against existing
// COMPLETED analyses (standalone runs only), creates an AnalysisSummary in
// RUNNING state, enqueues the analysis job, and returns immediately.
//
// sourceBranch is mandatory: an empty branch is rejected with ErrMissingSourceBranch.
// Analyses are unique per (repository_id, source_branch) — there is no
// default-branch fallback.
// rootSubdirectory optionally scopes the analysis to a repository-relative
// subdirectory (monorepo support); empty means the whole repository root. It is
// validated here (no traversal) before being snapshotted on the summary and
// forwarded to the worker.
// force bypasses the dedup check; when false and a duplicate is found,
// RunAnalysis returns immediately with RunAnalysisResult.Duplicate set.
// Migration-triggered runs (migrationID != 0) never run the dedup check here
// — they delegate dedup to the worker (branchSHAResolver path).
func (s *Service) RunAnalysis(ctx context.Context, repositoryID, migrationID, ownerUserID uint64, sourceBranch, rootSubdirectory string, force bool) (*RunAnalysisResult, error) {
	if repositoryID == 0 {
		return nil, domain.ErrMissingRepositoryID
	}
	// source_branch is mandatory: analyses are unique per (repository_id,
	// source_branch). There is no longer a default-branch fallback — the caller
	// must declare the branch explicitly.
	if sourceBranch == "" {
		return nil, domain.ErrMissingSourceBranch
	}

	// Validate and canonicalise the monorepo root subdirectory up front so an
	// invalid value (absolute path, traversal) is rejected synchronously rather
	// than failing the async worker job. Empty = whole repository root.
	rootSubdirectory, subErr := domain.NormalizeRootSubdirectory(rootSubdirectory)
	if subErr != nil {
		return nil, subErr
	}

	// Gate: live probe for standalone runs only. Migration-triggered runs
	// (migrationID != 0) were already validated by StartMigration before dispatch.
	if migrationID == 0 && s.repoSvc != nil {
		if probeErr := s.repoSvc.ProbeConnection(ctx, repositoryID); probeErr != nil {
			return nil, probeErr
		}
	}

	// source_branch is mandatory (validated above), so the analysed branch is
	// always the caller-supplied branch — never the repository default.
	branch := sourceBranch
	var remoteURL string
	if s.repoSvc != nil {
		url, _, err := s.repoSvc.GetRemoteURL(ctx, repositoryID)
		if err != nil {
			return nil, err
		}
		remoteURL = url
	}

	// Standalone dedup: before creating a new summary, check whether the branch
	// HEAD matches an existing COMPLETED analysis. Only for standalone runs
	// (migrationID == 0) and when not forced.
	if migrationID == 0 && !force && branch != "" && s.repoSvc != nil {
		headSHA, shaErr := s.repoSvc.GetBranchSHA(ctx, repositoryID, branch)
		if shaErr != nil {
			// Non-fatal: log and continue with normal analysis.
			applog.Warningf("analysis: GetBranchSHA failed repository_id=%d branch=%s: %v — skipping dedup", repositoryID, branch, shaErr)
		} else if headSHA != "" {
			stateCompleted := domain.AnalysisStateCompleted
			existing, _, listErr := s.repo.List(ctx, &analysissvcv1.AnalysisSummariesFilter{
				RepositoryId: &repositoryID,
				SourceBranch: &branch,
				State:        &stateCompleted,
			}, &queryparamsv1.PageQueryParams{PageSize: 1})
			if listErr != nil {
				applog.Warningf("analysis: dedup list failed repository_id=%d: %v — skipping dedup", repositoryID, listErr)
			} else if len(existing) > 0 && existing[0].GetCommitSha() == headSHA {
				applog.Infof("analysis: duplicate found existing_id=%d commit=%s repository_id=%d branch=%s",
					existing[0].GetIdentifier(), headSHA, repositoryID, branch)
				return &RunAnalysisResult{Duplicate: existing[0]}, nil
			}
		}
	}

	// Billing quota gate (hard block, count-based, per-month). Enforced only when
	// a NEW standalone summary would be created: dedup above already returned any
	// duplicate, so reaching here means a new analysis run. We deliberately do NOT
	// count/enforce on:
	//   - migration-triggered runs (migrationID != 0): those are gated by the
	//     migration quota in CreateMigration; counting here too would double-charge.
	//   - forced re-analyses (force == true): a re-run of an existing repo should
	//     not consume a fresh quota slot.
	// Unlimited (-1) plans (enterprise) are never blocked. Enforcement is skipped
	// when no plan provider is wired (degrade open).
	if migrationID == 0 && !force && s.plans != nil && ownerUserID != 0 {
		if qErr := s.enforceAnalysisQuota(ctx, ownerUserID); qErr != nil {
			return nil, qErr
		}
	}

	summary := &domain.AnalysisSummary{
		RepositoryId:     repositoryID,
		MigrationId:      migrationID,
		OwnerUserId:      ownerUserID,
		RepositoryUrl:    remoteURL,
		SourceBranch:     branch,
		RootSubdirectory: rootSubdirectory,
		State:            domain.AnalysisStateRunning,
	}
	created, err := s.repo.Create(ctx, summary)
	if err != nil {
		return nil, err
	}

	if s.enqueuer != nil {
		// Dispatch is best-effort on the hot path; failures are surfaced via the
		// summary's FAILED state transition by the worker, not by this RPC.
		if enqErr := s.enqueuer.EnqueueAnalysis(ctx, created.GetIdentifier(), repositoryID, migrationID, remoteURL, branch, rootSubdirectory); enqErr != nil {
			applog.Warningf("analysis: EnqueueAnalysis failed summary_id=%d: %v", created.GetIdentifier(), enqErr)
		}
	}
	return &RunAnalysisResult{Summary: created}, nil
}

// SelectRoot resolves the project root for an analysis that is awaiting a root
// selection (a monorepo with multiple detected roots). It fails closed:
//   - the analysis must exist and be owned by the caller (enforced by the
//     handler before this call),
//   - rootDirectory must be non-empty and listed in the analysis's
//     root_candidates — any other value is rejected with ErrInvalidRootSelection.
//
// On success it transitions the analysis AWAITING_ROOT_SELECTION → RUNNING with
// the chosen root persisted (and candidates cleared), then re-enqueues the
// analysis scoped to that root (carrying the original repository, migration,
// owner, remote URL and branch snapshotted on the summary). Returns the updated
// summary. Re-enqueue failure is non-fatal to the state transition: the summary
// is RUNNING and a re-dispatch can be triggered, mirroring RunAnalysis semantics.
func (s *Service) SelectRoot(ctx context.Context, identifier uint64, rootDirectory string) (*domain.AnalysisSummary, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	// Normalise/validate the path shape (no traversal/absolute) before the
	// candidate-membership check.
	normalized, err := domain.NormalizeRootSubdirectory(rootDirectory)
	if err != nil {
		return nil, err
	}
	// Awaiting-selection is precisely the ambiguous case: an empty root is never
	// a valid resolution here.
	if normalized == "" {
		return nil, domain.ErrInvalidRootSelection
	}

	current, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if current.GetState() != domain.AnalysisStateAwaitingRootSelection {
		return nil, domain.ErrInvalidRootSelection
	}
	if !containsCandidate(current.GetRootCandidates(), normalized) {
		return nil, domain.ErrInvalidRootSelection
	}

	updated, err := s.repo.MarkRootSelected(ctx, identifier, normalized)
	if err != nil {
		return nil, err
	}

	if s.enqueuer != nil {
		if enqErr := s.enqueuer.EnqueueAnalysis(ctx,
			updated.GetIdentifier(),
			updated.GetRepositoryId(),
			updated.GetMigrationId(),
			updated.GetRepositoryUrl(),
			updated.GetSourceBranch(),
			normalized,
		); enqErr != nil {
			applog.Warningf("analysis: SelectRoot re-enqueue failed summary_id=%d: %v", updated.GetIdentifier(), enqErr)
		}
	}
	return updated, nil
}

// CancelAnalysis transitions a non-terminal analysis to CANCELLED.
//
// It is a soft-cancel: an in-flight worker computation may still finish but its
// result is discarded (the worker only persists a final state while the analysis
// is still RUNNING). A cancel on an already-terminal analysis (COMPLETED, FAILED,
// CANCELLED) is rejected with ErrInvalidStateTransition. Ownership is enforced by
// the handler before this call. Returns the updated summary.
func (s *Service) CancelAnalysis(ctx context.Context, identifier uint64) (*domain.AnalysisSummary, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	current, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	if isTerminalAnalysisState(current.GetState()) {
		return nil, domain.ErrInvalidStateTransition
	}
	if err := s.repo.UpdateState(ctx, identifier, domain.AnalysisStateCancelled); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, identifier, false)
}

// DeleteAnalysisSummary soft-deletes an analysis summary.
//
// Two guards, both fail-closed:
//   - the analysis must be in a terminal state (COMPLETED, FAILED, CANCELLED);
//     deleting a running/awaiting analysis is rejected with ErrInvalidStateTransition.
//   - no active (non-terminal) migration may still reference the analysis;
//     otherwise the delete is rejected with ErrAnalysisHasLiveMigrations so a
//     running migration never loses its analysis. The count comes from the
//     migration service via the MigrationClient port (forwarding the caller's
//     token). When no migration client is wired the guard degrades CLOSED.
//
// Ownership is enforced by the handler before this call.
func (s *Service) DeleteAnalysisSummary(ctx context.Context, identifier uint64) error {
	if identifier == 0 {
		return domain.ErrMissingIdentifier
	}
	current, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return err
	}
	if !isTerminalAnalysisState(current.GetState()) {
		return domain.ErrInvalidStateTransition
	}
	if s.migrations == nil {
		// Degrade closed: without the migration service we cannot prove no live
		// migration depends on this analysis, so refuse rather than risk an orphan.
		applog.Warningf("analysis: DeleteAnalysisSummary refused id=%d — migration client not wired", identifier)
		return domain.ErrInternal
	}
	live, err := s.migrations.CountLiveMigrationsByAnalysis(ctx, identifier)
	if err != nil {
		applog.Warningf("analysis: live-migration count failed id=%d: %v", identifier, err)
		return domain.ErrInternal
	}
	if live > 0 {
		return domain.ErrAnalysisHasLiveMigrations
	}
	return s.repo.SoftDelete(ctx, identifier)
}

// isTerminalAnalysisState reports whether state is a terminal node in the
// analysis lifecycle: COMPLETED, FAILED, or CANCELLED. RUNNING and
// AWAITING_ROOT_SELECTION are non-terminal.
func isTerminalAnalysisState(state domain.AnalysisState) bool {
	switch state {
	case domain.AnalysisStateCompleted,
		domain.AnalysisStateFailed,
		domain.AnalysisStateCancelled:
		return true
	default:
		return false
	}
}

// enforceAnalysisQuota resolves the owner's plan and rejects the operation when
// the analyses-per-month count limit has been reached. Unlimited (-1) plans are
// never blocked. A plan-resolution or count error degrades open (logged, not
// fatal) so a transient billing/store failure never blocks a paying user.
func (s *Service) enforceAnalysisQuota(ctx context.Context, ownerUserID uint64) error {
	plan, err := s.plans.GetUserPlan(ctx, ownerUserID)
	if err != nil {
		applog.Warningf("analysis: plan lookup failed owner_user_id=%d: %v — quota check skipped", ownerUserID, err)
		return nil
	}
	limit := plan.GetMaxAnalysesPerMonth()
	if limit == billingdomain.Unlimited {
		return nil
	}
	since := startOfMonthUTC(time.Now())
	count, err := s.repo.CountByOwnerSince(ctx, ownerUserID, since)
	if err != nil {
		applog.Warningf("analysis: quota count failed owner_user_id=%d: %v — quota check skipped", ownerUserID, err)
		return nil
	}
	if count >= limit {
		applog.Infof("analysis: plan limit reached owner_user_id=%d count=%d limit=%d", ownerUserID, count, limit)
		return domain.NewErrPlanLimitExceeded(limit)
	}
	return nil
}

// startOfMonthUTC returns the first instant of the current calendar month in UTC.
func startOfMonthUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// containsCandidate reports whether choice is one of the persisted candidates.
func containsCandidate(candidates []string, choice string) bool {
	for _, c := range candidates {
		if c == choice {
			return true
		}
	}
	return false
}
