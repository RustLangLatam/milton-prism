// Package grpc_handlers exposes the billing application service as a
// BillingServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"

	"milton_prism/core/services/billing/application"
	"milton_prism/core/services/billing/domain"
	"milton_prism/core/services/billing/ports"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	billingsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// BillingHandler implements billingsvcv1.BillingServiceServer.
type BillingHandler struct {
	billingsvcv1.UnimplementedBillingServiceServer
	svc         *application.Service
	authExtract AuthExtractor
}

// NewBillingHandler builds a handler bound to the application service.
func NewBillingHandler(svc *application.Service, authExtract AuthExtractor) *BillingHandler {
	return &BillingHandler{svc: svc, authExtract: authExtract}
}

// RecordUsage persists a usage record. System-user only.
func (h *BillingHandler) RecordUsage(ctx context.Context, req *billingsvcv1.RecordUsageRequest) (*billingv1.UsageRecord, error) {
	_, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("billing: RecordUsage authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if !isSystem {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingPayload, "Failure_System_User_Required")
	}
	if req.GetUsageRecord() == nil {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	out, err := h.svc.RecordUsage(ctx, req.GetUsageRecord())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

// ListUsageRecords returns raw usage records. Non-system callers are scoped to
// their own user_id regardless of the requested filter.
func (h *BillingHandler) ListUsageRecords(ctx context.Context, req *billingsvcv1.ListUsageRecordsRequest) (*billingsvcv1.ListUsageRecordsResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("billing: ListUsageRecords authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	filter := ports.UsageFilter{
		UserID:      req.GetUserId(),
		AnalysisID:  req.GetAnalysisId(),
		MigrationID: req.GetMigrationId(),
	}
	if !isSystem {
		filter.UserID = callerID // scope non-system callers to their own records
	}
	records, pagination, err := h.svc.ListUsageRecords(ctx, filter, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &billingsvcv1.ListUsageRecordsResponse{
		UsageRecords: records,
		Pagination:   pagination,
	}, nil
}

// GetUserUsage aggregates usage for a user. Non-system callers may only query
// their own usage.
func (h *BillingHandler) GetUserUsage(ctx context.Context, req *billingsvcv1.GetUserUsageRequest) (*billingsvcv1.UsageAggregateResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("billing: GetUserUsage authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetUserId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingUserID, domain.ErrMissingUserID.Message)
	}
	if !isSystem && req.GetUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingUserID, domain.ErrMissingUserID.Message)
	}
	return h.aggregate(ctx, ports.UsageFilter{UserID: req.GetUserId()})
}

// GetAnalysisUsage aggregates usage for a single analysis.
func (h *BillingHandler) GetAnalysisUsage(ctx context.Context, req *billingsvcv1.GetAnalysisUsageRequest) (*billingsvcv1.UsageAggregateResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("billing: GetAnalysisUsage authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetAnalysisId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	return h.aggregate(ctx, ports.UsageFilter{AnalysisID: req.GetAnalysisId()})
}

// GetMigrationUsage aggregates usage for a single migration.
func (h *BillingHandler) GetMigrationUsage(ctx context.Context, req *billingsvcv1.GetMigrationUsageRequest) (*billingsvcv1.UsageAggregateResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("billing: GetMigrationUsage authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetMigrationId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	return h.aggregate(ctx, ports.UsageFilter{MigrationID: req.GetMigrationId()})
}

func (h *BillingHandler) aggregate(ctx context.Context, filter ports.UsageFilter) (*billingsvcv1.UsageAggregateResponse, error) {
	total, byOp, err := h.svc.AggregateUsage(ctx, filter)
	if err != nil {
		return nil, h.mapError(err)
	}
	return &billingsvcv1.UsageAggregateResponse{
		Total:       total,
		ByOperation: byOp,
	}, nil
}

// ListPlans returns the plan catalog. Authentication is required but any
// authenticated caller may read the catalog.
func (h *BillingHandler) ListPlans(ctx context.Context, _ *billingsvcv1.ListPlansRequest) (*billingsvcv1.ListPlansResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("billing: ListPlans authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	return &billingsvcv1.ListPlansResponse{Plans: h.svc.ListPlans(ctx)}, nil
}

// GetUserPlan returns the plan a user is on. Non-system callers may only query
// their own plan.
func (h *BillingHandler) GetUserPlan(ctx context.Context, req *billingsvcv1.GetUserPlanRequest) (*billingv1.Plan, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("billing: GetUserPlan authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetUserId() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingUserID, domain.ErrMissingUserID.Message)
	}
	if !isSystem && req.GetUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeMissingUserID, domain.ErrMissingUserID.Message)
	}
	plan, err := h.svc.GetUserPlan(ctx, req.GetUserId())
	if err != nil {
		return nil, h.mapError(err)
	}
	return plan, nil
}

func (h *BillingHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodePlanNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingPayload, domain.ErrCodeMissingUserID, domain.ErrCodeMissingIdentifier:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("billing: mapError: unhandled domain error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
