//go:build integration

// Validation test for fix #7 (unique fan-in in PHPAwareInfraDetector fallback).
//
// Run:
//
//	go test ./core/worker/decomposition/infrastructure/adapters/... -tags integration -run TestFix7_BookStack_FanInEdgesVsUnique -v
//
// Purpose: determine whether fix #7 (unique importers vs raw edge count) produces an
// observable change in BookStack's classification by comparing the two algorithms on
// the actual BookStack dependency graph (analysis summary 10021, migration 10014).
package adapters_test

import (
	"context"
	"sort"
	"testing"

	"milton_prism/core/shared/phpclassify"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	adapters "milton_prism/core/worker/decomposition/infrastructure/adapters"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestFix7_BookStack_FanInEdgesVsUnique answers three questions:
//
//  1. How many BookStack modules have edge fan-in ≠ unique-importer fan-in?
//     (i.e., how many (from, to) edge pairs are NOT unique in the stored graph)
//  2. Of those, does any module change classification (domain↔infra) between
//     old (edge count) and new (unique importers) algorithm?
//  3. Conclusion: validated in vivo by observable effect, or covered by unit test
//     without effect in this data set?
func TestFix7_BookStack_FanInEdgesVsUnique(t *testing.T) {
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:bimtra654@localhost:27017/?authSource=admin&directConnection=true"))
	if err != nil {
		t.Skipf("MongoDB unavailable: %v", err)
	}
	defer client.Disconnect(ctx) //nolint:errcheck

	loader := adapters.NewMongoGraphLoader(client.Database("milton_prism_analysis"))

	// BookStack post-fix: migration 10014 → analysis_summary_id 10021.
	const bookStackSummaryID = 10021
	graph, err := loader.Load(ctx, bookStackSummaryID)
	if err != nil {
		t.Skipf("summary %d not found: %v", bookStackSummaryID, err)
	}

	t.Logf("BookStack graph: %d edges total", len(graph.Edges))

	// ── Step 1: count (from, to) duplicates in the stored graph. ─────────────
	// The PHP builder deduplicates via seen map; this confirms it empirically.
	type edgeKey struct{ from, to workerdomain.Module }
	edgeSeen := make(map[edgeKey]int, len(graph.Edges))
	for _, e := range graph.Edges {
		edgeSeen[edgeKey{e.From, e.To}]++
	}
	var duplicatePairs int
	for _, count := range edgeSeen {
		if count > 1 {
			duplicatePairs++
		}
	}
	t.Logf("(from, to) pairs with >1 edge: %d (should be 0 for PHP-only repos)", duplicatePairs)

	// ── Step 2: identify unmatched modules (fallback path). ──────────────────
	all := graph.AllModules()
	var unmatched []workerdomain.Module
	for _, m := range all {
		if phpclassify.LayerOf(string(m)) == "" {
			unmatched = append(unmatched, m)
		}
	}
	sort.Slice(unmatched, func(i, j int) bool { return unmatched[i] < unmatched[j] })
	t.Logf("total modules in graph: %d", len(all))
	t.Logf("unmatched modules (fallback path): %d", len(unmatched))

	threshold := len(unmatched) / 4
	if threshold < 2 {
		threshold = 2
	}
	t.Logf("fallback threshold: %d (max(2, %d/4))", threshold, len(unmatched))

	// ── Step 3: compute edge fan-in (old) vs unique fan-in (new) per unmatched module. ──
	unmatchedSet := make(map[workerdomain.Module]bool, len(unmatched))
	for _, m := range unmatched {
		unmatchedSet[m] = true
	}

	// Old algorithm: raw edge count (pre-fix #7).
	edgeFanIn := make(map[workerdomain.Module]int, len(unmatched))
	for _, m := range unmatched {
		edgeFanIn[m] = 0
	}
	for _, e := range graph.Edges {
		if unmatchedSet[e.To] {
			edgeFanIn[e.To]++
		}
	}

	// New algorithm: unique importers (post-fix #7).
	uniqueFanIn := make(map[workerdomain.Module]map[workerdomain.Module]struct{}, len(unmatched))
	for _, m := range unmatched {
		uniqueFanIn[m] = make(map[workerdomain.Module]struct{})
	}
	for _, e := range graph.Edges {
		if set, ok := uniqueFanIn[e.To]; ok {
			set[e.From] = struct{}{}
		}
	}

	// ── Step 4: find modules where the two counts differ AND where the
	//    difference would cross the threshold (changing infra↔domain). ──────
	var differentCount int
	var classificationChanges []struct {
		module    workerdomain.Module
		edgeCount int
		uniqCount int
		oldCls    string
		newCls    string
	}

	for _, m := range unmatched {
		ec := edgeFanIn[m]
		uc := len(uniqueFanIn[m])
		if ec == uc {
			continue
		}
		differentCount++

		oldCls := "domain"
		if ec > threshold {
			oldCls = "infra"
		}
		newCls := "domain"
		if uc > threshold {
			newCls = "infra"
		}

		if oldCls != newCls {
			classificationChanges = append(classificationChanges, struct {
				module    workerdomain.Module
				edgeCount int
				uniqCount int
				oldCls    string
				newCls    string
			}{m, ec, uc, oldCls, newCls})
		}
		t.Logf("  FAN-IN DIFF: %s  edge=%d  unique=%d  old=%s  new=%s",
			m, ec, uc, oldCls, newCls)
	}

	// ── Report ────────────────────────────────────────────────────────────────
	t.Logf("--- FIX #7 VALIDATION REPORT ---")
	t.Logf("Modules where edge_fan_in ≠ unique_fan_in: %d / %d unmatched",
		differentCount, len(unmatched))
	t.Logf("Modules that would change classification: %d", len(classificationChanges))
	for _, ch := range classificationChanges {
		t.Logf("  CHANGE: %s  edge=%d→%s  unique=%d→%s",
			ch.module, ch.edgeCount, ch.oldCls, ch.uniqCount, ch.newCls)
	}

	if len(classificationChanges) == 0 && differentCount == 0 {
		t.Logf("CONCLUSION: Fix #7 has NO observable effect on BookStack.")
		t.Logf("  Reason: PHP BuildGraphEdges deduplicates (from,to) pairs globally.")
		t.Logf("  All %d duplicate edge pairs: 0 (confirmed).", duplicatePairs)
		t.Logf("  Fix is covered by TestPHPAwareInfraDetector_FallbackCountsUniqueImporters.")
	} else if len(classificationChanges) == 0 {
		t.Logf("CONCLUSION: Fan-in counts differ for %d modules but NONE cross the threshold.",
			differentCount)
		t.Logf("  Fix #7 is defensively correct but produces no observable effect on BookStack.")
	} else {
		t.Logf("CONCLUSION: Fix #7 DOES change classification for %d module(s) in BookStack.",
			len(classificationChanges))
	}
}
