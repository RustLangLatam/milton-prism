package application

import (
	"strings"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// conduitCandidates returns the canonical 3-service Conduit characterisation
// used as the microservices baseline. The monolith path collapses these.
func conduitCandidates() []workerdomain.ServiceCandidate {
	return []workerdomain.ServiceCandidate{
		{
			Name:           "articles",
			ErrorPrefix:    "ART",
			OwnedResources: []workerdomain.Module{"conduit.articles.models"},
			Deps:           []string{"profile"},
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
			},
		},
		{
			Name:           "profile",
			ErrorPrefix:    "PRO",
			OwnedResources: []workerdomain.Module{"conduit.profile.models"},
			Deps:           []string{"user"},
		},
		{
			Name:           "user",
			ErrorPrefix:    "USE",
			OwnedResources: []workerdomain.Module{"conduit.user.models"},
		},
	}
}

// TestCollapseToMonolith_Conduit verifies that the 3 Conduit candidates collapse
// into one service that owns every resource and has no cross-service deps.
func TestCollapseToMonolith_Conduit(t *testing.T) {
	got := workerdomain.CollapseToMonolith(conduitCandidates(), "APP")
	if len(got) != 1 {
		t.Fatalf("monolith collapse: expected 1 service, got %d", len(got))
	}
	svc := got[0]
	if svc.Name != workerdomain.MonolithServiceName {
		t.Errorf("monolith service name: got %q want %q", svc.Name, workerdomain.MonolithServiceName)
	}
	if svc.ErrorPrefix != "APP" {
		t.Errorf("monolith prefix: got %q want APP", svc.ErrorPrefix)
	}
	wantResources := []string{"conduit.articles.models", "conduit.profile.models", "conduit.user.models"}
	if len(svc.OwnedResources) != len(wantResources) {
		t.Fatalf("monolith resources: got %d want %d (%v)", len(svc.OwnedResources), len(wantResources), svc.OwnedResources)
	}
	for i, r := range wantResources {
		if string(svc.OwnedResources[i]) != r {
			t.Errorf("monolith resource[%d]: got %q want %q", i, svc.OwnedResources[i], r)
		}
	}
	if len(svc.Deps) != 0 {
		t.Errorf("monolith deps must be empty (single service), got %v", svc.Deps)
	}
	if len(svc.OperationalCouplings) != 0 {
		t.Errorf("monolith op-couplings must be empty (all internal), got %v", svc.OperationalCouplings)
	}
}

// TestCollapseToMonolith_Empty verifies the no-domain-layer case returns nil so
// the pipeline's no-boundaries path stays unchanged for monolith migrations.
func TestCollapseToMonolith_Empty(t *testing.T) {
	if got := workerdomain.CollapseToMonolith(nil, "APP"); got != nil {
		t.Errorf("empty collapse: expected nil, got %v", got)
	}
}

// TestAssemblePlan_Monolith verifies the monolith plan is a single service with
// no cross-service FK debt and a monolith-specific rationale (HTTP-native, no
// gateway), while the microservices default is untouched.
func TestAssemblePlan_Monolith(t *testing.T) {
	candidates := workerdomain.CollapseToMonolith(conduitCandidates(), "APP")
	// MONOLITH ownership: analyzeDataOwnership(_, _, true) yields no cross-FKs.
	ownership := analyzeDataOwnership(candidates, nil, true)
	if len(ownership.CrossServiceFKs) != 0 {
		t.Errorf("monolith ownership: expected 0 cross-service FKs, got %d", len(ownership.CrossServiceFKs))
	}
	if !ownership.SharedDatabase {
		t.Errorf("monolith ownership: shared_database must be true (one DB)")
	}

	plan := assemblePlan(candidates, ownership, &workerdomain.ClusteringResult{Modularity: 0.42}, true)
	if len(plan.GetServices()) != 1 {
		t.Fatalf("monolith plan: expected 1 service, got %d", len(plan.GetServices()))
	}
	if plan.GetServices()[0].GetName() != workerdomain.MonolithServiceName {
		t.Errorf("monolith plan service name: got %q", plan.GetServices()[0].GetName())
	}
	if len(plan.GetServices()[0].GetCrossServiceFks()) != 0 {
		t.Errorf("monolith plan: service must carry no cross-service FKs")
	}
	if !strings.Contains(plan.GetRationale(), "HTTP-native") {
		t.Errorf("monolith rationale must mention HTTP-native, got %q", plan.GetRationale())
	}
	if strings.Contains(plan.GetRationale(), "Louvain community detection produced") {
		t.Errorf("monolith rationale must not use the microservices wording, got %q", plan.GetRationale())
	}
}

// TestBuildMonolithArtifacts_Merges verifies all per-cluster contracts merge into
// one artifact with a monolith boundary spec (no gateway, HTTP-native).
func TestBuildMonolithArtifacts_Merges(t *testing.T) {
	candidates := workerdomain.CollapseToMonolith(conduitCandidates(), "APP")
	plan := assemblePlan(candidates, analyzeDataOwnership(candidates, nil, true),
		&workerdomain.ClusteringResult{}, true)

	contracts := []workerdomain.DerivedContract{
		{ServiceName: "articles", ProtoContent: "message Article {}"},
		{ServiceName: "user", ProtoContent: "message User {}", Incomplete: true, IncompleteReason: "non-CRUD route"},
	}
	arts := buildArtifacts(plan, contracts, workerdomain.DataOwnership{SharedDatabase: true}, candidates, true)
	if len(arts) != 1 {
		t.Fatalf("monolith artifacts: expected 1, got %d", len(arts))
	}
	a := arts[0]
	if a.ServiceName != workerdomain.MonolithServiceName {
		t.Errorf("artifact name: got %q", a.ServiceName)
	}
	if !strings.Contains(a.ProtoContent, "message Article") || !strings.Contains(a.ProtoContent, "message User") {
		t.Errorf("monolith proto must merge all cluster contracts, got:\n%s", a.ProtoContent)
	}
	if !a.Incomplete {
		t.Errorf("monolith artifact must inherit incomplete flag from any source contract")
	}
	if !strings.Contains(a.BoundarySpec, "topology: monolith") ||
		!strings.Contains(a.BoundarySpec, "api_gateway: false") {
		t.Errorf("monolith boundary spec must declare topology + no gateway, got:\n%s", a.BoundarySpec)
	}
}

// TestMicroservicesPath_Unchanged is a no-regression guard: the default (monolith=false)
// ownership still emits cross-service FKs from the derived contracts.
func TestMicroservicesPath_Unchanged(t *testing.T) {
	candidates := conduitCandidates()
	contracts := []workerdomain.DerivedContract{
		{
			ServiceName: "articles",
			Messages: []workerdomain.ProtoMessage{
				{Name: "Article", Fields: []workerdomain.ProtoField{
					{Name: "author_identifier", IsCrossFK: true, RefTable: "userprofile", RefService: "profile"},
				}},
			},
		},
	}
	ownership := analyzeDataOwnership(candidates, contracts, false)
	if len(ownership.CrossServiceFKs) != 1 {
		t.Fatalf("microservices ownership: expected 1 cross-service FK, got %d", len(ownership.CrossServiceFKs))
	}
}
