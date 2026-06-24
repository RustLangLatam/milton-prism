package adapters

import (
	"os"
	"path/filepath"
	"strings"
)

// GoModuleResolver classifies Go import paths as internal (intra-repo) or
// external using the module path declared in the workspace's root go.mod.
//
// Go's package graph is deterministic: the canonical node identity is the
// package import path, which equals <modulePath>/<dir-relative-to-module-root>.
// Every .go file in a directory shares that import path, so the graph node is
// the directory's import path (NOT the file). Standard-library imports
// (fmt, net/http) and third-party imports (github.com/...) never have the
// module prefix and so are discarded — the graph is intra-repo coupling only,
// which is the Louvain input. They remain available pre-discard for framework
// detection.
//
// Known limitation: multi-module monorepos (nested go.mod files) are not
// resolved; the single root go.mod is the module identity. Files outside the
// root module's tree therefore resolve to no internal node.
type GoModuleResolver struct {
	modulePath string // root module path from go.mod, e.g. "example.com/app"
	hasModule  bool   // false when no go.mod was found
}

// NewGoModuleResolver reads the root go.mod and returns a resolver ready for use.
func NewGoModuleResolver(workspacePath string) *GoModuleResolver {
	modulePath, ok := readGoModulePath(workspacePath)
	return &GoModuleResolver{modulePath: modulePath, hasModule: ok}
}

// dirImportPath returns the package import path for a slash-separated directory
// relative to the workspace (module) root. "" (root) → the module path itself.
func (r *GoModuleResolver) dirImportPath(dir string) string {
	if !r.hasModule {
		return ""
	}
	if dir == "" || dir == "." {
		return r.modulePath
	}
	return r.modulePath + "/" + dir
}

// isInternal reports whether an import path belongs to this repo's module.
func (r *GoModuleResolver) isInternal(importPath string) bool {
	if !r.hasModule {
		return false
	}
	return importPath == r.modulePath || strings.HasPrefix(importPath, r.modulePath+"/")
}

// BuildGraphEdges counts intra-repo (from, to) coupling per directory-level
// package import path. Each import spec that resolves to an internal package
// increments the weight for that edge. Self-edges (a file importing its own
// package — impossible in valid Go, but a same-package import path collapses to
// from == to) are discarded. Dot- and blank-imports still produce an edge: a
// blank import is a real compile-time coupling (init side effects), and a
// dot-import injects names into scope.
func (r *GoModuleResolver) BuildGraphEdges(files []goRawFile) map[[2]string]uint32 {
	weights := make(map[[2]string]uint32)
	if !r.hasModule {
		return weights
	}

	count := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		weights[[2]string{from, to}]++
	}

	for _, f := range files {
		from := r.dirImportPath(f.Dir)
		if from == "" {
			continue
		}
		for _, imp := range f.Imports {
			if !r.isInternal(imp.Path) {
				continue // stdlib + third-party → no edge
			}
			count(from, imp.Path)
		}
	}
	return weights
}

// readGoModulePath reads the `module <path>` directive from go.mod in dir and
// returns the module path. It uses a simple line scanner (no x/mod dependency):
// the module directive is the first non-comment line of every valid go.mod.
// Returns ("", false) when go.mod is absent or has no module directive.
//
// Shared by the manifest parser, which needs the same module identity.
func readGoModulePath(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "module"); ok {
			rest = strings.TrimSpace(rest)
			// Strip an optional inline comment.
			if i := strings.Index(rest, "//"); i >= 0 {
				rest = strings.TrimSpace(rest[:i])
			}
			rest = strings.Trim(rest, "\"`")
			if rest != "" {
				return rest, true
			}
		}
	}
	return "", false
}
