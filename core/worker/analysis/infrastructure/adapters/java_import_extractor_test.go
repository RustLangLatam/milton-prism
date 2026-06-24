package adapters

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseJavaFile is a test helper that parses inline Java source into a javaRawFile
// using the same extractJavaFile walk the production extractor uses per file.
func parseJavaFile(t *testing.T, relPath, src string) javaRawFile {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(java.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(src))
	require.NoError(t, err)
	f := extractJavaFile(tree.RootNode(), []byte(src), relPath)
	f.Loc = javaCountLOC([]byte(src))
	return f
}

func TestJavaExtract_PackageImportsTypes(t *testing.T) {
	src := `package com.acme.web;

import com.acme.service.UserService;
import static com.acme.Util.help;
import com.acme.model.*;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/users")
public class UserController {
    private static int counter;
    private final UserService svc;

    @GetMapping("/{id}")
    public User get(int id) { return svc.find(id); }

    @PostMapping
    public void create() {}
}

interface Repo {}
enum Color { RED }
`
	f := parseJavaFile(t, "src/main/java/com/acme/web/UserController.java", src)

	assert.Equal(t, "com.acme.web", f.Package)
	assert.Equal(t, "UserController", f.PrimaryType)
	assert.Equal(t, "class", f.PrimaryKind)

	// Imports: precise, static, wildcard, and a third-party annotation.
	fqns := map[string]javaImport{}
	for _, imp := range f.Imports {
		fqns[imp.FQN] = imp
	}
	assert.Contains(t, fqns, "com.acme.service.UserService")
	require.Contains(t, fqns, "com.acme.Util.help")
	assert.True(t, fqns["com.acme.Util.help"].IsStatic, "static import flagged")
	require.Contains(t, fqns, "com.acme.model")
	assert.True(t, fqns["com.acme.model"].IsWildcard, "wildcard import flagged")
	assert.Contains(t, fqns, "org.springframework.web.bind.annotation.RestController")

	// Top-level types: class + interface + enum.
	assert.ElementsMatch(t, []string{"UserController", "Repo", "Color"}, f.Types)

	// Methods + static state.
	assert.ElementsMatch(t, []string{"get", "create"}, f.Methods)
	assert.Equal(t, []string{"counter"}, f.StaticState)

	// Spring controller surface.
	assert.True(t, f.IsController)
	assert.Equal(t, "/api/users", f.ClassPrefix)

	routes := map[string]javaRoute{}
	for _, r := range f.Routes {
		routes[r.Method+" "+r.Path] = r
	}
	assert.Equal(t, "get", routes["GET /api/users/{id}"].Handler)
	assert.Equal(t, "create", routes["POST /api/users"].Handler)
}

func TestJavaResolver_IgnoresExternalAndExpandsWildcard(t *testing.T) {
	files := []javaRawFile{
		{
			RelPath: "a/A.java", Package: "com.acme.a", Types: []string{"A"}, PrimaryType: "A",
			Imports: []javaImport{
				{FQN: "com.acme.b", IsWildcard: true},          // expands to com.acme.b.B
				{FQN: "java.util.List"},                        // JDK — ignored
				{FQN: "org.springframework.stereotype.Service"}, // third-party — ignored
			},
		},
		{RelPath: "b/B.java", Package: "com.acme.b", Types: []string{"B"}, PrimaryType: "B"},
	}
	r := NewJavaModuleResolver(files)
	w := r.BuildGraphEdges(files)

	require.Len(t, w, 1, "only the intra-repo wildcard edge survives")
	assert.Equal(t, uint32(1), w[[2]string{"com.acme.a.A", "com.acme.b.B"}])
}

func TestJavaExtract_SkipsBuildDirs(t *testing.T) {
	e := NewJavaImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-java")
	require.NoError(t, err)
	for _, f := range files {
		assert.NotContains(t, f.RelPath, "target", "files under target/ must be skipped")
	}
	require.NotEmpty(t, files)
}
