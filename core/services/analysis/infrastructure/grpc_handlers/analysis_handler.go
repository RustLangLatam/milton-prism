// Package grpc_handlers exposes the analysis application service as an
// AnalysisServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"

	"milton_prism/core/services/analysis/application"
	"milton_prism/core/services/analysis/domain"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	anlsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// AnalysisHandler implements anlsvcv1.AnalysisServiceServer.
type AnalysisHandler struct {
	anlsvcv1.UnimplementedAnalysisServiceServer
	svc         *application.Service
	authExtract AuthExtractor
}

// NewAnalysisHandler builds a handler bound to the application service.
func NewAnalysisHandler(svc *application.Service, authExtract AuthExtractor) *AnalysisHandler {
	return &AnalysisHandler{svc: svc, authExtract: authExtract}
}

func (h *AnalysisHandler) GetAnalysisSummary(ctx context.Context, req *anlsvcv1.GetAnalysisSummaryRequest) (*analysisv1.AnalysisSummary, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: GetAnalysisSummary authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	s, err := h.svc.GetAnalysisSummary(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && s.GetOwnerUserId() != callerID {
		return nil, h.mapError(domain.ErrAnalysisSummaryNotFound)
	}
	return s, nil
}

func (h *AnalysisHandler) ListAnalysisSummaries(ctx context.Context, req *anlsvcv1.ListAnalysisSummariesRequest) (*anlsvcv1.ListAnalysisSummariesResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: ListAnalysisSummaries authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	filter := req.GetFilter()
	if filter == nil {
		filter = &anlsvcv1.AnalysisSummariesFilter{}
	}
	if !isSystem {
		filter.OwnerUserId = &callerID
	}
	items, pag, err := h.svc.ListAnalysisSummaries(ctx, filter, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &anlsvcv1.ListAnalysisSummariesResponse{
		AnalysisSummaries: items,
		Pagination:        pag,
	}, nil
}

func (h *AnalysisHandler) RunAnalysis(ctx context.Context, req *anlsvcv1.RunAnalysisRequest) (*anlsvcv1.RunAnalysisResponse, error) {
	callerID, _, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: RunAnalysis authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetRepositoryId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingRepositoryID, domain.ErrMissingRepositoryID.Message)
	}
	result, err := h.svc.RunAnalysis(ctx, req.GetRepositoryId(), req.GetMigrationId(), callerID, req.GetSourceBranch(), req.GetRootSubdirectory(), req.GetForce())
	if err != nil {
		return nil, h.mapError(err)
	}
	if result.Duplicate != nil {
		return &anlsvcv1.RunAnalysisResponse{
			DuplicateFound:   true,
			ExistingAnalysis: result.Duplicate,
		}, nil
	}
	return &anlsvcv1.RunAnalysisResponse{
		AnalysisSummary: result.Summary,
	}, nil
}

func (h *AnalysisHandler) EvaluateMigrability(ctx context.Context, req *anlsvcv1.EvaluateMigrabilityRequest) (*commonv1.MigrabilityAssessment, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: EvaluateMigrability authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	s, err := h.svc.GetAnalysisSummary(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && s.GetOwnerUserId() != callerID {
		return nil, h.mapError(domain.ErrAnalysisSummaryNotFound)
	}
	assessment, err := h.svc.AssessMigrability(ctx, req.GetIdentifier(), req.GetLanguage())
	if err != nil {
		return nil, h.mapError(err)
	}
	return assessment, nil
}

// SelectRoot resolves the project root for an analysis awaiting a root
// selection. Auth + ownership are enforced here (mirroring EvaluateMigrability):
// the caller must own the analysis unless it is a system user.
func (h *AnalysisHandler) SelectRoot(ctx context.Context, req *anlsvcv1.SelectRootRequest) (*analysisv1.AnalysisSummary, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: SelectRoot authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	s, err := h.svc.GetAnalysisSummary(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && s.GetOwnerUserId() != callerID {
		// Hide existence from non-owners, same as GetAnalysisSummary.
		return nil, h.mapError(domain.ErrAnalysisSummaryNotFound)
	}
	updated, err := h.svc.SelectRoot(ctx, req.GetIdentifier(), req.GetRootDirectory())
	if err != nil {
		return nil, h.mapError(err)
	}
	return updated, nil
}

// CancelAnalysis transitions a non-terminal analysis to CANCELLED. Auth +
// ownership are enforced here (mirroring SelectRoot): the caller must own the
// analysis unless it is a system user.
func (h *AnalysisHandler) CancelAnalysis(ctx context.Context, req *anlsvcv1.CancelAnalysisRequest) (*analysisv1.AnalysisSummary, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: CancelAnalysis authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	s, err := h.svc.GetAnalysisSummary(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && s.GetOwnerUserId() != callerID {
		return nil, h.mapError(domain.ErrAnalysisSummaryNotFound)
	}
	updated, err := h.svc.CancelAnalysis(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return updated, nil
}

// DeleteAnalysisSummary soft-deletes a terminal analysis summary with no active
// migration referencing it. Auth + ownership are enforced here.
func (h *AnalysisHandler) DeleteAnalysisSummary(ctx context.Context, req *anlsvcv1.DeleteAnalysisSummaryRequest) (*emptypb.Empty, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("analysis: DeleteAnalysisSummary authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	s, err := h.svc.GetAnalysisSummary(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && s.GetOwnerUserId() != callerID {
		return nil, h.mapError(domain.ErrAnalysisSummaryNotFound)
	}
	if err := h.svc.DeleteAnalysisSummary(ctx, req.GetIdentifier()); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (h *AnalysisHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodeAnalysisSummaryNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeRepositoryNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeRepoAuthFailed, domain.ErrCodeRepoUnreachable:
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeNoDeepData:
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodePlanLimitExceeded:
			// Hard block: the owner's monthly analysis quota is exhausted. The
			// client must upgrade the plan or wait for the next billing month.
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeInvalidRootSelection:
			// Wrong state or unlisted/empty choice: a precondition the client must fix.
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeAnalysisAlreadyExists:
			// Unique-index collision: another analysis already covers this repo+branch.
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeAnalysisHasLiveMigrations:
			// An active migration still depends on this analysis: a precondition
			// the client must resolve (cancel/finish the migration) before deleting.
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeInvalidStateTransition:
			// Cancel on a terminal analysis, or delete on a non-terminal one.
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingIdentifier, domain.ErrCodeMissingRepositoryID, domain.ErrCodeInvalidRootSubdirectory, domain.ErrCodeMissingSourceBranch:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		case domain.ErrCodeInternal:
			applog.Warningf("internal analysis error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("unhandled analysis error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	applog.Warningf("unhandled analysis error: error=%v", err)
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
