package adapters

import (
	"context"
	"math"
	"sort"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
)

// blueprintBiasMultiplier scales the maximum domain-edge weight to produce
// the virtual affinity edge weight added between modules of the same blueprint
// group. A value of 4 means blueprint membership is 4× stronger than the
// heaviest real coupling in the graph — sufficient to keep small, loosely
// connected blueprint members (e.g. conduit.profile.models) in their group
// even when cross-group edges exist.
const blueprintBiasMultiplier = 4.0

// lowConfidenceThreshold is the modularity Q below which the clustering result
// is marked low-confidence. Q ≥ 0.3 is conventionally "good" modularity;
// 0.25 gives a small safety margin for messy graphs.
const lowConfidenceThreshold = 0.25

var _ ports.SemanticClusterer = (*LouvainClusterer)(nil)

// LouvainClusterer is the live deterministic adapter for the SemanticClusterer
// port. It builds an undirected weighted projection of the domain graph,
// augments it with blueprint-affinity edges so that modules in the same
// blueprint strongly prefer the same cluster, runs a single Louvain phase-1
// pass (node movement) seeded from the blueprint partition, and measures
// modularity on the original (non-augmented) graph.
type LouvainClusterer struct{}

// NewLouvainClusterer returns a ready-to-use LouvainClusterer.
func NewLouvainClusterer() *LouvainClusterer { return &LouvainClusterer{} }

// Cluster implements ports.SemanticClusterer. The Digest field of input is ignored
// by this adapter — it is reserved for the future LLM cascade adapter.
func (lc *LouvainClusterer) Cluster(
	_ context.Context,
	input ports.ClusterInput,
) (*workerdomain.ClusteringResult, error) {
	graph := input.DomainGraph
	domainModules := input.DomainModules
	if len(domainModules) == 0 {
		return &workerdomain.ClusteringResult{}, nil
	}

	// Build the undirected projection of the original domain graph.
	orig := newUndirectedGraph()
	for _, m := range domainModules {
		orig.ensure(string(m))
	}
	for _, e := range graph.Edges {
		orig.addEdge(string(e.From), string(e.To), float64(e.Weight))
	}

	// Compute blueprint affinity weight from the original graph's max edge weight.
	maxW := 0.0
	for _, nbrs := range orig.adj {
		for _, w := range nbrs {
			if w > maxW {
				maxW = w
			}
		}
	}
	if maxW == 0 {
		maxW = 1
	}
	affinity := blueprintBiasMultiplier * maxW

	// Build the augmented graph: real edges + intra-blueprint affinity edges.
	aug := cloneGraph(orig)
	bpGroups := blueprintGroupsFor(domainModules)
	for _, members := range bpGroups {
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				aug.addEdge(members[i], members[j], affinity)
			}
		}
	}

	// Seed communities from blueprint groups (one community per group).
	community := make(map[string]string, len(domainModules))
	for _, m := range domainModules {
		community[string(m)] = groupOf(string(m))
	}

	// Run Louvain phase-1 on the augmented graph.
	community = louvainPhase1(aug, community)

	// Absorb singleton communities into their strongest neighbour on the original
	// graph. Modules that end up alone after Louvain (isolated or weakly connected)
	// would otherwise become single-class micro-services; absorbing them into their
	// heaviest coupling partner is always the correct structural outcome.
	community = absorbSingletons(orig, community)

	// Measure modularity on the original graph (affinity edges must not inflate Q).
	Q := computeModularity(orig, community)

	// Collect clusters from the final community assignment.
	grouped := make(map[string][]workerdomain.Module)
	for _, m := range domainModules {
		comm := community[string(m)]
		grouped[comm] = append(grouped[comm], m)
	}

	clusters := make([]workerdomain.Cluster, 0, len(grouped))
	for comm, members := range grouped {
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		clusters = append(clusters, workerdomain.Cluster{
			BlueprintGroup: comm,
			Modules:        members,
		})
	}
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].BlueprintGroup < clusters[j].BlueprintGroup
	})

	return &workerdomain.ClusteringResult{
		Clusters:      clusters,
		Modularity:    Q,
		LowConfidence: Q < lowConfidenceThreshold,
	}, nil
}

// blueprintGroupsFor maps blueprint group → list of module names in that group.
func blueprintGroupsFor(modules []workerdomain.Module) map[string][]string {
	groups := make(map[string][]string)
	for _, m := range modules {
		g := groupOf(string(m))
		groups[g] = append(groups[g], string(m))
	}
	return groups
}

// --- undirected weighted graph ---

type undirectedGraph struct {
	adj map[string]map[string]float64 // symmetric: adj[a][b] == adj[b][a]
	deg map[string]float64            // total weighted degree per node
	m   float64                       // total edge weight (each edge counted once)
}

func newUndirectedGraph() *undirectedGraph {
	return &undirectedGraph{
		adj: make(map[string]map[string]float64),
		deg: make(map[string]float64),
	}
}

// ensure registers a node with zero degree (idempotent).
func (g *undirectedGraph) ensure(n string) {
	if g.adj[n] == nil {
		g.adj[n] = make(map[string]float64)
	}
}

// addEdge adds weight w to the undirected edge {a,b}. Calling it multiple
// times accumulates (directed edges are summed into the undirected projection).
func (g *undirectedGraph) addEdge(a, b string, w float64) {
	if a == b {
		return
	}
	g.ensure(a)
	g.ensure(b)
	g.adj[a][b] += w
	g.adj[b][a] += w
	g.deg[a] += w
	g.deg[b] += w
	g.m += w
}

func cloneGraph(src *undirectedGraph) *undirectedGraph {
	dst := newUndirectedGraph()
	dst.m = src.m
	for n, nbrs := range src.adj {
		dst.adj[n] = make(map[string]float64, len(nbrs))
		for nb, w := range nbrs {
			dst.adj[n][nb] = w
		}
		dst.deg[n] = src.deg[n]
	}
	return dst
}

// --- Louvain phase-1 (node movement) ---

// louvainPhase1 iterates over nodes in sorted order, moving each node to the
// neighbor community that maximises the modularity gain ΔQ. Iteration repeats
// until no node moves in a full pass (convergence). The modified community map
// is returned.
//
// ΔQ for moving node i from its current community C to candidate community D
// (Blondel et al., 2008):
//
//	ΔQ = (k_{i,D} − k_{i,C}) / m  −  k_i · (Σ_D − Σ_C_excl) / (2m)²
//
// where k_{i,X} is the sum of edges from i to members of X, Σ_X is the sum
// of degrees in X, and Σ_C_excl excludes i's own degree from Σ_C.
func louvainPhase1(g *undirectedGraph, community map[string]string) map[string]string {
	nodes := make([]string, 0, len(g.adj))
	for n := range g.adj {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	twoM := 2 * g.m
	if twoM == 0 {
		return community
	}

	for {
		moved := false

		for _, node := range nodes {
			currComm := community[node]
			ki := g.deg[node]

			// k_{i,C}: edges from node to other members of its current community.
			// Σ_C_excl: total degree of current community minus node's own degree.
			kic := 0.0
			sigmaCExcl := -ki
			for n2, c2 := range community {
				if c2 == currComm {
					sigmaCExcl += g.deg[n2]
					if n2 != node {
						kic += g.adj[node][n2]
					}
				}
			}

			// Gather candidate communities from direct neighbours (skip currComm).
			type candInfo struct {
				kid    float64 // edges from node to this community
				sigmaD float64 // total degree of this community
			}
			cands := make(map[string]*candInfo)
			for nb, w := range g.adj[node] {
				c := community[nb]
				if c == currComm {
					continue
				}
				if _, ok := cands[c]; !ok {
					cands[c] = &candInfo{}
				}
				cands[c].kid += w
			}

			// Accumulate sigmaD for each candidate community.
			for n2, c2 := range community {
				if n2 == node {
					continue
				}
				ci, ok := cands[c2]
				if !ok {
					continue
				}
				ci.sigmaD += g.deg[n2]
			}

			bestComm := currComm
			bestDelta := 0.0 // only move on strictly positive gain

			for c, ci := range cands {
				delta := (ci.kid-kic)/g.m - ki*(ci.sigmaD-sigmaCExcl)/(twoM*g.m)
				if delta > bestDelta || (delta == bestDelta && c < bestComm) {
					bestDelta = delta
					bestComm = c
				}
			}

			if bestComm != currComm {
				community[node] = bestComm
				moved = true
			}
		}

		if !moved {
			break
		}
	}

	return community
}

// --- modularity ---

// absorbSingletons merges singleton communities into the community of their
// strongest neighbour in orig. Iterates until stable so cascading absorptions
// are resolved (two connected singletons can each absorb into the same large
// community in successive passes). Isolated nodes with no neighbours keep
// their own community.
func absorbSingletons(orig *undirectedGraph, community map[string]string) map[string]string {
	for {
		sizes := make(map[string]int, len(community))
		for _, c := range community {
			sizes[c]++
		}
		moved := false
		for node, comm := range community {
			if sizes[comm] > 1 {
				continue
			}
			bestComm := comm
			bestW := 0.0
			for nb, w := range orig.adj[node] {
				if nbComm := community[nb]; nbComm != comm && w > bestW {
					bestW = w
					bestComm = nbComm
				}
			}
			if bestComm != comm {
				community[node] = bestComm
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	return community
}

// computeModularity calculates the Newman-Girvan modularity Q for the given
// community assignment on the given graph (Blondel et al., 2008):
//
//	Q = Σ_c  [ L_c/m  −  (d_c / 2m)² ]
//
// where L_c is the sum of internal edge weights in community c and d_c is the
// sum of all node degrees in c.
func computeModularity(g *undirectedGraph, community map[string]string) float64 {
	if g.m == 0 {
		return 0
	}

	lc := make(map[string]float64)
	dc := make(map[string]float64)

	for n := range g.adj {
		dc[community[n]] += g.deg[n]
	}

	// Count each undirected edge once via a visited set.
	visited := make(map[[2]string]bool)
	for a, nbrs := range g.adj {
		for b, w := range nbrs {
			rev := [2]string{b, a}
			if visited[rev] {
				continue
			}
			visited[[2]string{a, b}] = true
			if community[a] == community[b] {
				lc[community[a]] += w
			}
		}
	}

	twoM := 2 * g.m
	Q := 0.0
	for c := range dc {
		Q += lc[c]/g.m - math.Pow(dc[c]/twoM, 2)
	}
	return Q
}
