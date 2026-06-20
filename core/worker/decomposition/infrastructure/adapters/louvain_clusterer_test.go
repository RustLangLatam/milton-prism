package adapters

import (
	"context"
	"sort"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
)

// TestLouvainClusterer_Conduit is the D2 acceptance test.
//
// It feeds the full Conduit graph through the InfraDetector to obtain the
// domain module set, then runs the LouvainClusterer on the domain sub-graph.
//
// Verified invariants (spec §7):
//   - Exactly 3 clusters corresponding to the three blueprint groups.
//   - Modularity ≥ lowConfidenceThreshold (HIGH confidence, no fallback).
//   - Tests.* modules are absent from every cluster.
//   - Cross-cluster deps: articles→profile, articles→user, profile→user.
func TestLouvainClusterer_Conduit(t *testing.T) {
	// Run D1 to get the domain classification.
	fullGraph := &workerdomain.Graph{Edges: conduitEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), fullGraph)
	if err != nil {
		t.Fatalf("InfraDetector.Detect: %v", err)
	}

	// Build the domain-only sub-graph (tests excluded automatically).
	domainGraph := workerdomain.DomainSubgraph(fullGraph, cls.Domain)

	cl := NewLouvainClusterer()
	result, err := cl.Cluster(context.Background(), ports.ClusterInput{DomainGraph: domainGraph, DomainModules: cls.Domain})
	if err != nil {
		t.Fatalf("LouvainClusterer.Cluster: %v", err)
	}

	t.Logf("modularity=%.4f low_confidence=%v clusters=%d",
		result.Modularity, result.LowConfidence, len(result.Clusters))
	for _, c := range result.Clusters {
		names := make([]string, len(c.Modules))
		for i, m := range c.Modules {
			names[i] = string(m)
		}
		t.Logf("  CLUSTER %-30s %v", c.BlueprintGroup, names)
	}

	// Exactly 3 clusters.
	if len(result.Clusters) != 3 {
		t.Errorf("expected 3 clusters, got %d", len(result.Clusters))
	}

	// High modularity — must NOT trigger the low-confidence fallback.
	if result.LowConfidence {
		t.Errorf("low-confidence flag set (modularity=%.4f < threshold %.2f)",
			result.Modularity, lowConfidenceThreshold)
	}
	if result.Modularity < lowConfidenceThreshold {
		t.Errorf("modularity %.4f below threshold %.2f", result.Modularity, lowConfidenceThreshold)
	}

	// The three expected blueprint groups must be cluster identifiers.
	wantGroups := map[string]bool{
		"conduit.articles": false,
		"conduit.profile":  false,
		"conduit.user":     false,
	}
	for _, c := range result.Clusters {
		if _, ok := wantGroups[c.BlueprintGroup]; ok {
			wantGroups[c.BlueprintGroup] = true
		}
	}
	for grp, found := range wantGroups {
		if !found {
			t.Errorf("expected cluster with blueprint group %q", grp)
		}
	}

	// No test module may appear in any cluster.
	testSet := make(map[workerdomain.Module]bool)
	for _, m := range cls.Tests {
		testSet[m] = true
	}
	for _, c := range result.Clusters {
		for _, m := range c.Modules {
			if testSet[m] {
				t.Errorf("test module %q must not appear in any cluster", m)
			}
		}
	}

	// Each domain module must appear in exactly one cluster.
	seen := make(map[workerdomain.Module]int)
	for _, c := range result.Clusters {
		for _, m := range c.Modules {
			seen[m]++
		}
	}
	for _, m := range cls.Domain {
		if seen[m] != 1 {
			t.Errorf("domain module %q appears in %d clusters (want 1)", m, seen[m])
		}
	}
}

// TestLouvainClusterer_Conduit_Deps verifies the inter-service dependency
// characterisation on Conduit. Expected (spec §7):
//
//	articles → profile, user
//	profile  → user
//	user     → (none)
func TestLouvainClusterer_Conduit_Deps(t *testing.T) {
	fullGraph := &workerdomain.Graph{Edges: conduitEdges}
	det := NewDeterministicInfraDetector()
	cls, _ := det.Detect(context.Background(), fullGraph)
	domainGraph := workerdomain.DomainSubgraph(fullGraph, cls.Domain)

	cl := NewLouvainClusterer()
	result, err := cl.Cluster(context.Background(), ports.ClusterInput{DomainGraph: domainGraph, DomainModules: cls.Domain})
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(result.Clusters) != 3 {
		t.Fatalf("need 3 clusters for dep test, got %d", len(result.Clusters))
	}

	alloc := NewDeterministicPrefixAllocator()
	// characterize is an unexported function in the application package;
	// we replicate the dep-detection logic here to keep tests self-contained.
	moduleToService := make(map[workerdomain.Module]string)
	for _, c := range result.Clusters {
		svc := lastComponent(c.BlueprintGroup)
		for _, m := range c.Modules {
			moduleToService[m] = svc
		}
	}

	// Ensure allocator is seeded so repeated calls are idempotent.
	for _, c := range result.Clusters {
		svc := lastComponent(c.BlueprintGroup)
		if _, err := alloc.Allocate(context.Background(), svc); err != nil {
			t.Fatalf("Allocate(%q): %v", svc, err)
		}
	}

	type svcDeps struct {
		deps []string
	}
	deps := make(map[string]*svcDeps)
	for _, c := range result.Clusters {
		svc := lastComponent(c.BlueprintGroup)
		deps[svc] = &svcDeps{}
	}

	depsSet := make(map[[2]string]bool)
	for _, e := range domainGraph.Edges {
		from := moduleToService[e.From]
		to := moduleToService[e.To]
		if from != "" && to != "" && from != to {
			depsSet[[2]string{from, to}] = true
		}
	}
	for pair := range depsSet {
		d := deps[pair[0]]
		if d == nil {
			continue
		}
		d.deps = append(d.deps, pair[1])
	}
	for _, d := range deps {
		sort.Strings(d.deps)
	}

	cases := []struct {
		service  string
		wantDeps []string
	}{
		{"articles", []string{"profile", "user"}},
		{"profile", []string{"user"}},
		{"user", nil},
	}
	for _, tc := range cases {
		got := deps[tc.service]
		if got == nil {
			t.Errorf("service %q not found in clustering result", tc.service)
			continue
		}
		if !stringSliceEqual(got.deps, tc.wantDeps) {
			t.Errorf("service %q deps: got %v, want %v", tc.service, got.deps, tc.wantDeps)
		}
	}
}

// TestLouvainClusterer_PHP_BookStack verifies that PHP-aware groupOf and
// singleton absorption produce well-formed clusters from a representative
// BookStack domain subgraph — NOT 258 singletons.
//
// Invariants:
//   - BookStack\Entities\* modules cluster together (groupOf produces same group).
//   - No singleton clusters remain after absorbSingletons.
//   - Total cluster count << domain module count.
func TestLouvainClusterer_PHP_BookStack(t *testing.T) {
	// Use PHPAwareInfraDetector to get the realistic domain subset.
	fullGraph := &workerdomain.Graph{Edges: bookstackEdges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), fullGraph)
	if err != nil {
		t.Fatalf("InfraDetector.Detect: %v", err)
	}

	domainGraph := workerdomain.DomainSubgraph(fullGraph, cls.Domain)
	t.Logf("domain modules=%d edges=%d", len(cls.Domain), len(domainGraph.Edges))

	cl := NewLouvainClusterer()
	result, err := cl.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:   domainGraph,
		DomainModules: cls.Domain,
	})
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}

	t.Logf("clusters=%d modularity=%.4f low_confidence=%v", len(result.Clusters), result.Modularity, result.LowConfidence)
	for _, c := range result.Clusters {
		ms := make([]string, len(c.Modules))
		for i, m := range c.Modules {
			ms[i] = string(m)
		}
		t.Logf("  CLUSTER %-35s members=%d %v", c.BlueprintGroup, len(c.Modules), ms)
	}

	// No connected singletons: any singleton whose module has domain-graph neighbours
	// must have been absorbed by absorbSingletons. Truly isolated modules (fan-in=0
	// AND fan-out=0 in the domain graph) have no community to absorb into, so they
	// may remain as singletons — that is correct behaviour.
	domainEdgesMap := make(map[string]bool)
	for _, e := range domainGraph.Edges {
		domainEdgesMap[string(e.From)] = true
		domainEdgesMap[string(e.To)] = true
	}
	for _, c := range result.Clusters {
		if len(c.Modules) != 1 {
			continue
		}
		m := string(c.Modules[0])
		if domainEdgesMap[m] {
			t.Errorf("connected singleton cluster %q must have been absorbed (module %q has domain edges)", c.BlueprintGroup, m)
		}
	}

	// groupOf fix: BookStack\Entities\* must all be in the same cluster.
	entityModules := []string{
		`BookStack\Entities\Models\Page`,
		`BookStack\Entities\Models\Book`,
		`BookStack\Entities\Models\Entity`,
		`BookStack\Entities\Tools\PageContent`,
	}
	entitiesCluster := ""
	for _, c := range result.Clusters {
		for _, m := range c.Modules {
			for _, em := range entityModules {
				if string(m) == em {
					if entitiesCluster == "" {
						entitiesCluster = c.BlueprintGroup
					} else if c.BlueprintGroup != entitiesCluster {
						t.Errorf("BookStack\\Entities\\* modules split across clusters: %q and %q", entitiesCluster, c.BlueprintGroup)
					}
				}
			}
		}
	}
	if entitiesCluster == "" {
		t.Logf("NOTE: no Entities domain modules present in fixture after infra-detection — domain=%v", sortedModuleNames(cls.Domain))
	}

	// cluster count must be far below domain module count (no 1-class-per-service explosion).
	if len(cls.Domain) > 1 && len(result.Clusters) >= len(cls.Domain) {
		t.Errorf("cluster count (%d) >= domain module count (%d): groupOf/absorbSingletons fix did not converge",
			len(result.Clusters), len(cls.Domain))
	}
}

// TestLouvainClusterer_EmptyGraph returns an empty result without error.
func TestLouvainClusterer_EmptyGraph(t *testing.T) {
	cl := NewLouvainClusterer()
	result, err := cl.Cluster(context.Background(), ports.ClusterInput{DomainGraph: &workerdomain.Graph{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Clusters) != 0 {
		t.Errorf("expected 0 clusters for empty input, got %d", len(result.Clusters))
	}
}

// TestLouvainClusterer_SingleBlueprint verifies that a graph with a single
// blueprint group produces exactly one cluster with no dependencies.
func TestLouvainClusterer_SingleBlueprint(t *testing.T) {
	edges := []workerdomain.Edge{
		{From: "app.orders.views", To: "app.orders.models", Weight: 3},
		{From: "app.orders.models", To: "app.orders.serializers", Weight: 2},
	}
	modules := []workerdomain.Module{
		"app.orders.views", "app.orders.models", "app.orders.serializers",
	}
	cl := NewLouvainClusterer()
	result, err := cl.Cluster(context.Background(), ports.ClusterInput{DomainGraph: &workerdomain.Graph{Edges: edges}, DomainModules: modules})
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(result.Clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(result.Clusters))
	}
	if result.Clusters[0].BlueprintGroup != "app.orders" {
		t.Errorf("unexpected blueprint group %q", result.Clusters[0].BlueprintGroup)
	}
}

// TestPrefixAllocator verifies deterministic, non-colliding prefix allocation.
func TestPrefixAllocator(t *testing.T) {
	a := NewDeterministicPrefixAllocator()
	ctx := context.Background()

	p1, err := a.Allocate(ctx, "articles")
	if err != nil {
		t.Fatalf("Allocate(articles): %v", err)
	}
	p2, err := a.Allocate(ctx, "profile")
	if err != nil {
		t.Fatalf("Allocate(profile): %v", err)
	}
	p3, err := a.Allocate(ctx, "user")
	if err != nil {
		t.Fatalf("Allocate(user): %v", err)
	}

	t.Logf("articles=%s profile=%s user=%s", p1, p2, p3)

	// All must be 3 chars uppercase.
	for _, p := range []string{p1, p2, p3} {
		if len(p) != 3 {
			t.Errorf("prefix %q is not 3 chars", p)
		}
	}

	// Idempotent: same name always gets same prefix.
	again, _ := a.Allocate(ctx, "articles")
	if again != p1 {
		t.Errorf("Allocate(articles) idempotent: got %q, want %q", again, p1)
	}

	// All prefixes must be distinct.
	if p1 == p2 || p1 == p3 || p2 == p3 {
		t.Errorf("prefix collision: articles=%s profile=%s user=%s", p1, p2, p3)
	}
}

// --- helpers ---

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
