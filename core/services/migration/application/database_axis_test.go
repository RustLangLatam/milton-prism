package application_test

import (
	"context"
	"testing"

	"milton_prism/core/services/migration/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateMigration_GoPostgres_Accepted proves the (Go, PostgreSQL) cell is
// generable: CreateMigration accepts a Go + PostgreSQL migration (no MIG111) and
// proceeds to create it. This is the v1 certified SQL cell.
func TestCreateMigration_GoPostgres_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Database = domain.TargetDatabasePostgres
	stored := &domain.Migration{Identifier: 10020, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10020), out.GetIdentifier())
}

// TestCreateMigration_GoMongo_Unaffected proves the established Go + MongoDB
// default is still accepted unchanged (no regression from the database guard).
func TestCreateMigration_GoMongo_Unaffected(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration() // Go + MongoDB
	stored := &domain.Migration{Identifier: 10021, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10021), out.GetIdentifier())
}

// TestCreateMigration_GoMySQL_Accepted proves the (Go, MySQL/MariaDB) cell is now
// generable via the same GORM layer as PostgreSQL: CreateMigration accepts a Go +
// MariaDB migration (no MIG111) and proceeds to create it.
func TestCreateMigration_GoMySQL_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Database = domain.TargetDatabaseMariaDB
	stored := &domain.Migration{Identifier: 10022, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10022), out.GetIdentifier())
}

// TestCreateMigration_PythonPostgres_Accepted proves the (Python, PostgreSQL) cell
// is now generable via the SQLAlchemy 2.0 async layer: CreateMigration accepts a
// Python + PostgreSQL migration (no MIG111) and proceeds to create it.
func TestCreateMigration_PythonPostgres_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguagePython
	m.Target.Database = domain.TargetDatabasePostgres
	stored := &domain.Migration{Identifier: 10023, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10023), out.GetIdentifier())
}

// TestCreateMigration_PythonMySQL_Accepted proves the (Python, MySQL/MariaDB) cell
// is generable via the same SQLAlchemy layer as PostgreSQL (same models/repos, only
// the async driver differs): CreateMigration accepts it (no MIG111).
func TestCreateMigration_PythonMySQL_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguagePython
	m.Target.Database = domain.TargetDatabaseMariaDB
	stored := &domain.Migration{Identifier: 10024, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10024), out.GetIdentifier())
}

// TestCreateMigration_NodePostgres_Accepted proves the (Node, PostgreSQL) cell is
// now generable via the Prisma layer: CreateMigration accepts a Node + PostgreSQL
// migration (no MIG111) and proceeds to create it.
func TestCreateMigration_NodePostgres_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageNode
	m.Target.Database = domain.TargetDatabasePostgres
	stored := &domain.Migration{Identifier: 10025, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10025), out.GetIdentifier())
}

// TestCreateMigration_NodeMySQL_Accepted proves the (Node, MySQL/MariaDB) cell is
// generable via the same Prisma layer as PostgreSQL (one schema.prisma, only the
// datasource provider differs): CreateMigration accepts it (no MIG111).
func TestCreateMigration_NodeMySQL_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageNode
	m.Target.Database = domain.TargetDatabaseMariaDB
	stored := &domain.Migration{Identifier: 10026, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10026), out.GetIdentifier())
}

// TestCreateMigration_RustPostgres_Accepted proves the (Rust, PostgreSQL) cell is
// now generable via the SeaORM layer: CreateMigration accepts a Rust + PostgreSQL
// migration (no MIG111) and proceeds to create it. With this the DB axis is
// complete (Go-GORM, Python-SQLAlchemy, Node-Prisma, Rust-SeaORM).
func TestCreateMigration_RustPostgres_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageRust
	m.Target.Database = domain.TargetDatabasePostgres
	stored := &domain.Migration{Identifier: 10027, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10027), out.GetIdentifier())
}

// TestCreateMigration_RustMySQL_Accepted proves the (Rust, MySQL/MariaDB) cell is
// generable via the same SeaORM layer as PostgreSQL (one set of entities, only the
// sqlx driver feature differs): CreateMigration accepts it (no MIG111).
func TestCreateMigration_RustMySQL_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageRust
	m.Target.Database = domain.TargetDatabaseMariaDB
	stored := &domain.Migration{Identifier: 10028, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10028), out.GetIdentifier())
}

// TestCreateMigration_RustMongo_Unaffected proves the established Rust + MongoDB
// path is unchanged (native `mongodb` crate, NOT SeaORM): no MIG111, accepted.
func TestCreateMigration_RustMongo_Unaffected(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageRust
	m.Target.Database = domain.TargetDatabaseMongoDB
	stored := &domain.Migration{Identifier: 10029, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10029), out.GetIdentifier())
}
