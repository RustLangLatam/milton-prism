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

// sqlDBPort returns the default DB_PORT for a SQL store: 5432 for PostgreSQL,
// 3306 for MySQL/MariaDB. The .env.example default must match the cell's real
// engine (the audit found a MariaDB Rust deliverable advertising DB_PORT=5432).
func sqlDBPort(store string) int {
	if store == "mysql" {
		return 3306
	}
	return 5432
}

// authBlock returns the auth section for a service .env.example, or "" when the
// service uses no auth. authEnabled is detected from the generated code (does any
// artifact read JWT_SECRET / a token secret). When auth is off, no JWT block is
// emitted — the audit found a Postgres Python deliverable advertising a JWT block
// though the cell wires no auth. The block is identical across languages (a single
// JWT_SECRET the auth interceptor reads).
func authBlock(authEnabled bool) string {
	if !authEnabled {
		return ""
	}
	return `
# ── Auth ───────────────────────────────────────────────────────────────────
# JWT_SECRET: signing/validation secret. Generate with: openssl rand -hex 32
JWT_SECRET=<your-jwt-secret>
`
}

// serviceUsesAuth reports whether the generated code for service `slug` (under the
// given source root, e.g. "python"/"node"/"rust"/"java"/"core") reads an auth
// secret — i.e. references JWT_SECRET or a token validator/generator. It scans the
// generated artifacts already in the assembled map for the service. When no
// generated file is present (e.g. tests with empty artifacts) it returns true so
// the auth block is kept by default (never silently drop a needed secret). This is
// the cross-language homologue of detectTokenRole.
func serviceUsesAuth(assembled map[string][]byte, root, slug string) bool {
	prefix := root + "/services/" + slug + "/"
	sawService := false
	for path, content := range assembled {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		sawService = true
		c := string(content)
		if strings.Contains(c, "JWT_SECRET") ||
			strings.Contains(c, "jwt_secret") ||
			strings.Contains(c, "TokenValidator") ||
			strings.Contains(c, "TokenGenerator") ||
			strings.Contains(c, "token_validator") ||
			strings.Contains(c, "token_generator") ||
			strings.Contains(c, "AuthInterceptor") ||
			strings.Contains(c, "auth_interceptor") {
			return true
		}
	}
	// Default: keep auth when no generated service files were seen (defensive — the
	// test harness may pass no artifacts); drop only when the service IS present and
	// provably never references auth.
	return !sawService
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
func generateSQLConfigExamples(assembled map[string][]byte, store string) error {
	services := discoverGeneratedServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as the Mongo path).
		port := 50051 + i
		// Go places generated code under core/cmd/<svc>-services/; the auth secret is
		// read by the service main.go (EdDSA token role). Reuse detectTokenRole's
		// signal: a Go service that sets a token role uses auth.
		authEnabled := detectTokenRole(svc, assembled) != "" && goServiceUsesAuth(assembled, svc)
		content := sqlServiceEnvExample(svc, port, store, authEnabled)
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("core/cmd/%s-services/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// goServiceUsesAuth reports whether the generated Go service `slug` reads an auth
// token secret — it references a token role/validator in its cmd main.go or wire.
// Defaults to true when no main.go is present (defensive, mirrors serviceUsesAuth).
func goServiceUsesAuth(assembled map[string][]byte, slug string) bool {
	mainPath := fmt.Sprintf("core/cmd/%s-services/main.go", slug)
	content, ok := assembled[mainPath]
	if !ok {
		return true
	}
	c := string(content)
	return strings.Contains(c, "TokenRole") || strings.Contains(c, "tokenValidator") ||
		strings.Contains(c, "tokenGenerator") || strings.Contains(c, "auth")
}

// sqlServiceEnvExample returns the content of a .env.example for one generated
// Go + SQL (GORM) microservice. It documents BOTH the single DATABASE_URL DSN
// (the GORM DSN — PostgreSQL or MySQL/MariaDB form) and the discrete DB_* parts
// (the generator may read either), the gRPC server bind, and the auth secret.
// Every value is a placeholder; no MONGO_* variable is present.
func sqlServiceEnvExample(name string, port int, store string, authEnabled bool) string {
	db := name + "_db"
	dbPort := sqlDBPort(store)
	var dsnExample, engineLabel, sslLine string
	if store == "mysql" {
		dsnExample = fmt.Sprintf("user:password@tcp(host:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local", dbPort, db)
		engineLabel = "MySQL/MariaDB"
		sslLine = "# MySQL/MariaDB uses tls in the DSN; there is no DB_SSLMODE.\n"
	} else {
		dsnExample = fmt.Sprintf("postgres://user:password@host:%d/%s?sslmode=disable", dbPort, db)
		engineLabel = "PostgreSQL"
		sslLine = "DB_SSLMODE=disable\n"
	}
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via GORM)
# Copy this file to .env (in the directory the service is launched from) and fill
# in the placeholder values, or export them into the environment before starting
# the service: ./%s-services
#
# This service persists via the GORM ORM over %s. Schema is applied by GORM
# AutoMigrate on startup. Set EITHER the single DATABASE_URL DSN OR the discrete
# DB_* parts (DATABASE_URL wins when both are set).

# ── Database (GORM, %s) ───────────────────────────────────────────────────────
# DATABASE_URL: full GORM DSN, e.g.
#   %s
DATABASE_URL=<your-database-dsn>
DB_HOST=localhost
DB_PORT=%d
DB_USER=<your-db-user>
DB_PASSWORD=<your-db-password>
DB_NAME=%s
%s
# ── gRPC server ─────────────────────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
%s`, name, name, engineLabel, engineLabel, dsnExample, dbPort, db, sslLine, port, authBlock(authEnabled))
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
		content := pythonServiceEnvExample(svc, port, serviceUsesAuth(assembled, "python", svc))
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
func pythonServiceEnvExample(name string, port int, authEnabled bool) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory: pydantic-settings reads .env from the process cwd) and
# fill in the placeholder values. Alternatively export these as environment
# variables before starting the service: python -m services.%s
#
# These variables are consumed by core/shared/config/loader.py
# (MongoConfig env_prefix MONGO_, GrpcServerConfig env_prefix GRPC_).

# ── MongoDB (MongoConfig) ──────────────────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (GrpcServerConfig) ─────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
GRPC_MAX_WORKERS=10
%s`, name, name, db, port, authBlock(authEnabled))
}

// generatePythonSQLConfigExamples appends a SQL `.env.example` file to each
// generated Python service directory for a Python + SQL deliverable (SQLAlchemy
// 2.0 async over PostgreSQL or MySQL/MariaDB). It is the SQL homologue of
// generatePythonConfigExamples (the Motor .env.example): a Python + SQL service
// persists with SQLAlchemy, so its config is a DATABASE_URL / DB_* .env rather
// than the MONGO_* variables. The same .env shape serves both engines
// (DATABASE_URL is the SQLAlchemy async URL; only its scheme differs between
// asyncpg and aiomysql — documented inline). Zero MONGO_* variables ever appear.
//
// MUST run on the assembled map BEFORE the python/ → core/ rename (same as the
// Motor path), so service dirs are still keyed under python/services/<svc>/. Every
// value is a placeholder; assertNoSecrets guards each file.
func generatePythonSQLConfigExamples(assembled map[string][]byte, store string) error {
	services := discoverGeneratedPythonServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as the Motor path).
		port := 50051 + i
		content := pythonSQLServiceEnvExample(svc, port, store, serviceUsesAuth(assembled, "python", svc))
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("python/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// pythonSQLServiceEnvExample returns the content of a .env.example for one
// generated Python + SQL (SQLAlchemy async) microservice. It documents BOTH the
// single DATABASE_URL async URL (the SQLAlchemy DSN — PostgreSQL or MySQL/MariaDB
// form) and the discrete DB_* parts, the gRPC server bind, and the auth secret.
// Every value is a placeholder; no MONGO_* variable is present.
func pythonSQLServiceEnvExample(name string, port int, store string, authEnabled bool) string {
	db := name + "_db"
	dbPort := sqlDBPort(store)
	// Emit the active driver/URL example for the cell's real engine first.
	var driver, urlExample, engineLabel string
	if store == "mysql" {
		driver = "aiomysql"
		urlExample = fmt.Sprintf("mysql+aiomysql://user:password@host:%d/%s?charset=utf8mb4", dbPort, db)
		engineLabel = "MySQL/MariaDB"
	} else {
		driver = "asyncpg"
		urlExample = fmt.Sprintf("postgresql+asyncpg://user:password@host:%d/%s", dbPort, db)
		engineLabel = "PostgreSQL"
	}
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via SQLAlchemy 2.0 async)
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory: pydantic-settings reads .env from the process cwd) and fill
# in the placeholder values. Alternatively export these as environment variables
# before starting the service: python -m services.%s
#
# This service persists via the SQLAlchemy 2.0 async ORM over %s (driver: %s).
# Schema is applied by Base.metadata.create_all on startup. Set EITHER the single
# DATABASE_URL async URL OR the discrete DB_* parts.

# ── Database (SQLAlchemy async, %s) ──────────────────────────────────────────
# DATABASE_URL: full SQLAlchemy async URL, e.g.
#   %s
DATABASE_URL=<your-database-url>
DB_HOST=localhost
DB_PORT=%d
DB_USER=<your-db-user>
DB_PASSWORD=<your-db-password>
DB_NAME=%s

# ── gRPC server (GrpcServerConfig) ─────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
GRPC_MAX_WORKERS=10
%s`, name, name, engineLabel, driver, engineLabel, urlExample, dbPort, db, port, authBlock(authEnabled))
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
		content := nodeServiceEnvExample(svc, port, serviceUsesAuth(assembled, "node", svc))
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
func nodeServiceEnvExample(name string, port int, authEnabled bool) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# These variables are consumed by core/shared/config (the typed config loader).

# ── MongoDB (official mongodb driver) ──────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (@grpc/grpc-js) ────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
%s`, name, db, port, authBlock(authEnabled))
}

// generateNodeSQLConfigExamples appends a SQL `.env.example` file to each generated
// Node service directory for a Node + SQL deliverable (Prisma over PostgreSQL or
// MySQL/MariaDB). It is the SQL homologue of generateNodeConfigExamples (the
// native-`mongodb`-driver .env.example): a Node + SQL service persists with Prisma,
// so its config is a DATABASE_URL / DB_* .env rather than the MONGO_* variables.
// The same .env shape serves both engines (DATABASE_URL is the Prisma connection
// URL; only its scheme differs between postgresql:// and mysql:// — documented
// inline). Zero MONGO_* variables ever appear. It is the Node homologue of
// generateGoSQL/generatePythonSQLConfigExamples.
//
// MUST run on the assembled map BEFORE the node/ → core/ rename (same as the
// native-Mongo path), so service dirs are still keyed under node/services/<svc>/.
// Every value is a placeholder; assertNoSecrets guards each file.
func generateNodeSQLConfigExamples(assembled map[string][]byte, store string) error {
	services := discoverGeneratedNodeServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as the Mongo path).
		port := 50051 + i
		content := nodeSQLServiceEnvExample(svc, port, store, serviceUsesAuth(assembled, "node", svc))
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("node/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// nodeSQLServiceEnvExample returns the content of a .env.example for one generated
// Node + SQL (Prisma) microservice. It documents BOTH the single DATABASE_URL
// connection URL (the Prisma URL — PostgreSQL or MySQL/MariaDB form) and the
// discrete DB_* parts, the gRPC server bind, and the auth secret. Every value is a
// placeholder; no MONGO_* variable is present.
func nodeSQLServiceEnvExample(name string, port int, store string, authEnabled bool) string {
	db := name + "_db"
	dbPort := sqlDBPort(store)
	var urlExample, provider, engineLabel string
	if store == "mysql" {
		urlExample = fmt.Sprintf("mysql://user:password@host:%d/%s", dbPort, db)
		provider = "mysql"
		engineLabel = "MySQL/MariaDB"
	} else {
		urlExample = fmt.Sprintf("postgresql://user:password@host:%d/%s?schema=public", dbPort, db)
		provider = "postgresql"
		engineLabel = "PostgreSQL"
	}
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via Prisma)
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# This service persists via the Prisma ORM over %s (datasource provider: %s).
# Schema is applied by Prisma Migrate (prisma migrate deploy) or prisma db push.
# Prisma reads DATABASE_URL from the environment.

# ── Database (Prisma, %s) ─────────────────────────────────────────────────────
# DATABASE_URL: full Prisma connection URL, e.g.
#   %s
DATABASE_URL=<your-database-url>
DB_HOST=localhost
DB_PORT=%d
DB_USER=<your-db-user>
DB_PASSWORD=<your-db-password>
DB_NAME=%s

# ── gRPC server (@grpc/grpc-js) ────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
%s`, name, engineLabel, provider, engineLabel, urlExample, dbPort, db, port, authBlock(authEnabled))
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
		content := rustServiceEnvExample(svc, port, serviceUsesAuth(assembled, "rust", svc))
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
func rustServiceEnvExample(name string, port int, authEnabled bool) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# These variables are consumed by the typed config loader (dotenvy/envy).

# ── MongoDB (official mongodb crate) ───────────────────────────────────────
# MONGO_URI: full MongoDB connection string, e.g. mongodb://user:password@host:27017
MONGO_URI=<your-mongo-uri>
MONGO_DATABASE=%s

# ── gRPC server (Tonic) ────────────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
%s`, name, db, port, authBlock(authEnabled))
}

// generateRustSQLConfigExamples appends a SQL `.env.example` file to each generated
// Rust service directory for a Rust + SQL deliverable (SeaORM over PostgreSQL or
// MySQL/MariaDB). It is the SQL homologue of generateRustConfigExamples (the
// native-`mongodb`-crate .env.example): a Rust + SQL service persists with SeaORM,
// so its config is a DATABASE_URL / DB_* .env rather than the MONGO_* variables.
// The same .env shape serves both engines (DATABASE_URL is the SeaORM connection
// URL; only its scheme differs between postgres:// and mysql:// — documented
// inline). Zero MONGO_* variables ever appear. It is the Rust homologue of
// generateGoSQL/generatePythonSQL/generateNodeSQLConfigExamples.
//
// MUST run on the assembled map BEFORE the rust/ → core/ rename (same as the
// native-Mongo path), so service dirs are still keyed under rust/services/<svc>/.
// Every value is a placeholder; assertNoSecrets guards each file.
func generateRustSQLConfigExamples(assembled map[string][]byte, store string) error {
	services := discoverGeneratedRustServices(assembled)

	for i, svc := range services {
		// Assign sequential ports: 50051, 50052, ... (same scheme as the Mongo path).
		port := 50051 + i
		content := rustSQLServiceEnvExample(svc, port, store, serviceUsesAuth(assembled, "rust", svc))
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("rust/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// rustSQLServiceEnvExample returns the content of a .env.example for one generated
// Rust + SQL (SeaORM) microservice. It documents BOTH the single DATABASE_URL
// connection URL (the SeaORM URL — PostgreSQL or MySQL/MariaDB form) and the
// discrete DB_* parts, the gRPC server bind, and the auth secret. Every value is a
// placeholder; no MONGO_* variable is present.
func rustSQLServiceEnvExample(name string, port int, store string, authEnabled bool) string {
	db := name + "_db"
	dbPort := sqlDBPort(store)
	var urlExample, feature, engineLabel string
	if store == "mysql" {
		urlExample = fmt.Sprintf("mysql://user:password@host:%d/%s", dbPort, db)
		feature = "sqlx-mysql"
		engineLabel = "MySQL/MariaDB"
	} else {
		urlExample = fmt.Sprintf("postgres://user:password@host:%d/%s", dbPort, db)
		feature = "sqlx-postgres"
		engineLabel = "PostgreSQL"
	}
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via SeaORM)
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# This service persists via the SeaORM ORM (async, sqlx-backed) over %s — the
# Cargo.toml compiles the %s driver feature. Schema is applied by
# sea-orm-migration (Migrator::up) on startup. SeaORM reads DATABASE_URL via
# Database::connect.

# ── Database (SeaORM, %s) ─────────────────────────────────────────────────────
# DATABASE_URL: full SeaORM connection URL, e.g.
#   %s
DATABASE_URL=<your-database-url>
DB_HOST=localhost
DB_PORT=%d
DB_USER=<your-db-user>
DB_PASSWORD=<your-db-password>
DB_NAME=%s

# ── gRPC server (Tonic) ────────────────────────────────────────────────────
GRPC_HOST=0.0.0.0
GRPC_PORT=%d
%s`, name, engineLabel, feature, engineLabel, urlExample, dbPort, db, port, authBlock(authEnabled))
}

// generateJavaConfigExamples appends a `.env.example` to each generated Java
// service directory (the Spring Boot homologue of the Go config.toml.example /
// the Python/Node/Rust .env.example). It is the Mongo path: a Java + MongoDB
// service persists via Spring Data MongoDB, so its config carries the SPRING_DATA_
// MONGODB_* / MONGO_* variables. It is the Java homologue of generateRustConfigExamples.
//
// MUST run on the assembled map BEFORE the java/ → core/ rename, so service dirs
// are still keyed under java/services/<svc>/. Every value is a placeholder;
// assertNoSecrets guards each file.
func generateJavaConfigExamples(assembled map[string][]byte) error {
	services := discoverGeneratedJavaServices(assembled)

	for i, svc := range services {
		// Java gRPC port: net.devh grpc-spring-boot-starter binds 9090 by default
		// (NOT 50051). The generated Spring Boot service reads grpc.server.port /
		// GRPC_PORT, so seed sequential ports from 9090 to match the real bind.
		port := 9090 + i
		content := javaServiceEnvExample(svc, port, serviceUsesAuth(assembled, "java", svc))
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("java/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// discoverGeneratedJavaServices scans assembled paths for
// java/services/<name>/ directories and returns sorted service name slugs
// (e.g. "user"). Runs BEFORE the java/ → core/ rename, so paths are still
// java/-rooted. It is the Java homologue of discoverGeneratedRustServices.
func discoverGeneratedJavaServices(assembled map[string][]byte) []string {
	const prefix = "java/services/"
	seen := make(map[string]struct{})
	for path := range assembled {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue // a file directly under java/services/
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

// javaServiceEnvExample returns the content of a .env.example for one generated
// Java microservice (Spring Data MongoDB persistence). Every value is a
// placeholder — never a real credential. It is the Java homologue of
// rustServiceEnvExample.
func javaServiceEnvExample(name string, port int, authEnabled bool) string {
	db := name + "_db"
	return fmt.Sprintf(`# .env.example — %s service
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# These variables are consumed by the Spring Boot config (application.yml /
# environment). Spring Boot maps the SPRING_DATA_MONGODB_* env vars onto the
# spring.data.mongodb.* properties; the grpc server binds grpc.server.port.

# ── MongoDB (Spring Data MongoDB) ──────────────────────────────────────────
# SPRING_DATA_MONGODB_URI: full MongoDB connection string, e.g.
#   mongodb://user:password@host:27017/%s
SPRING_DATA_MONGODB_URI=<your-mongo-uri>
SPRING_DATA_MONGODB_DATABASE=%s

# ── gRPC server (grpc-java / grpc-spring-boot-starter) ─────────────────────
# GRPC_PORT maps to grpc.server.port (net.devh default 9090).
GRPC_PORT=%d
%s`, name, db, db, port, authBlock(authEnabled))
}

// generateJavaSQLConfigExamples appends a SQL `.env.example` file to each generated
// Java service directory for a Java + SQL deliverable (Spring Data JPA over
// PostgreSQL or MySQL/MariaDB). It is the SQL homologue of generateJavaConfigExamples
// (the Spring Data MongoDB .env.example): a Java + SQL service persists with JPA, so
// its config is a DATABASE_URL / SPRING_DATASOURCE_* .env rather than the MONGO_*
// variables. The same .env shape serves both engines (only the jdbc: URL scheme
// differs between jdbc:postgresql and jdbc:mariadb — documented inline). Zero MONGO_*
// variables ever appear. It is the Java homologue of generateRustSQLConfigExamples.
//
// MUST run on the assembled map BEFORE the java/ → core/ rename (same as the
// native-Mongo path), so service dirs are still keyed under java/services/<svc>/.
// Every value is a placeholder; assertNoSecrets guards each file.
func generateJavaSQLConfigExamples(assembled map[string][]byte, store string) error {
	services := discoverGeneratedJavaServices(assembled)

	for i, svc := range services {
		// Java gRPC port seeds from 9090 (net.devh grpc-spring-boot-starter default),
		// same as the Mongo path.
		port := 9090 + i
		content := javaSQLServiceEnvExample(svc, port, store, serviceUsesAuth(assembled, "java", svc))
		if err := assertNoSecrets(content, svc+" .env"); err != nil {
			return err
		}
		path := fmt.Sprintf("java/services/%s/.env.example", svc)
		assembled[path] = []byte(content)
	}

	return nil
}

// javaSQLServiceEnvExample returns the content of a .env.example for one generated
// Java + SQL (Spring Data JPA / Hibernate) microservice. It documents BOTH the
// single DATABASE_URL JDBC URL (the JPA DataSource URL — PostgreSQL or MySQL/MariaDB
// form) and the Spring SPRING_DATASOURCE_* parts, the gRPC server bind, and the auth
// secret. Every value is a placeholder; no MONGO_* variable is present.
func javaSQLServiceEnvExample(name string, port int, store string, authEnabled bool) string {
	db := name + "_db"
	dbPort := sqlDBPort(store)
	var jdbcExample, driverDep, engineLabel string
	if store == "mysql" {
		jdbcExample = fmt.Sprintf("jdbc:mariadb://host:%d/%s", dbPort, db)
		driverDep = "org.mariadb.jdbc:mariadb-java-client"
		engineLabel = "MySQL/MariaDB"
	} else {
		jdbcExample = fmt.Sprintf("jdbc:postgresql://host:%d/%s", dbPort, db)
		driverDep = "org.postgresql:postgresql"
		engineLabel = "PostgreSQL"
	}
	return fmt.Sprintf(`# .env.example — %s service (SQL persistence via Spring Data JPA)
# Copy this file to .env (in the source root the service is launched from, i.e.
# the core/ directory) and fill in the placeholder values. Alternatively export
# these as environment variables before starting the service.
#
# This service persists via Spring Data JPA (Hibernate) over %s — the pom.xml
# bundles the %s JDBC driver. Schema is applied by Hibernate ddl-auto=update on
# startup. Spring reads the DataSource from SPRING_DATASOURCE_* (or DATABASE_URL).

# ── Database (Spring Data JPA / Hibernate, %s) ────────────────────────────────
# DATABASE_URL / SPRING_DATASOURCE_URL: full JDBC URL, e.g.
#   %s
DATABASE_URL=<your-jdbc-url>
SPRING_DATASOURCE_URL=<your-jdbc-url>
SPRING_DATASOURCE_USERNAME=<your-db-user>
SPRING_DATASOURCE_PASSWORD=<your-db-password>
DB_NAME=%s

# ── gRPC server (grpc-java / grpc-spring-boot-starter) ─────────────────────
# GRPC_PORT maps to grpc.server.port (net.devh default 9090).
GRPC_PORT=%d
%s`, name, engineLabel, driverDep, engineLabel, jdbcExample, db, port, authBlock(authEnabled))
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
