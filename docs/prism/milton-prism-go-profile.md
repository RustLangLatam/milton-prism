# Milton Prism — Go Language Profile

**The Go mechanism layer. Composes with the Architecture Canon.**

This Profile fills in the *mechanisms* the Canon deliberately leaves open, for the **Go + MongoDB + gRPC** target. It is distilled from working reference code, not invented. Read it together with the Canon: where the Canon states a principle and this Profile states a mechanism, both apply; if they conflict, the Canon wins.

Placeholders used throughout: `<module>` is the Go module path; `<service>` is the service name (snake_case); `Foo`/`<Resource>` is a sample resource.

> **Holes in this Profile (v1).** MongoDB and gRPC are filled from real code. **PostgreSQL and NATS adapters are not** — section 15 fixes their *shape* only. A generator MUST NOT improvise them; if a project requires them, it stops and reports, per Canon §0.

---

## 1. Module and repository layout

The output is a **single Go module monorepo**. The Canon's conceptual layers map to these concrete directories:

```
<module>/
├── go.mod                          # module path == <module>
├── core/
│   ├── cmd/                        # one entrypoint per service: <service>-services/main.go
│   ├── internal/
│   │   └── svc/                    # the shared Services infrastructure container
│   ├── services/
│   │   └── <service>/
│   │       ├── domain/             # type aliases + errors.go
│   │       ├── ports/              # repository interface(s) + transaction_manager.go
│   │       ├── application/        # service.go (use cases) + service_test.go
│   │       ├── infrastructure/
│   │       │   ├── grpc_handlers/  # <resource>_handler.go (+ _test.go)
│   │       │   └── repositories/   # mongo_<resource>_repository.go, mongo_transaction_manager.go, identifier.go
│   │       ├── mocks/              # mock_<resource>_repository.go (testify)
│   │       └── wire.go             # single composition point
│   └── shared/                     # cross-cutting packages (§10, §11, §12)
├── protobuf/
│   ├── proto/<module>/services/<service>/v1/
│   ├── proto/<module>/types/<domain>/v1/
│   ├── buf.yaml                    # lint/breaking config
│   ├── buf.go.gen.yaml             # Go codegen
│   └── buf.docs.gen.yaml           # OpenAPI emission (feeds openapi-generator-cli, Canon §7)
├── pkg/
│   ├── pb/gen/                     # protoc/buf output — NEVER edited by hand, never in a diff
│   ├── config/                     # microservice configuration
│   ├── log/                        # the applog package (§14)
│   └── gateway/common/error/       # per-service friendly message maps (Canon §4.4)
└── api-gateway/                    # HTTP → gRPC gateway (grpc-gateway)
```

---

## 2. Naming conventions

| Element | Convention | Example |
|---------|-----------|---------|
| Files | snake_case | `foo_repository.go`, `mongo_foo_repository.go` |
| Packages | lowercase, no underscores | `application`, `grpchandlers`, `repositories` |
| Exported types | PascalCase | `FooRepository`, `MongoFooRepository` |
| Exported funcs | PascalCase | `NewFooHandler`, `BuildFooServer` |
| Unexported | camelCase | `mapError`, `validFoo`, `generateIdentifier` |
| Mock types | `Mock<Entity>Repository` | `MockFooRepository` |
| Test funcs | `Test<Operation>_<Scenario>` | `TestCreate_OK`, `TestGet_NotFound` |
| Fixture helpers | `valid<Entity>()` | `validFoo()` |

Go baseline: **Go 1.22+**. `context.Context` is always the first parameter and is never stored in a struct. `panic`, `log.Fatal`, `os.Exit` are forbidden outside `main`. Block comments `/* */` are forbidden except package godoc with a real justification.

---

## 3. Domain layer

Domain types are **type aliases** of the generated proto types — never parallel structs. Re-export the enum/state constants for ergonomic use.

```go
// domain/domain.go
package domain

import foov1 "<module>/pkg/pb/gen/<module>/types/foo/v1"

type (
    Foo       = foov1.Foo
    FooFilter = foov1.FoosFilter
    FooState  = foov1.FooState
)

const (
    FooStateUnspecified = foov1.FooState_FOO_STATE_UNSPECIFIED
    FooStateActive      = foov1.FooState_FOO_STATE_ACTIVE
    FooStateArchived    = foov1.FooState_FOO_STATE_ARCHIVED
)
```

### Typed errors (`domain/errors.go`)

```go
package domain

import "fmt"

type Error struct {
    Code    string
    Message string
}

func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }

const (
    ErrCodeMissingIdentifier = "<PREFIX>101" // prefix assigned by the orchestrator registry (Canon §4.2)
    ErrCodeMissingPayload    = "<PREFIX>102"
    ErrCodeFooNotFound       = "<PREFIX>201"
    ErrCodeFooAlreadyExists  = "<PREFIX>202"
    ErrCodeInternal          = "<PREFIX>500"
)

var (
    ErrMissingIdentifier = &Error{Code: ErrCodeMissingIdentifier, Message: "Failure_Missing_Identifier"}
    ErrMissingPayload    = &Error{Code: ErrCodeMissingPayload,    Message: "Failure_Missing_Payload"}
    ErrFooNotFound       = &Error{Code: ErrCodeFooNotFound,       Message: "Failure_Foo_Not_Found"}
    ErrFooAlreadyExists  = &Error{Code: ErrCodeFooAlreadyExists,  Message: "Failure_Foo_Already_Exists"}
    ErrInternal          = &Error{Code: ErrCodeInternal,          Message: "Failure_Internal"}
)
```

The `Message` value MUST follow `Failure_Noun_Descriptor` (Canon §4.1). The numeric prefix is requested from the orchestrator's registry, never hardcoded.

---

## 4. Ports layer

A repository is a Go interface over domain types. The CRUD shape from the Canon is the **baseline**; real services extend it (soft-delete flags, pagination, nested-resource operations) as the contract requires.

```go
// ports/foo_repository.go
type FooRepository interface {
    Create(ctx context.Context, foo *domain.Foo) (uint64, error)
    GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Foo, error)
    List(ctx context.Context, filter *domain.FooFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Foo, *paginationv1.Pagination, error)
    Update(ctx context.Context, foo *domain.Foo) error
    Delete(ctx context.Context, identifier uint64, hardDelete bool) error
}
```

The transaction boundary uses the closure signature (this is the Go mechanism for Canon §5.2):

```go
// ports/transaction_manager.go
type TransactionManager interface {
    WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
```

---

## 5. Application layer

```go
// application/service.go
type Service struct {
    repo      ports.FooRepository
    txManager ports.TransactionManager // may be nil; methods that need it check explicitly
}

func NewService(repo ports.FooRepository, txManager ports.TransactionManager) *Service {
    return &Service{repo: repo, txManager: txManager}
}
```

Rules (Go mechanics for the Canon's business-logic placement):

- All validation, state defaults, and business rules live here.
- Wrap to preserve the error chain: `fmt.Errorf("%w: %v", domain.ErrInternal, err)`.
- Compare typed errors with `errors.Is(err, domain.ErrFooAlreadyExists)` — never string comparison, never `==` on error values.
- Propagate the received `ctx`. **Never** `context.Background()` outside `main`/bootstrap.
- Update honors the FieldMask path-by-path and handles the nil/empty mask as `"*"` (Canon §2.4).

---

## 6. Infrastructure — transport handlers (gRPC)

The handler is the driving adapter. It embeds the generated `Unimplemented...Server`, receives the application service, and receives auth as an **injected function** so it stays independent of the auth stack.

```go
// infrastructure/grpc_handlers/foo_handler.go
type AuthExtractor func(ctx context.Context) (uint64, error)

type FooHandler struct {
    foov1.UnimplementedFooServiceServer
    svc         *application.Service
    authExtract AuthExtractor
}

func NewFooHandler(svc *application.Service, authExtract AuthExtractor) *FooHandler {
    return &FooHandler{svc: svc, authExtract: authExtract}
}

func (h *FooHandler) GetFoo(ctx context.Context, req *foov1.GetFooRequest) (*foov1.Foo, error) {
    if _, err := h.authExtract(ctx); err != nil {
        applog.Warningf("<service>: GetFoo authentication failed: error=%v", err)
        return nil, coreerror.TokenValidationErrorInvalid
    }
    if req == nil || req.GetIdentifier() == 0 {
        return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
    }
    foo, err := h.svc.Get(ctx, req.GetIdentifier())
    if err != nil {
        return nil, h.mapError(err)
    }
    return foo, nil
}
```

- Direct `coreerror.New*Error(...)` (or `status.Error`) is allowed **only** for request-field validation before delegating. All domain errors go through `mapError`.
- `mapError` is a **method on the handler** and uses `errors.As` to unwrap the typed domain error:

```go
func (h *FooHandler) mapError(err error) error {
    if err == nil {
        return nil
    }
    var dErr *domain.Error
    if errors.As(err, &dErr) {
        switch dErr.Code {
        case domain.ErrCodeFooNotFound:
            return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
        case domain.ErrCodeFooAlreadyExists:
            return coreerror.NewAlreadyExistsError(dErr.Code, dErr.Message)
        case domain.ErrCodeMissingIdentifier, domain.ErrCodeMissingPayload:
            return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
        case domain.ErrCodeInternal:
            applog.Warningf("internal <service> error: code=%s error=%v", dErr.Code, err)
            return coreerror.NewInternalError(dErr.Code, dErr.Message)
        }
    }
    applog.Warningf("unhandled <service> error: error=%v", err)
    return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
```

### `coreerror` constructors (in `core/shared/error`)

All take `(codeError, msg string)` and return `error`:

```
NewInvalidArgumentError   NewNotFoundError        NewAlreadyExistsError
NewPermissionDeniedError  NewUnauthenticatedError NewInternalError
NewAbortedError           NewFailedPreconditionError
NewOutOfRangeError        NewResourceExhaustedError
```

Each new error code also gets a friendly-message entry in `pkg/gateway/common/error/<service>_errors.go` (Canon §4.4).

---

## 7. Infrastructure — MongoDB adapters

```go
// infrastructure/repositories/mongo_foo_repository.go
var _ ports.FooRepository = (*MongoFooRepository)(nil) // compile-time interface check

type MongoFooRepository struct {
    db *mongo.Database
}

func NewMongoFooRepository(db *mongo.Database) *MongoFooRepository {
    return &MongoFooRepository{db: db}
}
```

### Transaction manager (adapter over `UseSession`, nil-safe)

```go
// infrastructure/repositories/mongo_transaction_manager.go
var _ ports.TransactionManager = (*MongoTransactionManager)(nil)

type MongoTransactionManager struct{ client *mongo.Client }

func NewMongoTransactionManager(client *mongo.Client) *MongoTransactionManager {
    if client == nil {
        return nil // callers that need no transactional boundary omit the dependency
    }
    return &MongoTransactionManager{client: client}
}

func (m *MongoTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
    if m == nil || m.client == nil {
        return fn(ctx)
    }
    return m.client.UseSession(ctx, func(sc mongo.SessionContext) error { return fn(sc) })
}
```

### Identifier generation (Mongo mechanism for Canon §5.3)

A dedicated `system_counters` collection backs per-collection monotonic `uint64` IDs via `FindOneAndUpdate` with `$inc` (`SetReturnDocument(After)`), seeding at a reserved start value and retrying on duplicate-key. The result is conflict-free and consistent across the service. *(The Postgres equivalent — sequence or identity column — is a hole, §15.)*

---

## 8. Wire — the single composition point

`wire.go` is the **only** file that constructs the graph (Canon §1.2). One exported builder per service:

```go
// wire.go
func BuildFooServer(svc *services.Services, server *grpc.Server) error {
    repo    := repositories.NewMongoFooRepository(svc.Mongo().GetDatabase())
    tx      := repositories.NewMongoTransactionManager(svc.Mongo().GetClient())
    app     := application.NewService(repo, tx)
    handler := grpc_handlers.NewFooHandler(app, svc.ExtractUserIDFromContext)
    foov1.RegisterFooServiceServer(server, handler)
    return nil
}
```

`svc *services.Services` (next section) exposes the shared infrastructure via accessors.

---

## 9. Shared infrastructure container (`core/internal/svc`)

A single `Services` struct holds config and lazily-initialized clients. A client is built **only if its config section is present**.

```go
type Services struct {
    config         *config.MicroserviceServerCfg
    cacheClient    *cache_client.CacheClient
    mongo          *mongo_client.MongoClient
    validatorToken auth_token.TokenValidator
    creatorToken   auth_token.TokenManager
    mu             sync.Mutex
}

func NewServicesFromConfig(cfg *config.MicroserviceServerCfg) (*Services, error) {
    s := &Services{config: cfg}
    if err := s.initServices(); err != nil { // builds cache if cfg.Cache != nil, mongo if cfg.Mongo != nil
        return nil, err
    }
    // token creator/validator selected by cfg.Auth shape
    return s, nil
}

// Accessors: Config() Mongo() Cache() CreatorToken()
// Auth helpers used by handlers: ExtractUserIDFromContext(ctx) (uint64, error), and role/session variants.
```

`NewGRPCServer` builds the server with the **standard interceptor chain** (order matters): `CtxIdUnaryInterceptor → LogUnaryInterceptor → PanicRecoveryInterceptor → metrics`, plus OTel stats handler and configured max message sizes. A new infrastructure client is added here by (1) adding a config section, (2) constructing it in `initServices` guarded by config presence, (3) exposing an accessor.

---

## 10. Client builders (`core/shared/<x>_client`)

Each infrastructure client is one package with a `builder.go`. The Mongo builder is the template every new client follows:

- A struct holding config + the underlying client, connected once via `sync.Once`.
- Connection options derived from typed config (pool min/max, timeouts, retry, heartbeat).
- A `Ping` on connect to fail fast.
- Lifecycle methods: an accessor for the usable handle (`GetDatabase()`/`GetClient()`) and `Disconnect(ctx)`.

This shape is what the Postgres and NATS holes (§15) must follow.

---

## 11. Inter-service gRPC clients (`core/shared/grpc_client_sdk`)

One file per target service (`grpc_<service>_client.go`) plus a `builder.go` holding:

- `ConnectParams` with exponential backoff (base/multiplier/jitter/max).
- A per-call credentials type that forwards access token, refresh token, and ctx-id via gRPC metadata.
- Insecure transport (`RequireTransportSecurity() == false`) — this is an internal mesh; transport security is terminated at the mesh/gateway edge.

This is the Go realization of the Canon's synchronous-gRPC inter-service rule (Canon §6.1).

---

## 12. Service lifecycle and entrypoint

Lifecycle is coordinated with `oklog/run`: the gRPC server, the metrics HTTP server, the HTTP gRPC-Gateway, and a signal handler each run as a group actor with a matching graceful-stop function. The `main.go` bootstrap sequence is fixed:

```go
func main() {
    log.InitLogger("microservice")
    cfg, err := config.LoadMicroserviceCfg(config.TokenRoleValidator, nil)            // load
    // cfg.ValidateWithFlags(config.RequiredFields{ RequireAuth, RequireMongoDb, ... }) // validate per service needs
    newServices, err := services.NewServicesFromConfig(cfg)                            // shared infra
    grpcSrv, metricsReg, err := newServices.NewGRPCServer(cfg.Server.ServerOptionCgf)  // server + interceptors
    grpc_health.SetupHealthCheck(grpcSrv, nil)                                          // health
    foo.BuildFooServer(newServices, grpcSrv)                                            // wire this service
    serverGroup := services.NewServerGroup(cfg, grpcSrv, metricsReg,
        foov1.RegisterFooServiceHandlerFromEndpoint, "/health:<service>")               // gateway register fn + health path
    serverGroup.Run()                                                                   // block until signal
}
```

---

## 13. Logging

The **only** permitted logger is the project `applog` package (`<module>/pkg/log`), used package-level.

```go
import applog "<module>/pkg/log"

applog.Warningf("mapError: unhandled domain error: code=%s error=%v", dErr.Code, err)
```

Available: `Info/Infof`, `Warning/Warningf`, `Error/Errorf`, `Fatal/Fatalf` (+ `*ln` variants). Format is structured: `"<context>: key=value key=value"`. **Forbidden:** stdlib `log`, `log/slog`, `fmt.Print*`, `print`/`println` as diagnostics.

---

## 14. Testing

testify-based, mechanism for the Canon's testing philosophy (Canon §8).

- Tests live in a **separate `_test` package** (e.g. `application_test`).
- `t.Parallel()` in every unit test, no exception.
- Mocks embed `mock.Mock`, live in `mocks/`, and carry compile-time interface checks:

```go
var _ ports.FooRepository = (*MockFooRepository)(nil)

type MockFooRepository struct{ mock.Mock }

func (m *MockFooRepository) Create(ctx context.Context, f *domain.Foo) (uint64, error) {
    args := m.Called(ctx, f)
    return args.Get(0).(uint64), args.Error(1)
}
// pointer returns use the nil-safe assertion: v, _ := args.Get(0).(*domain.Foo)
```

- Verify typed errors with `assert.ErrorIs(t, err, domain.ErrFooNotFound)` — never string match.
- MongoDB is **not** mocked; repositories get integration tests against a real database.
- Coverage floor (Canon §8): every exported application method has `Test<Method>_OK` + ≥1 error scenario; every error sentinel is asserted by ≥1 `assert.ErrorIs` test.

---

## 15. Profile holes (designed shape, not yet filled)

A generator encountering these MUST stop and report (Canon §0), never improvise.

- **PostgreSQL adapter.** Follows §10's builder shape: `core/shared/postgres_client/builder.go` with a connection pool, fail-fast ping, and lifecycle methods. Repositories implement the same ports as Mongo. The transaction manager wraps `begin/commit/rollback` behind `WithTransaction`. Identifier generation uses a database sequence or identity column instead of `system_counters`.
- **NATS messaging.** Out of scope for v1 per Canon §6.2. When introduced: a `core/shared/nats_client/builder.go` following §10, publish/subscribe expressed as **ports** (Canon §6.2), payloads schema'd in proto, wired in `wire.go` like any other adapter.
- **Rust and Python profiles.** Separate documents; this Profile is Go-only.

---

## 16. Go-specific verifier gates

In addition to the Canon-level gates, the Go verifier enforces:

- **Layer-import linter** — rejects any import that violates the Canon §1.1 dependency table (e.g. `application` importing the mongo driver, a handler importing `repositories`).
- **`wire.go` is the sole composition point** — no full-graph construction elsewhere.
- **Logger** — only `applog` imported; no `log`, `log/slog`, `fmt.Print*`.
- **Domain types are aliases** — no parallel structs duplicating a proto message.
- **Test runner + coverage floor** — `go test` with the §14 minimums.
- **No generated `.pb.go` in the diff.**
