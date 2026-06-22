// Package billing wires the hexagonal billing service onto a gRPC server.
//
// The billing capability is co-located on the analysis service's gRPC server
// and shares the analysis database (milton_prism_analysis), where the in-process
// assessment spend is recorded. This keeps the assessment spend instrumentation
// in-process (no hot-path network call) while still exposing a RecordUsage RPC
// for cross-service spend reporting from the migration / generation workers.
package billing

import (
	services "milton_prism/core/internal/svc"
	billingapp "milton_prism/core/services/billing/application"
	billinggrpc "milton_prism/core/services/billing/infrastructure/grpc_handlers"
	billingrepo "milton_prism/core/services/billing/infrastructure/repositories"
	billingsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"

	"google.golang.org/grpc"
)

// BuildBillingServer wires the billing application service and registers it on
// server. It returns the constructed usage repository so the host service can
// share the same writer for in-process spend instrumentation, and the billing
// application service so the co-located analysis service can enforce plan quotas
// in-process (no network hop on the analysis hot path).
func BuildBillingServer(svc *services.Services, server *grpc.Server) (*billingrepo.MongoUsageRepository, *billingapp.Service, error) {
	db := svc.Mongo().GetDatabase()

	usageRepo := billingrepo.NewMongoUsageRepository(db)
	planRepo := billingrepo.NewMongoPlanRepository(db)

	app := billingapp.NewService(usageRepo, planRepo)
	handler := billinggrpc.NewBillingHandler(app, svc.ExtractUserIDAndRoleFromContext)
	billingsvcv1.RegisterBillingServiceServer(server, handler)
	return usageRepo, app, nil
}
