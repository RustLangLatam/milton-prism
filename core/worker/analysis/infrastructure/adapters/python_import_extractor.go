package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// PythonImportExtractor extracts raw import statements and Flask blueprint
// metadata from Python source files using tree-sitter.
//
// All tree-sitter internals are confined to this package (Canon dependency
// rule: adapters only).
//
// Known limitation: dynamic imports (importlib.import_module, __import__)
// are not detectable via static AST analysis and are silently skipped.
type PythonImportExtractor struct{}

// NewPythonImportExtractor returns a new PythonImportExtractor.
func NewPythonImportExtractor() *PythonImportExtractor {
	return &PythonImportExtractor{}
}

// ExtractImports walks workspacePath for .py files, parses each with
// tree-sitter, and returns:
//   - raw import statements (one entry per import target, in walk order)
//   - Flask blueprint metadata correlated by variable name across the workspace
//
// A context cancellation aborts the walk. Per-file parse errors are skipped
// without failing the whole extraction.
func (e *PythonImportExtractor) ExtractImports(ctx context.Context, workspacePath string) ([]workerdomain.RawImport, []workerdomain.BlueprintInfo, error) {
	lang := python.GetLanguage()

	var allImports []workerdomain.RawImport

	// Blueprint correlation state: keyed by the variable name that holds the
	// Blueprint instance. Last-writer-wins for each varName across files.
	type bpDef struct{ name, file string }
	defs := make(map[string]bpDef)
	regs := make(map[string]string) // varName → urlPrefix

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}

		relPath, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			relPath = path
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}

		parser := sitter.NewParser()
		parser.SetLanguage(lang)
		tree, parseErr := parser.ParseCtx(ctx, nil, source)
		if parseErr != nil {
			return nil // skip files that fail to parse (e.g. syntax errors)
		}

		fileImports, fileDefs, fileRegs := extractPythonNodes(tree.RootNode(), source, relPath)
		allImports = append(allImports, fileImports...)
		for _, d := range fileDefs {
			defs[d.varName] = bpDef{name: d.name, file: d.file}
		}
		for _, r := range fileRegs {
			if _, exists := regs[r.varName]; !exists {
				regs[r.varName] = r.urlPrefix
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Correlate: every Blueprint() definition gets a BlueprintInfo entry.
	// If a matching register_blueprint() was found by variable name, include
	// its url_prefix; otherwise URLPrefix is left empty.
	var blueprints []workerdomain.BlueprintInfo
	for varName, def := range defs {
		blueprints = append(blueprints, workerdomain.BlueprintInfo{
			Name:      def.name,
			File:      def.file,
			URLPrefix: regs[varName],
		})
	}

	return allImports, blueprints, nil
}

// ExtractModuleCards walks workspacePath for .py files and produces one
// rawModuleCard per file. It uses PythonModuleResolver to obtain accurate
// dotted module names (same root-detection as the graph builder).
//
// Extracted per module:
//   - functions: module-level function_definition names
//   - classes:   module-level class_definition names
//   - state:     non-ALL_CAPS module-level assignment targets (mutable state)
//   - routes:    routes from @<anything>.route(...) decorators
//   - docstring_head: first 120 chars of the module docstring
//   - loc:       non-blank, non-comment line count
func (e *PythonImportExtractor) ExtractModuleCards(ctx context.Context, workspacePath string) ([]rawModuleCard, error) {
	resolver, err := NewPythonModuleResolver(workspacePath)
	if err != nil {
		return nil, err
	}

	lang := python.GetLanguage()
	var cards []rawModuleCard

	err = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}

		relPath, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			relPath = path
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		parser := sitter.NewParser()
		parser.SetLanguage(lang)
		tree, parseErr := parser.ParseCtx(ctx, nil, source)
		if parseErr != nil {
			return nil
		}

		moduleName := resolver.ModuleName(relPath)
		card := extractModuleCard(tree.RootNode(), source, moduleName, relPath)
		cards = append(cards, card)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// ── module card internal types ────────────────────────────────────────────────

// rawRoute is a route extracted from a web-framework decorator (@bp.route).
type rawRoute struct {
	Method  string // HTTP method(s), comma-joined; defaults to "GET"
	Path    string // URL pattern, e.g. "/users/<int:id>"
	Handler string // decorated function name
}

// rawModuleCard holds the structural summary of one .py file before conversion
// to the proto-generated domain type.
type rawModuleCard struct {
	Module    string
	File      string
	Functions []string
	Classes   []string
	State     []string // mutable module-level variable names (non-ALL_CAPS)
	Routes    []rawRoute
	Docstring string // first 120 chars of module docstring
	Loc       uint32 // non-blank, non-comment line count
}

// ── internal types for pre-correlation blueprint data ─────────────────────────

type rawBpDef struct {
	varName string
	name    string
	file    string
}

type rawBpReg struct {
	varName   string
	urlPrefix string
}

// ── tree walking ──────────────────────────────────────────────────────────────

// extractPythonNodes performs a full AST walk and collects imports, blueprint
// definitions, and register_blueprint calls from a single parsed file.
func extractPythonNodes(root *sitter.Node, source []byte, relPath string) (
	imports []workerdomain.RawImport,
	defs []rawBpDef,
	regs []rawBpReg,
) {
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		switch node.Type() {
		case "import_statement":
			imports = append(imports, parseImportStmt(node, source, relPath)...)
		case "import_from_statement":
			imp := parseFromImportStmt(node, source, relPath)
			// Only include when the statement produced meaningful data.
			if len(imp.Names) > 0 || imp.Module != "" || imp.IsRelative {
				imports = append(imports, imp)
			}
		case "assignment":
			if def, ok := parseBlueprintDef(node, source, relPath); ok {
				defs = append(defs, def)
			}
		case "call":
			if reg, ok := parseRegisterBlueprint(node, source); ok {
				regs = append(regs, reg)
			}
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)
	return
}

// ── import_statement: `import a.b.c` / `import a.b.c as x` ──────────────────

func parseImportStmt(node *sitter.Node, source []byte, relPath string) []workerdomain.RawImport {
	var result []workerdomain.RawImport
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "dotted_name":
			module := nodeText(child, source)
			result = append(result, workerdomain.RawImport{
				ImportingFile: relPath,
				Module:        module,
				Names:         []string{module},
			})
		case "aliased_import":
			nameNode := child.ChildByFieldName("name")
			aliasNode := child.ChildByFieldName("alias")
			if nameNode == nil {
				continue
			}
			module := nodeText(nameNode, source)
			alias := module
			if aliasNode != nil {
				alias = nodeText(aliasNode, source)
			}
			result = append(result, workerdomain.RawImport{
				ImportingFile: relPath,
				Module:        module,
				Names:         []string{alias},
			})
		}
	}
	return result
}

// ── import_from_statement: `from a.b import c, d` / `from . import x` ───────

func parseFromImportStmt(node *sitter.Node, source []byte, relPath string) workerdomain.RawImport {
	imp := workerdomain.RawImport{ImportingFile: relPath}
	if node.NamedChildCount() == 0 {
		return imp
	}

	// First named child is always the module (dotted_name or relative_import).
	moduleNode := node.NamedChild(0)
	switch moduleNode.Type() {
	case "dotted_name":
		imp.Module = nodeText(moduleNode, source)
	case "relative_import":
		imp.IsRelative = true
		raw := nodeText(moduleNode, source)
		for i := 0; i < len(raw) && raw[i] == '.'; i++ {
			imp.RelativeLevel++
		}
		imp.Module = raw[imp.RelativeLevel:] // "" when `from . import x`
	}

	// Remaining named children are the imported names.
	for i := 1; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "wildcard_import":
			imp.Names = append(imp.Names, "*")
		case "import_list":
			// Parenthesized: `from x import (a, b)`
			for j := 0; j < int(child.NamedChildCount()); j++ {
				imp.Names = append(imp.Names, resolvedImportName(child.NamedChild(j), source))
			}
		case "dotted_name", "identifier":
			imp.Names = append(imp.Names, nodeText(child, source))
		case "aliased_import":
			imp.Names = append(imp.Names, resolvedImportName(child, source))
		}
	}
	return imp
}

// resolvedImportName returns the alias when present, otherwise the name.
func resolvedImportName(node *sitter.Node, source []byte) string {
	if node.Type() == "aliased_import" {
		if alias := node.ChildByFieldName("alias"); alias != nil {
			return nodeText(alias, source)
		}
		if name := node.ChildByFieldName("name"); name != nil {
			return nodeText(name, source)
		}
	}
	return nodeText(node, source)
}

// ── Flask: Blueprint() definition ─────────────────────────────────────────────

// parseBlueprintDef matches `<var> = Blueprint(<name_str>, ...)` assignment nodes.
func parseBlueprintDef(node *sitter.Node, source []byte, relPath string) (rawBpDef, bool) {
	leftNode := node.ChildByFieldName("left")
	rightNode := node.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return rawBpDef{}, false
	}
	if leftNode.Type() != "identifier" || rightNode.Type() != "call" {
		return rawBpDef{}, false
	}

	funcNode := rightNode.ChildByFieldName("function")
	if funcNode == nil || funcNode.Type() != "identifier" {
		return rawBpDef{}, false
	}
	if nodeText(funcNode, source) != "Blueprint" {
		return rawBpDef{}, false
	}

	argsNode := rightNode.ChildByFieldName("arguments")
	if argsNode == nil {
		return rawBpDef{}, false
	}

	// First positional string argument is the blueprint name.
	name := ""
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		arg := argsNode.NamedChild(i)
		if arg.Type() == "string" {
			name = stripPyString(nodeText(arg, source))
			break
		}
	}

	return rawBpDef{
		varName: nodeText(leftNode, source),
		name:    name,
		file:    relPath,
	}, true
}

// ── Flask: register_blueprint() call ─────────────────────────────────────────

// parseRegisterBlueprint matches `<any>.register_blueprint(<var>, url_prefix=<str>)` call nodes.
func parseRegisterBlueprint(node *sitter.Node, source []byte) (rawBpReg, bool) {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil || funcNode.Type() != "attribute" {
		return rawBpReg{}, false
	}

	attrNode := funcNode.ChildByFieldName("attribute")
	if attrNode == nil || nodeText(attrNode, source) != "register_blueprint" {
		return rawBpReg{}, false
	}

	argsNode := node.ChildByFieldName("arguments")
	if argsNode == nil {
		return rawBpReg{}, false
	}

	varName := ""
	urlPrefix := ""

	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		arg := argsNode.NamedChild(i)
		switch arg.Type() {
		case "identifier":
			if varName == "" {
				varName = nodeText(arg, source)
			}
		case "keyword_argument":
			kwName := arg.ChildByFieldName("name")
			kwValue := arg.ChildByFieldName("value")
			if kwName != nil && kwValue != nil && nodeText(kwName, source) == "url_prefix" {
				urlPrefix = stripPyString(nodeText(kwValue, source))
			}
		}
	}

	if varName == "" {
		return rawBpReg{}, false
	}
	return rawBpReg{varName: varName, urlPrefix: urlPrefix}, true
}

// ── module card extraction ────────────────────────────────────────────────────

// extractModuleCard walks the top-level (module-scope) children of root and
// assembles a rawModuleCard. Inner scopes (function bodies, class bodies) are
// intentionally NOT traversed — only module-level declarations are captured.
//
// Tree-sitter Python wraps simple statements in expression_statement nodes at
// module scope, so assignments appear as:
//
//	module → expression_statement → assignment
//
// while function_definition and class_definition are direct module children.
func extractModuleCard(root *sitter.Node, source []byte, module, file string) rawModuleCard {
	card := rawModuleCard{
		Module: module,
		File:   file,
		Loc:    countLOC(source),
	}
	docstringFound := false

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "function_definition":
			if name := child.ChildByFieldName("name"); name != nil {
				card.Functions = append(card.Functions, nodeText(name, source))
			}

		case "class_definition":
			if name := child.ChildByFieldName("name"); name != nil {
				card.Classes = append(card.Classes, nodeText(name, source))
			}

		case "decorated_definition":
			if route, ok := extractDecoratedRoute(child, source); ok {
				card.Routes = append(card.Routes, route)
			}
			// Collect the function/class name from inside the decorated definition.
			for j := 0; j < int(child.NamedChildCount()); j++ {
				nc := child.NamedChild(j)
				switch nc.Type() {
				case "function_definition":
					if name := nc.ChildByFieldName("name"); name != nil {
						card.Functions = append(card.Functions, nodeText(name, source))
					}
				case "class_definition":
					if name := nc.ChildByFieldName("name"); name != nil {
						card.Classes = append(card.Classes, nodeText(name, source))
					}
				}
			}

		case "expression_statement":
			// expression_statement wraps both docstrings (string child) and
			// simple assignments (assignment child) at module scope.
			if child.NamedChildCount() == 0 {
				continue
			}
			nc := child.NamedChild(0)
			switch nc.Type() {
			case "string":
				// First string expression at module scope is the docstring.
				if !docstringFound {
					raw := stripPyString(nodeText(nc, source))
					raw = strings.TrimSpace(raw)
					if len(raw) > 120 {
						raw = raw[:120]
					}
					card.Docstring = raw
					docstringFound = true
				}
			case "assignment":
				if varName, ok := moduleStateName(nc, source); ok {
					card.State = append(card.State, varName)
				}
			}
		}
	}
	return card
}

// moduleStateName inspects an assignment node and returns the left-hand side
// identifier when it represents mutable module-level state (non-ALL_CAPS,
// non-Blueprint assignment). Returns ("", false) for constants and blueprints.
func moduleStateName(node *sitter.Node, source []byte) (string, bool) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil || left.Type() != "identifier" {
		return "", false
	}
	name := nodeText(left, source)
	if !hasMutableName(name) {
		return "", false
	}
	// Exclude Blueprint(...) assignments — those are captured as blueprints.
	if right != nil && right.Type() == "call" {
		if fn := right.ChildByFieldName("function"); fn != nil {
			if nodeText(fn, source) == "Blueprint" {
				return "", false
			}
		}
	}
	return name, true
}

// hasMutableName reports whether name is NOT an ALL_CAPS constant convention.
// Names with at least one lowercase letter are considered mutable.
func hasMutableName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
}

// extractDecoratedRoute inspects a decorated_definition node and returns a
// rawRoute when one of its decorators is a .route(...) call. Returns (rawRoute{},
// false) when no route decorator is found or the handler function name is absent.
func extractDecoratedRoute(decoratedDef *sitter.Node, source []byte) (rawRoute, bool) {
	handlerName := ""
	for i := 0; i < int(decoratedDef.NamedChildCount()); i++ {
		nc := decoratedDef.NamedChild(i)
		if nc.Type() == "function_definition" {
			if name := nc.ChildByFieldName("name"); name != nil {
				handlerName = nodeText(name, source)
			}
		}
	}

	for i := 0; i < int(decoratedDef.NamedChildCount()); i++ {
		nc := decoratedDef.NamedChild(i)
		if nc.Type() != "decorator" {
			continue
		}
		// decorator's named child is the primary expression after "@".
		if nc.NamedChildCount() == 0 {
			continue
		}
		expr := nc.NamedChild(0)
		if expr.Type() != "call" {
			continue
		}
		fn := expr.ChildByFieldName("function")
		if fn == nil || fn.Type() != "attribute" {
			continue
		}
		attrNode := fn.ChildByFieldName("attribute")
		if attrNode == nil || nodeText(attrNode, source) != "route" {
			continue
		}
		args := expr.ChildByFieldName("arguments")
		if args == nil {
			continue
		}
		path := ""
		method := "GET"
		for j := 0; j < int(args.NamedChildCount()); j++ {
			arg := args.NamedChild(j)
			if arg.Type() == "string" && path == "" {
				path = stripPyString(nodeText(arg, source))
			} else if arg.Type() == "keyword_argument" {
				kwName := arg.ChildByFieldName("name")
				kwValue := arg.ChildByFieldName("value")
				if kwName != nil && kwValue != nil && nodeText(kwName, source) == "methods" {
					method = extractMethodList(kwValue, source)
				}
			}
		}
		if path != "" {
			return rawRoute{Method: method, Path: path, Handler: handlerName}, true
		}
	}
	return rawRoute{}, false
}

// extractMethodList extracts comma-joined HTTP methods from a list literal node,
// e.g. ["GET", "POST"] → "GET,POST". Returns "GET" for empty or non-list nodes.
func extractMethodList(node *sitter.Node, source []byte) string {
	var methods []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "string" {
			methods = append(methods, stripPyString(nodeText(child, source)))
		}
	}
	if len(methods) == 0 {
		return "GET"
	}
	return strings.Join(methods, ",")
}

// countLOC counts non-blank, non-comment lines in source.
func countLOC(source []byte) uint32 {
	var count uint32
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			count++
		}
	}
	return count
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nodeText(node *sitter.Node, source []byte) string {
	return string(source[node.StartByte():node.EndByte()])
}

// stripPyString removes quotes from a Python string literal node text.
// Handles single, double, and triple-quoted forms.
func stripPyString(s string) string {
	for _, q := range []string{`"""`, `'''`} {
		if len(s) >= len(q)*2 && strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			return s[len(q) : len(s)-len(q)]
		}
	}
	for _, q := range []string{`"`, `'`} {
		if len(s) >= 2 && strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			return s[1 : len(s)-1]
		}
	}
	return s
}
