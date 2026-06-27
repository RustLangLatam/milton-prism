// Package application contains the analysis pipeline orchestrator.
// It is the only place that coordinates the stage ports; infrastructure adapters
// must never be imported here.
package application

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

// parserEntry pairs a ManifestParser with the ecosystem it handles. The
// pipeline calls each registered parser with its specific ecosystem constant
// so that parsers never need to self-declare their ecosystem.
type parserEntry struct {
	ecosystem workerdomain.Ecosystem
	parser    ports.ManifestParser
}

// Pipeline orchestrates the analysis pipeline stages for a single job.
// Stage implementations are wired via the With* builder methods. Any port that
// is nil (or empty slice) is skipped; the pipeline still produces a valid
// (possibly partial) AnalysisSummary in every case.
type Pipeline struct {
	writer ports.SummaryWriter

	// Stage 1 — source acquisition. When nil, stages that need a workspace are skipped.
	acquirer ports.SourceAcquirer
	// Stage 2 — language inventory. Requires a workspace from stage 1.
	detector ports.LanguageDetector
	// Stage 3 — manifest parsing. One entry per ecosystem; called in registration order.
	parsers []parserEntry
	// Stages 4–7 — not yet implemented; defined here so wiring is explicit.
	resolver     ports.VersionResolver
	scanner      ports.VulnerabilityScanner
	graphBuilder ports.DependencyGraphBuilder
	// Stage 6b — module cards. Extracts per-module structural data (functions,
	// classes, mutable state, routes) and blueprint registrations. Optional.
	cardProvider ports.ModuleCardProvider
	// Stage 6c — module classification. Classifies each module in the dependency
	// graph as domain, infrastructure, or test. Runs only when the graph is
	// non-empty. Optional; skipped when nil.
	classifier ports.ModuleClassifier
	clusterer  ports.SemanticClusterer
	// Stage 6d — deterministic migrability score. Runs when classification is
	// available and the dependency graph is non-empty. Nil = disabled.
	migrabilityScorer ports.MigrabilityScorer

	// decomposeEnqueuer dispatches a decompose:run job when analysis completes
	// and the migration advances to DESIGNING. Nil = disabled.
	decomposeEnqueuer ports.DecomposeJobEnqueuer

	// branchSHAResolver resolves the HEAD SHA of a specific branch on a remote
	// without cloning. Injected for testing; defaults to resolveRemoteBranchSHA.
	branchSHAResolver func(ctx context.Context, remoteURL, branch string) string

	// credentialReader resolves the git credential for a repository at clone
	// time. Nil = no credential injection (public repos only).
	credentialReader ports.RepositoryCredentialReader

	// frameworkDetector detects frameworks by file/directory markers (stage 3b).
	// Runs after manifest parsing so it can skip frameworks already identified
	// by a package manager. Nil = structural detection disabled.
	frameworkDetector ports.StructuralFrameworkDetector

	// dbDetector deterministically identifies the database engine(s) the analysed
	// code uses (stage 3c). Runs after manifest + framework detection so it can use
	// both the dependency list and the detected framework. Nil = DB detection disabled.
	dbDetector ports.DatabaseDetector

	// securityScanner deterministically detects code-level security findings IN the
	// analysed source (stage 3d): hardcoded secrets/credentials in cleartext. Walks
	// the workspace; distinct from the dependency CVE scan. Nil = scan disabled.
	securityScanner ports.SecurityScanner

	// authDetector deterministically identifies the request-authentication scheme
	// the analysed backend uses (stage 3e): JWT/OAuth2/session/API-key/Basic/none.
	// Runs after manifest + framework detection so it can use both the dependency
	// list and the detected framework. Nil = auth detection disabled.
	authDetector ports.AuthSchemeDetector

	// supportedLanguages is the set of languages that have a registered Tier-2
	// analyzer (PHP, Python today). Fed to the intake gate (stage 7-intake) so the
	// language-support guard reflects the actually-wired registry rather than a
	// hardcoded list. Empty/nil ⇒ every language reports unsupported.
	supportedLanguages map[string]struct{}
}

// NewPipeline constructs a Pipeline. Only writer is required; all other ports
// default to nil and are wired via the With* builder methods.
func NewPipeline(writer ports.SummaryWriter) *Pipeline {
	return &Pipeline{writer: writer}
}

// WithAcquirer wires the SourceAcquirer (stage 1). Returns p for chaining.
func (p *Pipeline) WithAcquirer(a ports.SourceAcquirer) *Pipeline {
	p.acquirer = a
	return p
}

// WithDetector wires the LanguageDetector (stage 2). Returns p for chaining.
func (p *Pipeline) WithDetector(d ports.LanguageDetector) *Pipeline {
	p.detector = d
	return p
}

// WithParser registers a ManifestParser for the given ecosystem (stage 3).
// Multiple parsers may be registered; they run in registration order.
// Returns p for chaining.
func (p *Pipeline) WithParser(ecosystem workerdomain.Ecosystem, parser ports.ManifestParser) *Pipeline {
	p.parsers = append(p.parsers, parserEntry{ecosystem, parser})
	return p
}

// WithResolver wires the VersionResolver (stage 4). Returns p for chaining.
func (p *Pipeline) WithResolver(r ports.VersionResolver) *Pipeline {
	p.resolver = r
	return p
}

// WithScanner wires the VulnerabilityScanner (stage 5). Returns p for chaining.
func (p *Pipeline) WithScanner(s ports.VulnerabilityScanner) *Pipeline {
	p.scanner = s
	return p
}

// WithGraphBuilder wires the DependencyGraphBuilder (stage 6). Returns p for chaining.
// Use LanguageAnalyzerRegistry to route by detected language.
func (p *Pipeline) WithGraphBuilder(g ports.DependencyGraphBuilder) *Pipeline {
	p.graphBuilder = g
	return p
}

// WithCardProvider wires the ModuleCardProvider (stage 6b). When set, per-module
// structural cards and blueprint metadata are extracted and persisted alongside
// the dependency graph. Returns p for chaining.
func (p *Pipeline) WithCardProvider(c ports.ModuleCardProvider) *Pipeline {
	p.cardProvider = c
	return p
}

// WithClassifier wires the ModuleClassifier (stage 6c). When set and the
// dependency graph is non-empty, classifies each module into domain, infra, or
// test and persists the result in the AnalysisSummary. Returns p for chaining.
func (p *Pipeline) WithClassifier(c ports.ModuleClassifier) *Pipeline {
	p.classifier = c
	return p
}

// WithMigrabilityScorer wires the MigrabilityScorer (stage 6d). When set and
// module classification is available, computes and persists the deterministic
// structural migrability score. Returns p for chaining.
func (p *Pipeline) WithMigrabilityScorer(s ports.MigrabilityScorer) *Pipeline {
	p.migrabilityScorer = s
	return p
}

// WithDecomposeEnqueuer wires the DecomposeJobEnqueuer. When set, a decompose:run
// job is dispatched after a successful write with a non-zero migration_id.
func (p *Pipeline) WithDecomposeEnqueuer(e ports.DecomposeJobEnqueuer) *Pipeline {
	p.decomposeEnqueuer = e
	return p
}

// WithCredentialReader wires the RepositoryCredentialReader. When set, the
// pipeline looks up the git credential at clone time and injects it into the
// source URL so private repositories can be cloned without embedding the
// token in the Asynq task payload.
func (p *Pipeline) WithCredentialReader(r ports.RepositoryCredentialReader) *Pipeline {
	p.credentialReader = r
	return p
}

// WithFrameworkDetector wires the StructuralFrameworkDetector (stage 3b).
// When set, it runs after manifest parsing to detect frameworks that are
// vendored into the workspace rather than declared as dependencies.
func (p *Pipeline) WithFrameworkDetector(f ports.StructuralFrameworkDetector) *Pipeline {
	p.frameworkDetector = f
	return p
}

// WithDatabaseDetector wires the DatabaseDetector (stage 3c). When set, it runs
// after framework detection and persists the detected database engine(s) on the
// AnalysisSummary. Returns p for chaining.
func (p *Pipeline) WithDatabaseDetector(d ports.DatabaseDetector) *Pipeline {
	p.dbDetector = d
	return p
}

// WithSecurityScanner wires the SecurityScanner (stage 3d). When set, it walks the
// workspace for hardcoded secrets/credentials and persists the findings on the
// AnalysisSummary (security_findings). Distinct from the dependency CVE scan; additive
// — it does not affect scores or verdicts. Returns p for chaining.
func (p *Pipeline) WithSecurityScanner(s ports.SecurityScanner) *Pipeline {
	p.securityScanner = s
	return p
}

// WithAuthSchemeDetector wires the AuthSchemeDetector (stage 3e). When set, it runs
// after framework detection and persists the detected authentication scheme on the
// AnalysisSummary (auth_scheme_detection). Mirror of the database detector; non-fatal.
// Returns p for chaining.
func (p *Pipeline) WithAuthSchemeDetector(a ports.AuthSchemeDetector) *Pipeline {
	p.authDetector = a
	return p
}

// WithSupportedLanguages declares the languages that have a registered Tier-2
// analyzer. The intake gate's language-support guard checks the primary detected
// language against this set, so the "unsupported language" warning always reflects
// the actually-wired analyzers (today: PHP, Python). Returns p for chaining.
func (p *Pipeline) WithSupportedLanguages(langs ...string) *Pipeline {
	if p.supportedLanguages == nil {
		p.supportedLanguages = make(map[string]struct{}, len(langs))
	}
	for _, l := range langs {
		if l != "" {
			p.supportedLanguages[l] = struct{}{}
		}
	}
	return p
}

// WithBranchSHAResolver wires a function that resolves the HEAD commit SHA for
// a given branch on a remote without cloning. Used by the dedup check to
// determine whether the remote branch has advanced since the last COMPLETED
// analysis. When nil, the dedup check is skipped and full analysis always runs.
func (p *Pipeline) WithBranchSHAResolver(fn func(ctx context.Context, remoteURL, branch string) string) *Pipeline {
	p.branchSHAResolver = fn
	return p
}

// MarkFailed transitions the migration from ANALYZING to FAILED and persists
// the human-readable reason. Called by the job handler when all Asynq retries
// are exhausted. No-op when no writer is wired.
func (p *Pipeline) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	if p.writer == nil {
		return nil
	}
	return p.writer.MarkFailed(ctx, migrationID, reason)
}

// MarkAnalysisFailed transitions the AnalysisSummary to FAILED and persists
// the human-readable reason. Called on final retry for any analysis, regardless
// of whether a migration is associated. No-op when no writer is wired.
func (p *Pipeline) MarkAnalysisFailed(ctx context.Context, summaryID uint64, reason string) error {
	if p.writer == nil {
		return nil
	}
	return p.writer.MarkAnalysisFailed(ctx, summaryID, reason)
}

// Run executes the analysis pipeline for the given job.
//
// Stage 1 (acquire): if acquirer is wired, clones/unzips the source into a
// temporary workspace. Otherwise workspace is empty and stages 2–3 are skipped.
//
// Stage 2 (inventory): if detector is wired and a workspace is available,
// walks the workspace with go-enry to produce detected languages and totals.
//
// Stage 3 (manifests): each registered parser runs against the workspace;
// dependencies are converted to Technology entries and merged in.
//
// Stage 4 (versions): if resolver is wired, fetches the latest published version
// for each manifest dependency and computes its currency status. Resolution is
// best-effort: a registry error skips that entry without failing the job.
//
// Stage 5 (vulnerabilities): if scanner is wired, queries OSV.dev for each
// manifest dependency and collects matching advisories. A scan error is
// non-fatal: the job completes without vulnerability data.
//
// Stages 6–7 are not yet implemented; they produce no output and are skipped.
//
// Stage 8 (write): always runs — calls SummaryWriter with the assembled summary
// (partial when earlier stages were skipped).
func (p *Pipeline) Run(ctx context.Context, job workerdomain.JobPayload) error {
	applog.Infof("analysis-worker: starting job summary_id=%d repository_id=%d migration_id=%d",
		job.SummaryID, job.RepositoryID, job.MigrationID)

	// cloneURL is job.RemoteURL with credentials injected when a private repo
	// token is registered. It is also forwarded to the decompose enqueuer so
	// that the decompose stage can clone the same repository without a separate
	// credential lookup.
	cloneURL := job.RemoteURL
	if p.credentialReader != nil && job.RepositoryID != 0 {
		token, credErr := p.credentialReader.GetCredentialRef(ctx, job.RepositoryID)
		if credErr != nil {
			applog.Warningf("analysis-worker: credential lookup failed repository_id=%d: %v", job.RepositoryID, credErr)
		} else if token != "" {
			cloneURL = injectToken(cloneURL, token)
		}
	}

	// Dedup check — runs only for migration-linked jobs when a branch SHA
	// resolver is wired. Gets the current HEAD SHA via ls-remote (no clone),
	// finds the last COMPLETED analysis for this repo+branch, and reuses it when
	// the commit has not changed. Returns immediately: the migration advances to
	// DESIGNING in milliseconds, no clone or analysis needed.
	if p.branchSHAResolver != nil && job.MigrationID != 0 && job.DefaultBranch != "" {
		headSHA := p.branchSHAResolver(ctx, cloneURL, job.DefaultBranch)
		if headSHA != "" {
			existing, findErr := p.writer.FindCompletedForBranch(ctx, job.RepositoryID, job.DefaultBranch)
			if findErr != nil {
				applog.Warningf("analysis-worker: dedup lookup failed repository_id=%d branch=%s: %v — falling back to full analysis",
					job.RepositoryID, job.DefaultBranch, findErr)
			} else if existing != nil && existing.GetCommitSha() == headSHA {
				applog.Infof("analysis-worker: reusing existing analysis summary_id=%d commit=%s migration_id=%d",
					existing.GetIdentifier(), headSHA, job.MigrationID)
				if reuseErr := p.writer.WriteReuse(ctx, existing.GetIdentifier(), job.MigrationID); reuseErr != nil {
					return fmt.Errorf("dedup WriteReuse: %w", reuseErr)
				}
				// Close the RUNNING summary that RunAnalysis created for this job.
				// Without this it would stay RUNNING forever (zombie). The summary
				// carries no useful data since the full analysis was skipped.
				if closeErr := p.writer.MarkAnalysisFailed(ctx, job.SummaryID, "superseded by dedup reuse"); closeErr != nil {
					applog.Warningf("analysis-worker: close zombie summary failed summary_id=%d: %v", job.SummaryID, closeErr)
				}
				if p.decomposeEnqueuer != nil {
					if enqErr := p.decomposeEnqueuer.EnqueueDecompose(ctx, job.MigrationID, existing.GetIdentifier(), cloneURL, job.DefaultBranch, job.RootSubdirectory); enqErr != nil {
						applog.Warningf("analysis-worker: decompose enqueue failed (reuse) migration_id=%d summary_id=%d: %v",
							job.MigrationID, existing.GetIdentifier(), enqErr)
					}
				}
				applog.Infof("analysis-worker: job complete (reused) migration_id=%d", job.MigrationID)
				return nil
			}
		}
	}

	// Stage 1 — acquire workspace.
	var workspacePath, commitSHA string
	if p.acquirer != nil {
		wp, sha, cleanup, err := p.acquirer.Acquire(ctx, cloneURL, job.DefaultBranch)
		if err != nil {
			applog.Warningf("analysis-worker: stage 1 failed summary_id=%d: %v", job.SummaryID, err)
			return fmt.Errorf("stage 1 (acquire): %w", err)
		}
		workspacePath = wp
		commitSHA = sha
		if commitSHA != "" {
			applog.Infof("analysis-worker: resolved HEAD commit sha=%s summary_id=%d", commitSHA, job.SummaryID)
		}
		defer cleanup()

		// Monorepo root resolution. The chosen root belongs to the ANALYSIS:
		//   - job.RootSubdirectory already set → a root was explicitly chosen
		//     (via SelectRoot re-enqueue, or carried by a single-root re-run).
		//     Skip detection and scope directly.
		//   - empty → detect candidate project roots in the freshly cloned tree.
		//     0/1 clear root: proceed automatically (single-root path, root_subdir
		//     stays "" or the one detected dir). ≥2 distinct roots: persist the
		//     candidate list, set the analysis AWAITING_ROOT_SELECTION, and stop
		//     cleanly (no error → no Asynq retry loop). The user picks via SelectRoot.
		resolvedRoot := job.RootSubdirectory
		if resolvedRoot == "" {
			candidates, detErr := DetectRootCandidates(workspacePath)
			if detErr != nil {
				applog.Warningf("analysis-worker: root detection failed summary_id=%d: %v — defaulting to repository root",
					job.SummaryID, detErr)
				candidates = nil
			}
			root, awaiting := ResolveSingleRoot(candidates)
			if awaiting {
				applog.Infof("analysis-worker: multiple project roots detected summary_id=%d candidates=%v — awaiting selection",
					job.SummaryID, candidates)
				if p.writer != nil {
					if mErr := p.writer.MarkAwaitingRootSelection(ctx, job.SummaryID, candidates); mErr != nil {
						// A persistence failure here IS retryable (transient Mongo), so
						// return the error to let Asynq retry the detection+persist.
						return fmt.Errorf("stage 1 (acquire): persist awaiting-root-selection: %w", mErr)
					}
				}
				// Stop cleanly: no heavy pipeline, no migration advance, no retry loop.
				return nil
			}
			resolvedRoot = root
			if resolvedRoot != "" {
				applog.Infof("analysis-worker: auto-selected single project root subdir=%q summary_id=%d",
					resolvedRoot, job.SummaryID)
			}
		}

		// Scope the workspace to the resolved root so every downstream stage
		// (inventory, manifests, framework/db/security detection, dependency graph,
		// module cards) walks only that subtree. The whole repository is still
		// cloned (above); only the analysis root moves. Empty = repository root
		// (no-op, the single-root default). An invalid/non-existent subdir is a
		// hard error: scoping to the wrong directory would silently analyse the
		// wrong code. job.RootSubdirectory is carried forward to the summary and
		// the decompose enqueuer below.
		job.RootSubdirectory = resolvedRoot
		if resolvedRoot != "" {
			scoped, scopeErr := scopeWorkspace(workspacePath, resolvedRoot)
			if scopeErr != nil {
				applog.Warningf("analysis-worker: stage 1 subdir scoping failed summary_id=%d subdir=%q: %v",
					job.SummaryID, resolvedRoot, scopeErr)
				return fmt.Errorf("stage 1 (acquire): scope to subdirectory %q: %w", resolvedRoot, scopeErr)
			}
			applog.Infof("analysis-worker: scoped analysis to subdir=%q workspace=%s summary_id=%d",
				resolvedRoot, scoped, job.SummaryID)
			workspacePath = scoped
		}
	}

	// technologies accumulates entries from all stages via MergeTechnologies.
	// Each stage merges its output in without overwriting what the previous wrote.
	var technologies []*analysisdomain.Technology
	var totalFiles, totalLines uint64

	// detectedLangs is populated by stage 2 and consumed by stage 6.
	var detectedLangs []workerdomain.DetectedLanguage

	// Stage 2 — inventory.
	if p.detector != nil && workspacePath != "" {
		langs, err := p.detector.Detect(ctx, workspacePath)
		if err != nil {
			applog.Warningf("analysis-worker: stage 2 failed summary_id=%d: %v", job.SummaryID, err)
			return fmt.Errorf("stage 2 (inventory): %w", err)
		}
		detectedLangs = langs
		for _, l := range langs {
			totalFiles += l.Files
			totalLines += l.Lines
		}
		technologies = MergeTechnologies(technologies, inventoryTechnologies(langs))
		applog.Infof("analysis-worker: inventory done langs=%d files=%d lines=%d",
			len(langs), totalFiles, totalLines)
	}

	// Stage 3 — manifest parsing. manifestDeps carries the full dependency list
	// with ecosystem info for stage 4; technology entries are merged separately.
	var manifestDeps []workerdomain.Dependency
	if len(p.parsers) > 0 && workspacePath != "" {
		for _, pe := range p.parsers {
			deps, err := pe.parser.Parse(ctx, workspacePath, pe.ecosystem)
			if err != nil {
				applog.Warningf("analysis-worker: stage 3 (%s) failed summary_id=%d: %v", pe.ecosystem, job.SummaryID, err)
				return fmt.Errorf("stage 3 (manifests/%s): %w", pe.ecosystem, err)
			}
			technologies = MergeTechnologies(technologies, manifestTechnologies(deps))
			manifestDeps = append(manifestDeps, deps...)
		}
		applog.Infof("analysis-worker: manifest stage done ecosystems=%d", len(p.parsers))
	}

	// Stage 3b — structural framework detection. Detects frameworks that are
	// vendored into the workspace (e.g. CodeIgniter 3 system/ directory) rather
	// than declared as package manager dependencies. Runs after stage 3 so that
	// manifest-detected frameworks are already in technologies and can be used
	// to skip duplicates.
	if p.frameworkDetector != nil && workspacePath != "" {
		detected, err := p.frameworkDetector.Detect(ctx, workspacePath, technologies)
		if err != nil {
			applog.Warningf("analysis-worker: stage 3b (structural frameworks) failed summary_id=%d: %v", job.SummaryID, err)
		} else if len(detected) > 0 {
			technologies = MergeTechnologies(technologies, detected)
			applog.Infof("analysis-worker: structural framework stage done detected=%d", len(detected))
		}
	}

	// Stage 3c — database engine detection. Deterministically identifies the
	// database engine(s) the analysed code uses from drivers/ORM packages, config
	// files (.env, config/database.php, Django settings), and the framework default.
	// Runs after framework detection so the framework-default tie-break can fire.
	// Non-fatal: a detection error leaves the field nil and the job still completes.
	var databaseDetection *analysisdomain.DatabaseDetection
	if p.dbDetector != nil && workspacePath != "" {
		dd, ddErr := p.dbDetector.Detect(ctx, workspacePath, manifestDeps, technologies)
		if ddErr != nil {
			applog.Warningf("analysis-worker: stage 3c (database detection) failed summary_id=%d: %v", job.SummaryID, ddErr)
		} else {
			databaseDetection = dd
			applog.Infof("analysis-worker: database detection done summary_id=%d engines=%v unknown=%v",
				job.SummaryID, dd.GetEngineNames(), dd.GetUnknown())
		}
	}

	// Stage 3d — code-level security scan. Deterministically detects hardcoded
	// secrets/credentials IN the analysed source (API keys, passwords, tokens,
	// private keys, JWTs, connection strings with embedded credentials) by walking
	// the workspace with a pattern + entropy scanner. Distinct from the dependency
	// CVE scan (stage 5). Conservative (placeholders suppressed, confidence carried);
	// additive — does not affect scores/verdicts. Non-fatal: a scan error leaves the
	// field empty and the job still completes.
	var securityFindings []*analysisdomain.SecurityFinding
	if p.securityScanner != nil && workspacePath != "" {
		sf, sfErr := p.securityScanner.Scan(ctx, workspacePath)
		if sfErr != nil {
			applog.Warningf("analysis-worker: stage 3d (security scan) failed summary_id=%d: %v", job.SummaryID, sfErr)
		} else {
			// Cap the persisted set (top-N by severity/confidence) and sanitise every
			// string field to valid UTF-8 so the summary always proto.Marshals. Without
			// the UTF-8 pass, a single invalid byte in a snippet makes proto.Marshal
			// fail and the write hangs/loops forever; without the cap, a noisy repo
			// bloats the document. capSecurityFindings does both.
			capped, dropped := capSecurityFindings(sf)
			securityFindings = capped
			if dropped > 0 {
				applog.Infof("analysis-worker: security scan done summary_id=%d findings=%d (capped from %d, dropped=%d)",
					job.SummaryID, len(capped), len(sf), dropped)
			} else {
				applog.Infof("analysis-worker: security scan done summary_id=%d findings=%d", job.SummaryID, len(capped))
			}
		}
	}

	// Stage 3e — authentication scheme detection. Deterministically identifies the
	// request-authentication scheme the analysed backend uses (JWT/OAuth2/session/
	// API-key/Basic/none) from auth packages, .env JWT config, the Authorization:
	// Bearer header convention, and the framework default. Mirror of stage 3c.
	// Non-fatal: a detection error leaves the field nil and the job still completes.
	var authSchemeDetection *analysisdomain.AuthSchemeDetection
	if p.authDetector != nil && workspacePath != "" {
		ad, adErr := p.authDetector.Detect(ctx, workspacePath, manifestDeps, technologies)
		if adErr != nil {
			applog.Warningf("analysis-worker: stage 3e (auth scheme detection) failed summary_id=%d: %v", job.SummaryID, adErr)
		} else {
			authSchemeDetection = ad
			applog.Infof("analysis-worker: auth scheme detection done summary_id=%d scheme=%s sig=%s unknown=%v",
				job.SummaryID, ad.GetSchemeName(), ad.GetSignatureAlg(), ad.GetUnknown())
		}
	}

	// Stage 4 — version currency. Fetch latest published version from each
	// registry and compute whether the detected version is current or outdated.
	// Registry errors skip the entry; they must not fail the job (graceful
	// degradation — a registry being unreachable is not the repository's fault).
	if p.resolver != nil && len(manifestDeps) > 0 {
		techByName := make(map[string]*analysisdomain.Technology, len(technologies))
		for i := range technologies {
			techByName[technologies[i].GetName()] = technologies[i]
		}
		// For framework deps whose Technology.Name was normalised to a display name
		// (e.g. "Laravel" instead of "laravel/framework"), also index by the raw
		// Package name so version resolution still finds the right entry.
		for _, dep := range manifestDeps {
			if dep.DisplayName != "" {
				if t, ok := techByName[dep.DisplayName]; ok {
					techByName[dep.Package] = t
				}
			}
		}
		resolved := 0
		for _, dep := range manifestDeps {
			t, ok := techByName[dep.Package]
			if !ok {
				continue
			}
			currency, err := p.resolver.Latest(ctx, dep.Ecosystem, dep.Package)
			if err != nil {
				applog.Warningf("analysis-worker: version resolve failed ecosystem=%s pkg=%s: %v",
					dep.Ecosystem, dep.Package, err)
				continue
			}
			t.LatestVersion = currency.LatestVersion
			t.Status = versionStatus(dep.Version, currency.LatestVersion)
			resolved++
		}
		applog.Infof("analysis-worker: version stage done resolved=%d/%d", resolved, len(manifestDeps))
	}

	// Stage 5 — vulnerability scanning. Checks each manifest dependency against
	// OSV.dev. Errors are non-fatal: the job still completes with whatever
	// technology data was collected, without vulnerability information.
	var vulnerabilities []*analysisdomain.Vulnerability
	if p.scanner != nil && len(manifestDeps) > 0 {
		scanned, err := p.scanner.Scan(ctx, manifestDeps)
		if err != nil {
			applog.Warningf("analysis-worker: vulnerability scan failed: %v", err)
		} else {
			vulnerabilities = scanned
			applog.Infof("analysis-worker: vulnerability stage done vulns=%d deps=%d",
				len(vulnerabilities), len(manifestDeps))
		}
	}

	// Stage 6 — dependency graph. For each detected language with a registered
	// analyzer, resolve internal imports and accumulate weighted edges. Languages
	// without a registered analyzer are holes: they are skipped without error and
	// produce a Tier-1-only summary (technologies + vulnerabilities, no graph).
	var dependencyGraph []*analysisdomain.DependencyEdge
	if p.graphBuilder != nil && workspacePath != "" {
		for _, lang := range detectedLangs {
			edges, err := p.graphBuilder.Build(ctx, workspacePath, lang.Name)
			if err != nil {
				applog.Warningf("analysis-worker: stage 6 (%s) failed: %v", lang.Name, err)
				continue
			}
			if edges == nil {
				// Hole: no analyzer registered for this language.
				applog.Infof("analysis-worker: deep analysis not available for %s; Tier-1 summary produced", lang.Name)
				continue
			}
			dependencyGraph = append(dependencyGraph, edges...)
		}
		if len(dependencyGraph) > 0 {
			applog.Infof("analysis-worker: dependency graph done edges=%d", len(dependencyGraph))
		}
	}

	// Stage 6b — module cards. For each detected language, extract per-module
	// structural cards (functions, classes, mutable state, routes) and Flask
	// blueprint registrations. Errors are non-fatal: the job still completes
	// with whatever structural data was collected.
	var moduleCards []*analysisdomain.ModuleCard
	var blueprints []*analysisdomain.BlueprintInfo
	if p.cardProvider != nil && workspacePath != "" {
		for _, lang := range detectedLangs {
			cards, bps, err := p.cardProvider.ExtractCards(ctx, workspacePath, lang.Name)
			if err != nil {
				applog.Warningf("analysis-worker: stage 6b (%s) card extraction failed: %v", lang.Name, err)
				continue
			}
			moduleCards = append(moduleCards, cards...)
			blueprints = append(blueprints, bps...)
		}
		if len(moduleCards) > 0 {
			applog.Infof("analysis-worker: module cards done modules=%d blueprints=%d",
				len(moduleCards), len(blueprints))
		}
	}

	// Stage 6c — module classification. Runs only when the graph is non-empty;
	// without edges there are no modules to classify. Non-fatal: log and skip.
	var moduleClassification *analysisdomain.ModuleClassification
	if p.classifier != nil && len(dependencyGraph) > 0 {
		mc, classErr := p.classifier.Classify(ctx, dependencyGraph)
		if classErr != nil {
			applog.Warningf("analysis-worker: stage 6c classification failed summary_id=%d: %v",
				job.SummaryID, classErr)
		} else {
			moduleClassification = mc
			applog.Infof("analysis-worker: classification done domain=%d application=%d infra=%d tests=%d structural_fallback=%v",
				len(mc.GetDomainModules()), len(mc.GetApplicationModules()),
				len(mc.GetInfraModules()), len(mc.GetTestModules()), mc.GetStructuralFallback())
		}
	}

	// Stage 6 (B₂) — live-system partition. A module belongs to the live system
	// iff it has fan-in > 0 OR fan-out > 0, i.e. it appears as an endpoint of at
	// least one dependency edge. The live set governs scoring/clustering/hub/god
	// analysis only — a node with no edges cannot cluster or be a hub anyway.
	//
	// The live set does NOT govern the production count. The count stays an
	// inventory of every module carrying code (see below): for languages whose
	// static import graph is incomplete — notably PHP/Laravel, where kernels,
	// middleware, providers, commands, factories and seeders are wired at runtime
	// by mechanisms no static extractor sees — an island is not evidence that the
	// module is absent from the running system. Decoupling the count from the live
	// set avoids under-counting those real production modules.
	//
	// Island disposition (islands carry no edges):
	//   - test islands → folded into TestModules; an unimported test is not dead.
	//   - has-code islands that are NOT known framework entry points → reported as
	//     statically unreachable (for review — never "delete"; see UnreachableModule).
	//   - has-code islands matching the framework-entry-point allowlist → suppressed
	//     from the report (known reachable-by-framework); still counted as production.
	//   - empty package markers (no functions/classes) → ignored everywhere.
	//
	// Skipped without a graph: with no edges there is no live/island distinction.
	allCards := moduleCards
	var unreachableModules []*analysisdomain.UnreachableModule
	if moduleClassification != nil && len(dependencyGraph) > 0 && len(moduleCards) > 0 {
		liveSet := make(map[string]struct{}, len(dependencyGraph)*2)
		for _, e := range dependencyGraph {
			if m := e.GetFromModule(); m != "" {
				liveSet[m] = struct{}{}
			}
			if m := e.GetToModule(); m != "" {
				liveSet[m] = struct{}{}
			}
		}
		liveCards := make([]*analysisdomain.ModuleCard, 0, len(moduleCards))
		frameworkSuppressed := 0
		for _, card := range moduleCards {
			m := card.GetModule()
			if _, live := liveSet[m]; live {
				liveCards = append(liveCards, card)
				continue
			}
			switch {
			case isTestModuleByName(m):
				// Fold so classification stays complete; still counted as a test
				// via allCards below, never reported.
				moduleClassification.TestModules = append(moduleClassification.TestModules, m)
			case len(card.GetFunctions()) == 0 && len(card.GetClasses()) == 0:
				// Empty package marker — no code, not reported, not production.
			case isFrameworkEntrypoint(m):
				// Reachable by framework wiring the static graph cannot see;
				// counted as production via allCards, suppressed from the report.
				frameworkSuppressed++
			default:
				unreachableModules = append(unreachableModules, &analysisdomain.UnreachableModule{
					Module: m,
					File:   card.GetFile(),
					Loc:    card.GetLoc(),
				})
			}
		}
		sort.Strings(moduleClassification.TestModules)
		sort.Slice(unreachableModules, func(i, j int) bool {
			return unreachableModules[i].GetModule() < unreachableModules[j].GetModule()
		})
		moduleCards = liveCards
		applog.Infof("analysis-worker: stage 6 (B₂) partition summary_id=%d live=%d unreachable=%d framework_suppressed=%d",
			job.SummaryID, len(liveCards), len(unreachableModules), frameworkSuppressed)
	}

	// Stage 6d — deterministic migrability score. Runs when both the scorer and
	// classification are available and the dependency graph is non-empty. Non-fatal.
	var migrabilityScore *commonv1.MigrabilityScore
	if p.migrabilityScorer != nil && moduleClassification != nil && len(dependencyGraph) > 0 {
		ms, scoreErr := p.migrabilityScorer.Score(ctx, dependencyGraph, moduleClassification, moduleCards, blueprints)
		if scoreErr != nil {
			applog.Warningf("analysis-worker: stage 6d migrability score failed summary_id=%d: %v",
				job.SummaryID, scoreErr)
		} else {
			migrabilityScore = ms
			applog.Infof("analysis-worker: stage 6d migrability score done summary_id=%d score=%d",
				job.SummaryID, ms.GetValue())
		}
	}

	// Manifest-language boost: when a backend package manager was detected,
	// promote its language to the front of technologies[] regardless of raw file
	// counts. This prevents vendored frontend assets (e.g. admin-template JS
	// libraries in assets/) from displacing the real backend language.
	if len(manifestDeps) > 0 {
		technologies = boostManifestLanguage(technologies, manifestDeps)
	}

	// Framework inference from code markers: when manifest parsing produced no
	// framework entry (e.g. Flask in build/backend/requirements.txt instead of
	// the repo root), derive the framework from code-level structural signals and
	// inject it into technologies before persisting. Fixing the data at write time
	// means every reader (GetAnalysisSummary, StackDetector, exports) benefits
	// without each needing its own inference pass. The inference is gated on the
	// PRIMARY backend language (same resolver the intake gate uses) so a polyglot
	// repo is never mislabelled with a secondary language's framework.
	primaryLanguage := primaryBackendLanguage(intakeInput{
		detectedLangs:      detectedLangs,
		supportedLanguages: p.supportedLanguages,
	})
	technologies = injectInferredFramework(technologies, blueprints, primaryLanguage)

	// Stage 6e — canonical hub enrichment. Compute weighted fan-in/fan-out from
	// the dependency graph (same formula the scorer/distiller use) and set them
	// on each ModuleCard, along with the canonical IsSharedStateHub flag. Also
	// build the SharedStateHubs list that the frontend reads instead of recomputing
	// with its own thresholds. Runs whenever we have cards and a graph.
	//
	// The test module set (from stage 6c) is threaded in so that
	// fan_in_from_production can be computed alongside fan_in in a single pass.
	var testModuleSet map[string]struct{}
	if moduleClassification != nil {
		testModuleSet = make(map[string]struct{}, len(moduleClassification.GetTestModules()))
		for _, m := range moduleClassification.GetTestModules() {
			testModuleSet[m] = struct{}{}
		}
	}
	var sharedStateHubs []*analysisdomain.SharedStateHub
	if len(moduleCards) > 0 && len(dependencyGraph) > 0 {
		moduleCards, sharedStateHubs = enrichModuleCards(moduleCards, dependencyGraph, testModuleSet)
	}

	// Build the classified set (domain + infra) used by the production-count
	// filter below. Kept separate from testModuleSet so the predicate is clear.
	var classifiedSet map[string]struct{}
	if moduleClassification != nil {
		total := len(moduleClassification.GetDomainModules()) + len(moduleClassification.GetInfraModules())
		classifiedSet = make(map[string]struct{}, total)
		for _, m := range moduleClassification.GetDomainModules() {
			classifiedSet[m] = struct{}{}
		}
		for _, m := range moduleClassification.GetInfraModules() {
			classifiedSet[m] = struct{}{}
		}
	}

	// Compute production/test counts over the FULL card universe (allCards), not
	// the B₂ live set: an island is still production (B₂ governs scoring, not the
	// inventory — see stage 6). A card counts as production when it is NOT a test
	// AND NOT an empty unclassified marker (a bare __init__.py with no functions
	// or classes). The three-way AND is intentional: a classified __init__.py that
	// registers a blueprint boundary is kept even when funcs=0 AND classes=0.
	var moduleCountProduction, moduleCountTest uint32
	for _, c := range allCards {
		if _, isTest := testModuleSet[c.GetModule()]; isTest {
			moduleCountTest++
			continue
		}
		_, isClassified := classifiedSet[c.GetModule()]
		if !isClassified && len(c.GetFunctions()) == 0 && len(c.GetClasses()) == 0 {
			continue
		}
		moduleCountProduction++
	}

	deepAnalysisAvailable := len(dependencyGraph) > 0 || len(allCards) > 0

	// Stage 6f — architectural pattern classification. A deterministic, rules-based
	// classifier maps the structural signals already computed above (domain/infra
	// ratio, layers present, framework, routing topology, cluster count) to one
	// canonical pattern with a confidence and the evidence used. No LLM call, no
	// I/O — a pure function of in-memory pipeline data. Runs only when module
	// classification exists; without it there is no structural basis to classify.
	var architecturalPattern *analysisdomain.ArchitecturalPattern
	if moduleClassification != nil {
		architecturalPattern = classifyArchitecturalPattern(patternInput{
			classification: moduleClassification,
			score:          migrabilityScore,
			cards:          moduleCards,
			blueprints:     blueprints,
			technologies:   technologies,
			edges:          dependencyGraph,
			deepAvailable:  deepAnalysisAvailable,
		})
		applog.Infof("analysis-worker: architectural pattern summary_id=%d pattern=%q confidence=%.2f",
			job.SummaryID, architecturalPattern.GetName(), architecturalPattern.GetConfidence())
	}

	// Stage 7-intake — intake gate. A deterministic, no-I/O, no-LLM verdict on whether
	// the platform can migrate this repository at all: (5) is it a backend? and (7) is
	// its primary language supported? Built from signals already in memory. Always runs
	// (independent of deep analysis) so even a Tier-1-only or non-backend repo gets an
	// honest verdict. Honest degradation: when a check fails the assessment carries
	// migratable=false plus a specific warning — the analysis still completes and Tier-1
	// facts are preserved; downstream gates the migrability report on Migratable.
	intakeAssessment := assessIntake(intakeInput{
		detectedLangs:      detectedLangs,
		technologies:       technologies,
		manifestDeps:       manifestDeps,
		cards:              allCards,
		blueprints:         blueprints,
		supportedLanguages: p.supportedLanguages,
	})
	applog.Infof("analysis-worker: intake gate summary_id=%d kind=%s lang=%q supported=%v migratable=%v warnings=%d",
		job.SummaryID, intakeAssessment.GetCodebaseKind(), intakeAssessment.GetPrimaryLanguage(),
		intakeAssessment.GetLanguageSupported(), intakeAssessment.GetMigratable(), len(intakeAssessment.GetWarnings()))

	summary := &analysisdomain.AnalysisSummary{
		Identifier:            job.SummaryID,
		RepositoryId:          job.RepositoryID,
		MigrationId:           job.MigrationID,
		State:                 analysisdomain.AnalysisStateCompleted,
		Technologies:          technologies,
		Vulnerabilities:       vulnerabilities,
		DependencyGraph:       dependencyGraph,
		ModuleCards:           moduleCards,
		Blueprints:            blueprints,
		ModuleClassification:  moduleClassification,
		MigrabilityScore:      migrabilityScore,
		SharedStateHubs:       sharedStateHubs,
		DatabaseDetection:     databaseDetection,
		ArchitecturalPattern:  architecturalPattern,
		IntakeAssessment:      intakeAssessment,
		SecurityFindings:      securityFindings,
		AuthSchemeDetection:   authSchemeDetection,
		TotalFiles:            totalFiles,
		TotalLines:            totalLines,
		CommitSha:             commitSHA,
		RootSubdirectory:      job.RootSubdirectory,
		ModuleCountProduction: moduleCountProduction,
		ModuleCountTest:       moduleCountTest,
		UnreachableModules:    unreachableModules,
		// Explicit deep-analysis-availability signal: the deep analyzer produced
		// structural output iff there is a dependency graph or any module card.
		// allCards is the full pre-B₂ card universe, so this is independent of the
		// B₂ live-set filter. Downstream (migrability assessment, UI) reads this to
		// degrade honestly instead of mistaking an analyzer hole for "no domain".
		DeepAnalysisAvailable: deepAnalysisAvailable,
	}

	if err := p.writer.Write(ctx, summary); err != nil {
		return err
	}

	// Trigger DESIGNING pipeline: dispatch a decompose:run job when this
	// analysis run is tied to a migration. Enqueue failures are best-effort —
	// the migration is already at DESIGNING, so a retry of the analysis job
	// would find the summary COMPLETED and skip, losing the trigger. Log the
	// failure loudly so operators can re-queue manually.
	if job.MigrationID != 0 && p.decomposeEnqueuer != nil {
		if enqErr := p.decomposeEnqueuer.EnqueueDecompose(ctx, job.MigrationID, job.SummaryID, cloneURL, job.DefaultBranch, job.RootSubdirectory); enqErr != nil {
			applog.Warningf("analysis-worker: decompose enqueue failed migration_id=%d summary_id=%d: %v",
				job.MigrationID, job.SummaryID, enqErr)
		}
	}

	applog.Infof("analysis-worker: job complete summary_id=%d", job.SummaryID)
	return nil
}

// scopeWorkspace resolves a repository-relative subdirectory against the clone
// root and returns the absolute path to analyse. It is the single point that
// narrows the workspace for monorepo support: every downstream stage receives
// the returned path, so the whole pipeline (inventory, manifests, framework/db/
// security detection, dependency graph, module cards) operates relative to it
// without any per-stage change.
//
// The subdir is sanitised defensively even though the API layer also validates:
//   - back-slashes are normalised to forward slashes (Windows-style input),
//   - the path is cleaned and must not escape the root (no "..", no absolute
//     path) — a traversal attempt is rejected rather than silently clamped,
//   - the resolved path must exist and be a directory.
//
// Returning an error (rather than falling back to the root) is deliberate:
// analysing the repository root when the user asked for "backend/" would produce
// a confidently wrong report. Honest failure beats a silent wrong scope.
func scopeWorkspace(root, subdir string) (string, error) {
	// Normalise separators and clean. path.Clean (forward-slash) is used for the
	// traversal check so the rule is OS-independent.
	rel := path.Clean(strings.ReplaceAll(subdir, "\\", "/"))
	if rel == "" || rel == "." {
		return root, nil
	}
	if path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("subdirectory %q escapes the repository root", subdir)
	}
	scoped := filepath.Join(root, filepath.FromSlash(rel))

	// Defence in depth: confirm the joined path is still under root even after
	// symlink-free cleaning (filepath.Join already cleans, but verify the prefix).
	rootClean := filepath.Clean(root)
	if scoped != rootClean && !strings.HasPrefix(scoped, rootClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("subdirectory %q escapes the repository root", subdir)
	}

	info, statErr := os.Stat(scoped)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return "", fmt.Errorf("subdirectory %q does not exist in the repository", subdir)
		}
		return "", fmt.Errorf("stat subdirectory %q: %w", subdir, statErr)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", subdir)
	}
	return scoped, nil
}

// enrichModuleCards computes weighted fan-in/fan-out for each module card from
// the dependency graph edges (same formula as the distiller: sum of edge weights),
// sets IsSharedStateHub = state>0 && fanIn>=2, and returns the enriched cards
// alongside the canonical SharedStateHubs list sorted by fan-in descending.
//
// testModules is the set of modules classified as tests. When non-nil it is used
// to compute FanInFromProduction (edges from non-test importers only). Passing
// nil or empty skips the production fan-in split (field stays zero).
func enrichModuleCards(
	cards []*analysisdomain.ModuleCard,
	edges []*analysisdomain.DependencyEdge,
	testModules map[string]struct{},
) ([]*analysisdomain.ModuleCard, []*analysisdomain.SharedStateHub) {
	fanIn := make(map[string]uint32, len(cards))
	fanInProd := make(map[string]uint32, len(cards))
	fanOut := make(map[string]uint32, len(cards))
	for _, e := range edges {
		if e.GetFromModule() != "" {
			fanOut[e.GetFromModule()] += e.GetWeight()
		}
		to := e.GetToModule()
		if to != "" {
			w := e.GetWeight()
			fanIn[to] += w
			if _, isTest := testModules[e.GetFromModule()]; !isTest {
				fanInProd[to] += w
			}
		}
	}
	enriched := make([]*analysisdomain.ModuleCard, 0, len(cards))
	var hubs []*analysisdomain.SharedStateHub
	for _, c := range cards {
		fi := fanIn[c.GetModule()]
		fo := fanOut[c.GetModule()]
		isHub := isSharedStateHubCard(c, fi)
		// Clone the card to avoid mutating the caller's slice.
		ec := &analysisdomain.ModuleCard{
			Module:              c.GetModule(),
			File:                c.GetFile(),
			Functions:           c.GetFunctions(),
			Classes:             c.GetClasses(),
			ModuleLevelState:    c.GetModuleLevelState(),
			Routes:              c.GetRoutes(),
			DocstringHead:       c.GetDocstringHead(),
			Loc:                 c.GetLoc(),
			FanIn:               fi,
			FanOut:              fo,
			FanInFromProduction: fanInProd[c.GetModule()],
			IsSharedStateHub:    isHub,
		}
		enriched = append(enriched, ec)
		if isHub {
			hubs = append(hubs, &analysisdomain.SharedStateHub{
				Module:       c.GetModule(),
				MutableState: c.GetModuleLevelState(),
				FanIn:        fi,
			})
		}
	}
	// Sort hubs by fan-in descending (worst first), matching distiller order.
	for i := 1; i < len(hubs); i++ {
		for j := i; j > 0 && hubs[j].FanIn > hubs[j-1].FanIn; j-- {
			hubs[j], hubs[j-1] = hubs[j-1], hubs[j]
		}
	}
	return enriched, hubs
}

// isSharedStateHubCard is the canonical (proto-side) hub predicate. It mirrors the
// distiller's isSharedStateHub so the persisted IsSharedStateHub flag and the
// SharedStateHubs list agree with the score the distiller computes.
//
//   - PSR-4 / Python: extracted module-level mutable state + fan-in >= 2.
//   - Convention-routed (CodeIgniter 3): no module-level mutable state is
//     extractable, so a high fan-in alone (a base class everything extends or a
//     model everything loads) is the honest structural coupling signal. CI3 cards
//     are recognised by Module == File ending in .php (extractCI3Cards sets both to
//     the workspace-relative path; Python dotted names and PSR-4 backslash FQNs
//     never equal their file path).
func isSharedStateHubCard(c *analysisdomain.ModuleCard, fanIn uint32) bool {
	if fanIn < 2 {
		return false
	}
	if len(c.GetModuleLevelState()) > 0 {
		return true
	}
	file := c.GetFile()
	return file != "" && c.GetModule() == file && strings.HasSuffix(strings.ToLower(file), ".php")
}

// inventoryTechnologies converts go-enry DetectedLanguage output to Technology
// entries for merging into the pipeline accumulator. Version and status are
// left unset; stage 4 (version currency) fills those fields via MergeTechnologies.
func inventoryTechnologies(detected []workerdomain.DetectedLanguage) []*analysisdomain.Technology {
	techs := make([]*analysisdomain.Technology, 0, len(detected))
	for _, l := range detected {
		techs = append(techs, &analysisdomain.Technology{
			Name:     l.Name,
			Category: "language",
		})
	}
	return techs
}

// versionStatus computes the TechnologyStatus from the detected (installed)
// version and the latest published version.
//
// When detected is empty the source had no pinned version (property reference,
// constraint-only manifest, indeterminate lock entry). Claiming "Outdated" against
// an unknown baseline would be misleading, so Unspecified is returned instead.
func versionStatus(detected, latest string) analysisdomain.TechnologyStatus {
	if detected == "" || latest == "" {
		return analysisdomain.TechnologyStatusUnspecified
	}
	if compareVersions(detected, latest) >= 0 {
		return analysisdomain.TechnologyStatusCurrent
	}
	return analysisdomain.TechnologyStatusOutdated
}

// compareVersions compares two version strings numerically, component by
// component, after stripping a leading "v" prefix and any pre-release or
// build-metadata suffixes (e.g. "-jre", "+build.1").
//
// Returns -1 if a < b, 0 if equal, 1 if a > b.
// Handles common formats: "1.2.3", "v1.2.3", "33.2.0-jre", "v11.0.0".
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	if a == b {
		return 0
	}
	aParts := strings.SplitN(a, ".", 4)
	bParts := strings.SplitN(b, ".", 4)
	n := len(aParts)
	if len(bParts) > n {
		n = len(bParts)
	}
	for i := range n {
		ai := versionInt(aParts, i)
		bi := versionInt(bParts, i)
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

// injectToken embeds token as the userinfo component of an HTTPS URL so that
// git can authenticate against private repositories without a credential
// helper. Only HTTPS URLs are modified; ssh:// and others are returned as-is.
func injectToken(rawURL, token string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return rawURL
	}
	u.User = url.User(token)
	return u.String()
}

func versionInt(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	s := parts[i]
	if j := strings.IndexAny(s, "-+"); j != -1 {
		s = s[:j]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// boostManifestLanguage reorders technologies so that the language indicated by
// the most authoritative backend package manager appears first in the language
// entries. This corrects cases where vendored frontend assets (e.g. an admin
// template's JS libraries under assets/) inflate the file count of a secondary
// language above the actual backend language.
//
// Priority: Composer→PHP > PyPI→Python > Maven→Java > NuGet→C# > RubyGems→Ruby > npm→JavaScript.
// npm is lowest priority because JS can legitimately co-exist with any backend stack.
func boostManifestLanguage(technologies []*analysisdomain.Technology, deps []workerdomain.Dependency) []*analysisdomain.Technology {
	primary := ""
	for _, dep := range deps {
		switch dep.Ecosystem {
		case workerdomain.EcosystemComposer:
			primary = "PHP"
		case workerdomain.EcosystemPyPI:
			if primary == "" {
				primary = "Python"
			}
		case workerdomain.EcosystemMaven:
			if primary == "" {
				primary = "Java"
			}
		case workerdomain.EcosystemNuGet:
			if primary == "" {
				primary = "C#"
			}
		case workerdomain.EcosystemRubyGems:
			if primary == "" {
				primary = "Ruby"
			}
		case workerdomain.EcosystemNpm:
			if primary == "" {
				primary = "JavaScript"
			}
		}
	}
	if primary == "" {
		return technologies
	}
	result := make([]*analysisdomain.Technology, 0, len(technologies))
	var head *analysisdomain.Technology
	for _, t := range technologies {
		if t.GetCategory() == "language" && t.GetName() == primary {
			head = t
		} else {
			result = append(result, t)
		}
	}
	if head == nil {
		return technologies
	}
	return append([]*analysisdomain.Technology{head}, result...)
}

// injectInferredFramework adds a framework Technology when manifest parsing
// produced no framework entry but code-level markers indicate one. Runs at
// write time so every reader (GetAnalysisSummary, StackDetector, exports) sees
// the correct framework without each performing its own inference pass.
//
// Manifest-detected frameworks always win: the function is a no-op when
// technologies already contains a category="framework" entry.
//
// Current marker rules — evaluated in order, first match wins:
//   - Flask: Blueprint registrations in a Python codebase are exclusive to Flask;
//     no other mainstream Python web framework uses Blueprint() as an
//     architectural concept. The Python gate is load-bearing: the BlueprintInfo
//     type is reused by the Java and C# analyzers to model Spring/ASP.NET
//     controllers (the cross-language analogue of a Flask blueprint), so a bare
//     "blueprints exist" check would false-positive Flask on Spring/ASP.NET
//     repos. The gate is on the PRIMARY language being Python, not merely Python
//     being present: a Go-primary (or Java/C#-primary) repo that happens to carry
//     some Python tooling must not be labelled Flask. primaryLanguage is the
//     backend language resolved by primaryBackendLanguage (the same value the
//     intake gate and the panel's stack label use).
//
// To add a rule (e.g. FastAPI @app.get router decorators, Django apps.py):
// add a block below following the same pattern — check blueprints or cards,
// gate on the PRIMARY language, return the canonical Technology entry, and
// document the exclusive signal.
func injectInferredFramework(technologies []*analysisdomain.Technology, blueprints []*analysisdomain.BlueprintInfo, primaryLanguage string) []*analysisdomain.Technology {
	for _, t := range technologies {
		if t.GetCategory() == "framework" {
			return technologies // manifest detection wins
		}
	}
	// Flask: Blueprint registrations are a Flask-exclusive structural signal, but
	// only when Python is the PRIMARY language. The BlueprintInfo type is shared
	// across language analyzers (Spring/ASP.NET controllers also produce
	// blueprints), and a polyglot repo can detect Python without being a Python
	// app, so gating on "Python present" (hasLanguage) false-positives Flask on
	// Go/Java/C#-primary repos (the "GO·Flask" bug). Gate on the primary language.
	if len(blueprints) > 0 && strings.EqualFold(primaryLanguage, "Python") {
		return append(technologies, &analysisdomain.Technology{
			Name:     "Flask",
			Category: "framework",
			Slug:     "flask",
		})
	}
	return technologies
}

// manifestTechnologies converts ManifestParser output to Technology entries.
// Dependency.Category is used as-is; empty values default to "library".
// DisplayName and Slug from parsers are forwarded to the Technology.
// LatestVersion and Status are left unset; stage 4 fills them via MergeTechnologies.
// isTestModuleByName reports whether a module name belongs to the test suite by
// checking every path component. Matches whole components only — never substrings —
// so "backend.contestant" is not a false positive.
// Supports both dot separators (Python: "app.tests.models") and backslash
// separators (PHP: `BookStack\Tests\Feature\AuthTest`).
// Mirrors the logic in classifierIsTest (analysis worker) and isTestModule
// (decomposition worker); kept separate to avoid cross-layer imports.
func isTestModuleByName(module string) bool {
	// Split on either separator so PHP backslash namespaces are handled correctly.
	// PHP uses PascalCase components (e.g. "Tests"), Python uses lowercase ("tests"),
	// so comparisons are case-insensitive.
	parts := strings.FieldsFunc(module, func(r rune) bool { return r == '.' || r == '\\' })
	for _, part := range parts {
		low := strings.ToLower(part)
		if low == "tests" || low == "test" || low == "spec" {
			return true
		}
		if strings.HasPrefix(low, "test_") || strings.HasSuffix(low, "_test") {
			return true
		}
	}
	return false
}

func manifestTechnologies(deps []workerdomain.Dependency) []*analysisdomain.Technology {
	techs := make([]*analysisdomain.Technology, 0, len(deps))
	for _, d := range deps {
		category := d.Category
		if category == "" {
			category = "library"
		}
		name := d.Package
		if d.DisplayName != "" {
			name = d.DisplayName
		}
		techs = append(techs, &analysisdomain.Technology{
			Name:            name,
			DetectedVersion: d.Version,
			Category:        category,
			Slug:            d.Slug,
		})
	}
	return techs
}
