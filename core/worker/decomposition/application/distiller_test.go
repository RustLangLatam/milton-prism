package application

import (
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conduitGraph mimics the Conduit dependency graph: 3 blueprint groups, clear
// domain layer, model edges crossing service boundaries.
func conduitGraph() *workerdomain.Graph {
	return &workerdomain.Graph{
		Edges: []workerdomain.Edge{
			{From: "conduit.articles.views", To: "conduit.articles.models", Weight: 2},
			{From: "conduit.articles.views", To: "conduit.user.models", Weight: 1},
			{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
			{From: "conduit.profile.views", To: "conduit.profile.models", Weight: 2},
			{From: "conduit.profile.views", To: "conduit.user.models", Weight: 1},
			{From: "conduit.user.views", To: "conduit.user.models", Weight: 2},
			{From: "conduit.user.views", To: "conduit.profile.models", Weight: 1},
		},
	}
}

func conduitClassification() *workerdomain.Classification {
	return &workerdomain.Classification{
		Domain: []workerdomain.Module{
			"conduit.articles.models",
			"conduit.articles.views",
			"conduit.profile.models",
			"conduit.profile.views",
			"conduit.user.models",
			"conduit.user.views",
		},
		Infra: []workerdomain.Module{"conduit.app", "conduit.config"},
	}
}

func conduitClusterResult() *workerdomain.ClusteringResult {
	return &workerdomain.ClusteringResult{
		Clusters: []workerdomain.Cluster{
			{
				BlueprintGroup: "conduit.articles",
				Modules: []workerdomain.Module{
					"conduit.articles.views",
					"conduit.articles.models",
				},
			},
			{
				BlueprintGroup: "conduit.profile",
				Modules: []workerdomain.Module{
					"conduit.profile.views",
					"conduit.profile.models",
				},
			},
			{
				BlueprintGroup: "conduit.user",
				Modules: []workerdomain.Module{
					"conduit.user.views",
					"conduit.user.models",
				},
			},
		},
		Modularity:    0.42,
		LowConfidence: false,
	}
}

// notiplanGraph mimics notiplan: a god module that everything imports.
func notiplanGraph() *workerdomain.Graph {
	return &workerdomain.Graph{
		Edges: []workerdomain.Edge{
			{From: "backend.routes", To: "backend.funcs", Weight: 3},
			{From: "backend.auth", To: "backend.funcs", Weight: 2},
			{From: "backend.tasks", To: "backend.funcs", Weight: 2},
			{From: "backend.models", To: "backend.funcs", Weight: 1},
			{From: "backend.utils", To: "backend.funcs", Weight: 1},
		},
	}
}

func notiplanClassification() *workerdomain.Classification {
	return &workerdomain.Classification{
		// Infra detector classifies everything as infra when no blueprint groups exist
		Infra: []workerdomain.Module{
			"backend.funcs",
			"backend.routes",
			"backend.auth",
			"backend.tasks",
			"backend.models",
			"backend.utils",
		},
		Domain: nil,
	}
}

func notiplanClusterResult() *workerdomain.ClusteringResult {
	return &workerdomain.ClusteringResult{
		Clusters:      nil, // 0 clusters = NoServiceBoundaries
		Modularity:    0.0,
		LowConfidence: true,
	}
}

func conduitSummaryCards() *workerdomain.SummaryCards {
	return &workerdomain.SummaryCards{
		Technologies: []string{"Python", "Flask", "SQLAlchemy"},
		Framework:    "Flask",
		ModuleCards: []workerdomain.SummaryModuleCard{
			{
				Module:    "conduit.articles.models",
				File:      "conduit/articles/models.py",
				Classes:   []string{"Article", "Comment", "Tags"},
				Docstring: "Article and comment domain models.",
				LOC:       120,
			},
			{
				Module:    "conduit.articles.views",
				File:      "conduit/articles/views.py",
				Functions: []string{"list_articles", "get_article", "create_article"},
				Routes: []workerdomain.SummaryRoute{
					{Method: "GET", Path: "/articles", Handler: "list_articles"},
					{Method: "GET", Path: "/articles/<slug>", Handler: "get_article"},
					{Method: "POST", Path: "/articles", Handler: "create_article"},
				},
				LOC: 200,
			},
			{
				Module:  "conduit.profile.models",
				File:    "conduit/profile/models.py",
				Classes: []string{"UserProfile"},
				LOC:     60,
			},
			{
				Module:  "conduit.user.models",
				File:    "conduit/user/models.py",
				Classes: []string{"User"},
				LOC:     80,
			},
		},
		Blueprints: []workerdomain.SummaryBlueprint{
			{Name: "articles", File: "conduit/articles/__init__.py", URLPrefix: "/articles"},
			{Name: "profile", File: "conduit/profile/__init__.py", URLPrefix: "/profiles"},
			{Name: "user", File: "conduit/user/__init__.py", URLPrefix: "/user"},
		},
	}
}

func notiplanSummaryCards() *workerdomain.SummaryCards {
	return &workerdomain.SummaryCards{
		Technologies: []string{"Python", "Flask"},
		Framework:    "Flask",
		ModuleCards: []workerdomain.SummaryModuleCard{
			{
				Module:    "backend.funcs",
				File:      "backend/funcs.py",
				Functions: makeNames("fn", 55),
				State:     []string{"manager_id_mesa_masters", "params"},
				LOC:       1161,
			},
			{
				Module:    "backend.routes",
				File:      "backend/routes.py",
				Functions: []string{"index"},
				Routes:    []workerdomain.SummaryRoute{{Method: "GET", Path: "/rest", Handler: "index"}},
				LOC:       30,
			},
		},
		Blueprints: []workerdomain.SummaryBlueprint{
			{Name: "rest", File: "backend/ingeteam_backend.py", URLPrefix: "/rest"},
		},
	}
}

func makeNames(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix
	}
	return out
}

// ── CodeIgniter 3 fixtures ──────────────────────────────────────────────────────
//
// CI3 application classes carry no namespace and no module-level mutable state:
// the analyzer cannot extract State. The honest, extractable coupling signal is
// structural fan-in. A CI3 card is recognised by Module == File ending in .php
// (extractCI3Cards sets both to the workspace-relative path). These fixtures model
// a god-model loaded by many controllers (Users_model, 51 methods) and a base
// class everyone extends (MY_Controller).

// ci3Graph mirrors a CodeIgniter 3 dependency graph keyed by .php file paths:
// many controllers load application/models/Users_model.php (fan-in 6) and extend
// application/core/MY_Controller.php (fan-in 6).
func ci3Graph() *workerdomain.Graph {
	var edges []workerdomain.Edge
	controllers := []string{
		"application/controllers/Admin.php",
		"application/controllers/Users.php",
		"application/controllers/Memo.php",
		"application/controllers/Workers.php",
		"application/controllers/Reports.php",
		"application/controllers/Auth.php",
	}
	for _, ctl := range controllers {
		edges = append(edges,
			workerdomain.Edge{From: workerdomain.Module(ctl), To: "application/models/Users_model.php", Weight: 1},
			workerdomain.Edge{From: workerdomain.Module(ctl), To: "application/core/MY_Controller.php", Weight: 1},
		)
	}
	return &workerdomain.Graph{Edges: edges}
}

func ci3Classification() *workerdomain.Classification {
	return &workerdomain.Classification{
		// CI3 convention modules fall through to the structural-fallback infra bucket
		// (no namespace heuristics apply); domain stays empty as in the real run.
		Infra: []workerdomain.Module{
			"application/models/Users_model.php",
			"application/core/MY_Controller.php",
			"application/controllers/Admin.php",
		},
		StructuralFallback: true,
	}
}

func ci3ClusterResult() *workerdomain.ClusteringResult {
	return &workerdomain.ClusteringResult{Clusters: nil, LowConfidence: true}
}

// ci3SummaryCards models the extracted CI3 cards: Users_model with 51 methods and
// NO module-level state (State is empty — unextractable for CI3), plus the
// MY_Controller base class. Module == File for every card, the CI3 marker.
func ci3SummaryCards() *workerdomain.SummaryCards {
	return &workerdomain.SummaryCards{
		Technologies: []string{"PHP", "CodeIgniter"},
		Framework:    "CodeIgniter",
		ModuleCards: []workerdomain.SummaryModuleCard{
			{
				Module:    "application/models/Users_model.php",
				File:      "application/models/Users_model.php",
				Functions: makeNames("method", 51),
				Classes:   []string{"class:Users_model"},
				LOC:       900,
			},
			{
				Module:    "application/core/MY_Controller.php",
				File:      "application/core/MY_Controller.php",
				Functions: makeNames("method", 8),
				Classes:   []string{"class:MY_Controller"},
				LOC:       300,
			},
			{
				Module:    "application/controllers/Admin.php",
				File:      "application/controllers/Admin.php",
				Functions: makeNames("action", 12),
				Classes:   []string{"class:Admin"},
				LOC:       200,
			},
		},
	}
}

// ── conduitWithHub fixtures ────────────────────────────────────────────────────
//
// These fixtures extend the base Conduit case with a shared-state hub
// (conduit.database, FanIn=12) so that the scorer emits a non-empty typed_blockers
// list despite a MIGRABLE verdict. The fixture is crafted for one specific test:
// "MIGRABLE with typed_blockers=[blocker.shared_state]" — the regression case for
// the frontend guard that must NOT display structural blockers in the MIGRABLE panel.
//
// Score design (post-ramp): domain_presence low (7, ramp from ratio 25%) +
// hub_severity severe (20, hubRatio≈0.63 capped) + routing_layout single_blueprint
// (5) = 32 → score=68 → band=MEDIUM. (Pre-ramp the domain step was 10 → score=65;
// the ramp moved the intermediate penalty but kept the band MEDIUM.)

// conduitWithHubGraph extends conduitGraph() with 6 directed edges into conduit.database
// (weight 2 each), giving FanIn=12. The 7-node graph produces hubRatio=12/19≈0.63
// (≥0.5 = severe severity in the scorer).
func conduitWithHubGraph() *workerdomain.Graph {
	base := conduitGraph()
	domainSources := []string{
		"conduit.articles.views",
		"conduit.articles.models",
		"conduit.profile.views",
		"conduit.profile.models",
		"conduit.user.views",
		"conduit.user.models",
	}
	extra := make([]workerdomain.Edge, len(domainSources))
	for i, src := range domainSources {
		extra[i] = workerdomain.Edge{From: workerdomain.Module(src), To: "conduit.database", Weight: 2}
	}
	return &workerdomain.Graph{Edges: append(base.Edges, extra...)}
}

// conduitWithHubClassification sets domain=[articles.models, user.models] (2 modules)
// and infra=6 modules (ratio=25%), triggering signal.domain_presence.low (penalty=10).
// The 3-cluster result is kept so cluster_count stays at penalty=0 — the hub is the
// only new structural issue, which is what this fixture exists to test.
func conduitWithHubClassification() *workerdomain.Classification {
	return &workerdomain.Classification{
		Domain: []workerdomain.Module{
			"conduit.articles.models",
			"conduit.user.models",
		},
		Infra: []workerdomain.Module{
			"conduit.app",
			"conduit.config",
			"conduit.articles.views",
			"conduit.profile.views",
			"conduit.profile.models",
			"conduit.database",
		},
	}
}

// conduitWithHubCards returns SummaryCards with conduit.database added (State present
// so the distiller marks it IsSharedStateHub when FanIn≥2) and a single blueprint,
// which triggers signal.routing_layout.single_blueprint (penalty=5).
func conduitWithHubCards() *workerdomain.SummaryCards {
	base := conduitSummaryCards()
	base.ModuleCards = append(base.ModuleCards, workerdomain.SummaryModuleCard{
		Module: "conduit.database",
		File:   "conduit/database.py",
		State:  []string{"db", "session"},
		LOC:    80,
	})
	// Single blueprint collapses the 3 per-domain blueprints into one app blueprint,
	// triggering the routing_layout.single_blueprint signal (penalty=5).
	base.Blueprints = []workerdomain.SummaryBlueprint{
		{Name: "api", File: "conduit/app.py", URLPrefix: "/api"},
	}
	return base
}

// ── Conduit tests ─────────────────────────────────────────────────────────────

func TestDistill_Conduit_ThreeClusters(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	require.Len(t, d.Clusters, 3)
	assert.False(t, d.NoServiceBoundaries)
}

func TestDistill_Conduit_HasDomainModules(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.False(t, d.Classification.DomainEmpty)
	assert.NotEmpty(t, d.Classification.DomainModules)
	assert.NotEmpty(t, d.Classification.InfraModules)
}

func TestDistill_Conduit_BlueprintCount(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.Equal(t, 3, d.EntryPoints.BlueprintCount)
	assert.False(t, d.EntryPoints.SingleBlueprint)
	assert.True(t, d.EntryPoints.HasHTTPRoutes)
	assert.Equal(t, 3, d.EntryPoints.TotalRoutes)
}

func TestDistill_Conduit_Framework(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.Equal(t, "Flask", d.Framework)
}

func TestDistill_Conduit_FanInFanOut(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)

	byModule := make(map[string]workerdomain.DigestModuleCard, len(d.ModuleCards))
	for _, c := range d.ModuleCards {
		byModule[c.Module] = c
	}

	// articles.models is imported by articles.views (weight=2) — fan-in=2, fan-out=1 (→ profile.models)
	artModels := byModule["conduit.articles.models"]
	assert.Equal(t, uint32(2), artModels.FanIn, "articles.models fan-in from views (weight 2)")
	assert.Equal(t, uint32(1), artModels.FanOut, "articles.models fan-out to profile.models")
}

func TestDistill_Conduit_NoSharedStateHubs(t *testing.T) {
	// conduit modules have no module-level mutable state.
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.Empty(t, d.SharedStateHubs)
}

func TestDistill_Conduit_AllModulesSampled(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.Equal(t, d.TotalModules, d.SampledModules)
	assert.Nil(t, d.AggregateCard)
}

// ── Notiplan tests ─────────────────────────────────────────────────────────────

func TestDistill_Notiplan_ZeroClusters(t *testing.T) {
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	assert.Empty(t, d.Clusters)
	assert.True(t, d.NoServiceBoundaries)
}

func TestDistill_Notiplan_DomainEmpty(t *testing.T) {
	// notiplan has no domain modules — the "acantilado" signal.
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	assert.True(t, d.Classification.DomainEmpty)
	assert.Empty(t, d.Classification.DomainModules)
}

func TestDistill_Notiplan_SharedStateHub(t *testing.T) {
	// backend.funcs has mutable state and fan-in ≥ 2 from multiple modules.
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	require.Len(t, d.SharedStateHubs, 1)
	hub := d.SharedStateHubs[0]
	assert.Equal(t, "backend.funcs", hub.Module)
	assert.Contains(t, hub.State, "manager_id_mesa_masters")
	assert.GreaterOrEqual(t, hub.FanIn, uint32(2))
}

func TestDistill_Notiplan_SingleBlueprint(t *testing.T) {
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	assert.True(t, d.EntryPoints.SingleBlueprint)
	assert.Equal(t, 1, d.EntryPoints.BlueprintCount)
}

func TestDistill_Notiplan_GodModuleFuncs(t *testing.T) {
	// backend.funcs is the god module with 55 functions.
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	byModule := make(map[string]workerdomain.DigestModuleCard, len(d.ModuleCards))
	for _, c := range d.ModuleCards {
		byModule[c.Module] = c
	}
	funcs := byModule["backend.funcs"]
	assert.GreaterOrEqual(t, len(funcs.Functions), 50, "god module must have ≥50 functions")
}

// ── Cap tests ─────────────────────────────────────────────────────────────────

func TestDistill_TopKCap(t *testing.T) {
	// With topK=2, only the 2 highest-degree modules get full cards; the rest
	// are collapsed into AggregateCard.
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 2)
	assert.Equal(t, 4, d.TotalModules)
	assert.Equal(t, 2, d.SampledModules)
	assert.Len(t, d.ModuleCards, 2)
	require.NotNil(t, d.AggregateCard)
	assert.Equal(t, 2, d.AggregateCard.ModuleCount)
}

func TestDistill_NilData_ReturnsValidDigest(t *testing.T) {
	// A nil SummaryCards is valid: non-Python repos have no card data.
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), nil, 0)
	assert.NotNil(t, d)
	assert.Empty(t, d.ModuleCards)
	assert.Empty(t, d.Blueprints)
	assert.Empty(t, d.Technologies)
}

func TestDistill_Deterministic(t *testing.T) {
	// Running Distill twice with the same inputs must produce identical output.
	t.Parallel()
	g := conduitGraph()
	cls := conduitClassification()
	cr := conduitClusterResult()
	cards := conduitSummaryCards()
	d1 := Distill(g, cls, cr, cards, 0)
	d2 := Distill(g, cls, cr, cards, 0)

	require.Equal(t, d1.Clusters, d2.Clusters)
	require.Equal(t, d1.ModuleCards, d2.ModuleCards)
	require.Equal(t, d1.SharedStateHubs, d2.SharedStateHubs)
	require.Equal(t, d1.Classification, d2.Classification)
}

// ── CI3 hub/god-module distillation ─────────────────────────────────────────────

func TestDistill_CI3_StructuralHubsByFanIn(t *testing.T) {
	// CI3 cards carry no module-level State, yet the god-model (fan-in 6) and the
	// MY_Controller base class (fan-in 6) must surface as shared-state hubs by their
	// structural fan-in alone (the convention-routed regime).
	t.Parallel()
	d := Distill(ci3Graph(), ci3Classification(), ci3ClusterResult(), ci3SummaryCards(), 0)

	require.NotEmpty(t, d.SharedStateHubs, "CI3 high-fan-in modules must become hubs despite empty State")
	byModule := make(map[string]workerdomain.DigestSharedStateHub, len(d.SharedStateHubs))
	for _, h := range d.SharedStateHubs {
		byModule[h.Module] = h
	}
	usersHub, ok := byModule["application/models/Users_model.php"]
	require.True(t, ok, "Users_model must be a structural hub")
	assert.GreaterOrEqual(t, usersHub.FanIn, uint32(2))
	assert.Empty(t, usersHub.State, "CI3 hub carries no extracted mutable state — honest empty")

	_, ok = byModule["application/core/MY_Controller.php"]
	assert.True(t, ok, "MY_Controller base class must be a structural hub")
}

func TestDistill_CI3_GodModuleHubFlag(t *testing.T) {
	// Users_model has 51 methods AND high fan-in → must carry IsSharedStateHub so
	// the scorer's god-module gate (>=20 funcs AND IsSharedStateHub) fires.
	t.Parallel()
	d := Distill(ci3Graph(), ci3Classification(), ci3ClusterResult(), ci3SummaryCards(), 0)
	var users workerdomain.DigestModuleCard
	for _, c := range d.ModuleCards {
		if c.Module == "application/models/Users_model.php" {
			users = c
		}
	}
	require.Equal(t, "application/models/Users_model.php", users.Module)
	assert.GreaterOrEqual(t, len(users.Functions), godFunctionThreshold)
	assert.True(t, users.IsSharedStateHub, "CI3 god-model must be flagged as shared-state hub")
}

func TestDistill_NonCI3_NoStructuralHubFromFanIn(t *testing.T) {
	// Guard: a Python/PSR-4 module with high fan-in but no State must NOT become a
	// hub. Only the convention-routed (Module==File .php) regime relaxes the State
	// requirement; Conduit (dotted names, no State) keeps zero hubs.
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	assert.Empty(t, d.SharedStateHubs, "non-CI3 high-fan-in modules without State must not become hubs")
}
