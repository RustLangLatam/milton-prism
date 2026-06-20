package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

var _ ports.ContractDeriver = (*FlaskSQLAlchemyDeriver)(nil)

// FlaskSQLAlchemyDeriver is the live adapter for the ContractDeriver port.
// It parses SQLAlchemy model classes and Flask route decorators from the
// source workspace to produce an AIP-compliant .proto file per cluster.
//
// Model→message mapping is fully deterministic. Route→RPC mapping is
// deterministic for standard CRUD patterns; non-CRUD routes are marked
// TODO in the generated service block rather than interpreted.
type FlaskSQLAlchemyDeriver struct{}

// NewFlaskSQLAlchemyDeriver returns a ready-to-use FlaskSQLAlchemyDeriver.
func NewFlaskSQLAlchemyDeriver() *FlaskSQLAlchemyDeriver { return &FlaskSQLAlchemyDeriver{} }

// Derive implements ports.ContractDeriver for Flask/SQLAlchemy workspaces.
// tableServiceMap maps SQLAlchemy __tablename__ values to service names so that
// cross-service FK annotations carry the target service name.
func (d *FlaskSQLAlchemyDeriver) Derive(
	ctx context.Context,
	cluster workerdomain.Cluster,
	workspacePath string,
	tableServiceMap map[string]string,
) (*workerdomain.DerivedContract, error) {
	svcName := lastComponent(cluster.BlueprintGroup)

	// Collect models from all .models modules in the cluster.
	var allMessages []workerdomain.ProtoMessage
	var modelsModulesCount, modelsFilesRead int
	for _, m := range cluster.Modules {
		if !strings.HasSuffix(string(m), ".models") {
			continue
		}
		modelsModulesCount++
		src, err := readModuleFile(workspacePath, m)
		if err != nil {
			continue // file absent → skip (holes are silently omitted)
		}
		modelsFilesRead++
		msgs := parseModelsFromAST(ctx, []byte(src), tableServiceMap)
		allMessages = append(allMessages, msgs...)
	}

	// Detect incomplete derivation: models modules present but zero messages extracted.
	var incomplete bool
	var incompleteReason string
	if modelsModulesCount > 0 && len(allMessages) == 0 {
		incomplete = true
		if modelsFilesRead == 0 {
			incompleteReason = fmt.Sprintf(
				"%d models module(s) in cluster but no model files could be read",
				modelsModulesCount,
			)
		} else {
			incompleteReason = fmt.Sprintf(
				"%d models file(s) parsed but no extractable message patterns found — check for unsupported ORM patterns",
				modelsFilesRead,
			)
		}
	}

	// Collect RPCs from all .views modules in the cluster.
	var allRPCs []workerdomain.ServiceRPC
	for _, m := range cluster.Modules {
		if !strings.HasSuffix(string(m), ".views") {
			continue
		}
		src, err := readModuleFile(workspacePath, m)
		if err != nil {
			continue
		}
		rpcs := parseFlaskRoutes(src, svcName)
		allRPCs = append(allRPCs, rpcs...)
	}

	hasTODO := false
	for _, rpc := range allRPCs {
		if rpc.IsTODO {
			hasTODO = true
			break
		}
	}

	protoContent := generateProto(svcName, cluster.BlueprintGroup, allMessages, allRPCs)

	// Write to workspace.
	outDir := filepath.Join(workspacePath, ".milton_prism", "contracts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("flask/sqlalchemy deriver: create contracts dir: %w", err)
	}
	protoPath := filepath.Join(".milton_prism", "contracts", svcName+".proto")
	if err := os.WriteFile(filepath.Join(workspacePath, protoPath), []byte(protoContent), 0o644); err != nil {
		return nil, fmt.Errorf("flask/sqlalchemy deriver: write proto: %w", err)
	}

	return &workerdomain.DerivedContract{
		ServiceName:      svcName,
		ProtoContent:     protoContent,
		ProtoPath:        protoPath,
		Messages:         allMessages,
		RPCs:             allRPCs,
		HasTODORoutes:    hasTODO,
		Incomplete:       incomplete,
		IncompleteReason: incompleteReason,
	}, nil
}

// readModuleFile converts a dotted module name to its filesystem path and reads it.
func readModuleFile(workspacePath string, m workerdomain.Module) (string, error) {
	parts := strings.Split(string(m), ".")
	relPath := filepath.Join(parts...) + ".py"
	data, err := os.ReadFile(filepath.Join(workspacePath, relPath))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- SQLAlchemy model parser (tree-sitter AST) ---

// sqlalchemyModelBases are lowercase class parent names that identify SQLAlchemy model classes.
var sqlalchemyModelBases = map[string]bool{
	"base": true, "model": true, "db.model": true, "declarativebase": true,
	"crudmixin": true, "surrogatepk": true,
}

// rawColAST holds parsed data from one SQLAlchemy column assignment in the AST.
type rawColAST struct {
	name     string // Python attribute name (e.g. "author_id")
	sqlType  string // SQLAlchemy type name (e.g. "Integer", "String", "Text")
	isFKCol  bool   // true when the column carries a ForeignKey or is reference_col
	refTable string // FK target table (from ForeignKey arg or reference_col first arg)
}

// rawRelAST holds parsed data from a relationship() assignment.
type rawRelAST struct {
	name        string // Python attribute name (e.g. "author")
	targetClass string // first string arg to relationship() (e.g. "UserProfile")
}

// parseModelsFromAST parses SQLAlchemy model classes from Python source using the
// tree-sitter AST. It handles reference_col(), relationship(), db.Column(), and
// bare Column() — patterns invisible to the former regex-based parser.
func parseModelsFromAST(ctx context.Context, src []byte, tableServiceMap map[string]string) []workerdomain.ProtoMessage {
	p := sitter.NewParser()
	p.SetLanguage(python.GetLanguage())
	tree, _ := p.ParseCtx(ctx, nil, src)
	if tree == nil {
		return nil
	}
	var messages []workerdomain.ProtoMessage
	walkForClasses(tree.RootNode(), src, tableServiceMap, &messages)
	return messages
}

// walkForClasses searches the AST for class_definition nodes and processes each
// one that looks like a SQLAlchemy model. Does not recurse into nested classes.
func walkForClasses(node *sitter.Node, src []byte, tableServiceMap map[string]string, out *[]workerdomain.ProtoMessage) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "class_definition" {
			if msg, ok := processModelClass(child, src, tableServiceMap); ok {
				*out = append(*out, msg)
			}
		} else {
			walkForClasses(child, src, tableServiceMap, out)
		}
	}
}

// processModelClass extracts a ProtoMessage from a class_definition node when
// the class is a recognised SQLAlchemy model with at least one mappable field.
func processModelClass(node *sitter.Node, src []byte, tableServiceMap map[string]string) (workerdomain.ProtoMessage, bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return workerdomain.ProtoMessage{}, false
	}
	className := nodeText(nameNode, src)

	basesNode := node.ChildByFieldName("superclasses")
	if basesNode == nil || !hasSQLAlchemyBase(basesNode, src) {
		return workerdomain.ProtoMessage{}, false
	}

	bodyNode := node.ChildByFieldName("body")
	if bodyNode == nil {
		return workerdomain.ProtoMessage{}, false
	}

	rawCols, rawRels := extractClassMembers(bodyNode, src)
	fields := buildProtoFields(rawCols, tableServiceMap)
	if len(fields) == 0 {
		return workerdomain.ProtoMessage{}, false
	}

	rels := make([]string, 0, len(rawRels))
	for _, r := range rawRels {
		if r.targetClass != "" {
			rels = append(rels, r.name+" → "+r.targetClass)
		} else {
			rels = append(rels, r.name)
		}
	}

	return workerdomain.ProtoMessage{
		Name:          className,
		Fields:        fields,
		Relationships: rels,
	}, true
}

// hasSQLAlchemyBase returns true if the argument_list node contains at least one
// base class that is a recognised SQLAlchemy model base (case-insensitive).
func hasSQLAlchemyBase(basesNode *sitter.Node, src []byte) bool {
	for i := 0; i < int(basesNode.NamedChildCount()); i++ {
		child := basesNode.NamedChild(i)
		var name string
		switch child.Type() {
		case "identifier":
			name = strings.ToLower(nodeText(child, src))
		case "attribute":
			objNode := child.ChildByFieldName("object")
			attrNode := child.ChildByFieldName("attribute")
			if objNode != nil && attrNode != nil {
				name = strings.ToLower(nodeText(objNode, src)) + "." + strings.ToLower(nodeText(attrNode, src))
			}
		}
		if sqlalchemyModelBases[name] {
			return true
		}
	}
	return false
}

// extractClassMembers walks the direct children of a class body block, collecting
// column and relationship assignments without recursing into method bodies.
func extractClassMembers(bodyNode *sitter.Node, src []byte) ([]rawColAST, []rawRelAST) {
	var cols []rawColAST
	var rels []rawRelAST

	for i := 0; i < int(bodyNode.ChildCount()); i++ {
		child := bodyNode.Child(i)

		// Locate the assignment node: either direct child or inside expression_statement.
		var assignNode *sitter.Node
		switch child.Type() {
		case "assignment":
			assignNode = child
		case "expression_statement":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				if nc := child.NamedChild(j); nc.Type() == "assignment" {
					assignNode = nc
					break
				}
			}
		}
		if assignNode == nil {
			continue // function_definition, decorated_definition, etc. → skip
		}

		leftNode := assignNode.ChildByFieldName("left")
		rightNode := assignNode.ChildByFieldName("right")
		if leftNode == nil || rightNode == nil || leftNode.Type() != "identifier" {
			continue
		}

		fieldName := nodeText(leftNode, src)
		if strings.HasPrefix(fieldName, "__") {
			continue // dunder attributes (__tablename__, __repr__, etc.)
		}
		if rightNode.Type() != "call" {
			continue // annotated assignments (token: str = ''), plain literals, etc.
		}

		funcNode := rightNode.ChildByFieldName("function")
		if funcNode == nil {
			continue
		}
		argsNode := rightNode.ChildByFieldName("arguments")

		switch astCallName(funcNode, src) {
		case "column", "db.column":
			cols = append(cols, parseColumnCall(fieldName, argsNode, src))
		case "reference_col":
			// reference_col('tablename', ...) is always an integer FK column.
			cols = append(cols, rawColAST{
				name:     fieldName,
				sqlType:  "Integer",
				isFKCol:  true,
				refTable: astFirstStringArg(argsNode, src),
			})
		case "relationship":
			rels = append(rels, rawRelAST{
				name:        fieldName,
				targetClass: astFirstStringArg(argsNode, src),
			})
		}
	}
	return cols, rels
}

// astCallName returns the lowercase dotted function name from a call's function node.
// e.g. Column → "column", db.Column → "db.column", reference_col → "reference_col".
func astCallName(funcNode *sitter.Node, src []byte) string {
	switch funcNode.Type() {
	case "identifier":
		return strings.ToLower(nodeText(funcNode, src))
	case "attribute":
		objNode := funcNode.ChildByFieldName("object")
		attrNode := funcNode.ChildByFieldName("attribute")
		if objNode != nil && attrNode != nil {
			return strings.ToLower(nodeText(objNode, src)) + "." + strings.ToLower(nodeText(attrNode, src))
		}
	}
	return ""
}

// parseColumnCall builds a rawColAST from a Column(...) or db.Column(...) call.
// It extracts the SQLAlchemy type and detects ForeignKey references.
func parseColumnCall(fieldName string, argsNode *sitter.Node, src []byte) rawColAST {
	col := rawColAST{name: fieldName}
	if argsNode == nil {
		return col
	}
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		arg := argsNode.NamedChild(i)
		if arg.Type() == "keyword_argument" {
			continue
		}
		// First positional arg provides the SQLAlchemy type.
		if col.sqlType == "" {
			col.sqlType = astSQLAlchemyType(arg, src)
		}
		// Detect ForeignKey(...) in any positional arg.
		if arg.Type() == "call" {
			fkFuncNode := arg.ChildByFieldName("function")
			if fkFuncNode != nil {
				fkName := astCallName(fkFuncNode, src)
				if fkName == "foreignkey" || fkName == "db.foreignkey" {
					ref := astFirstStringArg(arg.ChildByFieldName("arguments"), src)
					if ref != "" {
						if dotIdx := strings.Index(ref, "."); dotIdx >= 0 {
							ref = ref[:dotIdx]
						}
						col.isFKCol = true
						col.refTable = ref
						col.sqlType = "Integer"
					}
				}
			}
		}
	}
	return col
}

// astSQLAlchemyType extracts the SQLAlchemy type name from a Column argument node.
// Handles db.Integer, db.String(100), Integer, String(255), etc.
func astSQLAlchemyType(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "attribute":
		if attrNode := node.ChildByFieldName("attribute"); attrNode != nil {
			return nodeText(attrNode, src)
		}
	case "identifier":
		return nodeText(node, src)
	case "call":
		if funcNode := node.ChildByFieldName("function"); funcNode != nil {
			return astSQLAlchemyType(funcNode, src)
		}
	}
	return ""
}

// astFirstStringArg returns the text (quotes stripped) of the first positional
// string literal in an argument_list node.
func astFirstStringArg(argsNode *sitter.Node, src []byte) string {
	if argsNode == nil {
		return ""
	}
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		arg := argsNode.NamedChild(i)
		if arg.Type() == "string" {
			return stripPyString(nodeText(arg, src))
		}
		if arg.Type() == "keyword_argument" {
			continue
		}
	}
	return ""
}

// nodeText returns the source bytes for a tree-sitter node as a string.
func nodeText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// stripPyString strips surrounding quotes from a Python string literal.
func stripPyString(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []string{`"""`, `'''`, `"`, `'`} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) && len(s) >= 2*len(q) {
			return s[len(q) : len(s)-len(q)]
		}
	}
	return s
}

// buildProtoFields converts raw AST column data into AIP-compliant ProtoFields.
func buildProtoFields(rawCols []rawColAST, tableServiceMap map[string]string) []workerdomain.ProtoField {
	var domainFields, timeFields []workerdomain.ProtoField

	for _, rc := range rawCols {
		if rc.name == "id" {
			continue // replaced by synthetic identifier field
		}

		aipName := aipFieldName(rc.name)

		var protoType string
		var isCrossFK bool
		var refTable, refService string

		if rc.isFKCol {
			protoType = "uint64"
			isCrossFK = true
			refTable = rc.refTable
			if tableServiceMap != nil {
				refService = tableServiceMap[refTable]
			}
		} else {
			protoType = sqlAlchemyTypeToProto(rc.sqlType)
		}

		if protoType == "" {
			continue // unknown type → skip
		}

		comment := ""
		if isCrossFK {
			if refService != "" {
				comment = fmt.Sprintf("// cross-service FK: references %s (service: %s)", refTable, refService)
			} else {
				comment = "// cross-service FK: references " + refTable
			}
		}

		f := workerdomain.ProtoField{
			Name:       aipName,
			Type:       protoType,
			Comment:    comment,
			IsCrossFK:  isCrossFK,
			RefTable:   refTable,
			RefService: refService,
		}
		if protoType == "google.protobuf.Timestamp" {
			timeFields = append(timeFields, f)
		} else {
			domainFields = append(domainFields, f)
		}
	}

	if len(domainFields) == 0 && len(timeFields) == 0 {
		return nil
	}

	result := []workerdomain.ProtoField{
		{Name: "identifier", Type: "uint64", Number: 1},
		{Name: "state", Type: "<enum>", Number: 2},
	}
	n := 3
	for i := range domainFields {
		domainFields[i].Number = n
		n++
	}
	for i := range timeFields {
		timeFields[i].Number = n
		n++
	}
	result = append(result, domainFields...)
	result = append(result, timeFields...)

	hasDeleteTime, hasPurgeTime := false, false
	for _, f := range result {
		switch f.Name {
		case "delete_time":
			hasDeleteTime = true
		case "purge_time":
			hasPurgeTime = true
		}
	}
	if !hasDeleteTime {
		result = append(result, workerdomain.ProtoField{
			Name: "delete_time", Type: "google.protobuf.Timestamp", Number: n,
			Comment: "// soft-delete: set on logical deletion",
		})
		n++
	}
	if !hasPurgeTime {
		result = append(result, workerdomain.ProtoField{
			Name: "purge_time", Type: "google.protobuf.Timestamp", Number: n,
			Comment: "// hard-delete: set when permanently purged",
		})
	}

	return result
}

// sqlAlchemyTypeToProto maps a SQLAlchemy column type name to a proto scalar type.
func sqlAlchemyTypeToProto(sqlType string) string {
	switch strings.ToLower(sqlType) {
	case "integer", "int", "biginteger", "smallinteger":
		return "int64"
	case "string", "varchar", "unicode", "nvarchar", "char":
		return "string"
	case "text", "unicodetext", "clob":
		return "string"
	case "float", "numeric", "decimal", "double":
		return "double"
	case "boolean", "bool":
		return "bool"
	case "datetime", "timestamp", "date", "time":
		return "google.protobuf.Timestamp"
	case "binary", "largebinary":
		return "bytes"
	default:
		return ""
	}
}

// aipFieldName applies AIP naming conventions to a raw Python field name.
// It accepts both snake_case and camelCase inputs — camelCase is normalised to
// snake_case first so the same AIP rules apply regardless of the source style.
func aipFieldName(raw string) string {
	// Normalise camelCase (e.g. createdAt → created_at, authorId → author_id)
	// before applying AIP rename rules.
	norm := camelToSnake(raw)

	switch norm {
	case "id":
		return "identifier"
	case "created_at":
		return "create_time"
	case "updated_at":
		return "update_time"
	case "deleted_at":
		return "delete_time"
	}
	if strings.HasSuffix(norm, "_id") {
		return norm[:len(norm)-3] + "_identifier"
	}
	return norm
}

// camelToSnake converts a camelCase identifier to snake_case.
// Already-snake_case identifiers (no uppercase) are returned unchanged.
func camelToSnake(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i > 0 && c >= 'A' && c <= 'Z' {
			b.WriteByte('_')
		}
		if c >= 'A' && c <= 'Z' {
			b.WriteByte(c + 32) // ASCII toLower
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// --- Flask route parser ---

// routeDecRe matches @blueprint.route('/path', ...) decorator lines.
// Group 1: the URL path string.  Group 2: the rest of the decorator args.
var routeDecRe = regexp.MustCompile(`@\w+\.route\(\s*['"]([^'"]+)['"]([^)]*)\)`)

// methodsRe extracts the methods list from decorator args.
var methodsRe = regexp.MustCompile(`methods\s*=\s*\[([^\]]+)\]`)

// parseFlaskRoutes parses a views.py source and returns one ServiceRPC per route.
func parseFlaskRoutes(src, svcName string) []workerdomain.ServiceRPC {
	var rpcs []workerdomain.ServiceRPC
	matches := routeDecRe.FindAllStringSubmatch(src, -1)

	for _, m := range matches {
		path := m[1]
		rest := m[2]

		methods := []string{"GET"} // Flask default when methods= is absent
		if mm := methodsRe.FindStringSubmatch(rest); mm != nil {
			methods = parseMethodsList(mm[1])
		}

		for _, httpMethod := range methods {
			rpcName, isTODO := classifyCRUD(path, httpMethod, svcName)
			rpcs = append(rpcs, workerdomain.ServiceRPC{
				Name:       rpcName,
				Path:       path,
				HTTPMethod: httpMethod,
				IsTODO:     isTODO,
			})
		}
	}
	return rpcs
}

// parseMethodsList splits a bracket-contents string like `'GET', 'POST'` or
// `"GET","POST"` into a slice of method strings.
func parseMethodsList(raw string) []string {
	var methods []string
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		tok = strings.Trim(tok, `'"`)
		tok = strings.ToUpper(tok)
		if tok != "" {
			methods = append(methods, tok)
		}
	}
	return methods
}

// classifyCRUD determines whether a Flask route maps to a standard CRUD method
// or must be marked TODO for human/LLM review. Rules:
//
//   - Resource with no remaining sub-path: GET→List, POST→Create
//   - Resource with exactly one dynamic remaining segment: GET→Get, PUT/PATCH→Update, DELETE→Delete
//   - Everything else (static sub-paths like /login, extra segments like /<slug>/favorite): TODO
//
// This is conservative: any ambiguous pattern becomes TODO rather than guessing.
func classifyCRUD(path, httpMethod, svcName string) (rpcName string, isTODO bool) {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return "", true
	}

	// Find the first non-dynamic segment — the "root resource".
	resourceIdx := -1
	for i, seg := range segments {
		if !isDynamic(seg) {
			resourceIdx = i
			break
		}
	}
	if resourceIdx < 0 {
		return "", true
	}

	singular := singularizeTitle(segments[resourceIdx])
	// Segments after the root resource.
	remaining := segments[resourceIdx+1:]

	switch {
	case len(remaining) == 0 && httpMethod == "GET":
		return "List" + singular, false
	case len(remaining) == 0 && httpMethod == "POST":
		return "Create" + singular, false
	case len(remaining) == 1 && isDynamic(remaining[0]) && httpMethod == "GET":
		return "Get" + singular, false
	case len(remaining) == 1 && isDynamic(remaining[0]) && (httpMethod == "PUT" || httpMethod == "PATCH"):
		return "Update" + singular, false
	case len(remaining) == 1 && isDynamic(remaining[0]) && httpMethod == "DELETE":
		return "Delete" + singular, false
	default:
		// Non-CRUD: static sub-paths (/login, /favorite), extra segments after ID,
		// or unusual patterns — all become explicit TODO entries.
		return "", true
	}
}

// pathSegments splits a URL path into non-empty, non-prefix segments.
// "api" and "v1/v2/..." prefixes are stripped.
func pathSegments(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var segments []string
	for _, p := range parts {
		if p != "" {
			segments = append(segments, p)
		}
	}
	// Strip common API prefix segments.
	for len(segments) > 1 && (segments[0] == "api" || segments[0] == "v1" || segments[0] == "v2") {
		segments = segments[1:]
	}
	return segments
}

// isDynamic returns true when a path segment is a Flask URL parameter (e.g. <slug>).
func isDynamic(seg string) bool {
	return strings.HasPrefix(seg, "<") && strings.HasSuffix(seg, ">")
}

// resourceFromPath finds the first non-dynamic, non-prefix segment.
func resourceFromPath(segments []string) string {
	for _, seg := range segments {
		if !isDynamic(seg) {
			return seg
		}
	}
	return "resource"
}

// singularizeTitle titlecases a word and removes a trailing "s" (simple heuristic).
// Handles the common English plurals used in REST APIs (articles→Article, etc.).
func singularizeTitle(word string) string {
	if word == "" {
		return "Resource"
	}
	// Title-case.
	result := strings.ToUpper(word[:1]) + word[1:]
	// Singularize.
	if strings.HasSuffix(result, "ies") && len(result) > 3 {
		result = result[:len(result)-3] + "y"
	} else if strings.HasSuffix(result, "s") && !strings.HasSuffix(result, "ss") {
		result = result[:len(result)-1]
	}
	return result
}

// --- Proto file generator ---

// generateProto produces the full text of an AIP-compliant .proto file for
// a single service cluster.
func generateProto(svcName, blueprintGroup string, messages []workerdomain.ProtoMessage, rpcs []workerdomain.ServiceRPC) string {
	var b strings.Builder

	// Singularize so "articles" → "Article" (ArticleService, not ArticlesService).
	serviceTitleCase := singularizeTitle(svcName)
	pkgName := "generated." + svcName + ".v1"
	goPackage := "generated/" + svcName + "/v1;" + svcName + "v1"

	b.WriteString("// Code generated by Milton Prism decomposition engine — DO NOT COMMIT to the proto tree.\n")
	b.WriteString("// Service boundary derived from cluster: " + blueprintGroup + "\n")
	b.WriteString("//\n")
	b.WriteString("// Review, adjust, and move to protobuf/proto/milton_prism/... when ready.\n")
	b.WriteString("// AIP compliance: identifier, state, _time suffixes, soft-delete required.\n")
	b.WriteString("\n")
	b.WriteString("syntax = \"proto3\";\n\n")
	b.WriteString("package " + pkgName + ";\n\n")
	b.WriteString("import \"google/protobuf/timestamp.proto\";\n\n")
	b.WriteString("option go_package = \"" + goPackage + "\";\n\n")

	// Messages + enums.
	b.WriteString("// ---- Resources (derived from SQLAlchemy models) ----\n\n")
	for _, msg := range messages {
		writeMessage(&b, msg)
	}

	// Service block.
	b.WriteString("// ---- Service (derived from Flask routes) ----\n\n")
	b.WriteString("service " + serviceTitleCase + "Service {\n")

	// Deduplicate CRUD RPCs (same name may appear from overlapping route patterns).
	seen := make(map[string]bool)
	for _, rpc := range rpcs {
		if rpc.IsTODO {
			continue
		}
		if seen[rpc.Name] {
			continue
		}
		seen[rpc.Name] = true
		req, resp := rpcTypes(rpc.Name, msg0Name(messages))
		b.WriteString(fmt.Sprintf("  rpc %s(%s) returns (%s) {}\n", rpc.Name, req, resp))
	}

	// TODO entries.
	for _, rpc := range rpcs {
		if !rpc.IsTODO {
			continue
		}
		b.WriteString(fmt.Sprintf(
			"  // TODO: custom route: %s %s\n  //       review and implement as a custom method :verbInCamelCase\n",
			rpc.HTTPMethod, rpc.Path,
		))
	}

	b.WriteString("}\n")

	return b.String()
}

// writeMessage writes a proto message block and its companion state enum.
func writeMessage(b *strings.Builder, msg workerdomain.ProtoMessage) {
	enumName := msg.Name + "State"
	enumPrefix := strings.ToUpper(msg.Name)

	b.WriteString("message " + msg.Name + " {\n")
	for _, f := range msg.Fields {
		if f.Type == "<enum>" {
			b.WriteString(fmt.Sprintf("  %s %s = %d;\n", enumName, f.Name, f.Number))
			continue
		}
		if f.Comment != "" {
			b.WriteString(fmt.Sprintf("  %s %s = %d;  %s\n", f.Type, f.Name, f.Number, f.Comment))
		} else {
			b.WriteString(fmt.Sprintf("  %s %s = %d;\n", f.Type, f.Name, f.Number))
		}
	}
	if len(msg.Relationships) > 0 {
		b.WriteString("  // Relationships (not proto fields — resolve via gRPC in generated code):\n")
		for _, r := range msg.Relationships {
			b.WriteString("  //   " + r + "\n")
		}
	}
	b.WriteString("}\n\n")

	// State enum.
	b.WriteString("enum " + enumName + " {\n")
	b.WriteString(fmt.Sprintf("  %s_UNSPECIFIED = 0;\n", enumPrefix))
	b.WriteString(fmt.Sprintf("  %s_ACTIVE = 1;\n", enumPrefix))
	b.WriteString(fmt.Sprintf("  %s_DELETED = 2;\n", enumPrefix))
	b.WriteString("}\n\n")
}

// msg0Name returns the name of the first message (the "primary" resource of the service).
func msg0Name(messages []workerdomain.ProtoMessage) string {
	if len(messages) > 0 {
		return messages[0].Name
	}
	return "Resource"
}

// rpcTypes returns the request and response message names for a standard CRUD rpc.
func rpcTypes(rpcName, primaryResource string) (req, resp string) {
	prefix := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(
		strings.TrimPrefix(strings.TrimPrefix(rpcName, "List"), "Create"), "Get"), "Update"), "Delete")
	if prefix == "" {
		prefix = primaryResource
	}
	switch {
	case strings.HasPrefix(rpcName, "List"):
		return "List" + prefix + "Request", "List" + prefix + "Response"
	case strings.HasPrefix(rpcName, "Create"):
		return "Create" + prefix + "Request", prefix
	case strings.HasPrefix(rpcName, "Get"):
		return "Get" + prefix + "Request", prefix
	case strings.HasPrefix(rpcName, "Update"):
		return "Update" + prefix + "Request", prefix
	case strings.HasPrefix(rpcName, "Delete"):
		return "Delete" + prefix + "Request", "Delete" + prefix + "Response"
	default:
		return rpcName + "Request", rpcName + "Response"
	}
}
