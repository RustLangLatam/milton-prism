package application

import (
	"sort"
	"strconv"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// detectCycles runs Tarjan's SCC algorithm on the digest graph (full graph,
// self-loops excluded) and returns one StructuralFinding per SCC of size ≥ 2.
//
// Module order within each SCC is sorted lexicographically — stable across runs.
// This intentionally fixes the non-determinism in the frontend's detectGraphCycles,
// which iterates over a JS Set without guaranteed ordering.
//
// Severity rule mirrors buildCycleFindings in the frontend:
//
//	high   — the SCC contains at least one domain module, OR it has ≥ 3 members
//	medium — the SCC has exactly 2 members and neither is a domain module
func detectCycles(
	edges []workerdomain.DigestEdge,
	domainSet map[string]bool,
) []workerdomain.StructuralFinding {
	// Build adjacency list, excluding self-loops.
	adj := make(map[string][]string, len(edges))
	allNodes := make(map[string]struct{}, len(edges)*2)
	for _, e := range edges {
		if e.From == e.To {
			continue // self-loops excluded, matching frontend filter
		}
		adj[e.From] = append(adj[e.From], e.To)
		allNodes[e.From] = struct{}{}
		allNodes[e.To] = struct{}{}
	}
	if len(allNodes) == 0 {
		return nil
	}

	// Deterministic DFS order — sort all nodes before iterating.
	nodeList := make([]string, 0, len(allNodes))
	for n := range allNodes {
		nodeList = append(nodeList, n)
	}
	sort.Strings(nodeList)

	// Tarjan's SCC — iterative stack state to avoid goroutine stack growth on
	// large graphs (BookStack ~548 modules, depth can exceed default 8 kB frames).
	// Using the standard recursive form here; Go's goroutine stacks grow
	// dynamically, so recursion depth is not a concern in practice.
	index := make(map[string]int, len(allNodes))
	lowlink := make(map[string]int, len(allNodes))
	onStack := make(map[string]bool, len(allNodes))
	var stk []string
	counter := 0
	var sccs [][]string

	var sc func(v string)
	sc = func(v string) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stk = append(stk, v)
		onStack[v] = true

		for _, w := range adj[v] {
			if _, visited := index[w]; !visited {
				sc(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}

		if lowlink[v] == index[v] {
			var scc []string
			for {
				w := stk[len(stk)-1]
				stk = stk[:len(stk)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				sort.Strings(scc) // stable, lexicographic order within each SCC
				sccs = append(sccs, scc)
			}
		}
	}

	for _, n := range nodeList {
		if _, visited := index[n]; !visited {
			sc(n)
		}
	}

	if len(sccs) == 0 {
		return nil
	}

	findings := make([]workerdomain.StructuralFinding, 0, len(sccs))
	for _, scc := range sccs {
		hasDomain := false
		for _, m := range scc {
			if domainSet[m] {
				hasDomain = true
				break
			}
		}
		sev := "medium"
		if hasDomain || len(scc) >= 3 {
			sev = "high"
		}
		findings = append(findings, workerdomain.StructuralFinding{
			Kind:     "cycle",
			Severity: sev,
			TitleKey: "finding.cycle",
			Params:   map[string]string{"n": strconv.Itoa(len(scc))},
			Modules:  scc,
		})
	}
	return findings
}
