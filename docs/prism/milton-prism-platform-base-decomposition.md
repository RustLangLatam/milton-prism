# Milton Prism — Platform Base Decomposition

**The hand-authored decomposition of Milton Prism's own backend.**

Prism is new software, not a monolith to migrate, so its decomposition is produced by hand and fed into the generator exactly as if it came from the (future) automatic decomposition engine. This document is the generator's input for building the platform base: it defines the services, allocates error prefixes, fixes the dependency graph and generation order, and provides the proto contracts and boundary specs.

> **How this is used.** For each service: take its proto block → save as the listed file; take its boundary spec → feed it to the Service Generator prompt together with the Canon and the Go Profile. See §8 (Integration recipe).

Proto project root and Go module: **`milton_prism`** (mirrors the sample's single-module monorepo).

---

## 1. Service map

| Service | Kind | Responsibility | Generator-buildable? |
|---------|------|----------------|----------------------|
| **identity** | CRUD + auth | Registered users, authentication, sessions | ✅ fully |
| **repository** | CRUD + state | Connected git repos: connect, clone, branches, push results | ✅ fully |
| **migration** | CRUD + state machine | The migration aggregate: lifecycle, links to analysis summary and restructure plan, human-approval gate | ✅ fully |
| **analysis** | engine | Produces the code summary (tech, versions, status, vulnerabilities, dependency graph) | ⚠️ scaffold only — custom application logic |
| **sandbox** | engine | Ephemeral isolated environments to run and verify generated/legacy code | ⚠️ scaffold only — custom application logic + infra |
| **orchestrator** | engine | Runs the end-to-end pipeline, the long-running migration workflow, and the multimodel router | ⚠️ scaffold only — workflow engine, not CRUD |

**Build the three CRUD services now** — they deliver the MVP user flow (register → connect a repo → start a migration) and the generator can produce them end-to-end. The three engine services are defined here at the contract level so the platform map is complete, but their application logic is custom (plan artifacts 8–10); the generator only scaffolds their hexagonal skeleton.

---

## 2. Error-prefix registry

The orchestrator owns this registry (Canon §4.2). Allocations for the base platform:

| Service | Prefix | Validation | Domain | Internal |
|---------|--------|-----------|--------|----------|
| identity | `IDN` | IDN1xx | IDN2xx | IDN500 |
| repository | `REPO` | REPO1xx | REPO2xx | REPO500 |
| migration | `MIG` | MIG1xx | MIG2xx | MIG500 |
| analysis | `ANL` | ANL1xx | ANL2xx | ANL500 |
| sandbox | `SBX` | SBX1xx | SBX2xx | SBX500 |
| orchestrator | `ORC` | ORC1xx | ORC2xx | ORC500 |

---

## 3. Dependency graph and generation order

```
identity        (no inter-service deps)            ← generate 1st: validates the full chain
repository      → identity                         ← generate 2nd
analysis        → repository                        (scaffold)
migration       → repository, identity, analysis   ← generate 3rd (core aggregate)
sandbox         → migration                         (scaffold)
orchestrator    → migration, analysis, sandbox, repository, generation  (scaffold)
```

Generate **identity first**: it has no dependencies and is closest to the sample's `users`/`profiles`, so it proves the generator→critic chain before you touch anything novel. Then `repository`, then `migration`.

---

## 4. Shared type contracts

Resources live in `types/`; services import them (Canon §2). The standard 7-option header (Canon §2.9) is shown once below and abbreviated as `// <standard option header>` thereafter. Reuse the sample's existing `milton_prism/types/pagination/v1` and `milton_prism/types/query_params/v1`.

### `protobuf/proto/milton_prism/types/identity/v1/user.proto`

```protobuf
syntax = "proto3";
package milton_prism.types.identity.v1;

option cc_enable_arenas     = true;
option csharp_namespace     = "MiltonPrism.Types.Identity.V1";
option go_package           = "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1;identityv1";
option java_multiple_files  = true;
option java_outer_classname = "UserProtoV1";
option java_package         = "com.miltonprism.types.identity.v1";
option objc_class_prefix    = "MPR";

import "google/api/field_behavior.proto";
import "google/protobuf/timestamp.proto";

message User {
  uint64 identifier = 1;
  string email = 2 [(google.api.field_behavior) = REQUIRED];
  string display_name = 3;
  bool system_user = 4;
  UserState state = 5;
  google.protobuf.Timestamp create_time = 6 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (google.api.field_behavior) = IMMUTABLE
  ];
  google.protobuf.Timestamp update_time = 7 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp delete_time = 8 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp purge_time = 9 [(google.api.field_behavior) = OUTPUT_ONLY];
}

enum UserState {
  USER_STATE_UNSPECIFIED = 0;
  USER_STATE_ACTIVE = 1;
  USER_STATE_SUSPENDED = 2;
  USER_STATE_DELETED = 3;
}

message UsersFilter {
  optional UserState state = 1;
  optional string email = 2;
}

message AuthTokens {
  string access_token = 1;
  string refresh_token = 2;
  google.protobuf.Timestamp access_expire_time = 3;
}
```

### `protobuf/proto/milton_prism/types/repository/v1/repository.proto`

```protobuf
syntax = "proto3";
package milton_prism.types.repository.v1;

// <standard option header — go_package …/types/repository/v1;repositoryv1>

import "google/api/field_behavior.proto";
import "google/protobuf/timestamp.proto";

message Repository {
  uint64 identifier = 1;
  uint64 owner_user_id = 2 [(google.api.field_behavior) = REQUIRED];
  GitProvider provider = 3 [(google.api.field_behavior) = REQUIRED];
  string remote_url = 4 [(google.api.field_behavior) = REQUIRED];
  string default_branch = 5;
  RepositoryState state = 6;
  ConnectionStatus connection_status = 7;
  // Credentials are never returned; only a reference to the secret store.
  string credential_ref = 8 [(google.api.field_behavior) = INPUT_ONLY];
  google.protobuf.Timestamp create_time = 9 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (google.api.field_behavior) = IMMUTABLE
  ];
  google.protobuf.Timestamp update_time = 10 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp delete_time = 11 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp purge_time = 12 [(google.api.field_behavior) = OUTPUT_ONLY];
}

enum GitProvider {
  GIT_PROVIDER_UNSPECIFIED = 0;
  GIT_PROVIDER_GITHUB = 1;
  GIT_PROVIDER_GITLAB = 2;
  GIT_PROVIDER_BITBUCKET = 3;
  GIT_PROVIDER_GENERIC = 4;
}

enum RepositoryState {
  REPOSITORY_STATE_UNSPECIFIED = 0;
  REPOSITORY_STATE_CONNECTED = 1;
  REPOSITORY_STATE_DISCONNECTED = 2;
  REPOSITORY_STATE_ERROR = 3;
}

enum ConnectionStatus {
  CONNECTION_STATUS_UNSPECIFIED = 0;
  CONNECTION_STATUS_OK = 1;
  CONNECTION_STATUS_AUTH_FAILED = 2;
  CONNECTION_STATUS_UNREACHABLE = 3;
}

message Branch {
  string name = 1;
  string commit_sha = 2;
  bool is_default = 3;
}

message RepositoriesFilter {
  optional uint64 owner_user_id = 1;
  optional RepositoryState state = 2;
  optional GitProvider provider = 3;
}
```

### `protobuf/proto/milton_prism/types/analysis/v1/analysis.proto`

```protobuf
syntax = "proto3";
package milton_prism.types.analysis.v1;

// <standard option header — go_package …/types/analysis/v1;analysisv1>

import "google/api/field_behavior.proto";
import "google/protobuf/timestamp.proto";

message AnalysisSummary {
  uint64 identifier = 1;
  uint64 repository_id = 2 [(google.api.field_behavior) = REQUIRED];
  uint64 migration_id = 3;
  AnalysisState state = 4;
  repeated Technology technologies = 5;
  repeated Vulnerability vulnerabilities = 6;
  repeated DependencyEdge dependency_graph = 7;
  uint64 total_files = 8;
  uint64 total_lines = 9;
  google.protobuf.Timestamp create_time = 10 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (google.api.field_behavior) = IMMUTABLE
  ];
  google.protobuf.Timestamp update_time = 11 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp delete_time = 12 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp purge_time = 13 [(google.api.field_behavior) = OUTPUT_ONLY];
}

enum AnalysisState {
  ANALYSIS_STATE_UNSPECIFIED = 0;
  ANALYSIS_STATE_RUNNING = 1;
  ANALYSIS_STATE_COMPLETED = 2;
  ANALYSIS_STATE_FAILED = 3;
}

message Technology {
  string name = 1;
  string detected_version = 2;
  string latest_version = 3;
  TechnologyStatus status = 4;
  string category = 5; // language | framework | database | runtime | library
}

enum TechnologyStatus {
  TECHNOLOGY_STATUS_UNSPECIFIED = 0;
  TECHNOLOGY_STATUS_CURRENT = 1;
  TECHNOLOGY_STATUS_OUTDATED = 2;
  TECHNOLOGY_STATUS_END_OF_LIFE = 3;
}

message Vulnerability {
  string identifier_ref = 1; // e.g. CVE id
  Severity severity = 2;
  string component = 3;
  string description = 4;
  string fixed_in_version = 5;
}

enum Severity {
  SEVERITY_UNSPECIFIED = 0;
  SEVERITY_LOW = 1;
  SEVERITY_MEDIUM = 2;
  SEVERITY_HIGH = 3;
  SEVERITY_CRITICAL = 4;
}

message DependencyEdge {
  string from_module = 1;
  string to_module = 2;
  uint32 weight = 3; // coupling strength
}
```

### `protobuf/proto/milton_prism/types/migration/v1/migration.proto`

```protobuf
syntax = "proto3";
package milton_prism.types.migration.v1;

// <standard option header — go_package …/types/migration/v1;migrationv1>

import "google/api/field_behavior.proto";
import "google/protobuf/timestamp.proto";

message Migration {
  uint64 identifier = 1;
  uint64 repository_id = 2 [(google.api.field_behavior) = REQUIRED];
  uint64 owner_user_id = 3 [(google.api.field_behavior) = REQUIRED];
  string source_branch = 4;
  MigrationState state = 5;
  TargetConfig target = 6;
  uint64 analysis_summary_id = 7;   // set once analysis completes
  RestructurePlan plan = 8;         // set once design completes; requires approval
  MigrationOutput output = 9;       // set once results are pushed
  google.protobuf.Timestamp create_time = 10 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (google.api.field_behavior) = IMMUTABLE
  ];
  google.protobuf.Timestamp update_time = 11 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp delete_time = 12 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional google.protobuf.Timestamp purge_time = 13 [(google.api.field_behavior) = OUTPUT_ONLY];
}

enum MigrationState {
  MIGRATION_STATE_UNSPECIFIED = 0;
  MIGRATION_STATE_PENDING = 1;
  MIGRATION_STATE_ANALYZING = 2;
  MIGRATION_STATE_DESIGNING = 3;
  MIGRATION_STATE_AWAITING_APPROVAL = 4;
  MIGRATION_STATE_GENERATING = 5;
  MIGRATION_STATE_TESTING = 6;
  MIGRATION_STATE_READY = 7;
  MIGRATION_STATE_PUSHED = 8;
  MIGRATION_STATE_FAILED = 9;
  MIGRATION_STATE_CANCELLED = 10;
}

message TargetConfig {
  TargetLanguage language = 1 [(google.api.field_behavior) = REQUIRED];
  TargetDatabase database = 2 [(google.api.field_behavior) = REQUIRED];
  Transport inter_service_transport = 3; // v1: gRPC only
  bool use_api_gateway = 4;
}

enum TargetLanguage {
  TARGET_LANGUAGE_UNSPECIFIED = 0;
  TARGET_LANGUAGE_GO = 1;     // v1 supported
  TARGET_LANGUAGE_RUST = 2;   // profile hole
  TARGET_LANGUAGE_PYTHON = 3; // profile hole
}

enum TargetDatabase {
  TARGET_DATABASE_UNSPECIFIED = 0;
  TARGET_DATABASE_MONGODB = 1;  // v1 supported
  TARGET_DATABASE_POSTGRES = 2; // profile hole
  TARGET_DATABASE_MARIADB = 3;  // profile hole
}

enum Transport {
  TRANSPORT_UNSPECIFIED = 0;
  TRANSPORT_GRPC = 1;
  TRANSPORT_HTTP = 2;
  TRANSPORT_NATS = 3; // out of scope v1
}

message RestructurePlan {
  repeated ProposedService services = 1;
  string rationale = 2;
}

message ProposedService {
  string name = 1;
  string error_prefix = 2;
  repeated string owned_resources = 3;
  repeated string inter_service_deps = 4;
}

message MigrationOutput {
  OutputTarget output_target = 1;
  string branch_name = 2;            // when pushing to a new branch
  string new_repository_url = 3;     // when pushing to a new repo
}

enum OutputTarget {
  OUTPUT_TARGET_UNSPECIFIED = 0;
  OUTPUT_TARGET_NEW_BRANCH = 1;
  OUTPUT_TARGET_NEW_REPOSITORY = 2;
}

message MigrationsFilter {
  optional uint64 owner_user_id = 1;
  optional uint64 repository_id = 2;
  optional MigrationState state = 3;
}
```

---

## 5. Core service contracts (generator-ready)

### 5.1 identity

**Boundary spec**

```yaml
service: identity
module: milton_prism
resources:
  - { name: User, proto_type: milton_prism/types/identity/v1.User, soft_delete: true }
rpcs: [CreateUser, GetUser, ListUsers, UpdateUser, DeleteUser, AuthenticateUser, RefreshToken, Logout, GetCurrentUser]
store: mongodb
needs_transaction: true
error_prefix: "IDN"
inter_service_deps: []
auth: mixed   # CreateUser/AuthenticateUser/RefreshToken are public; the rest require auth
```

**`protobuf/proto/milton_prism/services/identity/v1/identity_service.proto`**

```protobuf
syntax = "proto3";
package milton_prism.services.identity.v1;

// <standard option header — go_package milton_prism/services/identity/v1;identity>

import "google/api/annotations.proto";
import "google/api/client.proto";
import "google/api/field_behavior.proto";
import "google/protobuf/empty.proto";
import "google/protobuf/field_mask.proto";
import "milton_prism/types/identity/v1/user.proto";
import "milton_prism/types/pagination/v1/pagination.proto";
import "milton_prism/types/query_params/v1/query_params.proto";

service IdentityService {
  rpc CreateUser(CreateUserRequest) returns (milton_prism.types.identity.v1.User) {
    option (google.api.http) = { post: "/v1/users", body: "user" };
    option (google.api.method_signature) = "user";
  }
  rpc GetUser(GetUserRequest) returns (milton_prism.types.identity.v1.User) {
    option (google.api.http) = { get: "/v1/users/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc ListUsers(ListUsersRequest) returns (ListUsersResponse) {
    option (google.api.http) = { get: "/v1/users" };
  }
  rpc UpdateUser(UpdateUserRequest) returns (milton_prism.types.identity.v1.User) {
    option (google.api.http) = { patch: "/v1/users/{user.identifier}", body: "user" };
    option (google.api.method_signature) = "user,update_mask";
  }
  rpc DeleteUser(DeleteUserRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = { delete: "/v1/users/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc AuthenticateUser(AuthenticateUserRequest) returns (milton_prism.types.identity.v1.AuthTokens) {
    option (google.api.http) = { post: "/v1/users:authenticate", body: "*" };
  }
  rpc RefreshToken(RefreshTokenRequest) returns (milton_prism.types.identity.v1.AuthTokens) {
    option (google.api.http) = { post: "/v1/users:refreshToken", body: "*" };
  }
  rpc Logout(LogoutRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = { post: "/v1/users:logout", body: "*" };
  }
  rpc GetCurrentUser(GetCurrentUserRequest) returns (milton_prism.types.identity.v1.User) {
    option (google.api.http) = { get: "/v1/users:current" };
  }
}

message CreateUserRequest {
  milton_prism.types.identity.v1.User user = 1 [(google.api.field_behavior) = REQUIRED];
  string password = 2 [(google.api.field_behavior) = REQUIRED, (google.api.field_behavior) = INPUT_ONLY];
}
message GetUserRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message ListUsersRequest {
  milton_prism.types.identity.v1.UsersFilter filter = 1;
  milton_prism.types.query_params.v1.PageQueryParams page_params = 2;
}
message ListUsersResponse {
  repeated milton_prism.types.identity.v1.User users = 1;
  milton_prism.types.pagination.v1.Pagination pagination = 2;
}
message UpdateUserRequest {
  milton_prism.types.identity.v1.User user = 1 [(google.api.field_behavior) = REQUIRED];
  google.protobuf.FieldMask update_mask = 2 [(google.api.field_behavior) = REQUIRED];
}
message DeleteUserRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message AuthenticateUserRequest {
  string email = 1 [(google.api.field_behavior) = REQUIRED];
  string password = 2 [(google.api.field_behavior) = REQUIRED, (google.api.field_behavior) = INPUT_ONLY];
}
message RefreshTokenRequest { string refresh_token = 1 [(google.api.field_behavior) = REQUIRED]; }
message LogoutRequest {}
message GetCurrentUserRequest {}
```

### 5.2 repository

**Boundary spec**

```yaml
service: repository
module: milton_prism
resources:
  - { name: Repository, proto_type: milton_prism/types/repository/v1.Repository, soft_delete: true }
rpcs: [CreateRepository, GetRepository, ListRepositories, UpdateRepository, DeleteRepository, TestConnection, ListBranches, PushResult]
store: mongodb
needs_transaction: true
error_prefix: "REPO"
inter_service_deps: [identity]   # validates owner_user_id
auth: required
```

**`protobuf/proto/milton_prism/services/repository/v1/repository_service.proto`**

```protobuf
syntax = "proto3";
package milton_prism.services.repository.v1;

// <standard option header — go_package milton_prism/services/repository/v1;repository>

import "google/api/annotations.proto";
import "google/api/field_behavior.proto";
import "google/protobuf/empty.proto";
import "google/protobuf/field_mask.proto";
import "milton_prism/types/repository/v1/repository.proto";
import "milton_prism/types/pagination/v1/pagination.proto";
import "milton_prism/types/query_params/v1/query_params.proto";

service RepositoryService {
  rpc CreateRepository(CreateRepositoryRequest) returns (milton_prism.types.repository.v1.Repository) {
    option (google.api.http) = { post: "/v1/repositories", body: "repository" };
    option (google.api.method_signature) = "repository";
  }
  rpc GetRepository(GetRepositoryRequest) returns (milton_prism.types.repository.v1.Repository) {
    option (google.api.http) = { get: "/v1/repositories/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc ListRepositories(ListRepositoriesRequest) returns (ListRepositoriesResponse) {
    option (google.api.http) = { get: "/v1/repositories" };
  }
  rpc UpdateRepository(UpdateRepositoryRequest) returns (milton_prism.types.repository.v1.Repository) {
    option (google.api.http) = { patch: "/v1/repositories/{repository.identifier}", body: "repository" };
    option (google.api.method_signature) = "repository,update_mask";
  }
  rpc DeleteRepository(DeleteRepositoryRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = { delete: "/v1/repositories/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc TestConnection(TestConnectionRequest) returns (TestConnectionResponse) {
    option (google.api.http) = { post: "/v1/repositories/{identifier}:testConnection", body: "*" };
  }
  rpc ListBranches(ListBranchesRequest) returns (ListBranchesResponse) {
    option (google.api.http) = { get: "/v1/repositories/{identifier}:listBranches" };
  }
  rpc PushResult(PushResultRequest) returns (PushResultResponse) {
    option (google.api.http) = { post: "/v1/repositories/{identifier}:pushResult", body: "*" };
  }
}

message CreateRepositoryRequest {
  milton_prism.types.repository.v1.Repository repository = 1 [(google.api.field_behavior) = REQUIRED];
}
message GetRepositoryRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message ListRepositoriesRequest {
  milton_prism.types.repository.v1.RepositoriesFilter filter = 1;
  milton_prism.types.query_params.v1.PageQueryParams page_params = 2;
}
message ListRepositoriesResponse {
  repeated milton_prism.types.repository.v1.Repository repositories = 1;
  milton_prism.types.pagination.v1.Pagination pagination = 2;
}
message UpdateRepositoryRequest {
  milton_prism.types.repository.v1.Repository repository = 1 [(google.api.field_behavior) = REQUIRED];
  google.protobuf.FieldMask update_mask = 2 [(google.api.field_behavior) = REQUIRED];
}
message DeleteRepositoryRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message TestConnectionRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message TestConnectionResponse { milton_prism.types.repository.v1.ConnectionStatus status = 1; }
message ListBranchesRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message ListBranchesResponse { repeated milton_prism.types.repository.v1.Branch branches = 1; }
message PushResultRequest {
  uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED];
  string target_branch = 2;
  bool create_new_repository = 3;
}
message PushResultResponse {
  string pushed_branch = 1;
  string new_repository_url = 2;
}
```

### 5.3 migration

**Boundary spec**

```yaml
service: migration
module: milton_prism
resources:
  - { name: Migration, proto_type: milton_prism/types/migration/v1.Migration, soft_delete: true }
rpcs: [CreateMigration, GetMigration, ListMigrations, DeleteMigration, StartMigration, ApproveDesign, CancelMigration]
store: mongodb
needs_transaction: true
error_prefix: "MIG"
inter_service_deps: [repository, identity, analysis]
auth: required
notes: >
  The state machine (PENDING→ANALYZING→DESIGNING→AWAITING_APPROVAL→GENERATING→TESTING→READY→PUSHED,
  plus FAILED/CANCELLED) is enforced in the application layer. StartMigration/ApproveDesign/CancelMigration
  are state transitions; the actual analysis/design/generation work is driven by the orchestrator (engine),
  not by this service — this service owns the migration record and guards valid transitions.
```

**`protobuf/proto/milton_prism/services/migration/v1/migration_service.proto`**

```protobuf
syntax = "proto3";
package milton_prism.services.migration.v1;

// <standard option header — go_package milton_prism/services/migration/v1;migration>

import "google/api/annotations.proto";
import "google/api/field_behavior.proto";
import "google/protobuf/empty.proto";
import "milton_prism/types/migration/v1/migration.proto";
import "milton_prism/types/pagination/v1/pagination.proto";
import "milton_prism/types/query_params/v1/query_params.proto";

service MigrationService {
  rpc CreateMigration(CreateMigrationRequest) returns (milton_prism.types.migration.v1.Migration) {
    option (google.api.http) = { post: "/v1/migrations", body: "migration" };
    option (google.api.method_signature) = "migration";
  }
  rpc GetMigration(GetMigrationRequest) returns (milton_prism.types.migration.v1.Migration) {
    option (google.api.http) = { get: "/v1/migrations/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc ListMigrations(ListMigrationsRequest) returns (ListMigrationsResponse) {
    option (google.api.http) = { get: "/v1/migrations" };
  }
  rpc DeleteMigration(DeleteMigrationRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = { delete: "/v1/migrations/{identifier}" };
    option (google.api.method_signature) = "identifier";
  }
  rpc StartMigration(StartMigrationRequest) returns (milton_prism.types.migration.v1.Migration) {
    option (google.api.http) = { post: "/v1/migrations/{identifier}:start", body: "*" };
  }
  rpc ApproveDesign(ApproveDesignRequest) returns (milton_prism.types.migration.v1.Migration) {
    option (google.api.http) = { post: "/v1/migrations/{identifier}:approveDesign", body: "*" };
  }
  rpc CancelMigration(CancelMigrationRequest) returns (milton_prism.types.migration.v1.Migration) {
    option (google.api.http) = { post: "/v1/migrations/{identifier}:cancel", body: "*" };
  }
}

message CreateMigrationRequest {
  milton_prism.types.migration.v1.Migration migration = 1 [(google.api.field_behavior) = REQUIRED];
}
message GetMigrationRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message ListMigrationsRequest {
  milton_prism.types.migration.v1.MigrationsFilter filter = 1;
  milton_prism.types.query_params.v1.PageQueryParams page_params = 2;
}
message ListMigrationsResponse {
  repeated milton_prism.types.migration.v1.Migration migrations = 1;
  milton_prism.types.pagination.v1.Pagination pagination = 2;
}
message DeleteMigrationRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message StartMigrationRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
message ApproveDesignRequest {
  uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED];
  bool approved = 2 [(google.api.field_behavior) = REQUIRED];
}
message CancelMigrationRequest { uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED]; }
```

---

## 6. Engine services (scaffold only)

These are defined so the platform map is complete and the generator can produce a compiling hexagonal skeleton, but their **application logic is custom** and lives in later plan artifacts (analysis engine, sandbox, orchestrator/multimodel router). Generate the skeleton, then implement the application layer by hand or with a specialized prompt.

### 6.1 analysis — boundary spec

```yaml
service: analysis
module: milton_prism
resources:
  - { name: AnalysisSummary, proto_type: milton_prism/types/analysis/v1.AnalysisSummary, soft_delete: true }
rpcs: [GetAnalysisSummary, ListAnalysisSummaries, RunAnalysis]   # RunAnalysis is :runAnalysis (POST), custom — heavy logic is the analysis engine
store: mongodb
needs_transaction: false
error_prefix: "ANL"
inter_service_deps: [repository]
auth: required
scaffold_only: true
```

### 6.2 sandbox — boundary spec

```yaml
service: sandbox
module: milton_prism
resources:
  - { name: SandboxEnvironment }   # type to be defined when the sandbox infra is designed (plan artifact 8)
rpcs: [CreateSandbox, GetSandbox, DeleteSandbox, RunVerification]
store: mongodb
needs_transaction: false
error_prefix: "SBX"
inter_service_deps: [migration]
auth: required
scaffold_only: true
notes: "Requires strong isolation infra (gVisor/Firecracker/ephemeral k8s). Contract to be finalized with the sandbox spec."
```

### 6.3 orchestrator — boundary spec

```yaml
service: orchestrator
module: milton_prism
resources: []   # not a CRUD resource owner; coordinates the others
rpcs: [GetPipelineStatus]   # thin read surface; the workflow engine + multimodel router are custom
store: mongodb              # workflow state persistence (or a workflow engine like Temporal)
needs_transaction: false
error_prefix: "ORC"
inter_service_deps: [migration, analysis, sandbox, repository]
auth: required
scaffold_only: true
notes: "Owns the long-running migration workflow and the multimodel router. Not generator-buildable beyond scaffold; this is plan artifacts 9–10."
```

---

## 7. api-gateway

A single HTTP→gRPC gateway (grpc-gateway) fronts all services, exposing the REST surface from the `google.api.http` annotations above and emitting the OpenAPI document that feeds `openapi-generator-cli` (Canon §7). The gateway aggregates the per-service handler registrations (one `Register<Service>HandlerFromEndpoint` per service) and carries the per-service friendly error-message maps under `pkg/gateway/common/error/`.

---

## 8. Integration recipe (how to use the three prompts)

For each generator-ready service, in the order of §3:

1. **Save the proto contracts.** Write the type protos (§4) and the service proto (§5) to their listed paths. Run `buf lint` — it must pass before generating.
2. **Run the Service Generator prompt.** Provide it: the Canon, the Go Profile, the service's boundary spec (§5.x), and access to the repo. It reads both reference docs, generates the four layers + mocks + wire + entrypoint, and runs its self-verification loop.
3. **Review against the critic.** Confirm `buf lint`, `go build`, `go vet`, `go test`, and the layer-import self-audit all pass, and read the generation report.
4. **Repeat** for the next service. After `identity`, `repository`, and `migration` are green and boot, you have the platform base: a user can register, connect a repository, and create a migration record.

The engine services (§6) get the same step 1–2 to produce their skeletons, but stop before expecting working application logic — that comes from the analysis, sandbox, and orchestrator artifacts in the plan.

---

## 9. What this gives you, and what it does not

**Gives you:** a complete, AIP-correct contract set and boundary specs for the entire base platform; everything the generator needs to build the three core services end-to-end; and compiling skeletons for the three engines.

**Does not give you (by design):** the analysis engine's logic, the sandbox isolation infrastructure, the orchestrator's workflow and multimodel router, the secret store behind `credential_ref`, and the password hashing/token issuance details inside identity's application layer (those follow the sample's `auth_token`/`session` shared packages — wire them in when generating identity). These are the next plan artifacts.
