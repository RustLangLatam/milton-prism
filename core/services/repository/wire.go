// Package repository wires the hexagonal repository service onto a gRPC server.
package repository

import (
	"context"

	services "milton_prism/core/internal/svc"
	repositoryapp "milton_prism/core/services/repository/application"
	repositorygrpc "milton_prism/core/services/repository/infrastructure/grpc_handlers"
	repositoryrepo "milton_prism/core/services/repository/infrastructure/repositories"
	"milton_prism/core/services/repository/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	repositorysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"

	"google.golang.org/grpc"
)

// BuildRepositoryServer wires the hexagonal repository application service and
// registers it on server.
func BuildRepositoryServer(ctx context.Context, svc *services.Services, server *grpc.Server) error {
	db := svc.Mongo().GetDatabase()
	mongoClient := svc.Mongo().GetClient()
	cfg := svc.Config()

	repo := repositoryrepo.NewMongoRepositoryRepository(db)
	tx := repositoryrepo.NewMongoTransactionManager(mongoClient)
	gitClient := repositoryrepo.NewNoOpGitClient()

	var identityClient ports.IdentityClient
	if cfg.GrpcServices != nil && cfg.GrpcServices.IdentityClientConfig != nil && cfg.GrpcServices.IdentityClientConfig.Enabled {
		grpcIdentity, err := grpc_client_sdk.NewIdentityGRPCClient(ctx, cfg.GrpcServices.IdentityClientConfig)
		if err != nil {
			return err
		}
		identityClient = repositoryrepo.NewIdentityClientAdapter(grpcIdentity)
	}

	app := repositoryapp.NewService(repo, tx, identityClient, gitClient)
	handler := repositorygrpc.NewRepositoryHandler(app, svc.ExtractUserIDAndRoleFromContext)
	repositorysvcv1.RegisterRepositoryServiceServer(server, handler)
	return nil
}
