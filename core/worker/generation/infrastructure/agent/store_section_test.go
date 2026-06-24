package agent_test

import (
	"strings"
	"testing"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestStoreSection_MongoDBInjectsNothing pins the non-regression contract: the
// MongoDB store (the original path) and an empty store inject NO block, so the
// established Mongo prompt behaviour is unchanged for every profile.
func TestStoreSection_MongoDBInjectsNothing(t *testing.T) {
	for _, store := range []string{"", "mongodb", "MongoDB"} {
		for _, profile := range []string{"go", "python", "node", "rust", "java"} {
			if got := agent.StoreSection(profile, store); got != "" {
				t.Errorf("StoreSection(%q, %q) = %q, want empty", profile, store, got)
			}
		}
	}
}

// TestStoreSection_GoSQLGORM asserts the Go + PostgreSQL and Go + MySQL/MariaDB
// blocks instruct the generator to emit a GORM persistence layer: GORM models in
// infrastructure/repositories mapping to/from domain, repos implementing the same
// ports, a gorm_client builder opening the connection with the engine's driver,
// AutoMigrate, gorm.DeletedAt soft-delete, autoincrement IDs, and DATABASE_URL/DB_*
// env with zero MONGO_* — the v1 certified SQL cells. Both stores share the same
// GORM scaffold; only the driver import + DSN differ.
func TestStoreSection_GoSQLGORM(t *testing.T) {
	cases := []struct {
		store      string
		engine     string
		driverPkg  string
		driverCtor string
	}{
		{"postgres", "PostgreSQL", "gorm.io/driver/postgres", "postgres.Open(dsn)"},
		{"mysql", "MySQL/MariaDB", "gorm.io/driver/mysql", "mysql.Open(dsn)"},
	}
	for _, tc := range cases {
		t.Run(tc.store, func(t *testing.T) {
			s := agent.StoreSection("go", tc.store)
			if s == "" {
				t.Fatalf("StoreSection(go, %q) is empty, want a GORM block", tc.store)
			}
			for _, want := range []string{
				tc.engine,
				tc.driverPkg,
				tc.driverCtor,
				"gorm.io/gorm",
				"GORM",
				"gorm_<resource>_repository.go",
				"gorm_client",
				"AutoMigrate",
				"gorm.DeletedAt",
				"autoIncrement",
				"WithTransaction",
				"DATABASE_URL",
				"DB_HOST",
				"go build ./...",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("Go+%s GORM store section missing %q", tc.engine, want)
				}
			}
			// No raw-SQL dialect leftovers: GORM owns placeholders/upserts, so the
			// block must not prescribe ON CONFLICT or $1 placeholders. (pgx and
			// golang-migrate may appear only as explicit "do NOT use" negations.)
			for _, forbid := range []string{"ON CONFLICT", "$1, $2", "BIGSERIAL"} {
				if strings.Contains(s, forbid) {
					t.Errorf("Go+%s GORM store section must not mention %q (raw-SQL leftover)", tc.engine, forbid)
				}
			}
			// The block names MONGO_* only to FORBID it; it must not SET one.
			if strings.Contains(s, "MONGO_URI") || strings.Contains(s, "MONGO_DATABASE") {
				t.Errorf("Go+%s store section must not prescribe MONGO_* env vars", tc.engine)
			}
		})
	}
}

// TestStoreSection_PythonSQLAlchemy asserts the Python + PostgreSQL and Python +
// MySQL/MariaDB blocks instruct the generator to emit a SQLAlchemy 2.0 async
// persistence layer: DeclarativeBase models in infrastructure/repositories mapping
// to/from domain, repos implementing the same Protocol ports, an async engine
// builder selecting the driver by store, create_all schema, nullable soft-delete,
// autoincrement IDs, and DATABASE_URL/DB_* env with zero MONGO_* — the v1 certified
// Python SQL cells. Both stores share the same SQLAlchemy scaffold; only the async
// driver + URL scheme differ.
func TestStoreSection_PythonSQLAlchemy(t *testing.T) {
	cases := []struct {
		store     string
		engine    string
		driverPkg string
		urlScheme string
	}{
		{"postgres", "PostgreSQL", "asyncpg", "postgresql+asyncpg"},
		{"mysql", "MySQL/MariaDB", "aiomysql", "mysql+aiomysql"},
	}
	for _, tc := range cases {
		t.Run(tc.store, func(t *testing.T) {
			s := agent.StoreSection("python", tc.store)
			if s == "" {
				t.Fatalf("StoreSection(python, %q) is empty, want a SQLAlchemy block", tc.store)
			}
			for _, want := range []string{
				tc.engine,
				tc.driverPkg,
				tc.urlScheme,
				"SQLAlchemy 2.0",
				"sqlalchemy[asyncio]",
				"DeclarativeBase",
				"sqlalchemy_<resource>_repository.py",
				"sqlalchemy_client",
				"create_all",
				"AsyncSession",
				"with_transaction",
				"DATABASE_URL",
				"DB_HOST",
				"python -m compileall",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("Python+%s SQLAlchemy store section missing %q", tc.engine, want)
				}
			}
			// SQLAlchemy owns the dialect; the block must not prescribe raw SQL
			// placeholders or sync drivers (named only to forbid).
			for _, forbid := range []string{"ON CONFLICT", "BIGSERIAL", "Alembic versions"} {
				if strings.Contains(s, forbid) {
					t.Errorf("Python+%s SQLAlchemy store section must not mention %q", tc.engine, forbid)
				}
			}
			if strings.Contains(s, "MONGO_URI") || strings.Contains(s, "MONGO_DATABASE") {
				t.Errorf("Python+%s store section must not prescribe MONGO_* env vars", tc.engine)
			}
		})
	}
}

// TestStoreSection_NodeSQLPrisma asserts the Node + PostgreSQL and Node + MySQL/
// MariaDB blocks instruct the generator to emit a Prisma persistence layer: ONE
// schema.prisma (datasource provider by store) + @prisma/client + repos
// implementing the same ports mapping Prisma model↔domain, schema by Prisma Migrate
// / db push, autoincrement BigInt PK, nullable soft-delete, DATABASE_URL/DB_* env.
// It is the Node homologue of the Go-GORM / Python-SQLAlchemy tests. Both stores
// share the same Prisma scaffold; only the datasource provider + DATABASE_URL differ.
func TestStoreSection_NodeSQLPrisma(t *testing.T) {
	cases := []struct {
		store    string
		engine   string
		provider string
	}{
		{"postgres", "PostgreSQL", "postgresql"},
		{"mysql", "MySQL/MariaDB", "mysql"},
	}
	for _, tc := range cases {
		t.Run(tc.store, func(t *testing.T) {
			s := agent.StoreSection("node", tc.store)
			if s == "" {
				t.Fatalf("StoreSection(node, %q) is empty, want a Prisma block", tc.store)
			}
			for _, want := range []string{
				tc.engine,
				"Prisma",
				"schema.prisma",
				"@prisma/client",
				tc.provider,
				"autoincrement",
				"DATABASE_URL",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("Node+%s Prisma store section missing %q", tc.engine, want)
				}
			}
			// Prisma owns the dialect; the block must not prescribe a raw SQL driver
			// or another ORM. (It DOES say "do NOT emit any MONGO_* variable", so the
			// literal "MONGO_" is expected and not forbidden here.)
			for _, forbid := range []string{"mysql2 ", "import { Pool", "TypeORM", "Sequelize"} {
				if strings.Contains(s, forbid) {
					t.Errorf("Node+%s Prisma store section must not mention %q", tc.engine, forbid)
				}
			}
		})
	}
}

// TestStoreSection_RustSQLSeaORM asserts the Rust + PostgreSQL and Rust + MySQL/
// MariaDB blocks instruct the generator to emit a SeaORM persistence layer: SeaORM
// entities in infrastructure/repositories mapping to/from domain (proto/prost),
// repos implementing the same port traits, a Database::connect builder selecting
// the sqlx driver feature by store, sea-orm-migration schema, nullable soft-delete,
// autoincrement IDs, and DATABASE_URL/DB_* env with zero MONGO_* — the v1 certified
// Rust SQL cells (the DB axis is now complete: 4 langs × Mongo/Postgres/MySQL). Both
// stores share the same SeaORM scaffold; only the sqlx driver feature + URL differ.
func TestStoreSection_RustSQLSeaORM(t *testing.T) {
	cases := []struct {
		store      string
		engine     string
		driverFeat string
	}{
		{"postgres", "PostgreSQL", "sqlx-postgres"},
		{"mysql", "MySQL/MariaDB", "sqlx-mysql"},
	}
	for _, tc := range cases {
		t.Run(tc.store, func(t *testing.T) {
			s := agent.StoreSection("rust", tc.store)
			if s == "" {
				t.Fatalf("StoreSection(rust, %q) is empty, want a SeaORM block", tc.store)
			}
			for _, want := range []string{
				tc.engine,
				"SeaORM",
				"sea-orm",
				"sea-orm-migration",
				tc.driverFeat,
				"Database::connect",
				"seaorm_<resource>_repository.rs",
				"primary_key",
				"with_transaction",
				"DATABASE_URL",
				"DB_HOST",
				"cargo build",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("Rust+%s SeaORM store section missing %q", tc.engine, want)
				}
			}
			// SeaORM owns the dialect; the block must not prescribe another Rust ORM
			// or raw-SQL leftovers. (It DOES say "do NOT emit any MONGO_* variable"
			// and "NOT the `mongodb`/`bson` crates" as negations, so those literals
			// are expected and not forbidden here.)
			for _, forbid := range []string{"diesel", "ON CONFLICT", "BIGSERIAL"} {
				if strings.Contains(s, forbid) {
					t.Errorf("Rust+%s SeaORM store section must not mention %q", tc.engine, forbid)
				}
			}
		})
	}
}

// TestStoreSection_JavaSQLJPA asserts the Java + PostgreSQL and Java + MySQL/
// MariaDB blocks instruct the generator to emit a Spring Data JPA persistence layer:
// @Entity classes in infrastructure/repositories mapping to/from domain, JpaRepository
// adapters implementing the same ports, a DataSource picking the JDBC driver by store,
// Hibernate ddl-auto=update schema, @Id @GeneratedValue(IDENTITY) IDs, nullable
// delete_time soft-delete, and DATABASE_URL/SPRING_DATASOURCE_* env with zero MONGO_* —
// the v1 certified Java SQL cells (the DB axis is complete: 5 langs × Mongo/Postgres/
// MySQL). Both stores share the same JPA scaffold; only the JDBC driver dep + URL differ.
func TestStoreSection_JavaSQLJPA(t *testing.T) {
	cases := []struct {
		store      string
		engine     string
		driverDep  string
		jdbcScheme string
	}{
		{"postgres", "PostgreSQL", "org.postgresql:postgresql", "jdbc:postgresql"},
		{"mysql", "MySQL/MariaDB", "org.mariadb.jdbc:mariadb-java-client", "jdbc:mariadb"},
	}
	for _, tc := range cases {
		t.Run(tc.store, func(t *testing.T) {
			s := agent.StoreSection("java", tc.store)
			if s == "" {
				t.Fatalf("StoreSection(java, %q) is empty, want a JPA block", tc.store)
			}
			for _, want := range []string{
				tc.engine,
				tc.driverDep,
				tc.jdbcScheme,
				"Spring Data JPA",
				"spring-boot-starter-data-jpa",
				"@Entity",
				"JpaRepository",
				"ddl-auto=update",
				"@GeneratedValue",
				"IDENTITY",
				"delete_time",
				"DATABASE_URL",
				"SPRING_DATASOURCE_",
				"mvn -B package",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("Java+%s JPA store section missing %q", tc.engine, want)
				}
			}
			// JPA/Hibernate owns the dialect; the block must not prescribe raw SQL
			// placeholders or Flyway/Liquibase (named only to forbid).
			for _, forbid := range []string{"ON CONFLICT", "BIGSERIAL"} {
				if strings.Contains(s, forbid) {
					t.Errorf("Java+%s JPA store section must not mention %q", tc.engine, forbid)
				}
			}
			// The block names MONGO_* / Spring Data MongoDB only to FORBID it; it must
			// not SET a MONGO_* variable.
			if strings.Contains(s, "MONGO_URI") || strings.Contains(s, "MONGO_DATABASE") {
				t.Errorf("Java+%s store section must not prescribe MONGO_* env vars", tc.engine)
			}
		})
	}
}

// TestStoreSection_SQLHoleIsHonest asserts that a SQL store on a profile without a
// SQL cell (no such generable language remains — every generable language now has a
// SQL cell, so this is only reachable via an unknown profile string) yields an
// honest "NOT generated in v1" note that forbids guessing, rather than a fabricated
// implementation. Kept self-consistent in case the guard is ever relaxed.
func TestStoreSection_SQLHoleIsHonest(t *testing.T) {
	for _, profile := range []string{"unknownlang"} {
		s := agent.StoreSection(profile, "postgres")
		if s == "" {
			t.Fatalf("StoreSection(%q, postgres) is empty, want an honest hole note", profile)
		}
		for _, want := range []string{"NOT generated in v1", "Do NOT guess", "MongoDB"} {
			if !strings.Contains(s, want) {
				t.Errorf("SQL-hole store section (%s) missing %q; got:\n%s", profile, want, s)
			}
		}
	}
}
