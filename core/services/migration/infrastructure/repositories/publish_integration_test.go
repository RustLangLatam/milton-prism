package repositories_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/infrastructure/repositories"
	"milton_prism/core/services/migration/mocks"
	"milton_prism/core/services/migration/ports"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
)

const testMigrationID uint64 = 10018

// connectIntegMongo skips if MONGO_URI is not set and returns an open database.
func connectIntegMongo(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set — skipping integration test")
	}
	dbName := os.Getenv("MONGO_DB_NAME")
	if dbName == "" {
		dbName = "milton_prism_migration"
	}
	client, err := mongo.Connect(context.Background(), mongoopts.Client().ApplyURI(uri))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client.Database(dbName)
}

// localRepositoryClient implements ports.RepositoryClient by pushing to a fixed
// local bare repo using go-git. It captures the target directory so the targetURL
// parameter of PublishMigration does not need to be a real URI.
type localRepositoryClient struct {
	targetDir string
}

func (c *localRepositoryClient) FetchRepositoryURL(_ context.Context, _ uint64) (string, error) {
	return "", nil
}

func (c *localRepositoryClient) ProbeConnection(_ context.Context, _ uint64) error {
	return nil
}

func (c *localRepositoryClient) PushFiles(ctx context.Context, _, _ string, files []ports.PushFile, commitMessage string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "prism-integ-push-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	repo, err := gogit.PlainInit(tmpDir, false)
	if err != nil {
		return "", err
	}
	if err := repo.Storer.SetReference(gogitplumbing.NewSymbolicReference(
		gogitplumbing.HEAD, gogitplumbing.NewBranchReferenceName("main"),
	)); err != nil {
		return "", err
	}

	for _, f := range files {
		fullPath := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			return "", err
		}
	}

	w, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if _, err := w.Add("."); err != nil {
		return "", err
	}
	if commitMessage == "" {
		commitMessage = "chore: Milton Prism integration test push"
	}
	if _, err := w.Commit(commitMessage, &gogit.CommitOptions{
		Author: &gogitobject.Signature{Name: "Milton Prism", Email: "prism@miltonprism.io"},
	}); err != nil {
		return "", err
	}
	if _, err := repo.CreateRemote(&gogitconfig.RemoteConfig{Name: "origin", URLs: []string{c.targetDir}}); err != nil {
		return "", err
	}
	if err := repo.PushContext(ctx, &gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gogitconfig.RefSpec{"refs/heads/main:refs/heads/main"},
	}); err != nil {
		return "", err
	}
	return "main", nil
}

// countFiles walks dir, skips .git, and returns the number of regular files.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	var n int
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

// TestPublishMigration_Integration_ConflictDetected_10018 verifies that migration
// 10018 — which has divergent versions of pkg/gateway/common/error/message_error.go
// across services articles, profile, and user — is blocked by conflict detection
// before any git push is attempted. Migration stays in READY.
func TestPublishMigration_Integration_ConflictDetected_10018(t *testing.T) {
	db := connectIntegMongo(t)
	ctx := context.Background()

	artifactReader := repositories.NewMongoGenerationFileArtifactReader(db)
	files, err := artifactReader.ListArtifacts(ctx, testMigrationID, "")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	t.Logf("migration %d: %d artifact records", testMigrationID, len(files))

	migRepo := &mocks.MockMigrationRepository{}
	readyMig := &domain.Migration{
		Identifier:  testMigrationID,
		OwnerUserId: 1,
		State:       domain.MigrationStateReady,
	}
	migRepo.On("GetByID", mock.Anything, testMigrationID, false).Return(readyMig, nil)

	tx := &mocks.MockTransactionManager{}
	// localRepositoryClient must NOT be called — conflict is caught before push.
	targetDir := t.TempDir()
	_, err = gogit.PlainInit(targetDir, true)
	require.NoError(t, err)
	repoClient := &localRepositoryClient{targetDir: targetDir}

	svc := application.NewService(migRepo, tx, nil, repoClient, nil, nil, nil, nil, nil, artifactReader, nil, nil, nil, nil, "")
	_, _, err = svc.PublishMigration(ctx, testMigrationID, "https://example.com/repo.git", "", "")

	require.Error(t, err)
	var dErr *domain.Error
	require.True(t, errors.As(err, &dErr), "expected domain.Error, got %T: %v", err, err)
	assert.Equal(t, domain.ErrCodeArtifactConflict, dErr.Code)
	assert.Contains(t, dErr.Message, "pkg/gateway/common/error/message_error.go")
	assert.Contains(t, dErr.Message, "articles")
	assert.Contains(t, dErr.Message, "profile")
	assert.Contains(t, dErr.Message, "user")

	// No state change: push was not attempted, migration stays READY.
	migRepo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)

	// The bare repo must have no commits (no push was attempted).
	bareRepo, openErr := gogit.PlainOpen(targetDir)
	require.NoError(t, openErr)
	_, refErr := bareRepo.Head()
	assert.Error(t, refErr, "bare repo must have no HEAD (no commits pushed)")
}

// TestPublishMigration_Integration_CleanArtifacts verifies the happy path using
// a synthetic clean artifact set (no path conflicts). Confirms READY → PUSHED and
// file structure in the cloned repo.
func TestPublishMigration_Integration_CleanArtifacts(t *testing.T) {
	_ = connectIntegMongo(t) // skip if no MONGO_URI

	ctx := context.Background()

	cleanFiles := []ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain\n"},
		{ServiceName: "user", Path: "core/services/user/wire.go", Content: "package user\n"},
		{ServiceName: "articles", Path: "core/services/articles/domain/domain.go", Content: "package domain\n"},
		// Shared file — identical content across services: NOT a conflict.
		{ServiceName: "user", Path: "pkg/shared/util.go", Content: "package shared\n"},
		{ServiceName: "articles", Path: "pkg/shared/util.go", Content: "package shared\n"},
	}

	migRepo := &mocks.MockMigrationRepository{}
	readyMig := &domain.Migration{
		Identifier:  testMigrationID,
		OwnerUserId: 1,
		State:       domain.MigrationStateReady,
	}
	migRepo.On("GetByID", mock.Anything, testMigrationID, false).Return(readyMig, nil)
	migRepo.On("UpdateState", mock.Anything, testMigrationID, domain.MigrationStatePushed).Return(nil)

	fileReader := &mocks.MockGenerationFileArtifactReader{}
	fileReader.On("ListArtifacts", mock.Anything, testMigrationID, "").Return(cleanFiles, nil)

	tx := &mocks.MockTransactionManager{}
	targetDir := t.TempDir()
	_, err := gogit.PlainInit(targetDir, true)
	require.NoError(t, err)

	svc := application.NewService(migRepo, tx, nil, &localRepositoryClient{targetDir: targetDir}, nil, nil, nil, nil, nil, fileReader, nil, nil, nil, nil, "")
	updated, branch, err := svc.PublishMigration(ctx, testMigrationID, "https://example.com/repo.git", "", "")
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStatePushed, updated.GetState())
	assert.Equal(t, "main", branch)
	migRepo.AssertCalled(t, "UpdateState", mock.Anything, testMigrationID, domain.MigrationStatePushed)

	// Clone and verify: 4 unique paths (pkg/shared/util.go deduplicated).
	cloneDir := t.TempDir()
	_, err = gogit.PlainClone(cloneDir, false, &gogit.CloneOptions{
		URL:           targetDir,
		ReferenceName: "refs/heads/main",
	})
	require.NoError(t, err)
	assert.Equal(t, 4, countFiles(t, cloneDir))
	assert.FileExists(t, filepath.Join(cloneDir, "core", "services", "user", "domain", "domain.go"))
	assert.FileExists(t, filepath.Join(cloneDir, "core", "services", "articles", "domain", "domain.go"))
	assert.FileExists(t, filepath.Join(cloneDir, "pkg", "shared", "util.go"))
}
