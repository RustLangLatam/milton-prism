package services

import (
	"fmt"
	"sync"

	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/cache_client"
	coreerror "milton_prism/core/shared/error"
	"milton_prism/core/shared/grpc_health"
	"milton_prism/core/shared/interceptors"
	paniccontrol "milton_prism/core/shared/utils"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// Services holds the shared infrastructure clients used across all hexagonal
// service wire functions.
type Services struct {
	config         *config.MicroserviceServerCfg
	cacheClient    *cache_client.CacheClient
	// mongo holds the *mongo_client.MongoClient when the service is configured for
	// MongoDB persistence; it is nil otherwise (e.g. a GORM/SQL deliverable). It is
	// typed `any` so this store-agnostic builder carries NO compile-time dependency
	// on core/shared/mongo_client: the Mongo wiring and the typed Mongo() accessor
	// live in builder_mongo.go, which a Go+SQL deliverable prunes. The field must
	// stay in the struct (a Go struct cannot be extended from another file) but it
	// never names the mongo type here.
	mongo          any
	validatorToken auth_token.TokenValidator
	creatorToken   auth_token.TokenManager
	mu             sync.Mutex
}

// mongoInit wires the MongoDB client into a Services when MongoDB persistence is
// configured. It is registered by builder_mongo.go's init() — the only file that
// imports core/shared/mongo_client. When builder_mongo.go is absent (a pruned
// Go+SQL deliverable) mongoInit stays nil and no Mongo client is built, keeping
// this builder free of any compile-time mongo dependency.
var mongoInit func(*Services) error

// NewServicesFromConfig initialises the shared infrastructure (Redis, MongoDB,
// token services) from the supplied configuration.
func NewServicesFromConfig(cfg *config.MicroserviceServerCfg) (*Services, error) {
	go paniccontrol.PanicCounter(cfg.Server.PanicLimit, func() {
		log.Errorln("Panic limit reached! Server status set to NOT_SERVING.")
		grpc_health.HealthStatus = healthgrpc.HealthCheckResponse_NOT_SERVING
	})

	s := &Services{config: cfg}

	if err := s.initServices(); err != nil {
		return nil, err
	}

	if cfg.Auth != nil {
		switch {
		case cfg.Auth.TokenGeneratorConfig != nil:
			creator, err := auth_token.NewTokenCreator(cfg.Auth.TokenGeneratorConfig, s.cacheClient.TokenBlacklist)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize token creator: %w", err)
			}
			s.creatorToken = creator
			if validator, ok := creator.(auth_token.TokenValidator); ok {
				s.validatorToken = validator
			} else {
				return nil, fmt.Errorf("token creator does not implement TokenValidator")
			}

		case cfg.Auth.TokenValidatorConfig != nil:
			validator, err := auth_token.NewTokenValidator(cfg.Auth.TokenValidatorConfig, s.cacheClient.TokenBlacklist)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize token validator: %w", err)
			}
			s.validatorToken = validator

		default:
			log.Warningln("No token configuration provided. Token services will not be initialized.")
		}
	}

	return s, nil
}

func (s *Services) initServices() error {
	poolCache := cache_client.CreatePoolCache(s.config.Cache)
	if s.config.Cache != nil {
		s.cacheClient = cache_client.NewCacheClient(poolCache)
	}

	// Mongo wiring is registered by builder_mongo.go's init() into mongoInit when
	// that file ships (every Mongo deliverable + the platform monorepo). A Go+SQL
	// deliverable prunes builder_mongo.go, so mongoInit stays nil and no Mongo
	// client is built — this store-agnostic builder never references mongo_client.
	if mongoInit != nil {
		if err := mongoInit(s); err != nil {
			return err
		}
	}

	return nil
}

// NewGRPCServer creates a gRPC server with metrics, tracing, and panic-recovery
// interceptors. The server is not yet started.
func (s *Services) NewGRPCServer(serverOption *config.ServerOptionCgf) (*grpc.Server, *prometheus.Registry, error) {
	srvMetrics := grpcprom.NewServerMetrics(
		grpcprom.WithServerHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets([]float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120}),
		),
		grpcprom.WithContextLabels("tenant_name"),
	)
	reg := prometheus.NewRegistry()
	reg.MustRegister(srvMetrics)

	labelsFromContext := func(ctx context.Context) prometheus.Labels {
		labels := prometheus.Labels{}
		md := metadata.ExtractIncoming(ctx)
		tenantName := md.Get("tenant-name")
		if tenantName == "" {
			tenantName = "unknown"
		}
		labels["tenant_name"] = tenantName
		return labels
	}

	grpcSrv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.MaxRecvMsgSize(serverOption.MaxRecvMsgSizeMB),
		grpc.MaxSendMsgSize(serverOption.MaxSendMsgSizeMB),
		grpc.ChainUnaryInterceptor(
			interceptors.CtxIdUnaryInterceptor,
			interceptors.LogUnaryInterceptor,
			interceptors.PanicRecoveryInterceptor(reg),
			srvMetrics.UnaryServerInterceptor(
				grpcprom.WithLabelsFromContext(labelsFromContext),
			),
		),
	)

	srvMetrics.InitializeMetrics(grpcSrv)

	return grpcSrv, reg, nil
}

// Config returns the microservice configuration.
func (s *Services) Config() *config.MicroserviceServerCfg { return s.config }

// Cache returns the Redis cache client.
func (s *Services) Cache() *cache_client.CacheClient { return s.cacheClient }

// CreatorToken returns the token manager (nil when the service runs as a
// validator-only role).
func (s *Services) CreatorToken() auth_token.TokenManager { return s.creatorToken }

// ExtractUserIDFromContext validates the access token in ctx and returns the
// authenticated user's identifier.
func (s *Services) ExtractUserIDFromContext(ctx context.Context) (uint64, error) {
	props, err := s.verifyAccessTokenAndSession(ctx)
	if err != nil {
		return 0, err
	}
	return props.Identifier, nil
}

// ExtractUserIDAndRoleFromContext validates the access token in ctx and returns
// the authenticated user's identifier and whether the caller is a system/admin
// user. Used by the users handler for ownership enforcement.
func (s *Services) ExtractUserIDAndRoleFromContext(ctx context.Context) (uint64, bool, error) {
	props, err := s.verifyAccessTokenAndSession(ctx)
	if err != nil {
		return 0, false, err
	}
	return props.Identifier, props.SystemUser, nil
}

// ExtractSessionInfoFromContext validates the access token in ctx and returns
// the user ID, session ID, and raw token string — used by Logout to perform
// full session revocation.
func (s *Services) ExtractSessionInfoFromContext(ctx context.Context) (userID uint64, sessionID string, rawToken string, err error) {
	props, verr := s.verifyAccessTokenAndSession(ctx)
	if verr != nil {
		return 0, "", "", verr
	}
	token, terr := auth_token.ExtractTokenFromContext(ctx, auth_token.TokenAccessName)
	if terr != nil {
		return 0, "", "", terr
	}
	return props.Identifier, props.SessionId, *token, nil
}

// ExtractRefreshInfoFromContext validates the refresh token in ctx and returns
// the session ID and raw token — used by RefreshUserToken.
func (s *Services) ExtractRefreshInfoFromContext(ctx context.Context) (sessionID string, rawToken string, err error) {
	token, terr := auth_token.ExtractTokenFromContext(ctx, auth_token.TokenRefreshName)
	if terr != nil {
		return "", "", terr
	}
	claims := Claims{}
	if _, verr := s.validatorToken.Verify(*token, true, &claims); verr != nil {
		return "", "", coreerror.TokenValidationErrorInvalid
	}
	return claims.SessionId, *token, nil
}
