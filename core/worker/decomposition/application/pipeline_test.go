package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// TestAssemblePlan_Conduit verifies the RestructurePlan assembled from the
// expected Conduit decomposition result: 3 services with correct names,
// prefixes, deps (data-layer only — no cycles), operational couplings, and a
// rationale that mentions shared_database and FK debt.
func TestAssemblePlan_Conduit(t *testing.T) {
	candidates := []workerdomain.ServiceCandidate{
		{
			Name:           "articles",
			ErrorPrefix:    "ART",
			OwnedResources: []workerdomain.Module{"conduit.articles.models"},
			// articles has one data dep (profile, from articles.models → profile.models)
			// and one operational coupling (to user, from articles.views → user.models).
			Deps: []string{"profile"},
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
			},
		},
		{
			Name:           "profile",
			ErrorPrefix:    "PRO",
			OwnedResources: []workerdomain.Module{"conduit.profile.models"},
			// profile.Deps gets [user] from augmentDataDeps via the user_identifier FK.
			Deps: []string{"user"},
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "profile", ToService: "user", FromModule: "conduit.profile.views"},
			},
		},
		{
			Name:           "user",
			ErrorPrefix:    "USE",
			OwnedResources: []workerdomain.Module{"conduit.user.models"},
			// user has no data-layer deps; its coupling to profile is operational only.
			Deps: nil,
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "user", ToService: "profile", FromModule: "conduit.user.views"},
			},
		},
	}
	ownership := workerdomain.DataOwnership{
		SharedDatabase: true,
		CrossServiceFKs: []workerdomain.CrossServiceFK{
			{OwnerService: "articles", OwnerMessage: "Article", FieldName: "author_identifier", RefTable: "userprofile", RefService: "profile"},
			{OwnerService: "articles", OwnerMessage: "Comment", FieldName: "author_identifier", RefTable: "userprofile", RefService: "profile"},
		},
		OperationalCouplings: []workerdomain.OperationalCoupling{
			{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
			{FromService: "profile", ToService: "user", FromModule: "conduit.profile.views"},
			{FromService: "user", ToService: "profile", FromModule: "conduit.user.views"},
		},
	}

	plan := assemblePlan(candidates, ownership, &workerdomain.ClusteringResult{LowConfidence: false})

	if len(plan.GetServices()) != 3 {
		t.Fatalf("expected 3 services, got %d", len(plan.GetServices()))
	}

	byName := make(map[string]*workerdomain.ProposedService)
	for _, s := range plan.GetServices() {
		byName[s.GetName()] = s
	}

	arts, ok := byName["articles"]
	if !ok {
		t.Fatal("missing articles service in plan")
	}
	if arts.GetErrorPrefix() != "ART" {
		t.Errorf("articles ErrorPrefix: got %q, want ART", arts.GetErrorPrefix())
	}
	// articles has exactly one data dep (profile); user is operational, not hard.
	if got := arts.GetInterServiceDeps(); len(got) != 1 || got[0] != "profile" {
		t.Errorf("articles deps: got %v, want [profile]", got)
	}

	// Both Article.author_identifier and Comment.author_identifier are distinct FKs
	// on the articles service — now distinguishable by owner_message.
	if len(arts.GetCrossServiceFks()) != 2 {
		t.Errorf("articles.cross_service_fks: got %d, want 2 (Article.author + Comment.author)",
			len(arts.GetCrossServiceFks()))
	}
	fkMessages := make(map[string]bool)
	for _, fk := range arts.GetCrossServiceFks() {
		if fk.GetRefTable() != "userprofile" {
			t.Errorf("FK ref_table: got %q, want userprofile", fk.GetRefTable())
		}
		if fk.GetRefService() != "profile" {
			t.Errorf("FK ref_service: got %q, want profile", fk.GetRefService())
		}
		fkMessages[fk.GetOwnerMessage()] = true
	}
	if !fkMessages["Article"] || !fkMessages["Comment"] {
		t.Errorf("expected owner_message Article and Comment, got %v", fkMessages)
	}

	usr, ok := byName["user"]
	if !ok {
		t.Fatal("missing user service in plan")
	}
	if len(usr.GetInterServiceDeps()) != 0 {
		t.Errorf("user should have no data deps, got %v", usr.GetInterServiceDeps())
	}
	if len(usr.GetCrossServiceFks()) != 0 {
		t.Errorf("user should have no cross-service FKs, got %d", len(usr.GetCrossServiceFks()))
	}

	// Plan carries 3 operational couplings.
	if len(plan.GetOperationalCouplings()) != 3 {
		t.Errorf("plan operational_couplings: got %d, want 3", len(plan.GetOperationalCouplings()))
	}

	rationale := plan.GetRationale()
	if !strings.Contains(rationale, "Shared database") && !strings.Contains(rationale, "shared database") {
		t.Errorf("rationale should mention shared database, got: %q", rationale)
	}
	if !strings.Contains(rationale, "userprofile") {
		t.Errorf("rationale should mention FK debt (userprofile), got: %q", rationale)
	}
	if strings.Contains(rationale, "LOW CONFIDENCE") {
		t.Error("rationale should not contain LOW CONFIDENCE when lowConfidence=false")
	}
}

// TestAssemblePlan_CrossServiceFkMapping verifies that FKs are correctly grouped
// per service, owner_message is preserved, and the plan carries operational couplings.
func TestAssemblePlan_CrossServiceFkMapping(t *testing.T) {
	candidates := []workerdomain.ServiceCandidate{
		{
			Name:        "articles",
			ErrorPrefix: "ART",
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
			},
		},
		{Name: "user", ErrorPrefix: "USE"},
	}
	ownership := workerdomain.DataOwnership{
		SharedDatabase: true,
		CrossServiceFKs: []workerdomain.CrossServiceFK{
			{OwnerService: "articles", OwnerMessage: "Article", FieldName: "article_author_identifier", RefTable: "userprofile", RefService: "profile"},
			{OwnerService: "articles", OwnerMessage: "Comment", FieldName: "comment_author_identifier", RefTable: "userprofile", RefService: "profile"},
		},
		OperationalCouplings: []workerdomain.OperationalCoupling{
			{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
		},
	}

	plan := assemblePlan(candidates, ownership, &workerdomain.ClusteringResult{LowConfidence: false})

	var artSvc *migrationv1.ProposedService
	var usrSvc *migrationv1.ProposedService
	for _, s := range plan.GetServices() {
		switch s.GetName() {
		case "articles":
			artSvc = s
		case "user":
			usrSvc = s
		}
	}
	if artSvc == nil || usrSvc == nil {
		t.Fatal("missing articles or user service in plan")
	}

	// articles has 2 cross-service FKs.
	if got := len(artSvc.GetCrossServiceFks()); got != 2 {
		t.Errorf("articles cross_service_fks: got %d, want 2", got)
	}
	// user has no cross-service FKs.
	if got := len(usrSvc.GetCrossServiceFks()); got != 0 {
		t.Errorf("user cross_service_fks: got %d, want 0", got)
	}
	// Field names and owner_message are preserved.
	type fkKey struct{ msg, field string }
	fkSet := make(map[fkKey]bool)
	for _, fk := range artSvc.GetCrossServiceFks() {
		fkSet[fkKey{fk.GetOwnerMessage(), fk.GetField()}] = true
	}
	if !fkSet[fkKey{"Article", "article_author_identifier"}] {
		t.Errorf("missing Article.article_author_identifier FK: %v", fkSet)
	}
	if !fkSet[fkKey{"Comment", "comment_author_identifier"}] {
		t.Errorf("missing Comment.comment_author_identifier FK: %v", fkSet)
	}

	// Plan carries the operational coupling from articles to user.
	if len(plan.GetOperationalCouplings()) != 1 {
		t.Errorf("operational_couplings: got %d, want 1", len(plan.GetOperationalCouplings()))
	}
	if oc := plan.GetOperationalCouplings()[0]; oc.GetFromService() != "articles" || oc.GetToService() != "user" {
		t.Errorf("operational coupling: got %s→%s, want articles→user", oc.GetFromService(), oc.GetToService())
	}
}

// TestAssemblePlan_LowConfidence verifies the LOW CONFIDENCE prefix in the rationale
// and that the structured IsLowConfidence boolean is set as the programmatic source of truth.
func TestAssemblePlan_LowConfidence(t *testing.T) {
	candidates := []workerdomain.ServiceCandidate{
		{Name: "articles", ErrorPrefix: "ART"},
	}
	ownership := workerdomain.DataOwnership{SharedDatabase: true}

	plan := assemblePlan(candidates, ownership, &workerdomain.ClusteringResult{LowConfidence: true})

	if !strings.Contains(plan.GetRationale(), "LOW CONFIDENCE") {
		t.Errorf("rationale should be prefixed with LOW CONFIDENCE, got: %q", plan.GetRationale())
	}
	if !plan.GetIsLowConfidence() {
		t.Errorf("expected IsLowConfidence=true when lowConfidence=true, got false")
	}

	// High-confidence plan must NOT set the flag or the prefix.
	planHigh := assemblePlan(candidates, ownership, &workerdomain.ClusteringResult{LowConfidence: false})
	if planHigh.GetIsLowConfidence() {
		t.Errorf("expected IsLowConfidence=false when lowConfidence=false, got true")
	}
	if strings.Contains(planHigh.GetRationale(), "LOW CONFIDENCE") {
		t.Errorf("high-confidence rationale should not contain LOW CONFIDENCE, got: %q", planHigh.GetRationale())
	}
}

// TestAnalyzeDataOwnership_Conduit verifies that cross-service FKs are correctly
// identified with owner_message, intra-service FKs are excluded, and operational
// couplings are aggregated from candidates.
func TestAnalyzeDataOwnership_Conduit(t *testing.T) {
	candidates := []workerdomain.ServiceCandidate{
		{
			Name: "articles",
			OperationalCouplings: []workerdomain.OperationalCoupling{
				{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
			},
		},
		{Name: "user"},
	}
	contracts := []workerdomain.DerivedContract{
		{
			ServiceName: "articles",
			Messages: []workerdomain.ProtoMessage{
				{
					Name: "Article",
					Fields: []workerdomain.ProtoField{
						{Name: "author_identifier", IsCrossFK: true, RefTable: "userprofile", RefService: "profile"},
					},
				},
				{
					Name: "Comment",
					Fields: []workerdomain.ProtoField{
						// intra-service: excluded
						{Name: "article_identifier", IsCrossFK: true, RefTable: "article", RefService: "articles"},
						// cross-service: Article and Comment both have author_identifier → now distinct via OwnerMessage
						{Name: "author_identifier", IsCrossFK: true, RefTable: "userprofile", RefService: "profile"},
					},
				},
			},
		},
	}

	ownership := analyzeDataOwnership(candidates, contracts)

	if !ownership.SharedDatabase {
		t.Error("SharedDatabase must be true in v1")
	}

	// Exactly 2 cross-service FKs: Article.author_identifier and Comment.author_identifier.
	// Comment.article_identifier is intra-service → excluded.
	if len(ownership.CrossServiceFKs) != 2 {
		t.Errorf("expected 2 cross-service FKs, got %d: %+v", len(ownership.CrossServiceFKs), ownership.CrossServiceFKs)
	}

	// Verify OwnerMessage distinguishes the two FKs.
	msgs := make(map[string]bool)
	for _, fk := range ownership.CrossServiceFKs {
		if fk.RefService == fk.OwnerService {
			t.Errorf("intra-service FK should not be listed: %+v", fk)
		}
		if fk.RefTable != "userprofile" {
			t.Errorf("unexpected FK ref_table: %q (want userprofile)", fk.RefTable)
		}
		if fk.OwnerMessage == "" {
			t.Errorf("OwnerMessage must not be empty: %+v", fk)
		}
		msgs[fk.OwnerMessage] = true
	}
	if !msgs["Article"] || !msgs["Comment"] {
		t.Errorf("expected OwnerMessage Article and Comment, got %v", msgs)
	}

	// Operational coupling from articles.views is aggregated.
	if len(ownership.OperationalCouplings) != 1 {
		t.Errorf("expected 1 operational coupling, got %d", len(ownership.OperationalCouplings))
	}
	if oc := ownership.OperationalCouplings[0]; oc.FromService != "articles" || oc.ToService != "user" {
		t.Errorf("unexpected coupling: %+v", oc)
	}
}

// TestCharacterize_DataVsOperational verifies the split between data-layer
// dependencies (.models → .models) and operational couplings (.views → .models).
// Conduit scenario: user.views imports profile.models (operational, not hard dep).
func TestCharacterize_DataVsOperational(t *testing.T) {
	// Minimal graph capturing the Conduit user↔profile coupling pattern:
	//   articles.models → profile.models  (data dep: articles depends on profile)
	//   user.views      → profile.models  (operational: user.views imports profile)
	//   profile.views   → user.models     (operational: profile.views imports user)
	graph := &workerdomain.Graph{
		Edges: []workerdomain.Edge{
			{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
			{From: "conduit.user.views", To: "conduit.profile.models", Weight: 1},
			{From: "conduit.profile.views", To: "conduit.user.models", Weight: 1},
		},
	}
	clusters := []workerdomain.Cluster{
		{BlueprintGroup: "conduit.articles", Modules: []workerdomain.Module{
			"conduit.articles.models", "conduit.articles.views",
		}},
		{BlueprintGroup: "conduit.profile", Modules: []workerdomain.Module{
			"conduit.profile.models", "conduit.profile.views",
		}},
		{BlueprintGroup: "conduit.user", Modules: []workerdomain.Module{
			"conduit.user.models", "conduit.user.views",
		}},
	}

	alloc := &fixedAllocator{prefixes: map[string]string{
		"articles": "ART", "profile": "PRO", "user": "USE",
	}}
	candidates, err := characterize(t.Context(), graph, clusters, alloc)
	if err != nil {
		t.Fatalf("characterize: %v", err)
	}

	byName := make(map[string]workerdomain.ServiceCandidate)
	for _, c := range candidates {
		byName[c.Name] = c
	}

	arts := byName["articles"]
	if got := arts.Deps; len(got) != 1 || got[0] != "profile" {
		t.Errorf("articles data deps: got %v, want [profile]", got)
	}
	if len(arts.OperationalCouplings) != 0 {
		t.Errorf("articles should have no operational couplings from this graph, got %v", arts.OperationalCouplings)
	}

	usr := byName["user"]
	if len(usr.Deps) != 0 {
		t.Errorf("user data deps: got %v, want [] (user.views→profile is operational, not data)", usr.Deps)
	}
	if len(usr.OperationalCouplings) != 1 {
		t.Errorf("user operational couplings: got %d, want 1", len(usr.OperationalCouplings))
	}
	if oc := usr.OperationalCouplings[0]; oc.ToService != "profile" || oc.FromModule != "conduit.user.views" {
		t.Errorf("user operational coupling: got %+v, want {user→profile, conduit.user.views}", oc)
	}

	prof := byName["profile"]
	if len(prof.Deps) != 0 {
		// profile.models has no import-graph edge to user.models (FK is string-based, not an import).
		// Deps will be augmented via augmentDataDeps after stage 6 — not here.
		t.Errorf("profile data deps from import graph: got %v, want [] at this stage", prof.Deps)
	}
	if len(prof.OperationalCouplings) != 1 {
		t.Errorf("profile operational couplings: got %d, want 1", len(prof.OperationalCouplings))
	}
	if oc := prof.OperationalCouplings[0]; oc.ToService != "user" || oc.FromModule != "conduit.profile.views" {
		t.Errorf("profile operational coupling: got %+v, want {profile→user, conduit.profile.views}", oc)
	}
}

// TestAugmentDataDeps verifies that FK-derived dependencies are added to
// candidates after analyzeDataOwnership produces the cross-service FK list.
// This covers the case where the FK is expressed as a SQLAlchemy table-name
// string (not a Python import), so it doesn't appear as a graph edge.
func TestAugmentDataDeps(t *testing.T) {
	candidates := []workerdomain.ServiceCandidate{
		{Name: "articles", Deps: []string{"profile"}},
		{Name: "profile", Deps: []string{}}, // no import-graph edge to user.models
		{Name: "user", Deps: []string{}},
	}
	crossFKs := []workerdomain.CrossServiceFK{
		{OwnerService: "articles", OwnerMessage: "Article", FieldName: "author_identifier", RefTable: "userprofile", RefService: "profile"},
		{OwnerService: "profile", OwnerMessage: "UserProfile", FieldName: "user_identifier", RefTable: "users", RefService: "user"},
	}

	result := augmentDataDeps(candidates, crossFKs)

	byName := make(map[string]workerdomain.ServiceCandidate)
	for _, c := range result {
		byName[c.Name] = c
	}

	// articles already had profile — should still be exactly [profile], no duplicates.
	if got := byName["articles"].Deps; len(got) != 1 || got[0] != "profile" {
		t.Errorf("articles deps: got %v, want [profile]", got)
	}
	// profile gets user from the FK — this is the key fix.
	if got := byName["profile"].Deps; len(got) != 1 || got[0] != "user" {
		t.Errorf("profile deps after augmentation: got %v, want [user]", got)
	}
	// user has no FKs to other services.
	if got := byName["user"].Deps; len(got) != 0 {
		t.Errorf("user deps: got %v, want []", got)
	}
}

// TestAssemblePlan_NoServiceBoundaries verifies that a zero-candidate call sets
// the structured flag and writes a plain-language explanation, without Louvain jargon.
func TestAssemblePlan_NoServiceBoundaries(t *testing.T) {
	plan := assemblePlan(nil, workerdomain.DataOwnership{SharedDatabase: true}, &workerdomain.ClusteringResult{})

	if !plan.GetNoServiceBoundaries() {
		t.Error("expected NoServiceBoundaries=true when candidates=nil")
	}
	if plan.GetBoundariesExplanation() == "" {
		t.Error("expected a non-empty BoundariesExplanation")
	}
	if strings.Contains(plan.GetBoundariesExplanation(), "Louvain") {
		t.Errorf("BoundariesExplanation must not contain Louvain jargon, got: %q", plan.GetBoundariesExplanation())
	}
	if strings.Contains(plan.GetBoundariesExplanation(), "modularity") {
		t.Errorf("BoundariesExplanation must not contain modularity jargon, got: %q", plan.GetBoundariesExplanation())
	}
	if len(plan.GetServices()) != 0 {
		t.Errorf("expected 0 services, got %d", len(plan.GetServices()))
	}

	// Empty candidates should also work.
	plan2 := assemblePlan([]workerdomain.ServiceCandidate{}, workerdomain.DataOwnership{}, &workerdomain.ClusteringResult{})
	if !plan2.GetNoServiceBoundaries() {
		t.Error("expected NoServiceBoundaries=true when candidates=[]")
	}
}

// TestAssemblePlan_ModularityPropagated verifies that PartitionModularity from
// the ClusteringResult is written into the RestructurePlan for both the
// service-boundaries and no-boundaries paths.
func TestAssemblePlan_ModularityPropagated(t *testing.T) {
	t.Parallel()

	candidate := workerdomain.ServiceCandidate{
		Name:        "orders",
		ErrorPrefix: "ORD",
	}

	const q = 0.42
	plan := assemblePlan(
		[]workerdomain.ServiceCandidate{candidate},
		workerdomain.DataOwnership{},
		&workerdomain.ClusteringResult{Modularity: q},
	)
	if plan.GetPartitionModularity() != q {
		t.Errorf("service-boundaries path: got modularity=%v, want %v", plan.GetPartitionModularity(), q)
	}

	// No-boundaries path must also carry the modularity (useful for notiplan Q=0.14).
	planNoBound := assemblePlan(nil, workerdomain.DataOwnership{}, &workerdomain.ClusteringResult{Modularity: 0.14})
	if planNoBound.GetPartitionModularity() != 0.14 {
		t.Errorf("no-boundaries path: got modularity=%v, want 0.14", planNoBound.GetPartitionModularity())
	}
}

// fixedAllocator is a test-only PrefixAllocator stub.
type fixedAllocator struct {
	prefixes map[string]string
}

func (a *fixedAllocator) Allocate(_ context.Context, name string) (string, error) {
	if p, ok := a.prefixes[name]; ok {
		return p, nil
	}
	return strings.ToUpper(name[:3]), nil
}

// TestIsIncoherentFallback_StarTopology verifies that the guardrail fires when
// all clusters are isolated spokes with no internal edges — the notiplan case.
// Five singleton clusters, zero internal edges → coherent=0, 0*2 < 5 → true.
func TestIsIncoherentFallback_StarTopology(t *testing.T) {
	// Star topology: A…E each only connect to a hub (already removed from domain).
	// Domain sub-graph has zero edges.
	graph := &workerdomain.Graph{Edges: nil}
	clusters := []workerdomain.Cluster{
		{BlueprintGroup: "app.a", Modules: []workerdomain.Module{"app.a"}},
		{BlueprintGroup: "app.b", Modules: []workerdomain.Module{"app.b"}},
		{BlueprintGroup: "app.c", Modules: []workerdomain.Module{"app.c"}},
		{BlueprintGroup: "app.d", Modules: []workerdomain.Module{"app.d"}},
		{BlueprintGroup: "app.e", Modules: []workerdomain.Module{"app.e"}},
	}
	if !workerdomain.IsIncoherentFallback(graph, clusters) {
		t.Error("expected guardrail to fire for star-topology graph with zero internal edges")
	}
}

// TestIsIncoherentFallback_CoherentClusters verifies that the guardrail does NOT
// fire when every cluster has at least one internal edge — the "real domain with
// non-standard names" saved case. Two clusters, each with one internal edge.
func TestIsIncoherentFallback_CoherentClusters(t *testing.T) {
	graph := &workerdomain.Graph{Edges: []workerdomain.Edge{
		{From: "app.a1", To: "app.a2", Weight: 1}, // internal to cluster 0
		{From: "app.b1", To: "app.b2", Weight: 1}, // internal to cluster 1
	}}
	clusters := []workerdomain.Cluster{
		{BlueprintGroup: "app.a", Modules: []workerdomain.Module{"app.a1", "app.a2"}},
		{BlueprintGroup: "app.b", Modules: []workerdomain.Module{"app.b1", "app.b2"}},
	}
	if workerdomain.IsIncoherentFallback(graph, clusters) {
		t.Error("expected guardrail NOT to fire when all clusters have internal edges")
	}
}

// TestIsIncoherentFallback_Empty verifies that zero clusters returns false
// (nothing to evaluate — no boundaries is handled upstream).
func TestIsIncoherentFallback_Empty(t *testing.T) {
	if workerdomain.IsIncoherentFallback(&workerdomain.Graph{}, nil) {
		t.Error("expected false for nil clusters")
	}
	if workerdomain.IsIncoherentFallback(&workerdomain.Graph{}, []workerdomain.Cluster{}) {
		t.Error("expected false for empty clusters")
	}
}

// TestIsIncoherentFallback_ExactlyHalf verifies that exactly half coherent does
// NOT fire (strict less-than: coherent*2 < len).
// 2 clusters, 1 coherent → 1*2 < 2 is false → guardrail does not fire.
func TestIsIncoherentFallback_ExactlyHalf(t *testing.T) {
	graph := &workerdomain.Graph{Edges: []workerdomain.Edge{
		{From: "app.a1", To: "app.a2", Weight: 1}, // internal to cluster 0 only
	}}
	clusters := []workerdomain.Cluster{
		{BlueprintGroup: "app.a", Modules: []workerdomain.Module{"app.a1", "app.a2"}},
		{BlueprintGroup: "app.b", Modules: []workerdomain.Module{"app.b1"}}, // no internal edges
	}
	if workerdomain.IsIncoherentFallback(graph, clusters) {
		t.Error("expected guardrail NOT to fire when exactly half the clusters are coherent")
	}
}

// TestIsIncoherentFallback_MixedMajorityIncoherent verifies that 2 coherent
// clusters out of 8 (the live notiplan topology) fires the guardrail.
// 2*2=4 < 8 → true.
func TestIsIncoherentFallback_MixedMajorityIncoherent(t *testing.T) {
	// Simulate notiplan: two clusters have internal edges, six are singletons.
	graph := &workerdomain.Graph{Edges: []workerdomain.Edge{
		{From: "app.ingeteam", To: "app.plant_table", Weight: 2}, // cluster A internal
		{From: "app.parametros", To: "app.op_disp", Weight: 1},   // cluster B internal
	}}
	clusters := []workerdomain.Cluster{
		// Cluster A: two internal edges
		{BlueprintGroup: "app.main", Modules: []workerdomain.Module{"app.ingeteam", "app.plant_table"}},
		// Cluster B: one internal edge
		{BlueprintGroup: "app.ops", Modules: []workerdomain.Module{"app.parametros", "app.op_disp"}},
		// Singletons: no internal edges
		{BlueprintGroup: "app.itxi", Modules: []workerdomain.Module{"app.itxi"}},
		{BlueprintGroup: "app.itxi2", Modules: []workerdomain.Module{"app.itxi2"}},
		{BlueprintGroup: "app.create", Modules: []workerdomain.Module{"app.create"}},
		{BlueprintGroup: "app.reset", Modules: []workerdomain.Module{"app.reset"}},
		{BlueprintGroup: "app.session", Modules: []workerdomain.Module{"app.session"}},
		{BlueprintGroup: "app.tests", Modules: []workerdomain.Module{"app.test1", "app.test2"}},
	}
	if !workerdomain.IsIncoherentFallback(graph, clusters) {
		t.Error("expected guardrail to fire: 2 coherent out of 8 is a majority-incoherent result")
	}
}

// TestBuildTableServiceMap verifies that __tablename__ declarations are scanned
// from model files and correctly mapped to service names.
func TestBuildTableServiceMap(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(relPath, content string) {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	writeFile("conduit/user/models.py", `
class UserProfile(Model):
    __tablename__ = 'usersprofile'
    id = db.Column(db.Integer)
`)
	writeFile("conduit/articles/models.py", `
class Article(Model):
    __tablename__ = 'article'
    id = db.Column(db.Integer)

class Comment(Model):
    __tablename__ = 'comment'
    id = db.Column(db.Integer)
`)

	clusters := []workerdomain.Cluster{
		{
			BlueprintGroup: "conduit.user",
			Modules:        []workerdomain.Module{"conduit.user.models"},
		},
		{
			BlueprintGroup: "conduit.articles",
			Modules:        []workerdomain.Module{"conduit.articles.models"},
		},
	}

	m := buildTableServiceMap(dir, clusters)

	cases := []struct{ table, wantSvc string }{
		{"usersprofile", "user"},
		{"article", "articles"},
		{"comment", "articles"},
	}
	for _, c := range cases {
		if got := m[c.table]; got != c.wantSvc {
			t.Errorf("table %q → service: got %q, want %q", c.table, got, c.wantSvc)
		}
	}
}
