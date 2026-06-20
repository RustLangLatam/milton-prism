package adapters

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// pyModuleMap is the internal representation of all Python modules discovered
// in a workspace. It supports O(1) internal-or-external checks and file→module
// lookups needed for relative-import resolution.
type pyModuleMap struct {
	// fileToModule maps a file path relative to the workspace root to its
	// canonical dotted Python module name (e.g. "backend/utils.py" → "backend.utils").
	fileToModule map[string]string
	// modules is the set of all known internal dotted module names, including
	// both explicit __init__.py packages and namespace-package ancestors.
	// Contains both canonical names and alias names from subdir import roots.
	modules map[string]bool
	// isPackage marks module names that correspond to __init__.py files.
	// Required to compute the correct relative-import base for package init files,
	// where the file IS the package, not a module inside the package.
	isPackage map[string]bool
	// aliases maps a shorter alias module name to its canonical module name.
	// Populated when a subdirectory is an import root: "funcs" → "backend.funcs".
	aliases map[string]string
	// ambiguousAlias records aliases that were claimed by two or more different
	// canonical modules (e.g. both app/funcs.py and backend/funcs.py claim "funcs").
	// Ambiguous aliases are suppressed during resolution: a bare import whose only
	// match is an ambiguous alias produces no edge rather than a wrong edge.
	ambiguousAlias map[string]bool
}

// PythonModuleResolver resolves Python RawImport values into ResolvedImport
// edges by classifying each import as internal (in-repo) or external
// (stdlib / third-party). Only internal edges are emitted; external imports
// are already captured by the manifest dependency parser in Tier 1.
//
// Import-root detection:
//   - Workspace root is always an import root.
//   - If a src/ directory exists at the workspace root, it is treated as a
//     priority import root (src-layout; PEP 517/518 convention). Files
//     under src/ get shorter canonical names (e.g. "service.utils").
//   - Common subdirectory layouts (app/, backend/, lib/) and directories
//     containing entry-point files (app.py, main.py, wsgi.py, …) are treated
//     as alias roots: each file is registered under its canonical workspace-
//     rooted name AND a shorter alias so that bare imports (from funcs import X)
//     resolve to the same graph node as prefixed imports (from backend.funcs import X).
//
// Namespace packages (PEP 420) are supported: ancestor directories that lack
// __init__.py are registered as implicit packages so that bare `import pkg`
// resolves correctly even when pkg has no init file.
type PythonModuleResolver struct {
	modMap *pyModuleMap
}

// NewPythonModuleResolver builds the module map for the given workspace and
// returns a resolver ready for use.
func NewPythonModuleResolver(workspacePath string) (*PythonModuleResolver, error) {
	m, err := buildPyModuleMap(workspacePath)
	if err != nil {
		return nil, err
	}
	return &PythonModuleResolver{modMap: m}, nil
}

// ModuleName returns the dotted module name for workspacePath-relative relPath,
// using the same import-root detection as BuildGraphEdges. Falls back to the
// path-to-module heuristic when the file is not in the map.
func (r *PythonModuleResolver) ModuleName(relPath string) string {
	if m, ok := r.modMap.fileToModule[relPath]; ok {
		return m
	}
	m, _ := pathToPyModule(filepath.ToSlash(relPath))
	return m
}

// Resolve classifies each RawImport as internal or external and returns only
// the internal edges as ResolvedImport pairs. Duplicate (from, to) edges are
// collapsed; edges where from == to are discarded.
func (r *PythonModuleResolver) Resolve(rawImports []workerdomain.RawImport) []workerdomain.ResolvedImport {
	seen := make(map[[2]string]bool)
	var out []workerdomain.ResolvedImport

	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		k := [2]string{from, to}
		if !seen[k] {
			seen[k] = true
			out = append(out, workerdomain.ResolvedImport{FromModule: from, ToModule: to})
		}
	}

	for _, imp := range rawImports {
		fromModule, ok := r.modMap.fileToModule[imp.ImportingFile]
		if !ok {
			continue
		}

		if imp.IsRelative {
			base := r.relativeBase(imp, fromModule)
			if base == "" {
				continue
			}
			for _, name := range imp.Names {
				candidate := base + "." + name
				if r.modMap.modules[candidate] {
					add(fromModule, r.canonicalize(candidate))
				} else {
					// The name is not a sub-module (it's a class/function); the
					// edge still points to the base package.
					add(fromModule, r.canonicalize(base))
				}
			}
		} else {
			for _, name := range imp.Names {
				if to := r.resolveAbsolute(imp.Module, name); to != "" {
					add(fromModule, to)
				}
				// No else: external imports are silently discarded.
			}
		}
	}
	return out
}

// resolveAbsolute returns the deepest internal module name that matches the
// import, or "" if the import is external. Alias names are normalized to their
// canonical form so that bare imports ("funcs") and prefixed imports
// ("backend.funcs") produce edges to the same graph node.
//
// For `from x.y import z` the search order is: "x.y.z" → "x.y" → "x".
// For `import x.y.z` (where module==name) the search order is: "x.y.z" → "x.y" → "x".
// For `import x.y.z as alias` (module != name) the first candidate is
// "x.y.z.alias" (never found in practice), then "x.y.z" → "x.y" → "x".
func (r *PythonModuleResolver) resolveAbsolute(module, name string) string {
	var candidates []string

	// `from x.y import z` — try x.y.z first (z might be a sub-module).
	if name != "" && name != module {
		candidates = append(candidates, module+"."+name)
	}

	// Walk from deepest module component to shallowest.
	parts := strings.Split(module, ".")
	for i := len(parts); i > 0; i-- {
		candidates = append(candidates, strings.Join(parts[:i], "."))
	}

	for _, c := range candidates {
		if r.modMap.modules[c] {
			if canon := r.canonicalize(c); canon != "" {
				return canon
			}
			// Ambiguous alias: skip this candidate and try shallower forms.
		}
	}
	return ""
}

// canonicalize returns the canonical module name for c, or "" if c is an
// ambiguous alias (claimed by two or more distinct canonical modules). An
// empty return from here causes resolveAbsolute to discard the candidate
// rather than emit a wrong edge.
func (r *PythonModuleResolver) canonicalize(c string) string {
	if r.modMap.ambiguousAlias[c] {
		return ""
	}
	if canon, ok := r.modMap.aliases[c]; ok {
		return canon
	}
	return c
}

// relativeBase computes the dotted base module for a relative import.
//
//	from .   import x   (level=1, module="")     → current package
//	from ..  import x   (level=2, module="")     → parent package
//	from .m  import x   (level=1, module="m")    → current package + ".m"
//	from ..m import x   (level=2, module="m")    → parent package + ".m"
//
// Special case: if fromModule is a package init (__init__.py), the file IS
// the package, so level=1 means the package itself rather than its parent.
func (r *PythonModuleResolver) relativeBase(imp workerdomain.RawImport, fromModule string) string {
	var pkgParts []string

	if r.modMap.isPackage[fromModule] {
		// __init__.py: the module name is the package.
		pkgParts = strings.Split(fromModule, ".")
	} else {
		parts := strings.Split(fromModule, ".")
		if len(parts) <= 1 {
			// File at the workspace root with no package ancestry.
			pkgParts = nil
		} else {
			pkgParts = parts[:len(parts)-1]
		}
	}

	// Go up (RelativeLevel - 1) additional levels.
	upLevels := imp.RelativeLevel - 1
	if upLevels >= len(pkgParts) {
		pkgParts = nil
	} else if upLevels > 0 {
		pkgParts = pkgParts[:len(pkgParts)-upLevels]
	}

	if imp.Module != "" {
		modParts := strings.Split(imp.Module, ".")
		combined := make([]string, 0, len(pkgParts)+len(modParts))
		combined = append(combined, pkgParts...)
		combined = append(combined, modParts...)
		pkgParts = combined
	}

	return strings.Join(pkgParts, ".")
}

// BuildGraphEdges counts the number of import references per internal (from, to)
// module pair. Unlike Resolve, duplicates are NOT suppressed: each Name within
// a RawImport that resolves to the same pair increments the weight counter.
// This is used by the dependency graph builder (stage 6) to compute coupling
// strength as edge weight.
func (r *PythonModuleResolver) BuildGraphEdges(rawImports []workerdomain.RawImport) map[[2]string]uint32 {
	weights := make(map[[2]string]uint32)

	count := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		weights[[2]string{from, to}]++
	}

	for _, imp := range rawImports {
		fromModule, ok := r.modMap.fileToModule[imp.ImportingFile]
		if !ok {
			continue
		}
		if imp.IsRelative {
			base := r.relativeBase(imp, fromModule)
			if base == "" {
				continue
			}
			for _, name := range imp.Names {
				candidate := base + "." + name
				if r.modMap.modules[candidate] {
					count(fromModule, r.canonicalize(candidate))
				} else {
					count(fromModule, r.canonicalize(base))
				}
			}
		} else {
			for _, name := range imp.Names {
				if to := r.resolveAbsolute(imp.Module, name); to != "" {
					count(fromModule, to)
				}
			}
		}
	}
	return weights
}

// ── module map construction ───────────────────────────────────────────────────

func buildPyModuleMap(workspacePath string) (*pyModuleMap, error) {
	m := &pyModuleMap{
		fileToModule:   make(map[string]string),
		modules:        make(map[string]bool),
		isPackage:      make(map[string]bool),
		aliases:        make(map[string]string),
		ambiguousAlias: make(map[string]bool),
	}

	roots := detectPyImportRoots(workspacePath)
	processed := make(map[string]bool)

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Dangling symlinks or unreadable entries are skipped silently.
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() || !strings.HasSuffix(path, ".py") {
				return nil
			}

			relFromRoot, _ := filepath.Rel(root, path)
			relFromWorkspace, _ := filepath.Rel(workspacePath, path)

			modName, isPkg := pathToPyModule(relFromRoot)
			if modName == "" {
				return nil
			}

			if !processed[relFromWorkspace] {
				// Canonical registration: this root (priority or workspace) owns
				// the authoritative module name for this file.
				processed[relFromWorkspace] = true
				m.fileToModule[relFromWorkspace] = modName
				m.modules[modName] = true
				if isPkg {
					m.isPackage[modName] = true
				}
				addNamespaceAncestors(m.modules, modName)
			} else {
				// File already registered canonically. Register an alias only when
				// modName is strictly shorter (fewer dotted components) than the
				// canonical name — meaning this is an alias root giving a bare name
				// to a file whose canonical name is prefixed (e.g. "funcs" for
				// "backend.funcs"). Longer alternative paths (e.g. "src.pkg" for
				// canonical "pkg") are redundant and skipped.
				canonName := m.fileToModule[relFromWorkspace]
				if strings.Count(modName, ".")+1 < strings.Count(canonName, ".")+1 {
					addAlias(m, modName, canonName)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// addNamespaceAncestors registers ancestor directories as implicit namespace
// packages so that bare `import pkg` resolves when pkg has no __init__.py.
func addNamespaceAncestors(modules map[string]bool, modName string) {
	parts := strings.Split(modName, ".")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], ".")
		if !modules[ancestor] {
			modules[ancestor] = true
		}
	}
}

// addAlias registers alias (a shorter module name) as pointing to canonical and
// registers ancestors of alias as aliases of the corresponding canonical ancestors.
// e.g. alias="tests.foo", canonical="backend.tests.foo" also registers "tests"→"backend.tests".
//
// If alias was already registered by a different canonical (collision), the alias
// is marked ambiguous: resolution will discard it rather than emit a wrong edge.
func addAlias(m *pyModuleMap, alias, canonical string) {
	if existing, ok := m.aliases[alias]; ok {
		// Second claim on the same short name from a different directory.
		// Mark ambiguous so neither claimant produces wrong edges.
		if existing != canonical {
			m.ambiguousAlias[alias] = true
		}
		return
	}
	if m.modules[alias] {
		return // canonical module owns this name; don't override
	}
	m.modules[alias] = true
	m.aliases[alias] = canonical

	aliasParts := strings.Split(alias, ".")
	canonParts := strings.Split(canonical, ".")
	// offset = extra leading components canonical has vs. alias.
	// e.g. alias="tests.foo"(2), canonical="backend.tests.foo"(3) → offset=1
	offset := len(canonParts) - len(aliasParts)
	for i := 1; i < len(aliasParts); i++ {
		ancestorAlias := strings.Join(aliasParts[:i], ".")
		ancestorCanon := strings.Join(canonParts[:offset+i], ".")
		if !m.modules[ancestorAlias] {
			m.modules[ancestorAlias] = true
			m.aliases[ancestorAlias] = ancestorCanon
		}
	}
}

// detectPyImportRoots returns the import roots for the workspace in priority order.
//
// Order:
//  1. src/ — priority root for src-layout projects; files get short canonical names.
//  2. Workspace root — canonical root for all remaining files.
//  3. Alias roots: common subdirectory names (app/, backend/, lib/) and
//     directories containing entry-point files (app.py, main.py, wsgi.py, …).
//     These are walked after the workspace root so canonical names are already
//     registered; walking them adds short-name aliases for bare imports.
func detectPyImportRoots(workspacePath string) []string {
	var priorityRoots []string
	srcDir := filepath.Join(workspacePath, "src")
	if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
		priorityRoots = append(priorityRoots, srcDir)
	}

	aliasRootSet := make(map[string]bool)
	aliasRootSet[workspacePath] = true
	for _, r := range priorityRoots {
		aliasRootSet[r] = true
	}

	var aliasRoots []string
	for _, name := range []string{"app", "backend", "lib"} {
		dir := filepath.Join(workspacePath, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() && !aliasRootSet[dir] && dirContainsPyFile(dir) {
			aliasRootSet[dir] = true
			aliasRoots = append(aliasRoots, dir)
		}
	}

	// Entry-point signals: subdirectories (up to 2 levels deep) containing a
	// well-known entry-point file were likely on sys.path at runtime.
	entryPoints := map[string]bool{
		"app.py": true, "main.py": true, "wsgi.py": true, "asgi.py": true,
		"manage.py": true, "__main__.py": true, "run.py": true, "server.py": true,
	}
	_ = filepath.WalkDir(workspacePath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			return nil
		}
		if strings.Count(rel, string(filepath.Separator)) > 1 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && entryPoints[filepath.Base(path)] {
			dir := filepath.Dir(path)
			if !aliasRootSet[dir] {
				aliasRootSet[dir] = true
				aliasRoots = append(aliasRoots, dir)
			}
		}
		return nil
	})

	result := make([]string, 0, 1+len(priorityRoots)+len(aliasRoots))
	result = append(result, priorityRoots...)
	result = append(result, workspacePath)
	result = append(result, aliasRoots...)
	return result
}

// dirContainsPyFile reports whether dir contains at least one .py file at any
// depth. Stops at the first match.
func dirContainsPyFile(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".py") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// pathToPyModule converts a file path relative to an import root into a dotted
// Python module name and reports whether the file is a package init (__init__.py).
//
//	"foo.py"              → ("foo",       false)
//	"a/b/c.py"            → ("a.b.c",    false)
//	"a/b/__init__.py"     → ("a.b",      true)
//	"__init__.py"         → ("",         false)   root init — skip
func pathToPyModule(relPath string) (string, bool) {
	relPath = filepath.ToSlash(relPath)

	const initSuffix = "/__init__.py"
	const initFile = "__init__.py"

	switch {
	case relPath == initFile:
		return "", false
	case strings.HasSuffix(relPath, initSuffix):
		pkg := relPath[:len(relPath)-len(initSuffix)]
		return strings.ReplaceAll(pkg, "/", "."), true
	default:
		noExt := relPath[:len(relPath)-3] // strip .py
		return strings.ReplaceAll(noExt, "/", "."), false
	}
}
