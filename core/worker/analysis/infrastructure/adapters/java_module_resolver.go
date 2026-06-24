package adapters

import "strings"

// JavaModuleResolver classifies Java import statements as internal (intra-repo)
// or external using the set of packages actually declared by the repo's own
// source files. There is no manifest-derived prefix list: the ground truth is
// the union of every `package` declaration seen across the workspace, which is
// exactly the BookStack/Python approach (the graph is intra-repo coupling only).
//
// JDK (java.*, javax.*, jakarta.*) and third-party Maven/Gradle imports never
// match a declared package and so are discarded — they are already Tier-1
// manifest facts and must not become graph edges.
type JavaModuleResolver struct {
	// typeSet is the set of fully-qualified type names defined in this repo,
	// e.g. "com.acme.svc.UserService". The resolution target for a precise import.
	typeSet map[string]bool
	// packageSet is the set of packages declared in this repo, e.g. "com.acme.svc".
	// Used to resolve static imports and wildcard imports to a package node.
	packageSet map[string]bool
	// typeToFQN maps a fully-qualified type name to itself; also records, per
	// package, the types it contains so a wildcard import can be expanded.
	pkgTypes map[string][]string
}

// NewJavaModuleResolver builds a resolver from the extracted files. The internal
// package/type universe is derived purely from the repo's own declarations.
func NewJavaModuleResolver(files []javaRawFile) *JavaModuleResolver {
	r := &JavaModuleResolver{
		typeSet:    make(map[string]bool),
		packageSet: make(map[string]bool),
		pkgTypes:   make(map[string][]string),
	}
	for _, f := range files {
		if f.Package == "" {
			continue
		}
		r.packageSet[f.Package] = true
		for _, t := range f.Types {
			fqn := f.Package + "." + t
			r.typeSet[fqn] = true
			r.pkgTypes[f.Package] = append(r.pkgTypes[f.Package], fqn)
		}
	}
	return r
}

// moduleID returns the graph node identity for a file: the FQN of its primary
// type when present, else the package name. Mirrors the PHP analyzer, where the
// node is the fully-qualified class name.
func javaModuleID(f javaRawFile) string {
	if f.Package == "" {
		return ""
	}
	if f.PrimaryType != "" {
		return f.Package + "." + f.PrimaryType
	}
	return f.Package
}

// resolveImport returns the intra-repo target node(s) an import resolves to, or
// nil when the import is external (JDK / third-party). A precise import resolves
// to the exact type FQN; a static import resolves to the enclosing type (its
// declaring class, found by dropping the trailing member); a wildcard import
// resolves to every internal type declared in that package.
func (r *JavaModuleResolver) resolveImport(imp javaImport) []string {
	if imp.IsWildcard {
		// FQN is the package; expand to its declared types.
		if r.packageSet[imp.FQN] {
			return append([]string(nil), r.pkgTypes[imp.FQN]...)
		}
		return nil
	}

	if r.typeSet[imp.FQN] {
		return []string{imp.FQN}
	}

	if imp.IsStatic {
		// `import static a.b.Type.member` → drop the member, resolve the type.
		if i := strings.LastIndexByte(imp.FQN, '.'); i >= 0 {
			enclosing := imp.FQN[:i]
			if r.typeSet[enclosing] {
				return []string{enclosing}
			}
		}
	}
	return nil
}

// BuildGraphEdges counts intra-repo (from, to) coupling. Each import that
// resolves to an internal target increments the weight for that edge. Wildcard
// imports add one unit of weight per expanded internal type (coupling fan-out).
// Self-edges (from == to) and edges from a file with no module identity are
// discarded.
func (r *JavaModuleResolver) BuildGraphEdges(files []javaRawFile) map[[2]string]uint32 {
	weights := make(map[[2]string]uint32)

	count := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		weights[[2]string{from, to}]++
	}

	for _, f := range files {
		from := javaModuleID(f)
		if from == "" {
			continue
		}
		for _, imp := range f.Imports {
			for _, to := range r.resolveImport(imp) {
				count(from, to)
			}
		}
	}
	return weights
}
