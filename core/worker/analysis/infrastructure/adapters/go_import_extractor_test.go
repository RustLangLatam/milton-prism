package adapters_test

import (
	"context"
	"testing"

	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The extractor is package-private; we exercise it through the public analyzer
// surface (ExtractCards), which is the contract the pipeline relies on. The
// assertions below pin the alias / dot / blank import handling and the
// declaration-card content that the extractor produces.

func TestGoExtractor_AliasDotBlank_AndDecls(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoLanguageAnalyzer()

	// (1) Aliased + blank intra/third-party imports still resolve to the right
	//     intra-repo edges and never leak the alias/third-party as a node.
	edges, err := a.ResolveImports(context.Background(), fixtureGo)
	require.NoError(t, err)
	idx := goEdgeIndex(edges)

	// service uses `repository "example.com/app/internal/repo"` (alias): edge
	// must point to the real import path, not the alias.
	assert.Equal(t, uint32(1), idx[[2]string{modService, modRepo}],
		"aliased import must resolve to the canonical package import path")

	// repo has `_ "github.com/lib/pq"` (blank, third-party): no edge, but the
	// edge to model (an interpreted intra-repo import) is present.
	assert.Equal(t, uint32(1), idx[[2]string{modRepo, modModel}])

	// (2) web has a dot-import of a third-party package (uuid). It is external,
	//     so it produces no edge; the intra-repo edges remain intact.
	assert.Equal(t, uint32(1), idx[[2]string{modWeb, modModel}])
	for k := range idx {
		assert.NotContains(t, k[1], "uuid", "dot-imported third-party must not be an edge")
	}

	// (3) Declaration cards: kind prefixes and method receiver formatting.
	cards, _, err := a.ExtractCards(context.Background(), fixtureGo)
	require.NoError(t, err)
	byFile := map[string][]string{}
	funcsByFile := map[string][]string{}
	for _, c := range cards {
		byFile[c.GetFile()] = c.GetClasses()
		funcsByFile[c.GetFile()] = c.GetFunctions()
	}
	assert.ElementsMatch(t,
		[]string{"struct:User", "type:Role", "interface:Store"},
		byFile["internal/model/user.go"])
	assert.Contains(t, funcsByFile["internal/model/user.go"], "User.Display")
}
