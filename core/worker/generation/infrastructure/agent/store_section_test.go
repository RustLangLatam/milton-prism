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
		for _, profile := range []string{"go", "python", "node", "rust"} {
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

// TestStoreSection_SQLHoleIsHonest asserts that a SQL store on a non-Go, non-Python
// profile (a v1 hole) yields an honest "NOT generated in v1" note that forbids
// guessing, rather than a fabricated implementation. (Unreachable while the
// IsGenerableDatabase guard rejects the cell at creation, but kept self-consistent.)
func TestStoreSection_SQLHoleIsHonest(t *testing.T) {
	for _, profile := range []string{"node", "rust"} {
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
