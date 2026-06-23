package application_test

import (
	"context"
	"testing"

	"milton_prism/core/services/migration/domain"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateMigration_GoHTTP_Accepted proves the (Go, HTTP) cell is generable:
// CreateMigration accepts a Go + HTTP migration (no MIG109) and proceeds to
// create it. This is the first certified HTTP cell.
func TestCreateMigration_GoHTTP_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = domain.TransportHTTP
	stored := &domain.Migration{Identifier: 10010, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10010), out.GetIdentifier())
}

// TestCreateMigration_PythonHTTP_Accepted proves the (Python, HTTP) cell is now
// generable: CreateMigration accepts a Python + HTTP migration (no MIG109) and
// proceeds to create it. This is the FastAPI-native HTTP cell.
func TestCreateMigration_PythonHTTP_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguagePython
	m.Target.InterServiceTransport = domain.TransportHTTP
	stored := &domain.Migration{Identifier: 10013, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10013), out.GetIdentifier())
}

// TestCreateMigration_NodeHTTP_Accepted proves the (Node, HTTP) cell is now
// generable: CreateMigration accepts a Node + HTTP migration (no MIG109) and
// proceeds to create it. This is the Fastify-native HTTP cell.
func TestCreateMigration_NodeHTTP_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageNode
	m.Target.InterServiceTransport = domain.TransportHTTP
	stored := &domain.Migration{Identifier: 10014, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10014), out.GetIdentifier())
}

// TestCreateMigration_RustHTTP_Accepted proves the (Rust, HTTP) cell is now
// generable: CreateMigration accepts a Rust + HTTP migration (no MIG109) and
// proceeds to create it. This is the axum-native HTTP cell that completes the
// HTTP matrix (Go/Python/Node/Rust all support HTTP).
func TestCreateMigration_RustHTTP_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageRust
	m.Target.InterServiceTransport = domain.TransportHTTP
	stored := &domain.Migration{Identifier: 10015, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10015), out.GetIdentifier())
}

// TestCreateMigration_TransportCanonicalised proves the canonicalisation mirror of
// topology: an unspecified inter_service_transport is persisted as gRPC, so
// existing callers that omit the field behave exactly as before.
func TestCreateMigration_TransportCanonicalised(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration() // no InterServiceTransport set ⇒ UNSPECIFIED
	require.Equal(t, domain.TransportUnspecified, m.GetTarget().GetInterServiceTransport())

	var persisted *domain.Migration
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	stored := &domain.Migration{Identifier: 10011, State: domain.MigrationStatePending}
	repo.On("Create", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		persisted = args.Get(1).(*domain.Migration)
	}).Return(stored, nil)

	_, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, domain.TransportGRPC, persisted.GetTarget().GetInterServiceTransport(),
		"unspecified transport must canonicalise to gRPC on persist")
}

// TestCreateMigration_GoGRPC_Unaffected proves an explicit Go + gRPC migration
// (the established default) is still accepted unchanged.
func TestCreateMigration_GoGRPC_Unaffected(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = migrationv1.Transport_TRANSPORT_GRPC
	stored := &domain.Migration{Identifier: 10012, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10012), out.GetIdentifier())
}
