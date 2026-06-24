package assembler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// knownSecrets lists real credential values that must never appear in generated
// config templates. The set is checked after generation; a match returns an error.
var knownSecrets = []string{
	"bimtra654", // MongoDB dev password
	"cce81c0e198c6a7e88a96bb7a39b3bc11912fdde5021e98d3bb63a7bff6bd7e8", // JWT signKey (dev, lowercase)
	"1qaz2WSX", // Redis/KeyDB dev password
	"dev123",   // gateway API key
	"5A6C3D9BA08CB65DC1E3ED455C7115460F2DF5B9EC923E8CCF39AE88F624EB0A", // Ed25519 public key (dev, uppercase)
	"5a6c3d9ba08cb65dc1e3ed455c7115460f2df5b9ec923e8ccf39ae88f624eb0a", // Ed25519 public key (dev, lowercase)
}

// generateConfigExamples appends config.toml.example files to the assembled
// file map. It scans for service cmd directories and generates one example per
// service plus one gateway example.
//
// The assembled map is mutated in-place; no skeleton or artifact file is ever
// overwritten (example paths are derived synthetically and never collide with
// generated code paths).
func generateConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ...
		port := 50051 + i
		role := detectTokenRole(svc, assembled)
		content := serviceConfigExample(svc, port, role)
		if err := assertNoSecrets(content, svc+" config"); err != nil {
			return err
		}
		path := fmt.Sprintf("core/cmd/%s-services/config.toml.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// generateSQLConfigExamples appends a `.env.example` file to each generated
// Go service cmd directory for a Go + SQL deliverable (GORM over PostgreSQL or
// MySQL/MariaDB). It is the SQL homologue of generateConfigExamples (the Mongo
// config.toml.example): a Go + SQL service persists with GORM, so its config is a
// DATABASE_URL / DB_* .env rather than a Mongo `[mongo]` TOML section. The same
// .env shape serves both engines (DATABASE_URL is the GORM DSN; only its format
// differs between Postgres and MySQL — documented inline).
//
// The file is emitted per service at core/cmd/<svc>-services/.env.example,
// mirroring the Mongo config.toml.example placement. Zero MONGO_* variables ever
// appear. Every value is a placeholder — never a real credential (assertNoSecrets
// guards each file).
func generateSQLConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as the Mongo path).
		port := 50051 + i
		content := sqlServiceEnvExample(svc, port)
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("core/cmd/%s-services/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// sqlServiceEnvExample returns the content of a .env.example for one generated
// Go + SQL (GORM) microservice. It documents BOTH the single DATABASE_URL DSN
// (the GORM DSN — PostgreSQL or MySQL/MariaDB form) and the discrete DB_* parts
// (the generator may read either), the gRPC server bind, and the auth secret.
// Every value is a placeholder; no MONGO_* variable is present.
func sqlServiceEnvExample(name string, port int) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via GORM)
# Copy this file to .env (in the directory the service is launched from) and fill
# in the placeholder values, or export them into the environment before starting
# the service: ./%s-services
#
# This service persists via the GORM ORM. The same models/repos serve PostgreSQL
# and MySQL/MariaDB — only the driver and the DATABASE_URL DSN format differ.
# Schema is applied by GORM AutoMigrate on startup. Set EITHER the single
# DATABASE_URL DSN OR the discrete DB_* parts (DATABASE_URL wins when both are set).

# ── Database (GORM) ──────────────────────────────────────────────────────────
# DATABASE_URL: full GORM DSN. Examples:
#   PostgreSQL: postgres://user:password@host:5432/%s?sslmode=disable
#   MySQL/MariaDB: user:password@tcp(host:3306)/%s?charset=utf8mb4&parseTime=True&loc=Local
DATABASE_URL=<your-database-dsn>
DB_HOST=localhost
# DB_PORT: 5432 (PostgreSQL) or 3306 (MySQL/MariaDB)
DB_PORT=5432
DB_USER=<your-db-user>
DB_PASSWORD=<your-db-password>
DB_NAME=%s
# DB_SSLMODE applies to PostgreSQL; MySQL/MariaDB uses tls in the DSN instead.
DB_SSLMODE=disable

# ── gRPC server ─────────────────────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d

# ── Auth ───────────────────────────────────────────────────────────────────
# JWT_SECRET: signing/validation secret. Generate with: openssl rand -hex 32
JWT_SECRET=<your-jwt-secret>
`, name, name, db, db, db, port)
}

// generatePythonConfigExamples appends a `.env.example` file to each generated
// Python service directory in the assembled map. It is the pydantic homologue of
// the Go config.toml.example: the Go deliverable emits
// core/cmd/<svc>-services/config.toml.example; the Python deliverable emits one
// .env.example per service alongside its package.
//
// MUST run on the assembled map BEFORE the python/ → core/ rename, so service
// dirs are still keyed under "python/services/<svc>/…" — the same shape the Go
// path expects under core/cmd/. The emitted path (python/services/<svc>/.env.example)
// is rewritten to core/services/<svc>/.env.example by the rename step, matching
// the Go per-service placement.
//
// The variable names are taken verbatim from core/shared/config/loader.py:
//   - MongoConfig (env_prefix MONGO_): MONGO_URI, MONGO_DATABASE
//   - GrpcServerConfig (env_prefix GRPC_): GRPC_HOST, GRPC_PORT, GRPC_MAX_WORKERS
//   - JWT_SECRET is read directly via os.environ in each service __main__.py.
//
// pydantic-settings reads .env from the process cwd, which is the source root
// (core/ after rename) when a service is launched as `python -m services.<svc>`.
// The header documents that the file should be copied to <root>/.env (or have
// its values exported into the environment) before the service is started.
func generatePythonConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedPythonServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as Go).
		port := 50051 + i
		content := pythonServiceEnvExample(svc, port)
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("python/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// discoverGeneratedPythonServices scans assembled paths for
// python/services/<name>/ directories and returns sorted service name slugs
// (e.g. "user"). __pycache__ and the __init__.py-only root are never included.
// Runs BEFORE the python/ → core/ rename, so paths are still python/-rooted.
func discoverGeneratedPythonServices(assembled map[string][]byte) []string {
	const prefix = "python/services/"
	seen := make(map[string]struct{})
	for path := range assembled {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue // a file directly under python/services/ (e.g. __init__.py)
		}
		name := rest[:slash]
		if name == "" || name == "__pycache__" {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// pythonServiceEnvExample returns the content of a .env.example for one generated
// Python microservice. Every value is a placeholder — never a real credential.
// The env var names match core/shared/config/loader.py field aliases exactly.
func pythonServiceEnvExample(name string, port int) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory: pydantic-settings reads .env from the process cwd) and
# fill in the placeholder values. Alternatively export these as environment
# variables before starting the service: python -m services.%s
#
# These variables are consumed by core/shared/config/loader.py
# (MongoConfig env_prefix MONGO_, GrpcServerConfig env_prefix GRPC_) plus the
# JWT_SECRET read directly in services/%s/__main__.py.

# ── MongoDB (MongoConfig) ──────────────────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (GrpcServerConfig) ─────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
GRPC_MAX_WORKERS=10

# ── Auth ───────────────────────────────────────────────────────────────────
# JWT_SECRET: signing/validation secret. Generate with: openssl rand -hex 32
JWT_SECRET=<your-jwt-secret>
`, name, name, name, db, port)
}

// generateNodeConfigExamples appends a `.env.example` file to each generated
// Node service directory in the assembled map. It is the TypeScript homologue of
// the Go config.toml.example and the Python .env.example: the Node deliverable
// emits one .env.example per service alongside its package.
//
// MUST run on the assembled map BEFORE the node/ → core/ rename, so service dirs
// are still keyed under "node/services/<svc>/…". The emitted path
// (node/services/<svc>/.env.example) is rewritten to
// core/services/<svc>/.env.example by the rename step, matching the Go/Python
// per-service placement.
//
// The variable names match the Node profile config contract exactly:
//   - MONGO_URI, MONGO_DATABASE (MongoDB official driver)
//   - GRPC_HOST, GRPC_PORT (the @grpc/grpc-js server bind address)
//   - JWT_SECRET (EdDSA token signing/validation secret)
func generateNodeConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedNodeServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as Go/Python).
		port := 50051 + i
		content := nodeServiceEnvExample(svc, port)
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("node/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// discoverGeneratedNodeServices scans assembled paths for
// node/services/<name>/ directories and returns sorted service name slugs
// (e.g. "user"). Runs BEFORE the node/ → core/ rename, so paths are still
// node/-rooted.
func discoverGeneratedNodeServices(assembled map[string][]byte) []string {
	const prefix = "node/services/"
	seen := make(map[string]struct{})
	for path := range assembled {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue // a file directly under node/services/
		}
		name := rest[:slash]
		if name == "" || name == "node_modules" {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// nodeServiceEnvExample returns the content of a .env.example for one generated
// Node microservice. Every value is a placeholder — never a real credential.
// The env var names match the Node profile config contract exactly.
func nodeServiceEnvExample(name string, port int) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# These variables are consumed by core/shared/config (the typed config loader)
# plus the JWT_SECRET read by the auth interceptor.

# ── MongoDB (official mongodb driver) ──────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (@grpc/grpc-js) ────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d

# ── Auth ───────────────────────────────────────────────────────────────────
# JWT_SECRET: signing/validation secret. Generate with: openssl rand -hex 32
JWT_SECRET=<your-jwt-secret>
`, name, db, port)
}

// generateRustConfigExamples appends a `.env.example` file to each generated
// Rust service directory in the assembled map. It is the Tonic homologue of the
// Go config.toml.example and the Python/Node .env.example: the Rust deliverable
// emits one .env.example per service alongside its crate.
//
// MUST run on the assembled map BEFORE the rust/ → core/ rename, so service dirs
// are still keyed under "rust/services/<svc>/…". The emitted path
// (rust/services/<svc>/.env.example) is rewritten to
// core/services/<svc>/.env.example by the rename step, matching the
// Go/Python/Node per-service placement.
//
// The variable names match the Rust profile config contract exactly:
//   - MONGO_URI, MONGO_DATABASE (official mongodb crate)
//   - GRPC_HOST, GRPC_PORT (the Tonic server bind address)
//   - JWT_SECRET (EdDSA token signing/validation secret)
func generateRustConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedRustServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as Go/Python/Node).
		port := 50051 + i
		content := rustServiceEnvExample(svc, port)
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("rust/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// discoverGeneratedRustServices scans assembled paths for
// rust/services/<name>/ directories and returns sorted service name slugs
// (e.g. "user"). Runs BEFORE the rust/ → core/ rename, so paths are still
// rust/-rooted.
func discoverGeneratedRustServices(assembled map[string][]byte) []string {
	const prefix = "rust/services/"
	seen := make(map[string]struct{})
	for path := range assembled {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue // a file directly under rust/services/
		}
		name := rest[:slash]
		if name == "" || name == "target" {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// rustServiceEnvExample returns the content of a .env.example for one generated
// Rust microservice. Every value is a placeholder — never a real credential.
// The env var names match the Rust profile config contract exactly.
func rustServiceEnvExample(name string, port int) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# These variables are consumed by the typed config loader (dotenvy/envy) plus
# the JWT_SECRET read by the auth interceptor.

# ── MongoDB (official mongodb crate) ───────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (Tonic) ────────────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d

# ── Auth ───────────────────────────────────────────────────────────────────
# JWT_SECRET: signing/validation secret. Generate with: openssl rand -hex 32
JWT_SECRET=<your-jwt-secret>
`, name, db, port)
}

// detectTokenRole scans the generated main.go for the service to determine
// whether it acts as a token generator (emitter) or validator. The scan
// matches the package-qualified constant "config.TokenRoleGenerator" as it
// appears in the LoadMicroserviceCfg call — never a bare substring that could
// appear in a comment. Defaults to "validator" when the file is absent or
// the generator constant is not found.
func detectTokenRole(slug string, assembled map[string][]byte) string {
	mainPath := fmt.Sprintf("core/cmd/%s-services/main.go", slug)
	content, ok := assembled[mainPath]
	if !ok {
		return "validator"
	}
	if strings.Contains(string(content), "config.TokenRoleGenerator") {
		return "generator"
	}
	return "validator"
}

// generateServiceMakefiles appends a Makefile to each generated service cmd
// directory in the assembled map. It mirrors the gateway Makefile structure:
// same targets (all, build, run, clean, info), same ldflags, same OUTPUT_DIR.
//
// BINARY_NAME follows the platform convention from infra/build.sh and
// docker-compose: {slug}-services → {slug}_service (e.g. articles_service).
//
// OUTPUT_DIR is ../../bin, which resolves to core/bin/ from
// core/cmd/{slug}-services/ — the correct output directory for deliverable
// service binaries. (The gateway uses the identical relative path resolving
// to api-gateway/bin/; each top-level component owns its own bin/ subdir.)
func generateServiceMakefiles(assembled map[string][]byte) error {
	services := discoverGeneratedServices(assembled)
	for _, svc := range services {
		content := serviceMakefile(svc)
		if err := assertNoSecrets(content, svc+" Makefile"); err != nil {
			return err
		}
		assembled[fmt.Sprintf("core/cmd/%s-services/Makefile", svc)] = []byte(content)
	}
	return nil
}

// serviceMakefile returns the Makefile content for one generated microservice.
// BINARY_NAME convention: {slug}_service (matches infra/build.sh and docker-compose).
// OUTPUT_DIR ../../bin resolves to core/bin/ from core/cmd/{slug}-services/.
func serviceMakefile(slug string) string {
	return fmt.Sprintf("VERSION := 1.0.0-dev\n"+
		"BUILD_TIME := $(shell date +%%Y-%%m-%%dT%%H:%%M:%%S%%z)\n"+
		"GOMEMLIMIT := 100\n"+
		"GOGC := 100\n"+
		"GRPC_GO_LOG_SEVERITY_LEVEL := info\n"+
		"\n"+
		"CONFIG_PACKAGE := milton_prism/pkg/config\n"+
		"\n"+
		"BINARY_NAME := %s_service\n"+
		"OUTPUT_DIR := ../../bin\n"+
		"MAIN_SOURCE := main.go\n"+
		"CONFIG_PATH := config.toml\n"+
		"\n"+
		".PHONY: all\n"+
		"all: build info run\n"+
		"\n"+
		".PHONY: build clean\n"+
		"build:\n"+
		"\t@echo \"Building $(BINARY_NAME)...\"\n"+
		"\t@GOMEMLIMIT=$(GOMEMLIMIT) GOGC=$(GOGC) \\\n"+
		"\tgo build -ldflags \"-X '$(CONFIG_PACKAGE).Version=$(VERSION)' -X '$(CONFIG_PACKAGE).BuildTime=$(BUILD_TIME)' -s -w\" -o $(OUTPUT_DIR)/$(BINARY_NAME) -v $(MAIN_SOURCE)\n"+
		"\t@echo \"Build complete: $(OUTPUT_DIR)/$(BINARY_NAME)\"\n"+
		"\n"+
		".PHONY: run\n"+
		"run: build\n"+
		"\t@echo \"Running $(BINARY_NAME)...\"\n"+
		"\t@GOMEMLIMIT=$(GOMEMLIMIT) GOGC=$(GOGC) GRPC_GO_LOG_SEVERITY_LEVEL=$(GRPC_GO_LOG_SEVERITY_LEVEL) $(OUTPUT_DIR)/$(BINARY_NAME) --config-file-path=$(CONFIG_PATH)\n"+
		"\n"+
		".PHONY: clean\n"+
		"clean:\n"+
		"\t@echo \"Cleaning up...\"\n"+
		"\t@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)\n"+
		"\t@echo \"Clean complete.\"\n"+
		"\n"+
		".PHONY: info\n"+
		"info:\n"+
		"\t@echo \"Version: $(VERSION)\"\n"+
		"\t@echo \"Build Time: $(BUILD_TIME)\"\n"+
		"\t@echo \"Binary Name: $(BINARY_NAME)\"\n"+
		"\t@echo \"Output Directory: $(OUTPUT_DIR)\"\n",
		slug)
}

// discoverGeneratedServices scans assembled paths for core/cmd/*-services/
// directories and returns sorted service name slugs (e.g. "articles").
// The __pipeline__ pseudo-service is never included.
func discoverGeneratedServices(assembled map[string][]byte) []string {
	seen := make(map[string]struct{})
	for path := range assembled {
		// Match "core/cmd/<name>-services/<anything>"
		if !strings.HasPrefix(path, "core/cmd/") {
			continue
		}
		rest := strings.TrimPrefix(path, "core/cmd/")
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue
		}
		dir := rest[:slash] // e.g. "articles-services"
		if !strings.HasSuffix(dir, "-services") {
			continue
		}
		name := strings.TrimSuffix(dir, "-services")
		if name == "" || name == "__pipeline__" {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// serviceConfigExample returns the content of a config.toml.example for one
// generated microservice. The auth section is role-specific: generator services
// receive [auth.tokenGenerator] (signKey), validator services receive
// [auth.tokenValidator] (publicKey). All credential fields are placeholders.
func serviceConfigExample(name string, port int, role string) string {
	db := name + "_db"
	var authSection string
	if role == "generator" {
		authSection = `# Auth role: GENERATOR — this service EMITS JWT tokens.
# It requires the tokenGenerator section below (signKey). Do not replace it
# with a tokenValidator section — the service will fail to start.
[auth]
# algorithm 1 = EdDSA (Ed25519). Do not change unless the framework is updated.
algorithm = 1

[auth.tokenGenerator]
enabled               = true
issuer                = "milton-prism"
audience              = "milton-prism-clients"
# signKey: 64-character hex Ed25519 private key. Generate with: openssl rand -hex 32
signKey               = "<your-jwt-sign-key-hex-64>"
accessTokenDuration   = 3600
refreshTokenDuration  = 86400
storage               = false
blacklist             = false
validateIssuer        = false
validateAudience      = false

[auth.tokenGenerator.userClaims]
includeUserId       = true
includeUsername     = true
includeUserEmail    = true
includeUserProfiles = false
includeCustomClaims = false
`
	} else {
		authSection = `# Auth role: VALIDATOR — this service VALIDATES JWT tokens issued elsewhere.
# It requires the tokenValidator section below (publicKey). Do not replace it
# with a tokenGenerator section — the service will fail to start.
[auth]
# algorithm 1 = EdDSA (Ed25519). Do not change unless the framework is updated.
algorithm = 1

[auth.tokenValidator]
enabled           = true
issuer            = "milton-prism"
audience          = "milton-prism-clients"
# publicKey: 64-character hex Ed25519 public key. Must match the signKey of the token-issuing service.
publicKey         = "<your-jwt-public-key-hex-64>"
blacklist         = false
validateIssuer    = false
validateAudience  = false
`
	}

	return fmt.Sprintf(`# config.toml.example — %s service
# Copy this file to config.toml and fill in the placeholder values.
# Run: ./%s-services (or ./%s-services --config-file-path=/path/to/config.toml)
#
# Required sections: [microservice] [mongo] [auth]

[microservice]
name    = %q
host    = "0.0.0.0"
port    = %d
timeout = 30

[mongo]
# uri: full MongoDB connection string, e.g. mongodb://user:password@host:27017
uri                     = "<your-mongo-uri>"
database                = %q
connect_timeout         = 10000000000
socket_timeout          = 5000000000
max_pool_size           = 20
min_pool_size           = 2
retry_writes            = true
retry_reads             = true

%s`, name, name, name, name, port, db, authSection)
}

// gatewayConfigExample returns a config.toml.example for an HTTP→gRPC gateway
// that routes to the supplied generated services.
func gatewayConfigExample(services []string) string {
	var sb strings.Builder
	sb.WriteString(`# config.toml.example — API gateway
# Copy to config.toml and fill in the placeholder values.
# Run: ./milton-prism-gateway (or pass --config-file-path=...)
#
# This gateway routes HTTP/JSON requests to the generated gRPC microservices.

[gateway]
name             = "api-gateway"
port             = 8080
host             = "0.0.0.0"
# apiKey: arbitrary string used as the MiltonPrismApiKey header value
apiKey           = "<your-gateway-api-key>"
maxRecvMsgSizeMb = 250
maxSendMsgSizeMb = 250

[cors]
enabled        = true
AllowOrigin    = "*"
allowedMethods = ["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"]
allowedHeaders = ["Authorization", "Refresh", "Content-Type", "MiltonPrismApiKey"]
exposeHeaders  = ["Authorization", "Refresh", "MiltonPrismApiKey"]

[metrics]
port = 9000

`)
	for i, svc := range services {
		port := 50051 + i
		sb.WriteString(fmt.Sprintf(`[[grpcServices]]
name        = %q
# host: hostname or IP where the %s service is reachable
host        = "localhost"
port        = %d
enabled     = true
healthCheck = false

`, svc, svc, port))
	}
	return sb.String()
}

// assertNoSecrets verifies that none of the known real credential values appear
// in the generated content. Returns an error (never panics) so callers can
// surface the issue clearly.
func assertNoSecrets(content, label string) error {
	for _, secret := range knownSecrets {
		if strings.Contains(content, secret) {
			return fmt.Errorf("assembler: secret leak detected in %s template: found known credential value", label)
		}
	}
	return nil
}

// AssertPayloadNoSecrets runs the same known-credential check that guards the
// synthesised config templates over an ENTIRE push payload — every generated
// file that would leave the system on a git push. It is the write-side gate: the
// deliverable must contain zero known dev credentials before any push. The first
// offending file short-circuits with a labelled error; nil means grep=0 across
// the whole payload. The label carries the file path so a leak is locatable
// without logging file content.
func AssertPayloadNoSecrets(files []File) error {
	for _, f := range files {
		if err := assertNoSecrets(string(f.Content), f.Path); err != nil {
			return err
		}
	}
	return nil
}

// ── API Gateway generation ─────────────────────────────────────────────────────

// gwHandler holds the information needed to wire one generated service into the
// gateway main.go: the service slug, its import alias, the full Go import path,
// and the exact Register*HandlerFromEndpoint function name extracted from the
// .pb.gw.go artifact (never inferred from the slug to avoid naming surprises).
type gwHandler struct {
	slug        string // e.g. "articles"
	importAlias string // e.g. "articlesv1"
	importPath  string // e.g. "milton_prism/pkg/pb/gen/milton_prism/services/articles/v1"
	registerFn  string // e.g. "RegisterArticleServiceHandlerFromEndpoint"
}

// generateGatewayCode appends the three api-gateway files to the assembled map:
//
//	api-gateway/cmd/milton-prism-gateway/main.go   — generated, routes to discovered services
//	api-gateway/cmd/milton-prism-gateway/Makefile  — copied from skeleton (generic, no secrets)
//	api-gateway/config.toml.example               — generated placeholder config
func generateGatewayCode(assembled map[string][]byte, skeletonRoot string) error {
	handlers := discoverGatewayHandlers(assembled)
	if len(handlers) == 0 {
		return nil
	}

	mainGo := gatewayMainGo(handlers)
	if err := assertNoSecrets(mainGo, "gateway main.go"); err != nil {
		return err
	}
	assembled["api-gateway/cmd/milton-prism-gateway/main.go"] = []byte(mainGo)

	makefileSrc := filepath.Join(skeletonRoot, "api-gateway/cmd/milton-prism-gateway/Makefile")
	makefile, err := os.ReadFile(makefileSrc)
	if err != nil {
		return fmt.Errorf("assembler: read gateway Makefile from skeleton: %w", err)
	}
	assembled["api-gateway/cmd/milton-prism-gateway/Makefile"] = makefile

	// Build service-name list from handler slugs (same order, already sorted).
	slugs := make([]string, len(handlers))
	for i, h := range handlers {
		slugs[i] = h.slug
	}
	gwCfg := gatewayConfigExample(slugs)
	if err := assertNoSecrets(gwCfg, "gateway config.toml.example"); err != nil {
		return err
	}
	assembled["api-gateway/config.toml.example"] = []byte(gwCfg)

	return nil
}

// discoverGatewayHandlers scans the assembled map for generated pb.gw.go stubs
// and extracts the exact Register*HandlerFromEndpoint function name from each.
// Only files under pkg/pb/gen/milton_prism/services/{slug}/v1/ are considered.
func discoverGatewayHandlers(assembled map[string][]byte) []gwHandler {
	const prefix = "pkg/pb/gen/milton_prism/services/"
	seen := make(map[string]struct{})
	var handlers []gwHandler

	for path, content := range assembled {
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".pb.gw.go") {
			continue
		}
		// path after prefix: "{slug}/v1/{file}.pb.gw.go"
		rest := strings.TrimPrefix(path, prefix)
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) != 3 || parts[1] != "v1" {
			continue
		}
		slug := parts[0]
		if _, ok := seen[slug]; ok {
			continue
		}

		// Extract the exact Register*HandlerFromEndpoint name from the file content.
		registerFn := ""
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "func Register") && strings.Contains(line, "HandlerFromEndpoint(") {
				fn := strings.TrimPrefix(line, "func ")
				if idx := strings.Index(fn, "("); idx >= 0 {
					fn = strings.TrimSpace(fn[:idx])
				}
				registerFn = fn
				break
			}
		}
		if registerFn == "" {
			continue
		}

		seen[slug] = struct{}{}
		handlers = append(handlers, gwHandler{
			slug:        slug,
			importAlias: slug + "v1",
			importPath:  "milton_prism/" + prefix + slug + "/v1",
			registerFn:  registerFn,
		})
	}

	sort.Slice(handlers, func(i, j int) bool { return handlers[i].slug < handlers[j].slug })
	return handlers
}

// gatewayMainGo generates the main.go for the API gateway using the multi-service
// pattern (NewRestApiBuilder + loop). It never references platform services.
func gatewayMainGo(handlers []gwHandler) string {
	var imports strings.Builder
	for _, h := range handlers {
		fmt.Fprintf(&imports, "\t%s %q\n", h.importAlias, h.importPath)
	}

	var entries strings.Builder
	for _, h := range handlers {
		fmt.Fprintf(&entries, "\t%q: %s.%s,\n", h.slug, h.importAlias, h.registerFn)
	}

	return fmt.Sprintf(`package main

import (
	"fmt"
	"strconv"

	"milton_prism/pkg/config"
	"milton_prism/pkg/gateway"
	"milton_prism/pkg/log"
%s)

var serviceHandlers = map[string]gateway.RegisterServiceFunc{
%s}

func main() {
	log.InitLogger("gateway")

	cfg, err := config.LoadGatewayCfg()
	if err != nil {
		log.Fatalf("Failed to load config: %%v", err)
	}

	apiBuilder := gateway.NewRestApiBuilder()

	for _, svc := range cfg.GrpcServices {
		if !svc.Enabled {
			log.Infof("Skipping disabled service: %%s", svc.Name)
			continue
		}
		registerFn, ok := serviceHandlers[svc.Name]
		if !ok {
			log.Fatalf("Unknown gRPC service in config: %%s", svc.Name)
		}
		healthPath := fmt.Sprintf("/health/%%s", svc.Name)
		if err := apiBuilder.RegisterService(svc, registerFn, healthPath); err != nil {
			log.Fatalf("Failed to register service %%s: %%v", svc.Name, err)
		}
	}

	restApi := apiBuilder.Build(cfg.Server.ApiKey, cfg.Cors)

	log.Infof("Listening on %%s:%%d", cfg.Server.Host, *cfg.Server.Port)

	if err := restApi.Start(
		strconv.Itoa(int(*cfg.Server.Port)),
		strconv.Itoa(cfg.Metrics.Port),
	); err != nil {
		log.Fatalf("Failed to start REST API: %%v", err)
	}
}
`, imports.String(), entries.String())
}
