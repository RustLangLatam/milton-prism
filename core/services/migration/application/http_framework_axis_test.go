package application_test

import (
	"context"
	"testing"

	"milton_prism/core/services/migration/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateMigration_GoHTTPGin_Accepted proves the (Go, HTTP, Gin) cell is
// generable: CreateMigration accepts a Go + HTTP migration whose http_framework
// is GO_GIN (no MIG112) and persists the framework explicitly.
func TestCreateMigration_GoHTTPGin_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = domain.TransportHTTP
	m.Target.HttpFramework = domain.HttpFrameworkGoGin

	var persisted *domain.Migration
	stored := &domain.Migration{Identifier: 10030, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		persisted = args.Get(1).(*domain.Migration)
	}).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10030), out.GetIdentifier())
	require.NotNil(t, persisted)
	assert.Equal(t, domain.HttpFrameworkGoGin, persisted.GetTarget().GetHttpFramework(),
		"Gin must persist as the explicit framework")
}

// TestCreateMigration_GoHTTPUnspecifiedFramework_CanonicalisedToNetHTTP proves the
// framework canonicalisation mirror of transport: an HTTP migration with an
// UNSPECIFIED http_framework is persisted as the language default (Go →
// GO_NET_HTTP) so existing callers that omit the field keep the certified
// net/http behaviour.
func TestCreateMigration_GoHTTPUnspecifiedFramework_CanonicalisedToNetHTTP(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = domain.TransportHTTP // framework left UNSPECIFIED
	require.Equal(t, domain.HttpFrameworkUnspecified, m.GetTarget().GetHttpFramework())

	var persisted *domain.Migration
	stored := &domain.Migration{Identifier: 10031, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		persisted = args.Get(1).(*domain.Migration)
	}).Return(stored, nil)

	_, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, domain.HttpFrameworkGoNetHTTP, persisted.GetTarget().GetHttpFramework(),
		"unspecified HTTP framework must canonicalise to the Go default net/http")
}

// TestCreateMigration_GoHTTPEcho_Rejected proves a not-yet-generable HTTP
// framework (Go + Echo) is rejected at creation with MIG112. Echo is declared in
// the enum for selection but absent from supportedHttpFrameworkByLanguage.
func TestCreateMigration_GoHTTPEcho_Rejected(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = domain.TransportHTTP
	m.Target.HttpFramework = domain.HttpFrameworkGoEcho

	_, err := svc.CreateMigration(context.Background(), m)
	require.Error(t, err)
	de, ok := err.(*domain.Error)
	require.True(t, ok, "expected a typed domain error, got %T", err)
	assert.Equal(t, domain.ErrCodeUnsupportedHttpFramework, de.Code, "must be MIG112")
}

// TestCreateMigration_GoGRPCFrameworkIgnored proves the HTTP-framework sub-axis is
// IGNORED for gRPC: a Go + gRPC migration carrying a (meaningless) http_framework
// is accepted and the framework is forced back to UNSPECIFIED on persist.
func TestCreateMigration_GoGRPCFrameworkIgnored(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.InterServiceTransport = domain.TransportGRPC
	m.Target.HttpFramework = domain.HttpFrameworkGoGin // nonsensical for gRPC; must be ignored

	var persisted *domain.Migration
	stored := &domain.Migration{Identifier: 10032, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		persisted = args.Get(1).(*domain.Migration)
	}).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10032), out.GetIdentifier())
	require.NotNil(t, persisted)
	assert.Equal(t, domain.HttpFrameworkUnspecified, persisted.GetTarget().GetHttpFramework(),
		"gRPC must force the HTTP framework back to UNSPECIFIED")
}
