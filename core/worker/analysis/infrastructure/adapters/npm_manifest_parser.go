package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ManifestParser = (*NpmManifestParser)(nil)

// NpmManifestParser implements ports.ManifestParser for the npm (npmjs.org)
// ecosystem. It covers Node.js projects regardless of package manager.
//
// Detection strategy:
//   - Prefers package-lock.json (resolved, pinned versions). Supports both
//     lockfileVersion 1 (dependencies map) and 2/3 (packages map).
//   - Falls back to package.json (declared constraints) when no lock exists.
//
// devDependencies are always excluded. The full package tree from the lock
// (direct + transitive) is included, which maximises vulnerability coverage
// in stage 4.
type NpmManifestParser struct{}

// NewNpmManifestParser returns a new NpmManifestParser.
func NewNpmManifestParser() *NpmManifestParser {
	return &NpmManifestParser{}
}

func (p *NpmManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	lockPath := filepath.Join(workspacePath, "package-lock.json")
	if _, err := os.Stat(lockPath); err == nil {
		return parsePackageLock(lockPath)
	}
	pkgPath := filepath.Join(workspacePath, "package.json")
	if _, err := os.Stat(pkgPath); err == nil {
		return parsePackageJSON(pkgPath)
	}
	return nil, nil
}

// ── package-lock.json parsing ─────────────────────────────────────────────────

type rawPackageLock struct {
	LockfileVersion int                   `json:"lockfileVersion"`
	Packages        map[string]lockPkgV2  `json:"packages"`    // v2/v3
	Dependencies    map[string]lockDepV1  `json:"dependencies"` // v1
}

type lockPkgV2 struct {
	Version string `json:"version"`
	Dev     bool   `json:"dev"`
}

type lockDepV1 struct {
	Version string `json:"version"`
	Dev     bool   `json:"dev"`
}

func parsePackageLock(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawPackageLock
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.LockfileVersion >= 2 && raw.Packages != nil {
		return convertLockV2(raw.Packages), nil
	}
	return convertLockV1(raw.Dependencies), nil
}

func convertLockV2(pkgs map[string]lockPkgV2) []workerdomain.Dependency {
	deps := make([]workerdomain.Dependency, 0, len(pkgs))
	for key, pkg := range pkgs {
		// "" is the root workspace entry; skip it.
		if key == "" || pkg.Dev {
			continue
		}
		// Keys are always "node_modules/<name>" (including scoped: "node_modules/@scope/pkg").
		name := strings.TrimPrefix(key, "node_modules/")
		cat, display, slug := npmEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemNpm,
			Package:     name,
			Version:     pkg.Version,
			Category:    cat,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps
}

func convertLockV1(deps map[string]lockDepV1) []workerdomain.Dependency {
	result := make([]workerdomain.Dependency, 0, len(deps))
	for name, dep := range deps {
		if dep.Dev {
			continue
		}
		cat, display, slug := npmEntryFor(name)
		result = append(result, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemNpm,
			Package:     name,
			Version:     dep.Version,
			Category:    cat,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return result
}

// ── package.json fallback ─────────────────────────────────────────────────────

type rawPackageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func parsePackageJSON(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pkg rawPackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	deps := make([]workerdomain.Dependency, 0, len(pkg.Dependencies))
	for name, constraint := range pkg.Dependencies {
		cat, display, slug := npmEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemNpm,
			Package:     name,
			Version:     stripNpmConstraint(constraint),
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// npmEntryFor returns (category, displayName, slug) for an npm package name.
// Delegates to the framework catalog; unknown packages get category "library".
func npmEntryFor(name string) (category, displayName, slug string) {
	if e, ok := frameworkEntryForPkg(name); ok {
		return "framework", e.DisplayName, e.Slug
	}
	return "library", "", ""
}

// stripNpmConstraint removes leading semver range operators so that "^18.2.0"
// becomes "18.2.0". Non-semver values (git URLs, "latest", "*") become "".
func stripNpmConstraint(s string) string {
	s = strings.TrimSpace(s)
	// Non-semver specifiers: git URLs, file paths, tags.
	if strings.HasPrefix(s, "git") ||
		strings.HasPrefix(s, "file:") ||
		s == "*" || s == "latest" || s == "" {
		return ""
	}
	s = strings.TrimLeft(s, "^~>=!<v ")
	// Compound ranges (">=1.0.0 <2.0.0"): take the lower bound.
	if i := strings.IndexAny(s, " |"); i != -1 {
		s = s[:i]
	}
	return s
}
