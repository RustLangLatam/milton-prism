package adapters

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.DatabaseDetector = (*DatabaseDetector)(nil)

// DatabaseDetector deterministically identifies the database engine(s) the
// analysed code uses. It is intentionally conservative: a finding is recorded
// only when a concrete driver/ORM package or a config value names an engine.
// When nothing names an engine the result is Unknown — never a guess.
//
// Signal precedence (all signals contribute; precedence only orders evidence):
//  1. drivers/ORM declared in parsed manifests (most authoritative — the code
//     literally links against the engine's client);
//  2. config files in the workspace (.env DB_CONNECTION, Laravel
//     config/database.php default, DATABASE_URL scheme, Django settings ENGINE);
//  3. framework defaults (Laravel ⇒ MySQL) used ONLY as a tie-break when an ORM
//     is present but no explicit driver/connection is, so the result stays honest.
type DatabaseDetector struct{}

// NewDatabaseDetector returns a ready DatabaseDetector.
func NewDatabaseDetector() *DatabaseDetector { return &DatabaseDetector{} }

// engineDisplay maps an engine enum to its human-readable name.
var engineDisplay = map[analysisdomain.DatabaseEngine]string{
	analysisdomain.DatabaseEnginePostgreSQL: "PostgreSQL",
	analysisdomain.DatabaseEngineMySQL:      "MySQL",
	analysisdomain.DatabaseEngineMongoDB:    "MongoDB",
	analysisdomain.DatabaseEngineSQLite:     "SQLite",
	analysisdomain.DatabaseEngineSQLServer:  "SQL Server",
	analysisdomain.DatabaseEngineOracle:     "Oracle",
	analysisdomain.DatabaseEngineRedis:      "Redis",
}

// driverRule matches a database driver/ORM package by substring against the
// lowercased package name. The match is whole-token aware: substr must be
// bounded by a non-alphanumeric character (or string edge) so that, e.g.,
// "pg" does not match "imagepng". Composer vendor/name and the last path
// segment are both tested.
type driverRule struct {
	substr   string
	engine   analysisdomain.DatabaseEngine
	evidence string
}

// driverRules is the deterministic driver/ORM detection table. Order matters only
// for evidence stability; every matching rule contributes its engine.
var driverRules = []driverRule{
	// PostgreSQL
	{"psycopg2", analysisdomain.DatabaseEnginePostgreSQL, "driver: psycopg2 (PostgreSQL)"},
	{"psycopg", analysisdomain.DatabaseEnginePostgreSQL, "driver: psycopg (PostgreSQL)"},
	{"asyncpg", analysisdomain.DatabaseEnginePostgreSQL, "driver: asyncpg (PostgreSQL)"},
	{"pg8000", analysisdomain.DatabaseEnginePostgreSQL, "driver: pg8000 (PostgreSQL)"},
	{"node-postgres", analysisdomain.DatabaseEnginePostgreSQL, "driver: node-postgres (PostgreSQL)"},
	{"pg-promise", analysisdomain.DatabaseEnginePostgreSQL, "driver: pg-promise (PostgreSQL)"},
	{"postgres", analysisdomain.DatabaseEnginePostgreSQL, "driver: postgres (PostgreSQL)"},
	{"pdo_pgsql", analysisdomain.DatabaseEnginePostgreSQL, "driver: pdo_pgsql (PostgreSQL)"},
	{"pgsql", analysisdomain.DatabaseEnginePostgreSQL, "driver: pgsql (PostgreSQL)"},
	{"pg", analysisdomain.DatabaseEnginePostgreSQL, "driver: pg (node-postgres, PostgreSQL)"},
	// MySQL / MariaDB
	{"mysqlclient", analysisdomain.DatabaseEngineMySQL, "driver: mysqlclient (MySQL)"},
	{"mysql-connector", analysisdomain.DatabaseEngineMySQL, "driver: mysql-connector (MySQL)"},
	{"pymysql", analysisdomain.DatabaseEngineMySQL, "driver: PyMySQL (MySQL)"},
	{"mariadb", analysisdomain.DatabaseEngineMySQL, "driver: mariadb (MySQL/MariaDB)"},
	{"mysql2", analysisdomain.DatabaseEngineMySQL, "driver: mysql2 (MySQL)"},
	{"mysqli", analysisdomain.DatabaseEngineMySQL, "driver: mysqli (MySQL)"},
	{"pdo_mysql", analysisdomain.DatabaseEngineMySQL, "driver: pdo_mysql (MySQL)"},
	{"mysql", analysisdomain.DatabaseEngineMySQL, "driver: mysql (MySQL)"},
	// MongoDB
	{"mongoose", analysisdomain.DatabaseEngineMongoDB, "driver: mongoose (MongoDB)"},
	{"pymongo", analysisdomain.DatabaseEngineMongoDB, "driver: pymongo (MongoDB)"},
	{"mongoengine", analysisdomain.DatabaseEngineMongoDB, "driver: mongoengine (MongoDB)"},
	{"mongodb", analysisdomain.DatabaseEngineMongoDB, "driver: mongodb (MongoDB)"},
	{"mongo", analysisdomain.DatabaseEngineMongoDB, "driver: mongo (MongoDB)"},
	// SQLite
	{"better-sqlite3", analysisdomain.DatabaseEngineSQLite, "driver: better-sqlite3 (SQLite)"},
	{"pdo_sqlite", analysisdomain.DatabaseEngineSQLite, "driver: pdo_sqlite (SQLite)"},
	{"sqlite3", analysisdomain.DatabaseEngineSQLite, "driver: sqlite3 (SQLite)"},
	{"sqlite", analysisdomain.DatabaseEngineSQLite, "driver: sqlite (SQLite)"},
	// SQL Server
	{"pdo_sqlsrv", analysisdomain.DatabaseEngineSQLServer, "driver: pdo_sqlsrv (SQL Server)"},
	{"sqlsrv", analysisdomain.DatabaseEngineSQLServer, "driver: sqlsrv (SQL Server)"},
	{"pymssql", analysisdomain.DatabaseEngineSQLServer, "driver: pymssql (SQL Server)"},
	{"tedious", analysisdomain.DatabaseEngineSQLServer, "driver: tedious (SQL Server)"},
	// Oracle
	{"cx_oracle", analysisdomain.DatabaseEngineOracle, "driver: cx_Oracle (Oracle)"},
	{"cx-oracle", analysisdomain.DatabaseEngineOracle, "driver: cx_Oracle (Oracle)"},
	{"oracledb", analysisdomain.DatabaseEngineOracle, "driver: oracledb (Oracle)"},
	{"oci8", analysisdomain.DatabaseEngineOracle, "driver: oci8 (Oracle)"},
	// Redis (only surfaced when it is the sole signal — see Detect)
	{"ioredis", analysisdomain.DatabaseEngineRedis, "driver: ioredis (Redis)"},
	{"predis", analysisdomain.DatabaseEngineRedis, "driver: predis (Redis)"},
	{"redis", analysisdomain.DatabaseEngineRedis, "driver: redis (Redis)"},
}

// ormRules detects ORMs that do not name an engine on their own. They do not add
// an engine directly; instead they enable the framework-default tie-break and are
// recorded as evidence so the report can explain the inference.
var ormRules = []struct {
	substr   string
	evidence string
}{
	{"sqlalchemy", "orm: SQLAlchemy"},
	{"flask-sqlalchemy", "orm: Flask-SQLAlchemy"},
	{"doctrine/orm", "orm: Doctrine"},
	{"laravel/framework", "orm: Eloquent (Laravel)"},
	{"sequelize", "orm: Sequelize"},
	{"typeorm", "orm: TypeORM"},
	{"prisma", "orm: Prisma"},
	{"django", "orm: Django ORM"},
	{"peewee", "orm: Peewee"},
}

// dbConnectionRe extracts the value of DB_CONNECTION from an .env file.
var dbConnectionRe = regexp.MustCompile(`(?mi)^\s*DB_CONNECTION\s*=\s*['"]?([a-z0-9_]+)`)

// databaseURLRe extracts the scheme of a DATABASE_URL value (postgres://, mysql://…).
var databaseURLRe = regexp.MustCompile(`(?mi)^\s*DATABASE_URL\s*=\s*['"]?([a-z0-9+]+)://`)

// laravelDefaultRe extracts the 'default' connection from Laravel's config/database.php,
// honouring the common env('DB_CONNECTION', 'mysql') fallback form.
var laravelDefaultRe = regexp.MustCompile(`(?s)'default'\s*=>\s*env\(\s*'DB_CONNECTION'\s*,\s*'([a-z0-9_]+)'`)

// djangoEngineRe extracts the ENGINE backend from a Django settings DATABASES block,
// e.g. 'django.db.backends.postgresql'.
var djangoEngineRe = regexp.MustCompile(`django\.db\.backends\.([a-z0-9_]+)`)

// ciDriverRe extracts the 'dbdriver' value from a CodeIgniter database config,
// matching both common CI forms:
//   - $db['default']['dbdriver'] = 'mysqli';
//   - $db['default'] = array( ... 'dbdriver' => 'mysqli', ... );
var ciDriverRe = regexp.MustCompile(`(?i)['"]dbdriver['"]\s*(?:\]\s*=|=>)\s*['"]([a-z0-9_]+)['"]`)

// connectionKeyword maps a Laravel/.env DB_CONNECTION or DATABASE_URL scheme to an
// engine. Keys are the lowercased connection/scheme tokens.
var connectionKeyword = map[string]analysisdomain.DatabaseEngine{
	"pgsql":      analysisdomain.DatabaseEnginePostgreSQL,
	"postgres":   analysisdomain.DatabaseEnginePostgreSQL,
	"postgresql": analysisdomain.DatabaseEnginePostgreSQL,
	"mysql":      analysisdomain.DatabaseEngineMySQL,
	"mariadb":    analysisdomain.DatabaseEngineMySQL,
	"sqlite":     analysisdomain.DatabaseEngineSQLite,
	"sqlsrv":     analysisdomain.DatabaseEngineSQLServer,
	"mssql":      analysisdomain.DatabaseEngineSQLServer,
	"oracle":     analysisdomain.DatabaseEngineOracle,
	"oci":        analysisdomain.DatabaseEngineOracle,
	"mongodb":    analysisdomain.DatabaseEngineMongoDB,
	"mongo":      analysisdomain.DatabaseEngineMongoDB,
	// CodeIgniter dbdriver tokens.
	"mysqli":  analysisdomain.DatabaseEngineMySQL,
	"postgre": analysisdomain.DatabaseEnginePostgreSQL,
	"oci8":    analysisdomain.DatabaseEngineOracle,
	"sqlite3": analysisdomain.DatabaseEngineSQLite,
}

// Detect returns the deterministically detected database engines and the evidence
// behind them. Errors are never returned for missing/unreadable config files —
// the detector degrades to whatever signals it could read.
func (d *DatabaseDetector) Detect(
	_ context.Context,
	workspacePath string,
	deps []workerdomain.Dependency,
	technologies []*analysisdomain.Technology,
) (*analysisdomain.DatabaseDetection, error) {
	// engineEvidence keeps, per engine, the first evidence string that surfaced it,
	// so output is deterministic and free of duplicates.
	engineEvidence := map[analysisdomain.DatabaseEngine]string{}
	var ormEvidence []string

	add := func(e analysisdomain.DatabaseEngine, ev string) {
		if _, ok := engineEvidence[e]; !ok {
			engineEvidence[e] = ev
		}
	}

	// ── Signal 1: drivers/ORM packages ──────────────────────────────────────
	for _, dep := range deps {
		name := strings.ToLower(dep.Package)
		for _, r := range driverRules {
			if tokenContains(name, r.substr) {
				add(r.engine, r.evidence)
				break // first (most specific) driver rule per package
			}
		}
		for _, o := range ormRules {
			if tokenContains(name, o.substr) {
				ormEvidence = appendUnique(ormEvidence, o.evidence)
				break
			}
		}
	}

	// ── Signal 2: config files ──────────────────────────────────────────────
	if workspacePath != "" {
		d.scanEnv(workspacePath, add)
		d.scanLaravelDatabaseConfig(workspacePath, add)
		d.scanCodeIgniterDatabaseConfig(workspacePath, add)
		d.scanDjangoSettings(workspacePath, add)
	}

	// ── Signal 3: framework default tie-break ───────────────────────────────
	// When an ORM/framework implies a primary store but no concrete *relational or
	// document* engine surfaced, fall back to the framework default. Laravel's
	// default connection is MySQL. Redis-only does NOT count as a primary engine —
	// it is almost always a cache, so the tie-break still fires past it.
	if !hasPrimaryEngine(engineEvidence) {
		if len(ormEvidence) > 0 && hasFramework(technologies, "laravel") {
			add(analysisdomain.DatabaseEngineMySQL, "framework default: Laravel ⇒ MySQL")
		} else if hasFramework(technologies, "laravel") {
			// Laravel without any DB package still defaults to a relational store.
			add(analysisdomain.DatabaseEngineMySQL, "framework default: Laravel ⇒ MySQL")
		}
	}

	// Redis demotion: Redis is a cache, not the system of record, whenever a primary
	// (relational/document) engine is also present — including one inferred from a
	// framework default above. Redis survives only when it is the sole signal.
	if hasPrimaryEngine(engineEvidence) {
		delete(engineEvidence, analysisdomain.DatabaseEngineRedis)
	}

	det := &analysisdomain.DatabaseDetection{}
	if len(engineEvidence) == 0 {
		det.Unknown = true
		// Preserve ORM evidence even when no engine was named — it explains the
		// honest 'unknown' (an ORM is present but the engine is configured at runtime).
		det.Evidence = ormEvidence
		return det, nil
	}

	engines := make([]analysisdomain.DatabaseEngine, 0, len(engineEvidence))
	for e := range engineEvidence {
		engines = append(engines, e)
	}
	sort.Slice(engines, func(i, j int) bool { return engines[i] < engines[j] })

	for _, e := range engines {
		det.Engines = append(det.Engines, e)
		det.EngineNames = append(det.EngineNames, engineDisplay[e])
		det.Evidence = append(det.Evidence, engineEvidence[e])
	}
	det.Evidence = append(det.Evidence, ormEvidence...)
	return det, nil
}

// scanEnv reads a .env (and .env.example as fallback) for DB_CONNECTION and
// DATABASE_URL. Best-effort: missing files are silently ignored.
func (d *DatabaseDetector) scanEnv(workspacePath string, add func(analysisdomain.DatabaseEngine, string)) {
	for _, name := range []string{".env", ".env.example", ".env.dist"} {
		body, err := os.ReadFile(filepath.Join(workspacePath, name))
		if err != nil {
			continue
		}
		text := string(body)
		if m := dbConnectionRe.FindStringSubmatch(text); m != nil {
			if e, ok := connectionKeyword[strings.ToLower(m[1])]; ok {
				add(e, "config: "+name+" DB_CONNECTION="+m[1])
			}
		}
		if m := databaseURLRe.FindStringSubmatch(text); m != nil {
			if e, ok := connectionKeyword[strings.ToLower(m[1])]; ok {
				add(e, "config: "+name+" DATABASE_URL scheme "+m[1])
			}
		}
		return // first existing env file wins
	}
}

// scanLaravelDatabaseConfig reads config/database.php and resolves the 'default'
// connection's env fallback (env('DB_CONNECTION', 'mysql')).
func (d *DatabaseDetector) scanLaravelDatabaseConfig(workspacePath string, add func(analysisdomain.DatabaseEngine, string)) {
	body, err := os.ReadFile(filepath.Join(workspacePath, "config", "database.php"))
	if err != nil {
		return
	}
	if m := laravelDefaultRe.FindStringSubmatch(string(body)); m != nil {
		if e, ok := connectionKeyword[strings.ToLower(m[1])]; ok {
			add(e, "config: config/database.php default="+m[1])
		}
	}
}

// scanCodeIgniterDatabaseConfig reads CodeIgniter's database config and extracts the
// configured dbdriver. CI3 uses application/config/database.php; CI4 uses
// app/Config/Database.php (which references env, handled by scanEnv).
func (d *DatabaseDetector) scanCodeIgniterDatabaseConfig(workspacePath string, add func(analysisdomain.DatabaseEngine, string)) {
	for _, rel := range []string{
		filepath.Join("application", "config", "database.php"),
		filepath.Join("app", "Config", "Database.php"),
	} {
		body, err := os.ReadFile(filepath.Join(workspacePath, rel))
		if err != nil {
			continue
		}
		if m := ciDriverRe.FindStringSubmatch(string(body)); m != nil {
			if e, ok := connectionKeyword[strings.ToLower(m[1])]; ok {
				add(e, "config: "+rel+" dbdriver="+m[1])
				return
			}
		}
	}
}

// hasPrimaryEngine reports whether any detected engine is a primary system of
// record (i.e. anything other than Redis, which is treated as a cache).
func hasPrimaryEngine(engines map[analysisdomain.DatabaseEngine]string) bool {
	for e := range engines {
		if e != analysisdomain.DatabaseEngineRedis {
			return true
		}
	}
	return false
}

// scanDjangoSettings looks for a Django settings module and extracts the DATABASES
// ENGINE backend. Searches the common settings.py locations shallowly.
func (d *DatabaseDetector) scanDjangoSettings(workspacePath string, add func(analysisdomain.DatabaseEngine, string)) {
	candidates := []string{
		"settings.py",
		filepath.Join("config", "settings.py"),
	}
	// Also scan one level of <project>/settings.py and <project>/settings/*.py.
	entries, _ := os.ReadDir(workspacePath)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidates = append(candidates,
			filepath.Join(e.Name(), "settings.py"),
			filepath.Join(e.Name(), "settings", "base.py"),
			filepath.Join(e.Name(), "settings", "production.py"),
		)
	}
	for _, rel := range candidates {
		body, err := os.ReadFile(filepath.Join(workspacePath, rel))
		if err != nil {
			continue
		}
		if m := djangoEngineRe.FindStringSubmatch(string(body)); m != nil {
			backend := strings.ToLower(m[1])
			if e, ok := connectionKeyword[backend]; ok {
				add(e, "config: "+rel+" ENGINE django.db.backends."+m[1])
				return
			}
		}
	}
}

// tokenContains reports whether substr appears in name as a whole token, i.e. it
// is bounded by a non-alphanumeric character (or the string edge) on both sides.
// This stops "pg" from matching "imagepng" while still matching "node-pg" or "pg".
// Composer vendor/name forms are handled because '/' is a boundary.
func tokenContains(name, substr string) bool {
	i := 0
	for {
		idx := strings.Index(name[i:], substr)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(substr)
		leftOK := start == 0 || !isAlnum(name[start-1])
		rightOK := end == len(name) || !isAlnum(name[end])
		if leftOK && rightOK {
			return true
		}
		i = start + 1
		if i >= len(name) {
			return false
		}
	}
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// hasFramework reports whether technologies contains a framework entry with the
// given slug (case-insensitive).
func hasFramework(technologies []*analysisdomain.Technology, slug string) bool {
	for _, t := range technologies {
		if t.GetCategory() == "framework" && strings.EqualFold(t.GetSlug(), slug) {
			return true
		}
		if strings.EqualFold(t.GetName(), slug) {
			return true
		}
	}
	return false
}

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}
