package domain

import "testing"

// TestIsGenerableDatabase proves the (language, database) persistence matrix that
// backs the MIG111 guard in CreateMigration. The DB axis is COMPLETE: Go persists
// to MongoDB, PostgreSQL AND MySQL/MariaDB (SQL via the same GORM layer); Python
// via the same SQLAlchemy 2.0 async layer; Node (Mongo native driver) via the same
// Prisma layer; Rust (Mongo native crate) via the same SeaORM layer. All four
// generable languages support all three engines — no language-level hole remains.
// A non-generable language is false for every database.
func TestIsGenerableDatabase(t *testing.T) {
	cases := []struct {
		name string
		lang TargetLanguage
		db   TargetDatabase
		want bool
	}{
		{"go_mongodb", TargetLanguageGo, TargetDatabaseMongoDB, true},
		{"go_postgres", TargetLanguageGo, TargetDatabasePostgres, true}, // ← v1 SQL cell (GORM)
		{"go_mariadb", TargetLanguageGo, TargetDatabaseMariaDB, true},   // ← v1 SQL cell (GORM, same models)
		{"python_mongodb", TargetLanguagePython, TargetDatabaseMongoDB, true},
		{"python_postgres", TargetLanguagePython, TargetDatabasePostgres, true}, // ← v1 SQL cell (SQLAlchemy)
		{"python_mariadb", TargetLanguagePython, TargetDatabaseMariaDB, true},   // ← v1 SQL cell (SQLAlchemy, same models)
		{"node_mongodb", TargetLanguageNode, TargetDatabaseMongoDB, true},
		{"node_postgres", TargetLanguageNode, TargetDatabasePostgres, true}, // ← v1 SQL cell (Prisma)
		{"node_mariadb", TargetLanguageNode, TargetDatabaseMariaDB, true},   // ← v1 SQL cell (Prisma, same schema)
		{"rust_mongodb", TargetLanguageRust, TargetDatabaseMongoDB, true},
		{"rust_postgres", TargetLanguageRust, TargetDatabasePostgres, true}, // ← v1 SQL cell (SeaORM)
		{"rust_mariadb", TargetLanguageRust, TargetDatabaseMariaDB, true},   // ← v1 SQL cell (SeaORM, same entities)
		{"unspecified_lang_mongodb", TargetLanguageUnspecified, TargetDatabaseMongoDB, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGenerableDatabase(tc.lang, tc.db); got != tc.want {
				t.Errorf("IsGenerableDatabase(%v, %v) = %v, want %v", tc.lang, tc.db, got, tc.want)
			}
		})
	}
}

// TestUnsupportedDatabaseError pins the MIG111 code and Failure message (the exact
// MIG107/MIG109 pattern) so the gateway error map and panel contract stay stable.
func TestUnsupportedDatabaseError(t *testing.T) {
	if ErrCodeUnsupportedDatabase != "MIG111" {
		t.Errorf("ErrCodeUnsupportedDatabase = %q, want MIG111", ErrCodeUnsupportedDatabase)
	}
	if ErrUnsupportedDatabase.Code != "MIG111" {
		t.Errorf("ErrUnsupportedDatabase.Code = %q, want MIG111", ErrUnsupportedDatabase.Code)
	}
	if ErrUnsupportedDatabase.Message != "Failure_Unsupported_Database" {
		t.Errorf("ErrUnsupportedDatabase.Message = %q, want Failure_Unsupported_Database", ErrUnsupportedDatabase.Message)
	}
}
