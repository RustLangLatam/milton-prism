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

var _ ports.ManifestParser = (*ComposerManifestParser)(nil)

// ComposerManifestParser implements ports.ManifestParser for the Composer
// (Packagist) ecosystem. It covers both Laravel and Symfony projects.
//
// Detection strategy:
//   - Prefers composer.lock (resolved, pinned versions) when present.
//   - Falls back to composer.json (declared constraints) when no lock exists.
//
// Only production dependencies from the "packages" section of the lock are
// parsed; "packages-dev" is excluded. Platform requirements (php, ext-*,
// lib-*) and meta-package types are skipped.
type ComposerManifestParser struct{}

// NewComposerManifestParser returns a new ComposerManifestParser.
func NewComposerManifestParser() *ComposerManifestParser {
	return &ComposerManifestParser{}
}

func (p *ComposerManifestParser) Parse(ctx context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	lockPath := filepath.Join(workspacePath, "composer.lock")
	if _, err := os.Stat(lockPath); err == nil {
		return parseLock(lockPath)
	}
	jsonPath := filepath.Join(workspacePath, "composer.json")
	if _, err := os.Stat(jsonPath); err == nil {
		return parseManifest(jsonPath)
	}
	return nil, nil
}

// ── lock file parsing ─────────────────────────────────────────────────────────

type composerLock struct {
	Packages    []composerPkg `json:"packages"`
	PackagesDev []composerPkg `json:"packages-dev"`
}

type composerPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

func parseLock(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock composerLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	return convertLockPackages(lock.Packages), nil
}

func convertLockPackages(pkgs []composerPkg) []workerdomain.Dependency {
	deps := make([]workerdomain.Dependency, 0, len(pkgs))
	for _, pkg := range pkgs {
		if skipLockPkg(pkg) {
			continue
		}
		cat, display, slug := composerEntryFor(pkg.Name, pkg.Type)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemComposer,
			Package:     pkg.Name,
			Version:     pkg.Version,
			Category:    cat,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps
}

func skipLockPkg(pkg composerPkg) bool {
	switch pkg.Type {
	case "project", "composer-plugin", "metapackage":
		return true
	}
	return false
}

// ── composer.json fallback ────────────────────────────────────────────────────

type composerManifest struct {
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
}

func parseManifest(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m composerManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	deps := make([]workerdomain.Dependency, 0, len(m.Require))
	for name, constraint := range m.Require {
		if isPlatformReq(name) {
			continue
		}
		cat, display, slug := composerEntryFor(name, "")
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemComposer,
			Package:     name,
			Version:     stripConstraint(constraint),
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// composerEntryFor resolves category, display name, and slug for a Composer
// package. Priority: (1) framework catalog, (2) Composer type field, (3) library.
func composerEntryFor(name, pkgType string) (category, displayName, slug string) {
	if e, ok := frameworkEntryForPkg(name); ok {
		return "framework", e.DisplayName, e.Slug
	}
	if pkgType == "framework" {
		return "framework", "", ""
	}
	return "library", "", ""
}

// isPlatformReq returns true for php, ext-*, and lib-* entries that represent
// platform requirements rather than real packages.
func isPlatformReq(name string) bool {
	return name == "php" ||
		strings.HasPrefix(name, "ext-") ||
		strings.HasPrefix(name, "lib-")
}

// stripConstraint removes leading version-constraint operators so that a
// declared constraint like "^11.0" becomes "11.0". The version resolver stage
// will later fetch the latest published version regardless.
func stripConstraint(s string) string {
	s = strings.TrimLeft(s, "^~>=!<v ")
	// In compound constraints (">=7.4 <9.0"), take the first token.
	if i := strings.IndexAny(s, " ,|"); i != -1 {
		s = s[:i]
	}
	if s == "" || s == "*" {
		return ""
	}
	return s
}
