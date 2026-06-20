// Package articles wires the hexagonal articles service onto a gRPC server.
package articles

import (
	"context"

	services "milton_prism/core/internal/svc"
	articlesapp "milton_prism/core/services/articles/application"
	articlesgrpc "milton_prism/core/services/articles/infrastructure/grpc_handlers"
	articlesrepo "milton_prism/core/services/articles/infrastructure/repositories"
	articlessvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/articles/v1"

	"google.golang.org/grpc"
)

// BuildArticleServer wires the hexagonal articles application service and
// registers it on server.
func BuildArticleServer(ctx context.Context, svc *services.Services, server *grpc.Server) error {
	db := svc.Mongo().GetDatabase()
	mongoClient := svc.Mongo().GetClient()

	articleRepo := articlesrepo.NewMongoArticleRepository(db)
	tagRepo := articlesrepo.NewMongoTagRepository(db)
	tx := articlesrepo.NewMongoTransactionManager(mongoClient)
	profileClient := articlesrepo.NewNoOpProfileClient()

	app := articlesapp.NewService(articleRepo, tagRepo, tx, profileClient)
	handler := articlesgrpc.NewArticleHandler(app, svc.ExtractUserIDAndRoleFromContext)
	articlessvcv1.RegisterArticleServiceServer(server, handler)
	return nil
}
