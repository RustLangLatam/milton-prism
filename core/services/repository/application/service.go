// Package application contains the repository service's use-case logic.
// It depends only on domain types and driven port interfaces — never on
// infrastructure packages.
package application

import (
	"context"
	"errors"
	"fmt"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Service orchestrates repository use cases.
type Service struct {
	repo     ports.RepositoryRepository
	tx       ports.TransactionManager
	identity ports.IdentityClient
	git      ports.GitClient
}

// NewService wires port implementations into the application service.
func NewService(
	repo ports.RepositoryRepository,
	tx ports.TransactionManager,
	identity ports.IdentityClient,
	git ports.GitClient,
) *Service {
	return &Service{repo: repo, tx: tx, identity: identity, git: git}
}

// CreateRepository validates the payload, confirms the owner exists, and
// persists the new repository.
func (s *Service) CreateRepository(ctx context.Context, r *domain.Repository) (*domain.Repository, error) {
	if r == nil || r.GetRemoteUrl() == "" {
		return nil, domain.ErrMissingPayload
	}
	if r.GetOwnerUserId() == 0 {
		return nil, domain.ErrMissingOwnerUserID
	}
	if r.GetProvider() == domain.GitProviderUnspecified {
		return nil, domain.ErrMissingPayload
	}
	if s.identity != nil {
		if err := s.identity.ValidateUserExists(ctx, r.GetOwnerUserId()); err != nil {
			return nil, err
		}
	}
	var out *domain.Repository
	err := s.tx.WithTransaction(ctx, func(txCtx context.Context) error {
		var createErr error
		out, createErr = s.repo.Create(txCtx, r)
		return createErr
	})
	if err != nil {
		return nil, fmt.Errorf("create repository: %w", err)
	}
	return out, nil
}

// GetRepository fetches a repository by identifier.
func (s *Service) GetRepository(ctx context.Context, identifier uint64) (*domain.Repository, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	r, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRepositories returns a paginated, filtered list of repositories.
func (s *Service) ListRepositories(ctx context.Context, filter *domain.RepositoriesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Repository, *paginationv1.Pagination, error) {
	return s.repo.List(ctx, filter, params)
}

// UpdateRepository applies a FieldMask-bounded partial update and returns the
// updated repository.
func (s *Service) UpdateRepository(ctx context.Context, r *domain.Repository, mask *fieldmaskpb.FieldMask) (*domain.Repository, error) {
	if r == nil || r.GetIdentifier() == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	existing, err := s.repo.GetByID(ctx, r.GetIdentifier(), false)
	if err != nil {
		return nil, err
	}
	applyRepositoryMask(existing, r, mask)
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// DeleteRepository soft-deletes a repository by identifier.
func (s *Service) DeleteRepository(ctx context.Context, identifier uint64) error {
	if identifier == 0 {
		return domain.ErrMissingIdentifier
	}
	return s.repo.SoftDelete(ctx, identifier)
}

// ProbeSourceRepository probes remoteURL without cloning. It determines whether
// the URL is reachable, its visibility, and whether token (if supplied) is valid.
// No repository record is required or created.
func (s *Service) ProbeSourceRepository(ctx context.Context, remoteURL, token string) (*domain.SourceProbeResult, error) {
	if remoteURL == "" {
		return nil, domain.ErrInvalidRemoteURL
	}
	return s.git.ProbeSource(ctx, remoteURL, token)
}

// TestConnection probes the remote, then persists both the connection_status
// and the derived state on the repository record.
func (s *Service) TestConnection(ctx context.Context, identifier uint64) (domain.ConnectionStatus, error) {
	if identifier == 0 {
		return domain.ConnectionStatusUnspecified, domain.ErrMissingIdentifier
	}
	r, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return domain.ConnectionStatusUnspecified, err
	}
	status, err := s.git.TestConnection(ctx, r.GetRemoteUrl(), r.GetCredentialRef())
	if err != nil {
		status = domain.ConnectionStatusUnreachable
	}
	r.ConnectionStatus = status
	r.State = connectionStatusToState(status)
	_ = s.repo.Update(ctx, r)
	return status, nil
}

// connectionStatusToState maps a ConnectionStatus to the corresponding
// RepositoryState so both fields are always in sync after a probe.
func connectionStatusToState(cs domain.ConnectionStatus) domain.RepositoryState {
	switch cs {
	case domain.ConnectionStatusOK:
		return domain.RepositoryStateConnected
	case domain.ConnectionStatusAuthFailed:
		return domain.RepositoryStateError
	default:
		return domain.RepositoryStateDisconnected
	}
}

// ListBranches returns the branches available on the remote and updates
// connection_status, state, and default_branch on the repository record as a
// side effect. On success: OK + CONNECTED + discovered default branch. On
// failure: AUTH_FAILED (token rejected) or UNREACHABLE (network / not found).
// The update is best-effort — a write failure does not fail the RPC.
func (s *Service) ListBranches(ctx context.Context, identifier uint64) ([]*domain.Branch, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	r, err := s.repo.GetByID(ctx, identifier, false)
	if err != nil {
		return nil, err
	}
	branches, listErr := s.git.ListBranches(ctx, r.GetRemoteUrl(), r.GetCredentialRef())
	if listErr != nil {
		r.ConnectionStatus, r.State = branchListErrStatus(listErr)
		_ = s.repo.Update(ctx, r)
		return nil, listErr
	}
	r.ConnectionStatus = domain.ConnectionStatusOK
	r.State = domain.RepositoryStateConnected
	for _, b := range branches {
		if b.GetIsDefault() {
			r.DefaultBranch = b.GetName()
			break
		}
	}
	_ = s.repo.Update(ctx, r)
	return branches, nil
}

// branchListErrStatus maps a ListBranches error to the (ConnectionStatus, State)
// pair that accurately reflects why the listing failed.
func branchListErrStatus(err error) (domain.ConnectionStatus, domain.RepositoryState) {
	if errors.Is(err, domain.ErrForbiddenAccess) {
		return domain.ConnectionStatusAuthFailed, domain.RepositoryStateError
	}
	return domain.ConnectionStatusUnreachable, domain.RepositoryStateDisconnected
}

// PushResult commits files to a temporary workspace and pushes to targetURL.
// writeToken is forwarded to the git client as-is; it is never stored, logged,
// or embedded in any error message at this layer.
func (s *Service) PushResult(ctx context.Context, targetURL, writeToken string, files []*domain.PushFile, commitMessage string) (string, error) {
	if targetURL == "" {
		return "", domain.ErrInvalidRemoteURL
	}
	if len(files) == 0 {
		return "", domain.ErrMissingPayload
	}
	return s.git.PushResult(ctx, targetURL, writeToken, files, commitMessage)
}

// applyRepositoryMask updates existing with values from update for each path in mask.
func applyRepositoryMask(existing, update *domain.Repository, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "remote_url":
			existing.RemoteUrl = update.GetRemoteUrl()
		case "default_branch":
			existing.DefaultBranch = update.GetDefaultBranch()
		case "state":
			existing.State = update.GetState()
		case "connection_status":
			existing.ConnectionStatus = update.GetConnectionStatus()
		case "credential_ref":
			existing.CredentialRef = update.GetCredentialRef()
		}
	}
}
