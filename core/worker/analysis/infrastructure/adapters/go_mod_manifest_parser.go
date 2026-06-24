package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ManifestParser = (*GoModManifestParser)(nil)

// GoModManifestParser implements ports.ManifestParser for the Go modules
// ecosystem (OSV identifier "Go"). It parses go.mod in the workspace root.
//
// Scope policy:
//   - direct requires → included (production dependencies).
//   - `// indirect` requires → excluded (transitive; resolved via the import
//     graph of direct deps, not declared production intent).
//
// go.mod versions are exact (e.g. v1.2.3), so Approximate is false: no registry
// round-trip is needed to know the pinned version. The shared readGoModulePath
// helper (in go_module_resolver.go) reuses the same go.mod location.
type GoModManifestParser struct{}

// NewGoModManifestParser returns a new GoModManifestParser.
func NewGoModManifestParser() *GoModManifestParser {
	return &GoModManifestParser{}
}

func (p *GoModManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	modPath := filepath.Join(workspacePath, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseGoMod(data), nil
}

// parseGoMod extracts direct require dependencies from go.mod content. It
// handles both the block form:
//
//	require (
//	    example.com/a v1.2.3
//	    example.com/b v0.4.0 // indirect
//	)
//
// and single-line requires (`require example.com/a v1.2.3`). Indirect
// dependencies are skipped (production scope).
func parseGoMod(data []byte) []workerdomain.Dependency {
	var deps []workerdomain.Dependency
	inBlock := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		if inBlock {
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if d, ok := parseGoRequireLine(trimmed); ok {
				deps = append(deps, d)
			}
			continue
		}

		if strings.HasPrefix(trimmed, "require") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "require"))
			if rest == "(" {
				inBlock = true
				continue
			}
			// Single-line require directive.
			if d, ok := parseGoRequireLine(rest); ok {
				deps = append(deps, d)
			}
		}
	}
	return deps
}

// parseGoRequireLine parses one require entry ("<module> <version>" with an
// optional "// indirect" marker) into a Dependency. Indirect deps return ok=false.
func parseGoRequireLine(line string) (workerdomain.Dependency, bool) {
	// Detect and strip the indirect marker before tokenising.
	if i := strings.Index(line, "//"); i >= 0 {
		comment := line[i+2:]
		if strings.Contains(comment, "indirect") {
			return workerdomain.Dependency{}, false
		}
		line = strings.TrimSpace(line[:i])
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return workerdomain.Dependency{}, false
	}
	module := fields[0]
	version := fields[1]
	if module == "" || version == "" {
		return workerdomain.Dependency{}, false
	}

	return workerdomain.Dependency{
		Ecosystem:   workerdomain.EcosystemGoModules,
		Package:     module,
		Version:     version,
		Category:    "library",
		Approximate: false,
	}, true
}
