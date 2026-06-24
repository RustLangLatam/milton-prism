package application_test

import (
	"context"
	"errors"
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

// TestCreateMigration_PythonPostgres_Rejected proves SQL on a non-Go language is a
// v1 hole: a Python + PostgreSQL migration is rejected with MIG111.
func TestCreateMigration_PythonPostgres_Rejected(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguagePython
	m.Target.Database = domain.TargetDatabasePostgres

	_, err := svc.CreateMigration(context.Background(), m)
	require.Error(t, err)
	var dErr *domain.Error
	require.True(t, errors.As(err, &dErr), "want a domain.Error")
	assert.Equal(t, domain.ErrCodeUnsupportedDatabase, dErr.Code)
}
