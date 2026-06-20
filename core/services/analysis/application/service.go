// Package application contains the analysis service's use-case logic.
// It depends only on domain types and driven port interfaces — never on
// infrastructure packages.
package application

import (
	"context"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	applog "milton_prism/pkg/log"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// Service orchestrates analysis use cases.
type Service struct {
	repo     ports.AnalysisSummaryRepository
	repoSvc  ports.RepositoryClient
	enqueuer ports.JobEnqueuer
	assessor ports.AnalysisMigrabilityAssessor
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
// sourceBranch overrides the repository's default_branch when non-empty.
// force bypasses the dedup check; when false and a duplicate is found,
// RunAnalysis returns immediately with RunAnalysisResult.Duplicate set.
// Migration-triggered runs (migrationID != 0) never run the dedup check here
// — they delegate dedup to the worker (branchSHAResolver path).
func (s *Service) RunAnalysis(ctx context.Context, repositoryID, migrationID, ownerUserID uint64, sourceBranch string, force bool) (*RunAnalysisResult, error) {
	if repositoryID == 0 {
		return nil, domain.ErrMissingRepositoryID
	}

	// Gate: live probe for standalone runs only. Migration-triggered runs
	// (migrationID != 0) were already validated by StartMigration before dispatch.
	if migrationID == 0 && s.repoSvc != nil {
		if probeErr := s.repoSvc.ProbeConnection(ctx, repositoryID); probeErr != nil {
			return nil, probeErr
		}
	}

	var remoteURL, branch string
	if s.repoSvc != nil {
		url, defaultBranch, err := s.repoSvc.GetRemoteURL(ctx, repositoryID)
		if err != nil {
			return nil, err
		}
		remoteURL = url
		if sourceBranch != "" {
			branch = sourceBranch
		} else {
			branch = defaultBranch
		}
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

	summary := &domain.AnalysisSummary{
		RepositoryId:  repositoryID,
		MigrationId:   migrationID,
		OwnerUserId:   ownerUserID,
		RepositoryUrl: remoteURL,
		SourceBranch:  branch,
		State:         domain.AnalysisStateRunning,
	}
	created, err := s.repo.Create(ctx, summary)
	if err != nil {
		return nil, err
	}

	if s.enqueuer != nil {
		// Dispatch is best-effort on the hot path; failures are surfaced via the
		// summary's FAILED state transition by the worker, not by this RPC.
		if enqErr := s.enqueuer.EnqueueAnalysis(ctx, created.GetIdentifier(), repositoryID, migrationID, remoteURL, branch); enqErr != nil {
			applog.Warningf("analysis: EnqueueAnalysis failed summary_id=%d: %v", created.GetIdentifier(), enqErr)
		}
	}
	return &RunAnalysisResult{Summary: created}, nil
}
