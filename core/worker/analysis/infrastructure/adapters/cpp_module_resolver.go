package adapters

import (
	"path"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// cppFileIndex maps the C/C++ source files of a workspace to their canonical
// node identity (the workspace-relative file path) and supports the pragmatic,
// best-effort include resolution C++ requires.
//
// C++ has no module name and no 1:1 file↔namespace mapping, and real include
// resolution depends on -I flags we cannot see. The only honest deterministic
// node identity is therefore the workspace-relative FILE path — coarser than
// Go/Python by design.
type cppFileIndex struct {
	// paths is the set of every known workspace-relative source path.
	paths map[string]bool
	// byBasename maps a file basename ("utils.h") to its unique workspace path.
	// Populated only for basenames owned by exactly one file.
	byBasename map[string]string
	// ambiguousBasename records basenames claimed by two or more files
	// (e.g. src/utils.h and lib/utils.h). The basename fallback is SUPPRESSED
	// for these so the resolver never emits a wrong edge (mirrors Python's
	// ambiguousAlias pattern).
	ambiguousBasename map[string]bool
}

// CppModuleResolver resolves quote-form #include directives into intra-repo
// file-to-file dependency edges. It never guesses an edge: an include that
// cannot be resolved to exactly one known file produces no edge (an honest
// miss rather than a fabricated coupling).
//
// Resolution order for `#include "X"` from file F (first hit wins):
//  1. dir(F)/X            — relative to the including file (most common).
//  2. root/X              — relative to the workspace root.
//  3. root/include/X, root/src/X, root/inc/X — common project roots.
//  4. basename(X) fallback — ONLY when basename(X) is unique (non-ambiguous).
//
// Node identity is the workspace-relative file path: FromModule and ToModule
// are both file paths. Header/impl edges (foo.cpp → foo.h) are emitted only
// when foo.cpp actually #includes "foo.h"; pairing alone never invents an edge.
type CppModuleResolver struct {
	index *cppFileIndex
}

// NewCppModuleResolver builds the file index from the parsed files and returns
// a resolver ready for use.
func NewCppModuleResolver(files []cppRawFile) *CppModuleResolver {
	idx := &cppFileIndex{
		paths:             make(map[string]bool, len(files)),
		byBasename:        make(map[string]string),
		ambiguousBasename: make(map[string]bool),
	}
	for _, f := range files {
		idx.paths[f.RelPath] = true
	}
	for _, f := range files {
		base := path.Base(f.RelPath)
		if idx.ambiguousBasename[base] {
			continue
		}
		if existing, ok := idx.byBasename[base]; ok {
			if existing != f.RelPath {
				// Two distinct files share a basename → ambiguous; drop the
				// fallback entry so neither file gets a wrong edge.
				delete(idx.byBasename, base)
				idx.ambiguousBasename[base] = true
			}
			continue
		}
		idx.byBasename[base] = f.RelPath
	}
	return &CppModuleResolver{index: idx}
}

// cppCommonRoots are the conventional include roots probed in step 3.
var cppCommonRoots = []string{"include", "src", "inc"}

// resolve returns the workspace-relative path that `#include "include"` from
// fromFile resolves to, or "" when no known file matches (honest miss).
func (r *CppModuleResolver) resolve(fromFile, include string) string {
	include = strings.TrimSpace(include)
	if include == "" {
		return ""
	}

	// 1. Relative to the including file's directory.
	dir := path.Dir(fromFile)
	if dir == "." {
		dir = ""
	}
	if dir != "" {
		if cand := path.Clean(dir + "/" + include); r.index.paths[cand] {
			return cand
		}
	}

	// 2. Relative to the workspace root.
	if cand := path.Clean(include); r.index.paths[cand] {
		return cand
	}

	// 3. Common project roots.
	for _, root := range cppCommonRoots {
		if cand := path.Clean(root + "/" + include); r.index.paths[cand] {
			return cand
		}
	}

	// 4. Basename fallback — only when unique (non-ambiguous).
	base := path.Base(include)
	if r.index.ambiguousBasename[base] {
		return ""
	}
	if cand, ok := r.index.byBasename[base]; ok {
		return cand
	}

	// No known file matches: emit no edge.
	return ""
}

// Resolve classifies each RawImport (a quote-form include) into an intra-repo
// file-to-file ResolvedImport. Duplicate (from, to) pairs are collapsed; a
// self-include (from == to) is discarded. Unresolvable includes are dropped.
func (r *CppModuleResolver) Resolve(rawImports []workerdomain.RawImport) []workerdomain.ResolvedImport {
	seen := make(map[[2]string]bool)
	var out []workerdomain.ResolvedImport
	for _, imp := range rawImports {
		to := r.resolve(imp.ImportingFile, imp.Module)
		if to == "" || to == imp.ImportingFile {
			continue
		}
		k := [2]string{imp.ImportingFile, to}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, workerdomain.ResolvedImport{FromModule: imp.ImportingFile, ToModule: to})
	}
	return out
}

// BuildGraphEdges counts intra-repo (from, to) coupling per file pair. Each
// quote-form include that resolves to a known file increments the weight for
// that edge (coupling strength = number of include references to the target).
func (r *CppModuleResolver) BuildGraphEdges(rawImports []workerdomain.RawImport) map[[2]string]uint32 {
	weights := make(map[[2]string]uint32)
	for _, imp := range rawImports {
		from := imp.ImportingFile
		to := r.resolve(from, imp.Module)
		if from == "" || to == "" || from == to {
			continue
		}
		weights[[2]string{from, to}]++
	}
	return weights
}
