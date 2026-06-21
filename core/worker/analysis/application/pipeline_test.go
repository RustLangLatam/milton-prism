package application_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/application"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"
	"milton_prism/core/worker/analysis/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const fixtureRepo = "../infrastructure/adapters/testdata/fixture-repo"

func newPipeline(t *testing.T) (*application.Pipeline, *mocks.MockSummaryWriter) {
	t.Helper()
	writer := &mocks.MockSummaryWriter{}
	return application.NewPipeline(writer), writer
}

// ── Run happy path ─────────────────────────────────────────────────────────────

func TestPipeline_Run_CallsWriter(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return s.GetIdentifier() == 1001 &&
			s.GetRepositoryId() == 42 &&
			s.GetState() == analysisdomain.AnalysisStateCompleted
	})).Return(nil)

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 1001, RepositoryID: 42, MigrationID: 0,
	})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

func TestPipeline_Run_PassesMigrationID(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return s.GetMigrationId() == 7
	})).Return(nil)

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 1001, RepositoryID: 42, MigrationID: 7,
	})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

func TestPipeline_Run_NoMigration_PassesZero(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return s.GetMigrationId() == 0
	})).Return(nil)

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2, RepositoryID: 10})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

func TestPipeline_Run_StubHasZeroMetrics(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return s.GetTotalFiles() == 0 &&
			s.GetTotalLines() == 0 &&
			len(s.GetTechnologies()) == 0 &&
			len(s.GetVulnerabilities()) == 0
	})).Return(nil)

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 3, RepositoryID: 5})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

func TestPipeline_Run_StubStateIsCompleted(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 4, RepositoryID: 1}))
	assert.Equal(t, analysisdomain.AnalysisStateCompleted, captured.GetState())
}

// ── Error propagation ─────────────────────────────────────────────────────────

func TestPipeline_Run_WriterError_Propagates(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.Anything).Return(errors.New("mongo: timeout"))

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1, RepositoryID: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mongo: timeout")
}

// ── Idempotency ───────────────────────────────────────────────────────────────

// Running the pipeline twice invokes Write twice.  The adapter (MongoSummaryWriter)
// guards against duplication via a state-filtered UpdateOne; the pipeline itself
// does not deduplicate — that is the adapter's contract.
func TestPipeline_Run_Idempotent_CallsWriteTwice(t *testing.T) {
	t.Parallel()
	p, writer := newPipeline(t)
	writer.On("Write", mock.Anything, mock.Anything).Return(nil).Times(2)

	job := workerdomain.JobPayload{SummaryID: 1001, RepositoryID: 42}
	require.NoError(t, p.Run(context.Background(), job))
	require.NoError(t, p.Run(context.Background(), job))
	writer.AssertExpectations(t)
}

// ── Stage 2: inventory ────────────────────────────────────────────────────────

// mockAcquirer is a minimal SourceAcquirer that returns a fixed workspace path.
type mockAcquirer struct{ workspace string }

func (a *mockAcquirer) Acquire(_ context.Context, _, _ string) (string, string, func(), error) {
	return a.workspace, "", func() {}, nil
}

// TestPipeline_Inventory_PopulatesTechnologiesAndTotals wires a real
// EnryLanguageDetector against the fixture repo and asserts that the summary
// passed to SummaryWriter has detected languages, correct file/line totals, and
// all Technology entries use category "language".
func TestPipeline_Inventory_PopulatesTechnologiesAndTotals(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}

	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRepo}).
		WithDetector(adapters.NewEnryLanguageDetector())

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 500, RepositoryID: 1})
	require.NoError(t, err)
	writer.AssertExpectations(t)

	require.NotNil(t, captured)

	// Three programming languages from the fixture (Java, PHP, Python).
	assert.Len(t, captured.GetTechnologies(), 3, "three languages expected")

	for _, tech := range captured.GetTechnologies() {
		assert.Equal(t, "language", tech.GetCategory(), "all entries must be category=language")
		assert.NotEmpty(t, tech.GetName(), "technology name must not be empty")
	}

	// Aggregate totals: 3 files, 7+6+6=19 lines.
	assert.Equal(t, uint64(3), captured.GetTotalFiles(), "total_files")
	assert.Equal(t, uint64(19), captured.GetTotalLines(), "total_lines")

	// Summary state must always be COMPLETED.
	assert.Equal(t, analysisdomain.AnalysisStateCompleted, captured.GetState())
}

// TestPipeline_Inventory_AcquirerError propagates a workspace acquisition failure.
func TestPipeline_Inventory_AcquirerError(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	badAcquirer := &mocks.MockSourceAcquirer{}
	badAcquirer.On("Acquire", mock.Anything, mock.Anything, mock.Anything).Return("", "", errors.New("git: clone failed"))

	p := application.NewPipeline(writer).
		WithAcquirer(badAcquirer).
		WithDetector(adapters.NewEnryLanguageDetector())

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 501, RepositoryID: 1, RemoteURL: "https://github.com/example/repo.git",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git: clone failed")
	// Write must NOT be called when acquisition fails.
	writer.AssertNotCalled(t, "Write")
}

// TestPipeline_NoAcquirer_SkipsInventory confirms that when no acquirer is
// wired the summary is written with zero metrics (walking-skeleton path).
func TestPipeline_NoAcquirer_SkipsInventory(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return s.GetTotalFiles() == 0 && s.GetTotalLines() == 0 && len(s.GetTechnologies()) == 0
	})).Return(nil)

	// Detector is wired but acquirer is not — inventory must be skipped.
	p := application.NewPipeline(writer).
		WithDetector(adapters.NewEnryLanguageDetector())

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 502, RepositoryID: 1})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

// ── Stage 3: Composer manifest parsing ───────────────────────────────────────

const fixtureComposerWithLockPath = "../infrastructure/adapters/testdata/fixture-composer/with-lock"

// techsByName is a test helper that indexes captured Technologies by Name.
func techsByName(techs []*analysisdomain.Technology) map[string]*analysisdomain.Technology {
	m := make(map[string]*analysisdomain.Technology, len(techs))
	for _, t := range techs {
		m[t.GetName()] = t
	}
	return m
}

// TestPipeline_Composer_PopulatesThreeProdPackages wires a real
// ComposerManifestParser against the with-lock fixture and asserts that the
// summary written by SummaryWriter contains exactly the four production
// packages from the lock (dev package excluded).
func TestPipeline_Composer_PopulatesThreeProdPackages(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 600, RepositoryID: 1})
	require.NoError(t, err)
	writer.AssertExpectations(t)

	require.NotNil(t, captured)
	assert.Len(t, captured.GetTechnologies(), 4, "exactly 4 prod packages from the lock")
}

// TestPipeline_Composer_ExcludesDevPackage asserts phpunit/phpunit (packages-dev)
// is absent from the Technology list.
func TestPipeline_Composer_ExcludesDevPackage(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 601, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.NotContains(t, m, "phpunit/phpunit", "dev dependency must be excluded from technologies")
}

// TestPipeline_Composer_FrameworkCategoryPreserved asserts that laravel/framework
// maps to category "framework" via the name-based framework map.
func TestPipeline_Composer_FrameworkCategoryPreserved(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 602, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Laravel"].GetCategory())
}

// TestPipeline_Composer_LibraryCategoryDefault asserts that packages without a
// "framework" type get category "library".
func TestPipeline_Composer_LibraryCategoryDefault(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 603, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "library", m["guzzlehttp/guzzle"].GetCategory())
	assert.Equal(t, "library", m["symfony/console"].GetCategory())
}

// TestPipeline_Composer_PinnedVersionFromLock asserts that the DetectedVersion
// stored in the Technology comes from the pinned lock entry, not a constraint.
func TestPipeline_Composer_PinnedVersionFromLock(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*analysisdomain.AnalysisSummary)
		}).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 604, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "v11.0.0", m["Laravel"].GetDetectedVersion(),
		"must be pinned lock version, not constraint")
}

// ── Stage 3: Maven manifest parsing ──────────────────────────────────────────

const fixtureMavenWithSpringPath = "../infrastructure/adapters/testdata/fixture-maven/with-spring"

// TestPipeline_Maven_PopulatesFourProdDeps wires a real MavenManifestParser
// against the with-spring fixture and asserts that exactly 4 production
// dependencies reach the summary (test and system scopes excluded).
func TestPipeline_Maven_PopulatesFourProdDeps(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureMavenWithSpringPath}).
		WithParser(workerdomain.EcosystemMaven, adapters.NewMavenManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 700, RepositoryID: 1}))
	writer.AssertExpectations(t)

	assert.Len(t, captured.GetTechnologies(), 4, "4 prod deps: compile×3 + provided×1")
}

// TestPipeline_Maven_ExcludesTestScope asserts junit-jupiter is absent.
func TestPipeline_Maven_ExcludesTestScope(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureMavenWithSpringPath}).
		WithParser(workerdomain.EcosystemMaven, adapters.NewMavenManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 701, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.NotContains(t, m, "org.junit.jupiter:junit-jupiter")
}

// TestPipeline_Maven_SpringBootCategoryIsFramework asserts Spring Boot starter
// is classified as framework.
func TestPipeline_Maven_SpringBootCategoryIsFramework(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureMavenWithSpringPath}).
		WithParser(workerdomain.EcosystemMaven, adapters.NewMavenManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 702, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Spring"].GetCategory())
}

// TestPipeline_Maven_PropertyVersionStoredAsEmpty asserts that ${...} versions
// become empty string in the Technology entry.
func TestPipeline_Maven_PropertyVersionStoredAsEmpty(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureMavenWithSpringPath}).
		WithParser(workerdomain.EcosystemMaven, adapters.NewMavenManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 703, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "", m["org.hibernate.orm:hibernate-core"].GetDetectedVersion(),
		"property-referenced version must be stored as empty (stage 4 will resolve it)")
}

// ── Stage 3: npm manifest parsing ────────────────────────────────────────────

const fixtureNpmWithLockfilePath = "../infrastructure/adapters/testdata/fixture-npm/with-lockfile"

// TestPipeline_Npm_PopulatesThreeProdDeps wires a real NpmManifestParser
// against the with-lockfile fixture and asserts that exactly 3 production
// packages reach the summary (2 dev packages excluded).
func TestPipeline_Npm_PopulatesThreeProdDeps(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 800, RepositoryID: 1}))
	writer.AssertExpectations(t)
	assert.Len(t, captured.GetTechnologies(), 3, "3 prod deps from lock; 2 dev excluded")
}

// TestPipeline_Npm_ExcludesDevDeps asserts jest and @types/node are absent.
func TestPipeline_Npm_ExcludesDevDeps(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 801, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.NotContains(t, m, "jest")
	assert.NotContains(t, m, "@types/node")
}

// TestPipeline_Npm_ExpressIsFramework asserts express is classified as framework.
func TestPipeline_Npm_ExpressIsFramework(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 802, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Express"].GetCategory())
}

// TestPipeline_Npm_PinnedVersionFromLock asserts that version comes from the
// lock (4.18.2) not the json constraint (^4.18.0 → 4.18.0).
func TestPipeline_Npm_PinnedVersionFromLock(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 803, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "4.18.2", m["Express"].GetDetectedVersion(),
		"must be pinned lock version, not stripped json constraint")
}

// ── Stage 3: PyPI manifest parsing ───────────────────────────────────────────

const fixturePyPIWithPoetryPath = "../infrastructure/adapters/testdata/fixture-pypi/with-poetry"

// TestPipeline_PyPI_PopulatesThreeProdDeps wires a real PyPIManifestParser
// against the with-poetry fixture. The lock has 3 main packages + 1 dev; only
// the 3 main packages must appear in the summary.
func TestPipeline_PyPI_PopulatesThreeProdDeps(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyPIWithPoetryPath}).
		WithParser(workerdomain.EcosystemPyPI, adapters.NewPyPIManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 900, RepositoryID: 1}))
	writer.AssertExpectations(t)
	assert.Len(t, captured.GetTechnologies(), 3)
}

// TestPipeline_PyPI_ExcludesDevPackage asserts pytest (groups=["dev"]) is absent.
func TestPipeline_PyPI_ExcludesDevPackage(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyPIWithPoetryPath}).
		WithParser(workerdomain.EcosystemPyPI, adapters.NewPyPIManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 901, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.NotContains(t, m, "pytest")
}

// TestPipeline_PyPI_DjangoIsFramework asserts django is classified as framework.
func TestPipeline_PyPI_DjangoIsFramework(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyPIWithPoetryPath}).
		WithParser(workerdomain.EcosystemPyPI, adapters.NewPyPIManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 902, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Django"].GetCategory())
}

// TestPipeline_PyPI_PinnedVersionFromLock asserts the lock version (4.2.13)
// is used, not the stripped pyproject constraint (4.2).
func TestPipeline_PyPI_PinnedVersionFromLock(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyPIWithPoetryPath}).
		WithParser(workerdomain.EcosystemPyPI, adapters.NewPyPIManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 903, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "4.2.13", m["Django"].GetDetectedVersion(),
		"must be the pinned lock version, not the stripped pyproject constraint")
}

// ── Stage 3: NuGet manifest parsing ──────────────────────────────────────────

const fixtureNuGetModernPath = "../infrastructure/adapters/testdata/fixture-nuget/modern"

// TestPipeline_NuGet_FindsFourPackages wires a real NuGetManifestParser against
// the modern fixture (.csproj with 4 PackageReference entries).
func TestPipeline_NuGet_FindsFourPackages(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNuGetModernPath}).
		WithParser(workerdomain.EcosystemNuGet, adapters.NewNuGetManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1000, RepositoryID: 1}))
	writer.AssertExpectations(t)
	assert.Len(t, captured.GetTechnologies(), 4)
}

// TestPipeline_NuGet_AspNetCoreCategoryIsFramework asserts that AspNetCore
// packages are classified as framework.
func TestPipeline_NuGet_AspNetCoreCategoryIsFramework(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNuGetModernPath}).
		WithParser(workerdomain.EcosystemNuGet, adapters.NewNuGetManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1001, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Microsoft.AspNetCore.OpenApi"].GetCategory())
}

// TestPipeline_NuGet_PropertyVersionStoredAsEmpty asserts that $(EFCoreVersion)
// becomes empty string in the Technology entry.
func TestPipeline_NuGet_PropertyVersionStoredAsEmpty(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNuGetModernPath}).
		WithParser(workerdomain.EcosystemNuGet, adapters.NewNuGetManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1002, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "", m["Microsoft.EntityFrameworkCore"].GetDetectedVersion(),
		"Central Package Management property reference must be stored as empty")
}

// ── Stage 3: RubyGems manifest parsing ───────────────────────────────────────

const fixtureRubyGemsWithLockfilePath = "../infrastructure/adapters/testdata/fixture-rubygems/with-lockfile"

// TestPipeline_RubyGems_ReturnsProdGems wires a real RubyGemsManifestParser
// against the with-lockfile fixture. rails and pg must be present; rspec-rails
// (:test group) must be absent.
func TestPipeline_RubyGems_ReturnsProdGems(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRubyGemsWithLockfilePath}).
		WithParser(workerdomain.EcosystemRubyGems, adapters.NewRubyGemsManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1100, RepositoryID: 1}))
	writer.AssertExpectations(t)

	m := techsByName(captured.GetTechnologies())
	assert.Contains(t, m, "Rails")
	assert.Contains(t, m, "pg")
	assert.NotContains(t, m, "rspec-rails")
}

// TestPipeline_RubyGems_PinnedVersionFromLock asserts rails version is 7.1.3
// (from lock), not 7.1 (stripped Gemfile constraint).
func TestPipeline_RubyGems_PinnedVersionFromLock(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRubyGemsWithLockfilePath}).
		WithParser(workerdomain.EcosystemRubyGems, adapters.NewRubyGemsManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1101, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "7.1.3", m["Rails"].GetDetectedVersion(),
		"must be the pinned lock version, not the stripped Gemfile constraint")
}

// TestPipeline_RubyGems_RailsIsFramework asserts rails category=framework.
func TestPipeline_RubyGems_RailsIsFramework(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRubyGemsWithLockfilePath}).
		WithParser(workerdomain.EcosystemRubyGems, adapters.NewRubyGemsManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 1102, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "framework", m["Rails"].GetCategory())
}

// ── Stage 4: version resolution ───────────────────────────────────────────────

// TestPipeline_Version_FillsLatestVersion wires a real ComposerParser against
// the with-lock fixture and a mock resolver. Asserts that LatestVersion is
// populated on the Technology entry for laravel/framework.
func TestPipeline_Version_FillsLatestVersion(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	resolver := &mocks.MockVersionResolver{}
	var captured *analysisdomain.AnalysisSummary

	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "laravel/framework").
		Return(workerdomain.VersionCurrency{LatestVersion: "v11.0.5"}, nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "guzzlehttp/guzzle").
		Return(workerdomain.VersionCurrency{LatestVersion: "7.8.1"}, nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "symfony/console").
		Return(workerdomain.VersionCurrency{LatestVersion: "v7.2.0"}, nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "codeigniter4/framework").
		Return(workerdomain.VersionCurrency{LatestVersion: "4.5.4"}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser()).
		WithResolver(resolver)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2000, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "v11.0.5", m["Laravel"].GetLatestVersion())
}

// TestPipeline_Version_SetsCurrentStatus asserts Current status when
// DetectedVersion matches LatestVersion.
func TestPipeline_Version_SetsCurrentStatus(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	resolver := &mocks.MockVersionResolver{}
	var captured *analysisdomain.AnalysisSummary

	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)
	// guzzlehttp/guzzle detected=7.8.1, latest=7.8.1 → Current
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, mock.Anything).
		Return(workerdomain.VersionCurrency{LatestVersion: "7.8.1"}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser()).
		WithResolver(resolver)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2001, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, analysisdomain.TechnologyStatusCurrent, m["guzzlehttp/guzzle"].GetStatus())
}

// TestPipeline_Version_SetsOutdatedStatus asserts Outdated status when
// DetectedVersion is older than LatestVersion.
func TestPipeline_Version_SetsOutdatedStatus(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	resolver := &mocks.MockVersionResolver{}
	var captured *analysisdomain.AnalysisSummary

	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)
	// laravel/framework detected=v11.0.0, latest=v11.0.5 → Outdated
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "laravel/framework").
		Return(workerdomain.VersionCurrency{LatestVersion: "v11.0.5"}, nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, mock.Anything).
		Return(workerdomain.VersionCurrency{LatestVersion: "1.0.0"}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser()).
		WithResolver(resolver)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2002, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, analysisdomain.TechnologyStatusOutdated, m["Laravel"].GetStatus())
}

// TestPipeline_Version_EmptyDetectedVersion_StatusUnspecified asserts that when
// DetectedVersion is "" (property reference or range-only manifest), the status
// remains Unspecified even if LatestVersion is resolved. Claiming "Outdated"
// against an unknown baseline would be misleading.
func TestPipeline_Version_EmptyDetectedVersion_StatusUnspecified(t *testing.T) {
	// Use the Maven fixture: hibernate-core has Version="" (property reference).
	writer := &mocks.MockSummaryWriter{}
	resolver := &mocks.MockVersionResolver{}
	var captured *analysisdomain.AnalysisSummary

	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemMaven, mock.Anything).
		Return(workerdomain.VersionCurrency{LatestVersion: "6.5.0"}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureMavenWithSpringPath}).
		WithParser(workerdomain.EcosystemMaven, adapters.NewMavenManifestParser()).
		WithResolver(resolver)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2003, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	// hibernate-core has empty DetectedVersion → status must remain Unspecified.
	assert.Equal(t, analysisdomain.TechnologyStatusUnspecified,
		m["org.hibernate.orm:hibernate-core"].GetStatus(),
		"empty detected version must not produce a Current/Outdated status")
	// But LatestVersion is still filled in.
	assert.Equal(t, "6.5.0", m["org.hibernate.orm:hibernate-core"].GetLatestVersion())
}

// TestPipeline_Version_ResolverError_GracefulDegradation asserts that a
// resolver error on one package does not fail the job and leaves the
// Technology with its DetectedVersion intact.
func TestPipeline_Version_ResolverError_GracefulDegradation(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	resolver := &mocks.MockVersionResolver{}
	var captured *analysisdomain.AnalysisSummary

	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, "laravel/framework").
		Return(workerdomain.VersionCurrency{}, fmt.Errorf("registry timeout"))
	resolver.On("Latest", mock.Anything, workerdomain.EcosystemComposer, mock.Anything).
		Return(workerdomain.VersionCurrency{LatestVersion: "7.8.1"}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser()).
		WithResolver(resolver)

	// Job must succeed despite the resolver error.
	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2004, RepositoryID: 1})
	require.NoError(t, err)

	// The failed package keeps DetectedVersion and has no LatestVersion/Status.
	m := techsByName(captured.GetTechnologies())
	assert.Equal(t, "v11.0.0", m["Laravel"].GetDetectedVersion())
	assert.Empty(t, m["Laravel"].GetLatestVersion())
}

// TestPipeline_Version_SkippedWhenNoResolver asserts that Technologies have
// no LatestVersion when no resolver is wired.
func TestPipeline_Version_SkippedWhenNoResolver(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureComposerWithLockPath}).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())
	// No WithResolver call.

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 2005, RepositoryID: 1}))

	for _, tech := range captured.GetTechnologies() {
		assert.Empty(t, tech.GetLatestVersion(), "no resolver → LatestVersion must be empty")
	}
}

// TestPipeline_NoAcquirer_SkipsManifestStage confirms that when no acquirer is
// wired stage 3 is skipped even if a parser is registered.
func TestPipeline_NoAcquirer_SkipsManifestStage(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.MatchedBy(func(s *analysisdomain.AnalysisSummary) bool {
		return len(s.GetTechnologies()) == 0
	})).Return(nil)

	p := application.NewPipeline(writer).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 605, RepositoryID: 1})
	require.NoError(t, err)
	writer.AssertExpectations(t)
}

// ── Stage 5 — vulnerability scanning ─────────────────────────────────────────

// TestPipeline_Vuln_AppendedToSummary verifies that the scanner is called with
// the manifest deps and its results are forwarded to the summary writer.
func TestPipeline_Vuln_AppendedToSummary(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	scanner := &mocks.MockVulnerabilityScanner{}
	expectedVuln := &analysisdomain.Vulnerability{
		IdentifierRef: "CVE-2021-23337",
		Component:     "lodash",
	}
	scanner.On("Scan", mock.Anything, mock.Anything).
		Return([]*analysisdomain.Vulnerability{expectedVuln}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser()).
		WithScanner(scanner)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 3001, RepositoryID: 1}))

	require.Len(t, captured.GetVulnerabilities(), 1)
	assert.Equal(t, "CVE-2021-23337", captured.GetVulnerabilities()[0].GetIdentifierRef())
	scanner.AssertExpectations(t)
}

// TestPipeline_Vuln_GracefulDegradation confirms that a scanner error does not
// fail the job — the summary is still written with no vulnerabilities.
func TestPipeline_Vuln_GracefulDegradation(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	scanner := &mocks.MockVulnerabilityScanner{}
	scanner.On("Scan", mock.Anything, mock.Anything).
		Return(nil, errors.New("OSV unavailable"))

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser()).
		WithScanner(scanner)

	// Job must complete without error.
	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 3002, RepositoryID: 1}))

	assert.Nil(t, captured.GetVulnerabilities(),
		"scanner error must not fail the job; vulnerabilities should be nil")
}

// TestPipeline_Vuln_SkippedWhenNoScanner confirms that omitting WithScanner
// causes stage 5 to be skipped and the summary has no vulnerabilities.
func TestPipeline_Vuln_SkippedWhenNoScanner(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureNpmWithLockfilePath}).
		WithParser(workerdomain.EcosystemNpm, adapters.NewNpmManifestParser())
	// No WithScanner call.

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 3003, RepositoryID: 1}))
	assert.Nil(t, captured.GetVulnerabilities())
}

// ── Stage 6 — dependency graph ────────────────────────────────────────────────

const fixturePyMiniproject = "../infrastructure/adapters/testdata/fixture-python/miniproject"

// TestPipeline_Graph_EdgesInSummary verifies that when a PythonLanguageAnalyzer
// is registered for a Python workspace, the dependency graph is populated in the
// written AnalysisSummary.
func TestPipeline_Graph_EdgesInSummary(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	reg := application.NewLanguageAnalyzerRegistry()
	reg.Register(adapters.NewPythonLanguageAnalyzer())

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyMiniproject}).
		WithDetector(adapters.NewEnryLanguageDetector()).
		WithGraphBuilder(reg)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 4001, RepositoryID: 1}))

	edges := captured.GetDependencyGraph()
	require.NotEmpty(t, edges, "dependency graph must not be empty for a Python workspace")

	// Spot-check a known internal edge.
	found := false
	for _, e := range edges {
		if e.GetFromModule() == "myapp.views" && e.GetToModule() == "myapp.models" {
			found = true
			assert.GreaterOrEqual(t, e.GetWeight(), uint32(1))
		}
	}
	assert.True(t, found, "expected edge myapp.views → myapp.models in dependency graph")
}

// TestPipeline_Graph_HoleLanguage_SkippedWithoutError verifies that when a
// detected language has no registered analyzer (the hole condition), the stage
// skips that language and the job completes without error.
func TestPipeline_Graph_HoleLanguage_SkippedWithoutError(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	// Registry has NO Python analyzer — hole for the detected "Python" language.
	emptyReg := application.NewLanguageAnalyzerRegistry()

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyMiniproject}).
		WithDetector(adapters.NewEnryLanguageDetector()).
		WithGraphBuilder(emptyReg)

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 4002, RepositoryID: 1})
	require.NoError(t, err, "hole language must not fail the job")
	assert.Empty(t, captured.GetDependencyGraph(),
		"hole language must produce an empty dependency graph")
}

// TestPipeline_Graph_BuilderError_GracefulDegradation verifies that when
// DependencyGraphBuilder.Build returns an error, the job still completes and
// the dependency graph is empty (not partially populated).
func TestPipeline_Graph_BuilderError_GracefulDegradation(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	builder := &mocks.MockDependencyGraphBuilder{}
	builder.On("Build", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("parse failure"))

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyMiniproject}).
		WithDetector(adapters.NewEnryLanguageDetector()).
		WithGraphBuilder(builder)

	err := p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 4003, RepositoryID: 1})
	require.NoError(t, err, "graph builder error must not fail the job")
	assert.Empty(t, captured.GetDependencyGraph())
}

// TestPipeline_Graph_Idempotency verifies that running the pipeline twice for
// the same job produces identical dependency graphs and does not duplicate edges.
func TestPipeline_Graph_Idempotency(t *testing.T) {
	t.Parallel()

	runPipeline := func(summaryID uint64) []*analysisdomain.DependencyEdge {
		writer := &mocks.MockSummaryWriter{}
		var captured *analysisdomain.AnalysisSummary
		writer.On("Write", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
			Return(nil)

		reg := application.NewLanguageAnalyzerRegistry()
		reg.Register(adapters.NewPythonLanguageAnalyzer())

		p := application.NewPipeline(writer).
			WithAcquirer(&mockAcquirer{workspace: fixturePyMiniproject}).
			WithDetector(adapters.NewEnryLanguageDetector()).
			WithGraphBuilder(reg)

		require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{
			SummaryID: summaryID, RepositoryID: 1,
		}))
		return captured.GetDependencyGraph()
	}

	first := runPipeline(4004)
	second := runPipeline(4005)

	require.Equal(t, len(first), len(second),
		"two runs must produce the same number of edges")

	// Build edge-key sets and compare.
	type edgeKey struct{ from, to string }
	firstKeys := make(map[edgeKey]uint32)
	for _, e := range first {
		firstKeys[edgeKey{e.GetFromModule(), e.GetToModule()}] = e.GetWeight()
	}
	for _, e := range second {
		k := edgeKey{e.GetFromModule(), e.GetToModule()}
		w, ok := firstKeys[k]
		assert.True(t, ok, "edge %s→%s in second run was not in first run", k.from, k.to)
		assert.Equal(t, w, e.GetWeight(), "weight mismatch for edge %s→%s", k.from, k.to)
	}
}

// TestPipeline_Graph_SkippedWhenNoGraphBuilder verifies that omitting
// WithGraphBuilder causes stage 6 to be skipped entirely, leaving an empty graph.
func TestPipeline_Graph_SkippedWhenNoGraphBuilder(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixturePyMiniproject}).
		WithDetector(adapters.NewEnryLanguageDetector())
	// No WithGraphBuilder.

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 4006, RepositoryID: 1}))
	assert.Empty(t, captured.GetDependencyGraph())
}

// fixtureCI3 is the reduced CodeIgniter 3 project (convention-based, no PSR-4).
const fixtureCI3 = "../infrastructure/adapters/testdata/fixture-ci3"

// TestPipeline_Inventory_CI3_DeepAnalysis wires the full Tier-2 stack against the
// CI3 fixture and asserts the convention path produced real structural data:
// DeepAnalysisAvailable, cards, edges, and at least one unreachable island that
// carries its file:line (the Admin controller, whose loads are all traps).
func TestPipeline_Inventory_CI3_DeepAnalysis(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	reg := application.NewLanguageAnalyzerRegistry()
	reg.Register(adapters.NewPHPLanguageAnalyzer())

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureCI3}).
		WithDetector(adapters.NewEnryLanguageDetector()).
		WithFrameworkDetector(adapters.NewFileSystemFrameworkDetector()).
		WithGraphBuilder(reg).
		WithCardProvider(reg).
		WithClassifier(adapters.NewLanguageAwareClassifier())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 6001, RepositoryID: 1}))

	assert.True(t, captured.GetDeepAnalysisAvailable(), "deep_analysis_available must be true for CI3")
	assert.NotEmpty(t, captured.GetDependencyGraph(), "CI3 convention edges must be present")
	assert.NotEmpty(t, captured.GetModuleCards(), "CI3 module cards must be present")

	// CodeIgniter 3.x must be reported as the framework.
	m := techsByName(captured.GetTechnologies())
	require.Contains(t, m, "CodeIgniter")
	assert.Equal(t, "3.x", m["CodeIgniter"].GetDetectedVersion())

	// Admin.php is an honest island: all its loads are traps (dynamic / subfolder /
	// nonexistent / helper) and nothing imports it. It must appear in the
	// unreachable report with its file:line, never as a fabricated edge.
	var adminUnreachable *analysisdomain.UnreachableModule
	for _, u := range captured.GetUnreachableModules() {
		if u.GetModule() == "application/controllers/Admin.php" {
			adminUnreachable = u
		}
	}
	require.NotNil(t, adminUnreachable, "Admin.php must be reported unreachable")
	assert.Equal(t, "application/controllers/Admin.php", adminUnreachable.GetFile(),
		"unreachable module must carry its file path")
	assert.Greater(t, adminUnreachable.GetLoc(), uint32(0), "unreachable module must carry its LOC")

	// No fabricated edge may originate from or target the trap controller.
	for _, e := range captured.GetDependencyGraph() {
		assert.NotEqual(t, "application/controllers/Admin.php", e.GetFromModule(),
			"Admin.php must produce no outgoing edge (all loads are traps)")
		assert.NotEqual(t, "application/controllers/Admin.php", e.GetToModule(),
			"nothing imports Admin.php")
	}
}

// ── Stage 3b — structural framework detection ─────────────────────────────────

// TestPipeline_FrameworkDetector_AddsTechnology verifies that the structural
// detector's output reaches the summary Technologies slice.
func TestPipeline_FrameworkDetector_AddsTechnology(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	det := &mocks.MockStructuralFrameworkDetector{}
	ci3 := &analysisdomain.Technology{Name: "CodeIgniter", DetectedVersion: "3.x", Category: "framework"}
	det.On("Detect", mock.Anything, fixtureRepo, mock.Anything).Return([]*analysisdomain.Technology{ci3}, nil)

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRepo}).
		WithFrameworkDetector(det)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 5001, RepositoryID: 1}))

	m := techsByName(captured.GetTechnologies())
	require.Contains(t, m, "CodeIgniter")
	assert.Equal(t, "3.x", m["CodeIgniter"].GetDetectedVersion())
	assert.Equal(t, "framework", m["CodeIgniter"].GetCategory())
	det.AssertExpectations(t)
}

// TestPipeline_FrameworkDetector_SkippedWhenNoAcquirer verifies that the
// structural detector is not called when no workspace is available.
func TestPipeline_FrameworkDetector_SkippedWhenNoAcquirer(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(nil)

	det := &mocks.MockStructuralFrameworkDetector{}
	// Detect must NOT be called — no workspace was acquired.

	p := application.NewPipeline(writer).
		WithFrameworkDetector(det)

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 5002, RepositoryID: 1}))
	det.AssertNotCalled(t, "Detect")
}

// ── Manifest-language boost ───────────────────────────────────────────────────

// TestPipeline_ManifestBoost_PHPFirst verifies that when a Composer manifest is
// present, PHP is the first language in technologies[] even if JS files
// outnumber PHP files in the raw file count.
func TestPipeline_ManifestBoost_PHPFirst(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	// Build a workspace where JS files outnumber PHP files by 10:1 but a
	// composer.json is present — the boost must promote PHP to first position.
	dir := t.TempDir()

	// PHP: one application file
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "application"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "application/index.php"),
		[]byte("<?php echo 'hello';"), 0644))
	// JS: ten app-tier files (not under assets/ to exercise the boost, not the filter)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "js"), 0755))
	for i := range 10 {
		path := filepath.Join(dir, "js", fmt.Sprintf("m%d.js", i))
		require.NoError(t, os.WriteFile(path, []byte("(function(){var x=1;})();\n"), 0644))
	}
	// Composer manifest
	require.NoError(t, os.WriteFile(filepath.Join(dir, "composer.json"),
		[]byte(`{"require":{"guzzlehttp/guzzle":"^7.0"}}`), 0644))

	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: dir}).
		WithDetector(adapters.NewEnryLanguageDetector()).
		WithParser(workerdomain.EcosystemComposer, adapters.NewComposerManifestParser())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 6001, RepositoryID: 1}))

	techs := captured.GetTechnologies()
	langs := make([]*analysisdomain.Technology, 0)
	for _, tech := range techs {
		if tech.GetCategory() == "language" {
			langs = append(langs, tech)
		}
	}
	require.NotEmpty(t, langs, "at least one language must be detected")
	assert.Equal(t, "PHP", langs[0].GetName(),
		"PHP must be first language when Composer manifest is present")
}

// ── Dedup ─────────────────────────────────────────────────────────────────────

// TestPipeline_Dedup_SameCommit_WritesReuse verifies that when the remote HEAD
// matches an existing COMPLETED analysis, the pipeline calls WriteReuse and
// returns without cloning or running the full analysis.
func TestPipeline_Dedup_SameCommit_WritesReuse(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	const sha = "cafe0000cafe0000cafe0000cafe0000cafe0000"
	existing := &analysisdomain.AnalysisSummary{
		Identifier:   9001,
		RepositoryId: 55,
		SourceBranch: "main",
		CommitSha:    sha,
		State:        analysisdomain.AnalysisStateCompleted,
	}
	writer.On("FindCompletedForBranch", mock.Anything, uint64(55), "main").Return(existing, nil)
	writer.On("WriteReuse", mock.Anything, uint64(9001), uint64(77)).Return(nil)
	// Zombie RUNNING summary created for this job must be closed after reuse.
	writer.On("MarkAnalysisFailed", mock.Anything, uint64(1), mock.AnythingOfType("string")).Return(nil)

	resolverCalled := false
	p := application.NewPipeline(writer).
		WithBranchSHAResolver(func(_ context.Context, _, _ string) string {
			resolverCalled = true
			return sha
		})

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 1, RepositoryID: 55, MigrationID: 77,
		RemoteURL: "https://github.com/org/repo.git", DefaultBranch: "main",
	})
	require.NoError(t, err)
	assert.True(t, resolverCalled, "branchSHAResolver must be called")
	writer.AssertCalled(t, "WriteReuse", mock.Anything, uint64(9001), uint64(77))
	writer.AssertCalled(t, "MarkAnalysisFailed", mock.Anything, uint64(1), mock.AnythingOfType("string"))
	// Write must NOT be called — no new analysis ran.
	writer.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

// TestPipeline_Dedup_DifferentCommit_RunsNormally verifies that when the remote
// HEAD differs from the existing summary's commit, full analysis runs.
func TestPipeline_Dedup_DifferentCommit_RunsNormally(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	existing := &analysisdomain.AnalysisSummary{
		Identifier: 9002, RepositoryId: 55, CommitSha: "oldsha", State: analysisdomain.AnalysisStateCompleted,
	}
	writer.On("FindCompletedForBranch", mock.Anything, uint64(55), "main").Return(existing, nil)
	// Full analysis → Write is called.
	writer.On("Write", mock.Anything, mock.Anything).Return(nil)

	p := application.NewPipeline(writer).
		WithBranchSHAResolver(func(_ context.Context, _, _ string) string { return "newsha" })

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 2, RepositoryID: 55, MigrationID: 77,
		RemoteURL: "https://github.com/org/repo.git", DefaultBranch: "main",
	})
	require.NoError(t, err)
	writer.AssertCalled(t, "Write", mock.Anything, mock.Anything)
	writer.AssertNotCalled(t, "WriteReuse", mock.Anything, mock.Anything, mock.Anything)
}

// TestPipeline_Dedup_NoExisting_RunsNormally verifies that when no COMPLETED
// analysis exists for the branch, full analysis always runs.
func TestPipeline_Dedup_NoExisting_RunsNormally(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	writer.On("FindCompletedForBranch", mock.Anything, uint64(55), "main").Return(nil, nil)
	writer.On("Write", mock.Anything, mock.Anything).Return(nil)

	p := application.NewPipeline(writer).
		WithBranchSHAResolver(func(_ context.Context, _, _ string) string { return "abc123" })

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 3, RepositoryID: 55, MigrationID: 77,
		RemoteURL: "https://github.com/org/repo.git", DefaultBranch: "main",
	})
	require.NoError(t, err)
	writer.AssertCalled(t, "Write", mock.Anything, mock.Anything)
}

// TestPipeline_Dedup_NoMigration_SkipsDedup verifies that the dedup check is
// skipped for standalone analysis jobs (MigrationID == 0).
func TestPipeline_Dedup_NoMigration_SkipsDedup(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(nil)

	resolverCalled := false
	p := application.NewPipeline(writer).
		WithBranchSHAResolver(func(_ context.Context, _, _ string) string {
			resolverCalled = true
			return "anysha"
		})

	err := p.Run(context.Background(), workerdomain.JobPayload{
		SummaryID: 4, RepositoryID: 55, MigrationID: 0,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	assert.False(t, resolverCalled, "branchSHAResolver must not be called for standalone jobs")
	writer.AssertNotCalled(t, "FindCompletedForBranch", mock.Anything, mock.Anything, mock.Anything)
}

// TestPipeline_ManifestBoost_NoManifest_OrderUnchanged verifies that when no
// backend manifest is detected the language order is left as enry returns it
// (file count descending).
func TestPipeline_ManifestBoost_NoManifest_OrderUnchanged(t *testing.T) {
	t.Parallel()
	writer := &mocks.MockSummaryWriter{}
	var captured *analysisdomain.AnalysisSummary
	writer.On("Write", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*analysisdomain.AnalysisSummary) }).
		Return(nil)

	// Fixture repo: Java, PHP, Python — equal file count, alphabetical order.
	// No manifest parser registered → no boost → order comes from enry.
	p := application.NewPipeline(writer).
		WithAcquirer(&mockAcquirer{workspace: fixtureRepo}).
		WithDetector(adapters.NewEnryLanguageDetector())

	require.NoError(t, p.Run(context.Background(), workerdomain.JobPayload{SummaryID: 6002, RepositoryID: 1}))

	langs := make([]string, 0)
	for _, tech := range captured.GetTechnologies() {
		if tech.GetCategory() == "language" {
			langs = append(langs, tech.GetName())
		}
	}
	// Without a boost the order is determined solely by file count then name.
	// Fixture has equal file counts so alphabetical: Java < PHP < Python.
	require.Len(t, langs, 3)
	assert.Equal(t, []string{"Java", "PHP", "Python"}, langs)
}
