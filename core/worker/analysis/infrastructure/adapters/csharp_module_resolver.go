package adapters

// CSharpModuleResolver classifies C# using directives as internal (intra-repo) or
// external using the set of namespaces actually declared by the repo's own source
// files. As with the Java resolver, the ground truth is the union of every
// `namespace` declaration seen across the workspace — the graph is intra-repo
// coupling only.
//
// BCL (System.*, Microsoft.*) and NuGet third-party usings never match a declared
// namespace and are discarded: they are Tier-1 manifest facts, not graph edges.
//
// C# `using` imports a NAMESPACE (not a single type), so an internal using edge
// points from the importing module to every internal type-bearing module of that
// namespace. To keep the graph at module granularity and comparable to Java, the
// edge target is the namespace node itself: each declared namespace is also a
// graph node, and an importing module couples to that namespace.
type CSharpModuleResolver struct {
	// namespaceSet is the set of namespaces declared in this repo, e.g. "Acme.Services".
	namespaceSet map[string]bool
	// nsModules maps a namespace to the module identities declared in it
	// (Namespace.TypeName), used to expand a using edge to concrete modules.
	nsModules map[string][]string
}

// NewCSharpModuleResolver builds a resolver from the extracted files.
func NewCSharpModuleResolver(files []csharpRawFile) *CSharpModuleResolver {
	r := &CSharpModuleResolver{
		namespaceSet: make(map[string]bool),
		nsModules:    make(map[string][]string),
	}
	for _, f := range files {
		for _, ns := range f.Namespaces {
			r.namespaceSet[ns] = true
		}
		mod := csharpModuleID(f)
		if mod != "" && f.Namespace != "" {
			r.nsModules[f.Namespace] = append(r.nsModules[f.Namespace], mod)
		}
	}
	return r
}

// csharpModuleID returns the graph node identity for a file: Namespace.PrimaryType
// when a type is declared, else the namespace itself. Mirrors the Java/PHP node
// identity (fully-qualified type name).
func csharpModuleID(f csharpRawFile) string {
	if f.Namespace == "" {
		return ""
	}
	if f.PrimaryType != "" {
		return f.Namespace + "." + f.PrimaryType
	}
	return f.Namespace
}

// BuildGraphEdges counts intra-repo (from, to) coupling. For each using that
// targets an internally-declared namespace, the importing module gets one unit of
// coupling weight to every module declared in that namespace (coupling fan-out).
// Self-edges and edges from a module with no identity are discarded. Aliased and
// global usings resolve the same way (the alias is irrelevant to the target).
func (r *CSharpModuleResolver) BuildGraphEdges(files []csharpRawFile) map[[2]string]uint32 {
	weights := make(map[[2]string]uint32)

	count := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		weights[[2]string{from, to}]++
	}

	for _, f := range files {
		from := csharpModuleID(f)
		if from == "" {
			continue
		}
		for _, u := range f.Usings {
			if !r.namespaceSet[u.Namespace] {
				continue // external (BCL / NuGet) — not a graph edge
			}
			for _, to := range r.nsModules[u.Namespace] {
				count(from, to)
			}
		}
	}
	return weights
}
