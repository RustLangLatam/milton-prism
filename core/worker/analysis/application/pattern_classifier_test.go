package application

import (
	"strconv"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

// scoreWithSignals builds a MigrabilityScore carrying the given cluster count and
// hub penalty so the classifier's signal readers have something to read.
func scoreWith(clusterCount int, hubPenalty int32) *commonv1.MigrabilityScore {
	return &commonv1.MigrabilityScore{
		Signals: []*commonv1.ScoreSignal{
			{
				Signal:       "cluster_count",
				DetailParams: map[string]string{"count": strconv.Itoa(clusterCount)},
			},
			{Signal: "hub_severity", Penalty: hubPenalty},
		},
	}
}

func fw(slug string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: slug, Category: "framework", Slug: slug}
}

func cls(domain, infra, app, test []string, fallback bool) *analysisdomain.ModuleClassification {
	return &analysisdomain.ModuleClassification{
		DomainModules:      domain,
		InfraModules:       infra,
		ApplicationModules: app,
		TestModules:        test,
		StructuralFallback: fallback,
	}
}

func names(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return out
}

func TestClassify_SpaghettiWhenNoDomain(t *testing.T) {
	t.Parallel()
	// notiplan-like: deep analysis present but no domain layer at all.
	in := patternInput{
		classification: cls(nil, names(12, "infra."), nil, nil, true),
		score:          scoreWith(0, 0),
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindSpaghetti {
		t.Fatalf("want Spaghetti, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_SpaghettiWhenDeepUnavailable(t *testing.T) {
	t.Parallel()
	in := patternInput{
		classification: cls(names(3, "d."), names(2, "i."), nil, nil, false),
		score:          scoreWith(0, 0),
		deepAvailable:  false,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindSpaghetti {
		t.Fatalf("want Spaghetti (blind), got %v", got.GetKind())
	}
	if got.GetConfidence() > 0.7 {
		t.Fatalf("blind classification must have reduced confidence, got %.2f", got.GetConfidence())
	}
}

func TestClassify_MVCFromLaravelWithRoutes(t *testing.T) {
	t.Parallel()
	// BookStack-like: Laravel MVC with controllers + routes.
	cards := []*analysisdomain.ModuleCard{
		{Module: `App\Http\Controllers\BookController`, Routes: []*analysisdomain.RouteInfo{{Method: "GET", Path: "/books"}}},
	}
	in := patternInput{
		classification: cls(names(20, "App.Models."), names(8, "App.Providers."), names(5, "App.Http."), nil, false),
		score:          scoreWith(4, 0),
		cards:          cards,
		technologies:   []*analysisdomain.Technology{fw("laravel")},
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindMVC {
		t.Fatalf("want MVC, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_MVCFromCodeIgniterBlueprints(t *testing.T) {
	t.Parallel()
	// eurofunding-like (CI3): MVC framework, routes via convention. Routing surfaces
	// through blueprints OR cards; here we use a card route to stand in.
	cards := []*analysisdomain.ModuleCard{
		{Module: "application/controllers/Users.php", Routes: []*analysisdomain.RouteInfo{{Method: "GET", Path: "/users"}}},
	}
	in := patternInput{
		classification: cls(names(2, "d."), names(16, "i."), nil, nil, true),
		score:          scoreWith(2, 14),
		cards:          cards,
		technologies:   []*analysisdomain.Technology{fw("codeigniter")},
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindMVC {
		t.Fatalf("want MVC for CodeIgniter, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_MVCFrameworkWithoutExtractedRoutes(t *testing.T) {
	t.Parallel()
	// BookStack/eurofunding-like: PHP MVC framework but the analyzer emitted no
	// RouteInfo (controllers wired by the framework router). Must still be MVC.
	in := patternInput{
		classification: cls(names(20, "App.Models."), names(8, "App.Providers."), nil, nil, false),
		score:          scoreWith(17, 12),
		technologies:   []*analysisdomain.Technology{fw("laravel")},
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindMVC {
		t.Fatalf("want MVC for Laravel without routes, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_LayeredWhenClustersCoupledByHub(t *testing.T) {
	t.Parallel()
	// Conduit-like: 3 clusters, healthy ratio, but a dominant hub couples them.
	in := patternInput{
		classification: cls(names(12, "conduit.domain."), names(9, "conduit.infra."), nil, nil, false),
		score:          scoreWith(3, 16), // dominant hub present
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindLayered {
		t.Fatalf("want Layered (clusters coupled by hub), got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_LayeredFlaskConduit(t *testing.T) {
	t.Parallel()
	// flask-realworld-like: domain present, healthy ratio, 3 clusters, Flask (not an
	// MVC framework here — Flask is a microframework, no controllers layer), routes
	// in a single blueprint. Should land Modular monolith (≥3 clusters, ratio ok)
	// OR Layered — both are acceptable "Layered/MVC" per the oracle. Assert it is
	// NOT spaghetti and names a structured pattern.
	bps := []*analysisdomain.BlueprintInfo{{Name: "articles"}, {Name: "user"}, {Name: "profile"}}
	in := patternInput{
		classification: cls(names(12, "conduit.domain."), names(9, "conduit.infra."), nil, names(8, "conduit.tests."), false),
		score:          scoreWith(3, 16),
		blueprints:     bps,
		technologies:   []*analysisdomain.Technology{fw("flask")},
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() == analysisdomain.APKindSpaghetti || got.GetKind() == analysisdomain.APKindUnspecified {
		t.Fatalf("Conduit must classify as a structured pattern, got %v", got.GetKind())
	}
}

func TestClassify_ModularMonolith(t *testing.T) {
	t.Parallel()
	in := patternInput{
		classification: cls(names(15, "d."), names(5, "i."), nil, nil, false),
		score:          scoreWith(5, 0), // many clusters, no dominant hub
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindModularMonolith {
		t.Fatalf("want Modular monolith, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_HexagonalWithAppLayer(t *testing.T) {
	t.Parallel()
	in := patternInput{
		classification: cls(names(10, "d."), names(4, "i."), names(3, "app."), nil, false),
		score:          scoreWith(2, 0), // app layer present, strong ratio, <3 clusters
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindHexagonal {
		t.Fatalf("want Hexagonal, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_LayeredFallback(t *testing.T) {
	t.Parallel()
	in := patternInput{
		classification: cls(names(3, "d."), names(7, "i."), nil, nil, false),
		score:          scoreWith(1, 0), // low ratio, 1 cluster, no app layer
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetKind() != analysisdomain.APKindLayered {
		t.Fatalf("want Layered, got %v (%s)", got.GetKind(), got.GetName())
	}
}

func TestClassify_ConfidenceClamped(t *testing.T) {
	t.Parallel()
	in := patternInput{
		classification: cls(names(20, "App.Models."), names(8, "i."), names(5, "App.Http."), nil, false),
		score:          scoreWith(4, 0),
		cards:          []*analysisdomain.ModuleCard{{Routes: []*analysisdomain.RouteInfo{{Method: "GET"}}}},
		technologies:   []*analysisdomain.Technology{fw("laravel")},
		deepAvailable:  true,
	}
	got := classifyArchitecturalPattern(in)
	if got.GetConfidence() < 0 || got.GetConfidence() > 1 {
		t.Fatalf("confidence out of range: %.2f", got.GetConfidence())
	}
}
