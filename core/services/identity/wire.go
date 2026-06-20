// Package identity wires the hexagonal identity service onto a gRPC server.
package identity

import (
	services "milton_prism/core/internal/svc"
	identityapp "milton_prism/core/services/identity/application"
	identitygrpc "milton_prism/core/services/identity/infrastructure/grpc_handlers"
	identityrepo "milton_prism/core/services/identity/infrastructure/repositories"
	"milton_prism/core/services/identity/ports"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"

	"google.golang.org/grpc"
)

// BuildIdentityServer wires the hexagonal identity application service and
// registers it on server.
func BuildIdentityServer(svc *services.Services, server *grpc.Server) error {
	db := svc.Mongo().GetDatabase()
	mongoClient := svc.Mongo().GetClient()
	cache := svc.Cache()
	cfg := svc.Config()

	repo := identityrepo.NewMongoUserRepository(db)
	tx := identityrepo.NewMongoTransactionManager(mongoClient)
	hasher := identityrepo.NewArgon2Hasher()

	var tokenMgr ports.TokenManager
	if creator := svc.CreatorToken(); creator != nil && cfg.Auth.TokenGeneratorConfig != nil {
		tokenMgr = identityrepo.NewTokenManagerAdapter(creator, cache.TokenBlacklist)
	}

	var sessionStore ports.SessionStore
	if cache != nil {
		sessionStore = identityrepo.NewSessionStoreAdapter(cache)
	}

	app := identityapp.NewService(repo, tx, hasher, tokenMgr, sessionStore)
	handler := identitygrpc.NewIdentityHandler(app, svc.ExtractUserIDAndRoleFromContext, svc.ExtractSessionInfoFromContext)
	identitysvcv1.RegisterIdentityServiceServer(server, handler)
	return nil
}
