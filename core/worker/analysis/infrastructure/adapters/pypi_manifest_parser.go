package adapters

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.ManifestParser = (*PyPIManifestParser)(nil)

// PyPIManifestParser implements ports.ManifestParser for the PyPI ecosystem.
//
// Detection strategy (in preference order):
//  1. poetry.lock  — resolved, pinned versions; packages marked groups=["dev"]
//     only are excluded.
//  2. pyproject.toml — [tool.poetry.dependencies] declared constraints; python
//     platform entry skipped.
//  3. requirements.txt — bare version specifiers; non-semver specifiers
//     (-r, --index-url, comments) skipped; version ranges stripped to lower
//     bound (same "version indeterminate" caveat as Maven property references).
//
// Like the other parsers, LatestVersion and Status are left unset for stage 4.
type PyPIManifestParser struct{}

// NewPyPIManifestParser returns a new PyPIManifestParser.
func NewPyPIManifestParser() *PyPIManifestParser {
	return &PyPIManifestParser{}
}

func (p *PyPIManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	var all []workerdomain.Dependency

	// poetry.lock: fully resolved lockfile — highest specificity (Approximate=false).
	if path := filepath.Join(workspacePath, "poetry.lock"); fileExists(path) {
		deps, err := parsePoetryLock(path)
		if err != nil {
			return nil, err
		}
		all = mergePyPIDeps(all, deps)
	}
	// pyproject.toml: declared constraints; lower specificity than a lockfile.
	if path := filepath.Join(workspacePath, "pyproject.toml"); fileExists(path) {
		deps, err := parsePyproject(path)
		if err != nil {
			return nil, err
		}
		all = mergePyPIDeps(all, deps)
	}
	// Pipfile: often uses "*" wildcards; processed before requirements.txt so
	// that requirements-pinned versions can override wildcards in the merge step.
	if path := filepath.Join(workspacePath, "Pipfile"); fileExists(path) {
		deps, err := parsePipfile(path)
		if err != nil {
			return nil, err
		}
		all = mergePyPIDeps(all, deps)
	}
	// requirements.txt (and any files it includes via -r): may contain exact
	// ==pins that override wildcard entries from other sources.
	if path := filepath.Join(workspacePath, "requirements.txt"); fileExists(path) {
		deps, err := parseRequirementsTxt(path)
		if err != nil {
			return nil, err
		}
		all = mergePyPIDeps(all, deps)
	}

	return all, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ── poetry.lock parsing ───────────────────────────────────────────────────────

// poetryLock represents the [[package]] array in poetry.lock (TOML format).
// The Groups field is present in lock-version 2.0 (Poetry ≥ 1.2). Older locks
// omit it; in that case all packages are included (dev cannot be distinguished).
type poetryLock struct {
	Package []poetryPkg `toml:"package"`
}

type poetryPkg struct {
	Name    string   `toml:"name"`
	Version string   `toml:"version"`
	Groups  []string `toml:"groups"`
}

func parsePoetryLock(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock poetryLock
	if _, err := toml.Decode(string(data), &lock); err != nil {
		return nil, err
	}

	deps := make([]workerdomain.Dependency, 0, len(lock.Package))
	for _, pkg := range lock.Package {
		if isDevOnlyPoetryPkg(pkg.Groups) {
			continue
		}
		cat, display, slug := pypiEntryFor(pkg.Name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemPyPI,
			Package:     pkg.Name,
			Version:     pkg.Version,
			Category:    cat,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// isDevOnlyPoetryPkg returns true when the package appears only in the dev
// group, meaning it is not needed in production. A package with no groups
// listed (older lock format) is assumed to be a production dependency.
func isDevOnlyPoetryPkg(groups []string) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if g != "dev" {
			return false
		}
	}
	return true
}

// ── pyproject.toml parsing ────────────────────────────────────────────────────

type pyprojectTOML struct {
	Tool struct {
		Poetry struct {
			Dependencies map[string]any `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

func parsePyproject(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var proj pyprojectTOML
	if _, err := toml.Decode(string(data), &proj); err != nil {
		return nil, err
	}

	deps := make([]workerdomain.Dependency, 0)
	for name, raw := range proj.Tool.Poetry.Dependencies {
		// "python" is a platform specifier, not a real package.
		if strings.EqualFold(name, "python") {
			continue
		}
		version := ""
		if s, ok := raw.(string); ok {
			version = stripPythonConstraint(s)
		}
		cat, display, slug := pypiEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemPyPI,
			Package:     name,
			Version:     version,
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// ── requirements.txt parsing ──────────────────────────────────────────────────

// maxRequirementsDepth caps -r include recursion to prevent infinite loops.
const maxRequirementsDepth = 10

// parseRequirementsTxt reads path and follows -r / --requirement include
// directives recursively. Cycle detection prevents infinite loops.
func parseRequirementsTxt(path string) ([]workerdomain.Dependency, error) {
	return parseRequirementsTxtInner(path, 0, make(map[string]bool))
}

func parseRequirementsTxtInner(path string, depth int, seen map[string]bool) ([]workerdomain.Dependency, error) {
	if depth > maxRequirementsDepth {
		return nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if seen[abs] {
		return nil, nil // cycle guard
	}
	seen[abs] = true

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []workerdomain.Dependency
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Follow -r / --requirement includes before the generic flag filter so
		// that includes are resolved and not silently discarded.
		if ref, ok := extractIncludePath(line); ok {
			incPath := ref
			if !filepath.IsAbs(ref) {
				incPath = filepath.Join(filepath.Dir(path), ref)
			}
			subdeps, incErr := parseRequirementsTxtInner(incPath, depth+1, seen)
			if incErr != nil {
				applog.Warningf("pypi parser: skip include %s: %v", ref, incErr)
				continue
			}
			deps = append(deps, subdeps...)
			continue
		}
		// Skip all other flag lines (--index-url, --extra-index-url, -c, etc.).
		if strings.HasPrefix(line, "-") {
			continue
		}
		name, version := splitRequirement(line)
		if name == "" {
			continue
		}
		cat, display, slug := pypiEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemPyPI,
			Package:     name,
			Version:     version,
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, scanner.Err()
}

// splitRequirement parses one requirements.txt line such as "flask==2.3.3"
// or "requests>=2.28.0,<3.0". Returns (name, version).
func splitRequirement(line string) (name, version string) {
	// Strip inline comments.
	if i := strings.Index(line, " #"); i != -1 {
		line = strings.TrimSpace(line[:i])
	}
	// Extras: "requests[security]>=2.28" → name="requests"
	line = strings.ReplaceAll(line, " ", "")
	i := strings.IndexAny(line, ">=<!~[;@")
	if i == -1 {
		// Bare package name, no version specifier.
		return line, ""
	}
	name = line[:i]
	// Strip extras bracket "requests[security]" → "requests"
	if j := strings.Index(name, "["); j != -1 {
		name = name[:j]
	}
	version = stripPythonConstraint(line[i:])
	return name, version
}

// ── Pipfile parsing ───────────────────────────────────────────────────────────

// pipfilePackageSpec represents a single package entry in a Pipfile.
// Values may be a plain version string ">=1.0" or a table {version=">=1.0", ...}.
// TOML decoding uses interface{} so both forms decode transparently.
type pipfileData struct {
	Packages    map[string]any `toml:"packages"`
	DevPackages map[string]any `toml:"dev-packages"`
}

func parsePipfile(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf pipfileData
	if _, err := toml.Decode(string(data), &pf); err != nil {
		return nil, err
	}
	deps := make([]workerdomain.Dependency, 0, len(pf.Packages))
	for name, raw := range pf.Packages {
		if strings.EqualFold(name, "python") {
			continue
		}
		version := ""
		switch v := raw.(type) {
		case string:
			if v != "*" {
				version = stripPythonConstraint(v)
			}
		case map[string]any:
			if s, ok := v["version"].(string); ok && s != "*" {
				version = stripPythonConstraint(s)
			}
		}
		cat, display, slug := pypiEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemPyPI,
			Package:     name,
			Version:     version,
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// stripPythonConstraint converts a Python version specifier to a bare version.
//
//	"==2.3.3"      → "2.3.3"
//	">=2.0.0,<3.0" → "2.0.0"   (lower bound; range cannot be collapsed further)
//	"~=5.3.0"      → "5.3.0"
//	"^4.2"         → "4.2"      (Poetry caret)
//	"*"            → ""
func stripPythonConstraint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return ""
	}
	// Take the first specifier before a comma (compound constraints).
	if i := strings.Index(s, ","); i != -1 {
		s = s[:i]
	}
	// Strip operator prefix characters.
	s = strings.TrimLeft(s, ">=<!~^v ")
	if s == "" || s == "*" {
		return ""
	}
	return s
}

// extractIncludePath detects pip's -r / --requirement directives and returns
// (path, true) when found. All other flag lines return ("", false).
func extractIncludePath(line string) (string, bool) {
	for _, prefix := range []string{"-r ", "--requirement ", "--requirement="} {
		if strings.HasPrefix(line, prefix) {
			ref := strings.TrimSpace(line[len(prefix):])
			// Strip trailing inline comment.
			if i := strings.Index(ref, " #"); i != -1 {
				ref = strings.TrimSpace(ref[:i])
			}
			if ref != "" {
				return ref, true
			}
		}
	}
	return "", false
}

// normalizePyPIName applies PyPI's canonical name normalization so that
// "Flask-SQLAlchemy", "Flask_SQLAlchemy", and "flask.sqlalchemy" all map to
// the same key during cross-source merging (PEP 503 / PyPI convention).
func normalizePyPIName(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(name))
}

// mergePyPIDeps merges incoming into existing by normalised package name.
//
// Specificity rules applied in priority order:
//  1. A non-Approximate entry (resolved lockfile) is never overwritten.
//  2. A non-empty incoming version overwrites an empty existing version.
//  3. An incoming non-Approximate version overwrites any Approximate one.
//  4. All other cases: the existing entry is preserved (first specific binding wins).
//
// Package names are preserved from whichever source first introduced the entry.
// New packages not yet in existing are always appended.
func mergePyPIDeps(existing, incoming []workerdomain.Dependency) []workerdomain.Dependency {
	result := make([]workerdomain.Dependency, len(existing))
	copy(result, existing)

	index := make(map[string]int, len(result))
	for i, d := range result {
		index[normalizePyPIName(d.Package)] = i
	}

	for _, inc := range incoming {
		key := normalizePyPIName(inc.Package)
		i, found := index[key]
		if !found {
			index[key] = len(result)
			result = append(result, inc)
			continue
		}
		ex := &result[i]
		if !ex.Approximate {
			continue // lock-file entry is the absolute truth
		}
		if inc.Version != "" && ex.Version == "" {
			// Non-empty beats empty regardless of Approximate level.
			ex.Version = inc.Version
			ex.Approximate = inc.Approximate
		} else if !inc.Approximate && ex.Approximate && inc.Version != "" {
			// Resolved lock overrides any constraint.
			ex.Version = inc.Version
			ex.Approximate = false
		}
	}
	return result
}

// pypiEntryFor returns (category, displayName, slug) for a PyPI package name.
// Delegates to the framework catalog; unknown packages get category "library".
func pypiEntryFor(name string) (category, displayName, slug string) {
	if e, ok := frameworkEntryForPkg(name); ok {
		return "framework", e.DisplayName, e.Slug
	}
	return "library", "", ""
}
