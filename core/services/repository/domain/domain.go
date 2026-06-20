// Package domain contains the repository service's domain types and errors.
// All types are aliases of the generated proto types — no parallel structs.
package domain

import (
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"
)

type (
	Repository           = repositoryv1.Repository
	RepositoriesFilter   = repositoryv1.RepositoriesFilter
	RepositoryState      = repositoryv1.RepositoryState
	ConnectionStatus     = repositoryv1.ConnectionStatus
	RepositoryVisibility = repositoryv1.RepositoryVisibility
	GitProvider          = repositoryv1.GitProvider
	Branch               = repositoryv1.Branch
)

const (
	RepositoryStateUnspecified      = repositoryv1.RepositoryState_REPOSITORY_STATE_UNSPECIFIED
	RepositoryStateConnected        = repositoryv1.RepositoryState_REPOSITORY_STATE_CONNECTED
	RepositoryStateDisconnected     = repositoryv1.RepositoryState_REPOSITORY_STATE_DISCONNECTED
	RepositoryStateError            = repositoryv1.RepositoryState_REPOSITORY_STATE_ERROR
	ConnectionStatusUnspecified     = repositoryv1.ConnectionStatus_CONNECTION_STATUS_UNSPECIFIED
	ConnectionStatusOK              = repositoryv1.ConnectionStatus_CONNECTION_STATUS_OK
	ConnectionStatusAuthFailed      = repositoryv1.ConnectionStatus_CONNECTION_STATUS_AUTH_FAILED
	ConnectionStatusUnreachable     = repositoryv1.ConnectionStatus_CONNECTION_STATUS_UNREACHABLE
	RepositoryVisibilityUnspecified = repositoryv1.RepositoryVisibility_REPOSITORY_VISIBILITY_UNSPECIFIED
	RepositoryVisibilityPublic      = repositoryv1.RepositoryVisibility_REPOSITORY_VISIBILITY_PUBLIC
	RepositoryVisibilityPrivate     = repositoryv1.RepositoryVisibility_REPOSITORY_VISIBILITY_PRIVATE
	GitProviderUnspecified          = repositoryv1.GitProvider_GIT_PROVIDER_UNSPECIFIED
	GitProviderGitHub               = repositoryv1.GitProvider_GIT_PROVIDER_GITHUB
	GitProviderGitLab               = repositoryv1.GitProvider_GIT_PROVIDER_GITLAB
	GitProviderBitbucket            = repositoryv1.GitProvider_GIT_PROVIDER_BITBUCKET
	GitProviderGeneric              = repositoryv1.GitProvider_GIT_PROVIDER_GENERIC
)

// SourceProbeResult is the outcome of a stateless pre-flight repository probe.
type SourceProbeResult struct {
	Reachable    bool
	Visibility   RepositoryVisibility
	AuthValid    bool
	ErrorMessage string
}

// PushFile is a single file to include in a git push operation.
type PushFile struct {
	Path    string // relative to repository root, e.g. "services/user/user.go"
	Content string // UTF-8 text content
}
