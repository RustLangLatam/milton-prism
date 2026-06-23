package adapters

import (
	"testing"

	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// TestDatabaseStoreToken proves the TargetConfig.database override maps to the
// store label: POSTGRES→postgres, MARIADB→mysql, MONGODB/UNSPECIFIED→mongodb.
// This is the override (non-Auto) branch of resolveDatabase and the worker-side
// twin of migration.Service.storeLabel.
func TestDatabaseStoreToken(t *testing.T) {
	cases := []struct {
		db   migrationv1.TargetDatabase
		want string
	}{
		{migrationv1.TargetDatabase_TARGET_DATABASE_POSTGRES, "postgres"},
		{migrationv1.TargetDatabase_TARGET_DATABASE_MARIADB, "mysql"},
		{migrationv1.TargetDatabase_TARGET_DATABASE_MONGODB, "mongodb"},
		{migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED, "mongodb"},
	}
	for _, tc := range cases {
		if got := databaseStoreToken(tc.db); got != tc.want {
			t.Errorf("databaseStoreToken(%v) = %q, want %q", tc.db, got, tc.want)
		}
	}
}

// TestDetectedEngineStore proves the Auto (UNSPECIFIED) mapping from the analysis
// database_detection engines to a generation store: PostgreSQL→postgres,
// MySQL→mysql, MongoDB→mongodb, and anything else / empty (Redis-only, SQLite,
// unknown) degrades to mongodb (the original, always-generable path). The first
// recognised primary engine wins.
func TestDetectedEngineStore(t *testing.T) {
	eng := func(es ...analysisv1.DatabaseEngine) []analysisv1.DatabaseEngine { return es }
	cases := []struct {
		name    string
		engines []analysisv1.DatabaseEngine
		want    string
	}{
		{"postgres", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_POSTGRESQL), "postgres"},
		{"mysql", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_MYSQL), "mysql"},
		{"mongodb", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_MONGODB), "mongodb"},
		{"empty_degrades_mongo", eng(), "mongodb"},
		{"redis_only_degrades_mongo", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_REDIS), "mongodb"},
		{"sqlite_degrades_mongo", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_SQLITE), "mongodb"},
		// Primary store + Redis auxiliary: the primary (postgres) wins.
		{"postgres_plus_redis", eng(analysisv1.DatabaseEngine_DATABASE_ENGINE_POSTGRESQL, analysisv1.DatabaseEngine_DATABASE_ENGINE_REDIS), "postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectedEngineStore(tc.engines); got != tc.want {
				t.Errorf("detectedEngineStore(%v) = %q, want %q", tc.engines, got, tc.want)
			}
		})
	}
}
