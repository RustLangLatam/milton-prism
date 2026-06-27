// Package adapters contains the infrastructure adapters for the decomposition worker.
package adapters

import (
	"context"
	"errors"
	"fmt"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var _ ports.GraphLoader = (*MongoGraphLoader)(nil)
var _ ports.SummaryLoader = (*MongoGraphLoader)(nil)

// analysisSummaryGraphDoc is a projection of the analysis_summaries document
// containing only the fields needed by the GraphLoader.
type analysisSummaryGraphDoc struct {
	Identifier           uint64 `bson:"identifier"`
	DependencyGraphBytes []byte `bson:"dependency_graph_bytes,omitempty"`
}

// analysisSummaryCardsDoc is a projection for loading module cards, blueprints,
// and technologies from the analysis_summaries document.
type analysisSummaryCardsDoc struct {
	Identifier        uint64 `bson:"identifier"`
	ModuleCardsBytes  []byte `bson:"module_cards_bytes,omitempty"`
	BlueprintsBytes   []byte `bson:"blueprints_bytes,omitempty"`
	TechnologiesBytes []byte `bson:"technologies_bytes,omitempty"`
}

// analysisSummaryAvailabilityDoc projects the explicit deep-analysis-availability
// signal set by the analysis pipeline.
type analysisSummaryAvailabilityDoc struct {
	Identifier            uint64 `bson:"identifier"`
	DeepAnalysisAvailable bool   `bson:"deep_analysis_available,omitempty"`
}

// MongoGraphLoader implements ports.GraphLoader by reading the persisted
// dependency_graph bytes from the analysis_summaries MongoDB collection.
type MongoGraphLoader struct {
	coll *mongo.Collection
}

// NewMongoGraphLoader returns a MongoGraphLoader targeting the given analysis database.
func NewMongoGraphLoader(db *mongo.Database) *MongoGraphLoader {
	return &MongoGraphLoader{coll: db.Collection("analysis_summaries")}
}

// Load retrieves the dependency graph for the given analysis summary identifier.
// An empty graph (no edges) is returned when the summary exists but has no graph data.
func (l *MongoGraphLoader) Load(ctx context.Context, summaryID uint64) (*workerdomain.Graph, error) {
	var doc analysisSummaryGraphDoc
	err := l.coll.FindOne(ctx, bson.M{"identifier": summaryID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("decomposition: analysis summary %d not found", summaryID)
		}
		return nil, fmt.Errorf("decomposition: load graph: %w", err)
	}

	if len(doc.DependencyGraphBytes) == 0 {
		return &workerdomain.Graph{}, nil
	}

	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(doc.DependencyGraphBytes, wrapper); err != nil {
		return nil, fmt.Errorf("decomposition: unmarshal dependency_graph: %w", err)
	}

	edges := make([]workerdomain.Edge, 0, len(wrapper.GetDependencyGraph()))
	for _, e := range wrapper.GetDependencyGraph() {
		edges = append(edges, workerdomain.Edge{
			From:   workerdomain.Module(e.GetFromModule()),
			To:     workerdomain.Module(e.GetToModule()),
			Weight: e.GetWeight(),
		})
	}
	return &workerdomain.Graph{Edges: edges}, nil
}

// LoadDeepAnalysisAvailable returns the explicit deep-analysis-availability flag
// the analysis pipeline persisted. A missing summary is an error; a missing flag
// (older summaries predating the field) decodes as false. The migrability
// assessor uses this to degrade honestly instead of scoring an empty graph.
func (l *MongoGraphLoader) LoadDeepAnalysisAvailable(ctx context.Context, summaryID uint64) (bool, error) {
	var doc analysisSummaryAvailabilityDoc
	err := l.coll.FindOne(ctx, bson.M{"identifier": summaryID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, fmt.Errorf("decomposition: analysis summary %d not found", summaryID)
		}
		return false, fmt.Errorf("decomposition: load deep_analysis_available: %w", err)
	}
	return doc.DeepAnalysisAvailable, nil
}

// LoadCards reads module cards, blueprints, and technologies for the given
// analysis summary. Returns an empty SummaryCards (no error) when the summary
// exists but has no card data — e.g. non-Python repositories.
func (l *MongoGraphLoader) LoadCards(ctx context.Context, summaryID uint64) (*workerdomain.SummaryCards, error) {
	var doc analysisSummaryCardsDoc
	err := l.coll.FindOne(ctx, bson.M{"identifier": summaryID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("decomposition: analysis summary %d not found", summaryID)
		}
		return nil, fmt.Errorf("decomposition: load cards: %w", err)
	}

	result := &workerdomain.SummaryCards{}

	if len(doc.ModuleCardsBytes) > 0 {
		wrapper := &analysisv1.AnalysisSummary{}
		if err := proto.Unmarshal(doc.ModuleCardsBytes, wrapper); err != nil {
			return nil, fmt.Errorf("decomposition: unmarshal module_cards: %w", err)
		}
		for _, c := range wrapper.GetModuleCards() {
			card := workerdomain.SummaryModuleCard{
				Module:    c.GetModule(),
				File:      c.GetFile(),
				Functions: c.GetFunctions(),
				Classes:   c.GetClasses(),
				State:     c.GetModuleLevelState(),
				Docstring: c.GetDocstringHead(),
				LOC:       c.GetLoc(),
			}
			for _, r := range c.GetRoutes() {
				card.Routes = append(card.Routes, workerdomain.SummaryRoute{
					Method:  r.GetMethod(),
					Path:    r.GetPath(),
					Handler: r.GetHandler(),
				})
			}
			result.ModuleCards = append(result.ModuleCards, card)
		}
	}

	if len(doc.BlueprintsBytes) > 0 {
		wrapper := &analysisv1.AnalysisSummary{}
		if err := proto.Unmarshal(doc.BlueprintsBytes, wrapper); err != nil {
			return nil, fmt.Errorf("decomposition: unmarshal blueprints: %w", err)
		}
		for _, bp := range wrapper.GetBlueprints() {
			result.Blueprints = append(result.Blueprints, workerdomain.SummaryBlueprint{
				Name:      bp.GetName(),
				File:      bp.GetFile(),
				URLPrefix: bp.GetUrlPrefix(),
			})
		}
	}

	if len(doc.TechnologiesBytes) > 0 {
		wrapper := &analysisv1.AnalysisSummary{}
		if err := proto.Unmarshal(doc.TechnologiesBytes, wrapper); err != nil {
			return nil, fmt.Errorf("decomposition: unmarshal technologies: %w", err)
		}
		for _, t := range wrapper.GetTechnologies() {
			result.Technologies = append(result.Technologies, t.GetName())
			// First framework-category technology wins. This is ecosystem-agnostic
			// by design: it trusts the upstream technologies list to be coherent.
			// That trust is now upheld at write time — injectInferredFramework
			// (analysis pipeline) gates inferred frameworks on the PRIMARY language,
			// so a polyglot repo no longer carries a secondary language's framework
			// (e.g. the "GO·Flask" false positive). Selecting the framework whose
			// ecosystem matches the primary language is NOT done here: this loader
			// only sees flattened technology NAMES (language and framework entries
			// are indistinguishable once appended), so it has no primary-language
			// context. True ecosystem-matching for display is the caller/panel's
			// responsibility.
			if t.GetCategory() == "framework" && result.Framework == "" {
				result.Framework = t.GetName()
			}
		}
	}

	return result, nil
}
