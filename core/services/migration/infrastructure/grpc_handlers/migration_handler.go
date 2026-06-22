// Package grpc_handlers exposes the migration application service as a
// MigrationServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"
	"fmt"

	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	migsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	httpbodypb "google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// MigrationHandler implements migsvcv1.MigrationServiceServer.
type MigrationHandler struct {
	migsvcv1.UnimplementedMigrationServiceServer
	svc         *application.Service
	authExtract AuthExtractor
}

// NewMigrationHandler builds a handler bound to the application service.
func NewMigrationHandler(svc *application.Service, authExtract AuthExtractor) *MigrationHandler {
	return &MigrationHandler{svc: svc, authExtract: authExtract}
}

// suppressOrphanRoadmap drops the restructuring roadmap from the served response
// when the migration's current verdict is INCOMPLETE_NO_STRUCTURAL_DATA. That
// verdict has no score signals, so a current-verdict roadmap carries no structural
// problems or action plan; any roadmap present is a stale blob persisted under an
// earlier verdict (e.g. a previous NOT_MIGRABLE generation) and serving it would
// expose a stale migrability_score and mismatched problems.
//
// This mutates only the in-memory response object; the persisted blob in Mongo is
// left intact. The normal path (MIGRABLE/PARTIAL/NOT_MIGRABLE) is untouched and
// still serves its roadmap.
func suppressOrphanRoadmap(m *migrationv1.Migration) {
	if m == nil || m.GetRestructuringRoadmap() == nil {
		return
	}
	if m.GetMigrabilityAssessment().GetVerdict() == domain.MigrabilityVerdictIncompleteNoStructuralData {
		m.RestructuringRoadmap = nil
	}
}

func (h *MigrationHandler) CreateMigration(ctx context.Context, req *migsvcv1.CreateMigrationRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: CreateMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetMigration() == nil {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	m := req.GetMigration()
	if !isSystem {
		m.OwnerUserId = callerID
	}
	out, err := h.svc.CreateMigration(ctx, m)
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) GetMigration(ctx context.Context, req *migsvcv1.GetMigrationRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: GetMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	suppressOrphanRoadmap(m)
	return m, nil
}

func (h *MigrationHandler) ListMigrations(ctx context.Context, req *migsvcv1.ListMigrationsRequest) (*migsvcv1.ListMigrationsResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: ListMigrations authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	filter := req.GetFilter()
	if filter == nil {
		filter = &migrationv1.MigrationsFilter{}
	}
	if !isSystem {
		filter.OwnerUserId = &callerID
	}
	items, pag, err := h.svc.ListMigrations(ctx, filter, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	for _, m := range items {
		suppressOrphanRoadmap(m)
	}
	return &migsvcv1.ListMigrationsResponse{
		Migrations: items,
		Pagination: pag,
	}, nil
}

func (h *MigrationHandler) DeleteMigration(ctx context.Context, req *migsvcv1.DeleteMigrationRequest) (*emptypb.Empty, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: DeleteMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	if err := h.svc.DeleteMigration(ctx, req.GetIdentifier()); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (h *MigrationHandler) StartMigration(ctx context.Context, req *migsvcv1.StartMigrationRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: StartMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.StartMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) RunMigration(ctx context.Context, req *migsvcv1.RunMigrationRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: RunMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.RunMigration(ctx, req.GetIdentifier(), req.GetServiceFilter())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) ApproveDesign(ctx context.Context, req *migsvcv1.ApproveDesignRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: ApproveDesign authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.ApproveDesign(ctx, req.GetIdentifier(), req.GetApproved(), req.GetServiceFilter())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) CancelMigration(ctx context.Context, req *migsvcv1.CancelMigrationRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: CancelMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.CancelMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) GetGenerationPackage(ctx context.Context, req *migsvcv1.GetGenerationPackageRequest) (*migrationv1.GenerationPackage, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: GetGenerationPackage authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	pkg, err := h.svc.GetGenerationPackage(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return pkg, nil
}

func (h *MigrationHandler) PublishMigration(ctx context.Context, req *migsvcv1.PublishMigrationRequest) (*migsvcv1.PublishMigrationResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: PublishMigration authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetMigrationId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetMigrationId())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	updated, pushedBranch, err := h.svc.PublishMigration(ctx, req.GetMigrationId(), req.GetTargetUrl(), req.GetWriteToken(), req.GetCommitMessage())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &migsvcv1.PublishMigrationResponse{Migration: updated, PushedBranch: pushedBranch}, nil
}

func (h *MigrationHandler) GetGenerationArtifacts(ctx context.Context, req *migsvcv1.GetGenerationArtifactsRequest) (*migsvcv1.GetGenerationArtifactsResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: GetGenerationArtifacts authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetMigrationId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetMigrationId())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	resp, err := h.svc.GetGenerationArtifacts(ctx, req.GetMigrationId(), req.GetServiceName())
	if err != nil {
		return nil, h.mapError(err)
	}
	return resp, nil
}

func (h *MigrationHandler) DownloadDeliverable(ctx context.Context, req *migsvcv1.DownloadDeliverableRequest) (*httpbodypb.HttpBody, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: DownloadDeliverable authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetMigrationId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetMigrationId())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	data, err := h.svc.DownloadDeliverable(ctx, req.GetMigrationId())
	if err != nil {
		return nil, h.mapError(err)
	}
	filename := fmt.Sprintf("deliverable-%d.zip", req.GetMigrationId())
	_ = grpc.SetHeader(ctx, metadata.Pairs(
		"content-disposition", fmt.Sprintf("attachment; filename=%q", filename),
	))
	return &httpbodypb.HttpBody{
		ContentType: "application/zip",
		Data:        data,
	}, nil
}

func (h *MigrationHandler) AssessMigrability(ctx context.Context, req *migsvcv1.AssessMigrabilityRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: AssessMigrability authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.AssessMigrability(ctx, req.GetIdentifier(), req.GetLanguage())
	if err != nil {
		return nil, h.mapError(err)
	}
	suppressOrphanRoadmap(out)
	return out, nil
}

func (h *MigrationHandler) GenerateRestructuringRoadmap(ctx context.Context, req *migsvcv1.GenerateRestructuringRoadmapRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: GenerateRestructuringRoadmap authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.GenerateRestructuringRoadmap(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) SetMigrabilityOverride(ctx context.Context, req *migsvcv1.SetMigrabilityOverrideRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: SetMigrabilityOverride authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.SetMigrabilityOverride(ctx, req.GetIdentifier(), req.GetOverride())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) EnrichRoadmap(ctx context.Context, req *migsvcv1.EnrichRoadmapRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: EnrichRoadmap authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.EnrichRoadmap(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) GenerateBlueprint(ctx context.Context, req *migsvcv1.GenerateBlueprintRequest) (*migrationv1.Migration, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: GenerateBlueprint authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.GenerateBlueprint(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *MigrationHandler) ExportActionPlanPrompt(ctx context.Context, req *migsvcv1.ExportActionPlanPromptRequest) (*httpbodypb.HttpBody, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("migration: ExportActionPlanPrompt authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	m, err := h.svc.GetMigration(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if m.GetOwnerUserId() != callerID && !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	filename, content, err := h.svc.ExportActionPlanPrompt(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs(
		"content-disposition", fmt.Sprintf("attachment; filename=%q", filename),
	))
	return &httpbodypb.HttpBody{
		ContentType: "text/markdown; charset=utf-8",
		Data:        content,
	}, nil
}

func (h *MigrationHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodeMigrationNotFound,
			domain.ErrCodeSourceAnalysisNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeRepositoryNotFound, domain.ErrCodeOwnerNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeForbiddenAccess:
			return coreerror.NewPermissionDeniedError(dErr.Code, dErr.Message)
		case domain.ErrCodeRepoAuthFailed, domain.ErrCodeRepoUnreachable:
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeInvalidStateTransition,
			domain.ErrCodeNoServiceBoundaries,
			domain.ErrCodeNoArtifacts,
			domain.ErrCodePushAuthFailed,
			domain.ErrCodePushConflict,
			domain.ErrCodePushNetworkError,
			domain.ErrCodeArtifactConflict,
			domain.ErrCodeNotMigrableBlocked,
			domain.ErrCodeNoAnalysisSummary,
			domain.ErrCodeRoadmapUnavailable,
			domain.ErrCodeSourceAnalysisInvalid,
			domain.ErrCodeNoRoadmap,
			domain.ErrCodeNoBlueprintAnalysis,
			domain.ErrCodeNoActionPlan,
			domain.ErrCodePlanLimitExceeded:
			return coreerror.NewFailedPreconditionError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingIdentifier,
			domain.ErrCodeMissingPayload,
			domain.ErrCodeMissingOwnerUserID,
			domain.ErrCodeMissingRepositoryID,
			domain.ErrCodeInvalidTargetConfig,
			domain.ErrCodeInvalidRootSubdirectory,
			domain.ErrCodeUnsupportedTargetLanguage:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		case domain.ErrCodeInternal:
			applog.Warningf("internal migration error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("unhandled migration error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	applog.Warningf("unhandled migration error: error=%v", err)
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
