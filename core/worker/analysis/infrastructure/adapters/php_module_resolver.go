package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// PHPModuleResolver classifies PHP use statements as internal or external using
// the PSR-4 namespace map declared in composer.json (autoload.psr-4 only).
//
// devAutoload (autoload-dev) is intentionally excluded so test namespaces
// (e.g. "Tests\") do not appear in the production dependency graph.
type PHPModuleResolver struct {
	// internalPrefixes holds the known namespace prefixes that belong to this
	// project, each with a trailing backslash (e.g. "BookStack\").
	// Sorted by descending length so longest-prefix matching wins when prefixes
	// overlap (e.g. "Database\Factories\" before "Database\").
	internalPrefixes []string
}

// composerJSON is the minimal subset of composer.json required for PSR-4 resolution.
type composerJSON struct {
	Autoload struct {
		PSR4 map[string]interface{} `json:"psr-4"`
	} `json:"autoload"`
}

// NewPHPModuleResolver reads composer.json from workspacePath and returns a
// resolver loaded with the PSR-4 namespace prefixes.
func NewPHPModuleResolver(workspacePath string) (*PHPModuleResolver, error) {
	raw, err := os.ReadFile(filepath.Join(workspacePath, "composer.json"))
	if err != nil {
		return nil, err
	}

	var cj composerJSON
	if err := json.Unmarshal(raw, &cj); err != nil {
		return nil, err
	}

	prefixes := make([]string, 0, len(cj.Autoload.PSR4))
	for ns := range cj.Autoload.PSR4 {
		if ns != "" {
			// composer.json keys are JSON-escaped, so after Unmarshal "App\\" → "App\"
			// (one real backslash). The trailing backslash is part of the PSR-4 key.
			prefixes = append(prefixes, ns)
		}
	}
	// Longest prefix first — avoids false positives when one namespace is a
	// prefix of another (e.g. "Database\Factories\" vs "Database\").
	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i]) > len(prefixes[j])
	})

	return &PHPModuleResolver{internalPrefixes: prefixes}, nil
}

// IsInternal reports whether fqn belongs to a namespace declared in this project.
func (r *PHPModuleResolver) IsInternal(fqn string) bool {
	for _, prefix := range r.internalPrefixes {
		if strings.HasPrefix(fqn, prefix) {
			return true
		}
	}
	return false
}

// BuildGraphEdges produces deduplicated internal dependency edges from the
// extracted PHP files. Each edge is (from FQN → to FQN) where:
//   - from = namespace + "\" + class name (PSR-4 FQN of the importing class)
//   - to   = the canonical FQN of the referenced class
//
// Two sources of edges:
//   - file-level `use` statements (resolved to internal targets by PSR-4 prefix).
//   - in-body references (type-hints, new, ::, ::class — Tier A): resolved by
//     PHP name rules (file use-aliases, then current namespace) and kept ONLY
//     when the resolved FQN is an actual module of this repo. This existence gate
//     is what keeps over-resolution out: a name that resolves to a PHP built-in,
//     a vendor class, or anything outside the codebase produces no edge.
//
// Edges are discarded when the source has no namespace or from == to.
func (r *PHPModuleResolver) BuildGraphEdges(files []phpRawFile) []workerdomain.ResolvedImport {
	// Set of every module FQN defined in this repo — the ground truth an in-body
	// reference must resolve to in order to create an edge.
	moduleSet := make(map[string]bool, len(files))
	for _, f := range files {
		if f.NS != "" && f.Class != "" {
			moduleSet[f.NS+`\`+f.Class] = true
		}
	}

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

	for _, f := range files {
		if f.NS == "" {
			continue
		}
		from := f.NS
		if f.Class != "" {
			from = f.NS + `\` + f.Class
		}
		for _, use := range f.Uses {
			if r.IsInternal(use) {
				add(from, use)
			}
		}
		for _, ref := range f.Refs {
			if to := phpResolveRef(ref, f.NS, f.UseAliases); to != "" && moduleSet[to] {
				add(from, to)
			}
		}
	}
	return out
}

// phpResolveRef resolves a class name written in code to its canonical FQN,
// following PHP name-resolution rules:
//   - leading "\"  → fully qualified; return as-is (backslash stripped).
//   - otherwise the first segment is subject to use-alias substitution; if no
//     alias matches, the whole name is relative to the current namespace.
//
// self/static/parent are not class references and resolve to "".
func phpResolveRef(ref, currentNS string, aliases map[string]string) string {
	ref = strings.TrimSpace(ref)
	switch ref {
	case "", "self", "static", "parent":
		return ""
	}
	if strings.HasPrefix(ref, `\`) {
		return strings.TrimPrefix(ref, `\`)
	}
	first, rest := ref, ""
	if i := strings.IndexByte(ref, '\\'); i >= 0 {
		first, rest = ref[:i], ref[i:]
	}
	if fqn, ok := aliases[first]; ok {
		return fqn + rest
	}
	if currentNS == "" {
		return ref
	}
	return currentNS + `\` + ref
}
