package adapters

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseCSharpFile(t *testing.T, relPath, src string) csharpRawFile {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(csharp.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(src))
	require.NoError(t, err)
	f := extractCSharpFile(tree.RootNode(), []byte(src), relPath)
	f.Loc = csharpCountLOC([]byte(src))
	return f
}

func TestCSharpExtract_NamespaceUsingsTypes(t *testing.T) {
	src := `namespace Acme.Web;

using Acme.Services;
using Sys = System.Text;
global using Acme.Common;

[ApiController]
[Route("api/users")]
public class UserController : ControllerBase {
    private static int counter;

    [HttpGet("{id}")]
    public User Get(int id) { return null; }

    [HttpPost]
    public void Create() {}
}
`
	f := parseCSharpFile(t, "Controllers/UserController.cs", src)

	assert.Equal(t, "Acme.Web", f.Namespace)
	assert.Equal(t, "UserController", f.PrimaryType)
	assert.Equal(t, "class", f.PrimaryKind)

	byNS := map[string]csharpUsing{}
	for _, u := range f.Usings {
		byNS[u.Namespace] = u
	}
	assert.Contains(t, byNS, "Acme.Services")
	require.Contains(t, byNS, "System.Text")
	assert.Equal(t, "Sys", byNS["System.Text"].Alias, "alias captured")
	require.Contains(t, byNS, "Acme.Common")
	assert.True(t, byNS["Acme.Common"].IsGlobal, "global using flagged")

	assert.ElementsMatch(t, []string{"Get", "Create"}, f.Methods)
	assert.Equal(t, []string{"counter"}, f.StaticState)

	assert.True(t, f.IsController)
	assert.Equal(t, "api/users", f.ClassPrefix)

	routes := map[string]csharpRoute{}
	for _, r := range f.Routes {
		routes[r.Method+" "+r.Path] = r
	}
	assert.Equal(t, "Get", routes["GET /api/users/{id}"].Handler)
	assert.Equal(t, "Create", routes["POST /api/users"].Handler)
}

func TestCSharpExtract_FileScopedAndBlockNamespaces(t *testing.T) {
	src := `namespace Acme.Other {
    public interface IRepo {}
    public record Pt(int X);
    public struct S {}
}
`
	f := parseCSharpFile(t, "Other.cs", src)
	assert.Equal(t, "Acme.Other", f.Namespace)
	assert.Equal(t, "IRepo", f.PrimaryType)
	assert.Equal(t, "interface", f.PrimaryKind)
	assert.ElementsMatch(t, []string{"IRepo", "Pt", "S"}, f.Types)
}

func TestCSharpExtract_MinimalAPI(t *testing.T) {
	src := `var app = WebApplication.Create();
app.MapGet("/hello", () => "hi");
app.MapPost("/items", (Item i) => Results.Ok());
`
	f := parseCSharpFile(t, "Program.cs", src)
	routes := map[string]bool{}
	for _, r := range f.Routes {
		routes[r.Method+" "+r.Path] = true
	}
	assert.True(t, routes["GET /hello"])
	assert.True(t, routes["POST /items"])
}

func TestCSharpResolver_IgnoresBCL(t *testing.T) {
	files := []csharpRawFile{
		{
			RelPath: "S/Svc.cs", Namespace: "Acme.S", Namespaces: []string{"Acme.S"},
			Types: []string{"Svc"}, PrimaryType: "Svc",
			Usings: []csharpUsing{
				{Namespace: "Acme.D"},                       // intra-repo
				{Namespace: "System.Collections.Generic"},   // BCL — ignored
				{Namespace: "Newtonsoft.Json"},              // NuGet — ignored
			},
		},
		{RelPath: "D/Repo.cs", Namespace: "Acme.D", Namespaces: []string{"Acme.D"}, Types: []string{"Repo"}, PrimaryType: "Repo"},
	}
	r := NewCSharpModuleResolver(files)
	w := r.BuildGraphEdges(files)
	require.Len(t, w, 1)
	assert.Equal(t, uint32(1), w[[2]string{"Acme.S.Svc", "Acme.D.Repo"}])
}
