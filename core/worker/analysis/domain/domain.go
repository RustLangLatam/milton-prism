// Package domain contains types used internally by the analysis pipeline worker.
// These are the worker's own value types and are distinct from the proto-generated
// domain types in core/services/analysis/domain.
package domain

// JobPayload is the Asynq task payload for analysis:run jobs.
type JobPayload struct {
	SummaryID     uint64 `json:"summary_id"`
	RepositoryID  uint64 `json:"repository_id"`
	MigrationID   uint64 `json:"migration_id"`
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch"`
}

// Ecosystem identifies a dependency-manager package ecosystem.
type Ecosystem string

const (
	EcosystemMaven    Ecosystem = "Maven"
	EcosystemNpm      Ecosystem = "npm"
	EcosystemComposer Ecosystem = "Packagist"
	EcosystemPyPI     Ecosystem = "PyPI"
	EcosystemNuGet    Ecosystem = "NuGet"
	EcosystemRubyGems Ecosystem = "RubyGems"
)

// DetectedLanguage is a single language entry produced by the inventory stage.
type DetectedLanguage struct {
	Name  string
	Files uint64
	Lines uint64
}

// Dependency is a resolved package entry from a manifest or lockfile.
// Category classifies the dependency (framework, library, tool, …); parsers
// set it from manifest metadata when available, leaving it empty when unknown.
// The pipeline application layer defaults empty to "library" before merging.
//
// Approximate is true when the version was derived by stripping a constraint
// expression rather than reading a resolved lockfile. The vulnerability scanner
// uses this flag to mark results as tentative — the scan ran against a version
// that may not match what is actually installed.
//
// DisplayName and Slug are set by parsers when the package matches the
// framework catalog. manifestTechnologies uses them to produce the normalized
// Technology fields; Package is still the raw registry name used for version
// resolution and vulnerability scanning.
type Dependency struct {
	Ecosystem   Ecosystem
	Package     string
	Version     string
	Category    string
	Approximate bool
	DisplayName string // canonical display name (e.g. "Laravel"); empty for unknown
	Slug        string // stable machine identifier (e.g. "laravel"); empty for unknown
}

// VersionCurrency holds the latest-version result for a package.
type VersionCurrency struct {
	LatestVersion string
	// Status maps to analysisdomain.TechnologyStatus; kept as string to avoid
	// a cross-package import from the pure domain layer.
	Status string
}

// CloneError is returned by SourceAcquirer implementations when repository
// cloning fails with a classifiable error (auth failure, repo not found,
// network error). Message is safe for display to end users — no credentials,
// actionable guidance. Detail holds raw git stderr for operator logs; it is
// never forwarded to user-visible surfaces.
type CloneError struct {
	Message string // user-facing, credential-free
	Detail  string // raw git output, for internal logs only
}

func (e *CloneError) Error() string { return e.Message }

// BoundedContext is a candidate service boundary proposed by the semantic
// clustering stage.
type BoundedContext struct {
	Name             string
	Responsibilities []string
	Modules          []string
}

// RawImport is a single import statement extracted verbatim from a Python
// source file. No module resolution is performed at this stage; the raw form
// is what the resolver (Tarea 2) turns into internal/external edges.
//
// Examples:
//
//	import a.b.c         → Module="a.b.c" Names=["a.b.c"] IsRelative=false
//	import a.b.c as x    → Module="a.b.c" Names=["x"]     IsRelative=false
//	from a.b import c, d → Module="a.b"   Names=["c","d"] IsRelative=false
//	from . import x      → Module=""      Names=["x"]     IsRelative=true  RelativeLevel=1
//	from ..pkg import y  → Module="pkg"   Names=["y"]     IsRelative=true  RelativeLevel=2
//
// Known limitation: dynamic imports via importlib.import_module() or
// __import__() are not detected — they cannot be resolved statically.
type RawImport struct {
	ImportingFile string
	Module        string
	IsRelative    bool
	RelativeLevel int
	Names         []string
}

// BlueprintInfo holds Flask Blueprint metadata extracted from a Python
// workspace. Blueprint() definitions and register_blueprint() calls are
// correlated by variable name (best-effort; cross-file aliasing is not tracked).
type BlueprintInfo struct {
	Name      string // first string argument of Blueprint(name, ...)
	File      string // relative workspace path of the file containing Blueprint()
	URLPrefix string // url_prefix= from register_blueprint(), empty if not found
}

// ResolvedImport is an internal Python module edge produced by the module
// resolver (Tarea 2). Only intra-repo imports appear here; external
// dependencies (stdlib, third-party) are discarded — they are already
// captured by the dependency manifest parser in Tier 1.
type ResolvedImport struct {
	FromModule string // dotted module name of the importing file
	ToModule   string // deepest dotted module name found in the workspace
}
