package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"
	workerdomain "milton_prism/core/worker/analysis/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dep(eco workerdomain.Ecosystem, pkg string) workerdomain.Dependency {
	return workerdomain.Dependency{Ecosystem: eco, Package: pkg}
}

func fwTech(slug string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: slug, Category: "framework", Slug: slug}
}

// engineSet returns the engine set as a map for order-independent assertions.
func engineSet(dd *analysisdomain.DatabaseDetection) map[analysisdomain.DatabaseEngine]bool {
	m := map[analysisdomain.DatabaseEngine]bool{}
	for _, e := range dd.GetEngines() {
		m[e] = true
	}
	return m
}

func TestDatabaseDetector_PostgresFromPsycopg2(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemPyPI, "psycopg2"),
		dep(workerdomain.EcosystemPyPI, "flask"),
	}, nil)
	require.NoError(t, err)
	require.False(t, dd.GetUnknown())
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEnginePostgreSQL])
	assert.Len(t, dd.GetEngines(), 1)
}

func TestDatabaseDetector_MySQLFromComposerPDO(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemComposer, "ext-pdo_mysql"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineMySQL])
}

func TestDatabaseDetector_LaravelDefaultMySQL(t *testing.T) {
	t.Parallel()
	// Laravel framework + Eloquent ORM but no explicit driver/config ⇒ MySQL default.
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemComposer, "laravel/framework"),
	}, []*analysisdomain.Technology{fwTech("laravel")})
	require.NoError(t, err)
	require.False(t, dd.GetUnknown())
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineMySQL])
	// Evidence must name the inference honestly.
	joined := ""
	for _, e := range dd.GetEvidence() {
		joined += e + "|"
	}
	assert.Contains(t, joined, "Laravel")
}

func TestDatabaseDetector_MongoFromPymongo(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemPyPI, "pymongo"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineMongoDB])
}

func TestDatabaseDetector_SQLiteFromSqlite3(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "sqlite3"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineSQLite])
}

func TestDatabaseDetector_RedisDemotedWhenRelationalPresent(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "pg"),
		dep(workerdomain.EcosystemNpm, "ioredis"),
	}, nil)
	require.NoError(t, err)
	es := engineSet(dd)
	assert.True(t, es[analysisdomain.DatabaseEnginePostgreSQL])
	assert.False(t, es[analysisdomain.DatabaseEngineRedis], "Redis must be demoted when a relational engine is present")
}

func TestDatabaseDetector_RedisKeptWhenSoleSignal(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "ioredis"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineRedis])
}

func TestDatabaseDetector_UnknownWhenNoSignal(t *testing.T) {
	t.Parallel()
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemPyPI, "requests"),
		dep(workerdomain.EcosystemPyPI, "click"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, dd.GetUnknown())
	assert.Empty(t, dd.GetEngines())
}

func TestDatabaseDetector_TokenBoundary_NoFalsePositive(t *testing.T) {
	t.Parallel()
	// "imagepng" must NOT match the "pg" PostgreSQL token; "mysqldump-helper" is a
	// real package containing "mysql" as a whole token and SHOULD match.
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "imagepng"),
	}, nil)
	require.NoError(t, err)
	assert.True(t, dd.GetUnknown(), "imagepng must not be read as a postgres driver")
}

func TestDatabaseDetector_EnvDBConnection(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".env"),
		[]byte("APP_ENV=local\nDB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\n"), 0644))
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), ws, nil, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEnginePostgreSQL])
}

func TestDatabaseDetector_LaravelConfigDefault(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "config"), 0755))
	php := `<?php return [ 'default' => env('DB_CONNECTION', 'mysql'), 'connections' => [] ];`
	require.NoError(t, os.WriteFile(filepath.Join(ws, "config", "database.php"), []byte(php), 0644))
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), ws, nil, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineMySQL])
}

func TestDatabaseDetector_LaravelDefaultBeatsRedisOnly(t *testing.T) {
	t.Parallel()
	// BookStack-like: Laravel + predis (Redis cache) but no relational driver.
	// MySQL (Laravel default) must win; Redis is demoted to a cache.
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemComposer, "laravel/framework"),
		dep(workerdomain.EcosystemComposer, "predis/predis"),
	}, []*analysisdomain.Technology{fwTech("laravel")})
	require.NoError(t, err)
	es := engineSet(dd)
	assert.True(t, es[analysisdomain.DatabaseEngineMySQL], "Laravel default MySQL must be detected")
	assert.False(t, es[analysisdomain.DatabaseEngineRedis], "Redis must be demoted when a primary engine is present")
}

func TestDatabaseDetector_CodeIgniterDbdriverArrayForm(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "application", "config"), 0755))
	php := `<?php
$db['default'] = array(
	'hostname' => 'localhost',
	'dbdriver' => 'mysqli',
	'username' => 'root',
);`
	require.NoError(t, os.WriteFile(filepath.Join(ws, "application", "config", "database.php"), []byte(php), 0644))
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), ws, nil, []*analysisdomain.Technology{fwTech("codeigniter")})
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEngineMySQL], "CI3 dbdriver=mysqli ⇒ MySQL")
}

func TestDatabaseDetector_CodeIgniterDbdriverAssignForm(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "application", "config"), 0755))
	php := `<?php $db['default']['dbdriver'] = 'postgre';`
	require.NoError(t, os.WriteFile(filepath.Join(ws, "application", "config", "database.php"), []byte(php), 0644))
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), ws, nil, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEnginePostgreSQL])
}

func TestDatabaseDetector_DjangoSettingsEngine(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "myproj"), 0755))
	settings := `DATABASES = {'default': {'ENGINE': 'django.db.backends.postgresql', 'NAME': 'app'}}`
	require.NoError(t, os.WriteFile(filepath.Join(ws, "myproj", "settings.py"), []byte(settings), 0644))
	d := adapters.NewDatabaseDetector()
	dd, err := d.Detect(context.Background(), ws, nil, nil)
	require.NoError(t, err)
	assert.True(t, engineSet(dd)[analysisdomain.DatabaseEnginePostgreSQL])
}
