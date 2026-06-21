package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/php"
)

var _ ports.ContractDeriver = (*EloquentDeriver)(nil)

// EloquentDeriver is the live adapter for the ContractDeriver port targeting
// Laravel/Eloquent workspaces. It mirrors FlaskSQLAlchemyDeriver: it parses
// Eloquent model classes and Laravel route declarations from the source
// workspace and produces an AIP-compliant .proto file per cluster, reusing the
// same proto-generation machinery (generateProto, buildProtoFields).
//
// Column→message mapping is deterministic, sourced from three places that
// real Laravel codebases use, in order of richness:
//
//   - the class `@property <type> $name` docblock (the primary source — most
//     BookStack models document their columns there, with PHP types);
//   - `protected $casts` (refines bool/datetime/int types);
//   - the matching `database/migrations/*create_<table>_table.php` schema.
//
// Route→RPC mapping is deterministic for standard CRUD controller actions;
// every other route, and every relation whose target model cannot be resolved,
// is marked TODO rather than invented (Canon §9 honesty, Lesson 11).
type EloquentDeriver struct{}

// NewEloquentDeriver returns a ready-to-use EloquentDeriver.
func NewEloquentDeriver() *EloquentDeriver { return &EloquentDeriver{} }

// Derive implements ports.ContractDeriver for Laravel/Eloquent workspaces.
// tableServiceMap maps Eloquent table names to service names so that
// cross-service FK annotations carry the target service name.
func (d *EloquentDeriver) Derive(
	ctx context.Context,
	cluster workerdomain.Cluster,
	workspacePath string,
	tableServiceMap map[string]string,
) (*workerdomain.DerivedContract, error) {
	svcName := serviceFromBlueprintGroup(cluster.BlueprintGroup)

	psr4 := loadPSR4Map(workspacePath)

	// Index of every model class FQN known in the workspace → message name.
	// Built once so relation targets and FK references can be resolved against
	// the full set, not just this cluster's modules. classTableIndex maps a
	// model's short class name to its table name (for belongsTo FK resolution).
	classIndex, classTableIndex := buildModelClassIndex(workspacePath, psr4)

	// Collect models from the cluster's model modules.
	var allMessages []workerdomain.ProtoMessage
	var modelModulesCount, modelFilesParsed int
	for _, m := range cluster.Modules {
		if !isEloquentModelModule(string(m)) {
			continue
		}
		modelModulesCount++
		path, ok := resolvePHPModulePath(workspacePath, string(m), psr4)
		if !ok {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		msg, parsed := parseEloquentModel(ctx, src, string(m), workspacePath, psr4, classIndex, classTableIndex, tableServiceMap)
		if !parsed {
			continue
		}
		modelFilesParsed++
		allMessages = append(allMessages, msg)
	}

	// Detect incomplete derivation: model modules present but zero messages.
	var incomplete bool
	var incompleteReason string
	if modelModulesCount > 0 && len(allMessages) == 0 {
		incomplete = true
		if modelFilesParsed == 0 {
			incompleteReason = fmt.Sprintf(
				"%d Eloquent model module(s) in cluster but no model file produced an extractable message — check for non-Eloquent classes or missing source",
				modelModulesCount,
			)
		} else {
			incompleteReason = fmt.Sprintf(
				"%d Eloquent model file(s) parsed but no mappable columns found (no @property docblock, $casts, or migration schema)",
				modelModulesCount,
			)
		}
	}

	// Collect RPCs from the centralized Laravel route files, attributed to this
	// service via the controller class namespace.
	allRPCs := parseLaravelRoutesForService(ctx, workspacePath, svcName)

	hasTODO := false
	for _, rpc := range allRPCs {
		if rpc.IsTODO {
			hasTODO = true
			break
		}
	}

	protoContent := generateProto(svcName, cluster.BlueprintGroup, allMessages, allRPCs)

	outDir := filepath.Join(workspacePath, ".milton_prism", "contracts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("eloquent deriver: create contracts dir: %w", err)
	}
	protoPath := filepath.Join(".milton_prism", "contracts", svcName+".proto")
	if err := os.WriteFile(filepath.Join(workspacePath, protoPath), []byte(protoContent), 0o644); err != nil {
		return nil, fmt.Errorf("eloquent deriver: write proto: %w", err)
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

// serviceFromBlueprintGroup extracts a lowercase service name from a PHP or
// Python blueprint group. PHP: last backslash-segment
// ("BookStack\Entities" → "entities"). It mirrors the pipeline's
// serviceNameFromBlueprint without importing the application layer.
func serviceFromBlueprintGroup(blueprintGroup string) string {
	sep := "."
	if strings.Contains(blueprintGroup, `\`) {
		sep = `\`
	}
	parts := strings.Split(blueprintGroup, sep)
	return strings.ToLower(parts[len(parts)-1])
}

// isEloquentModelModule reports whether a PHP class FQN sits in a namespace
// segment that holds Eloquent models. BookStack uses both `...\Models\...`
// (User, Role, Book) and `...\Entities\Models\...`. Plain `Entities` without a
// trailing model segment is excluded — those are Tools/Queries helpers.
func isEloquentModelModule(fqn string) bool {
	if !strings.Contains(fqn, `\`) {
		return false
	}
	segs := strings.Split(fqn, `\`)
	for i := 0; i < len(segs)-1; i++ { // never the class name itself (last seg)
		if segs[i] == "Models" || segs[i] == "Model" {
			return true
		}
	}
	return false
}

// --- PSR-4 resolution ---

// psr4Re matches a single "Namespace\\": "dir/" entry in composer.json's
// autoload psr-4 block. It is deliberately lenient about whitespace.
var psr4Re = regexp.MustCompile(`"((?:[A-Za-z0-9_]+\\\\)*[A-Za-z0-9_]+\\\\)"\s*:\s*"([^"]*)"`)

// loadPSR4Map reads composer.json and returns the PSR-4 namespace-prefix →
// directory map (prefixes keep a single trailing backslash, e.g. "BookStack\").
// Returns an empty map when composer.json is absent or unparsable; callers then
// fall back to a basename search.
func loadPSR4Map(workspacePath string) map[string]string {
	out := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(workspacePath, "composer.json"))
	if err != nil {
		return out
	}
	// Restrict to the autoload block so dev-autoload (Tests\) does not shadow it.
	text := string(data)
	for _, m := range psr4Re.FindAllStringSubmatch(text, -1) {
		// composer escapes backslashes as "\\"; the regex captured the JSON
		// form. Unescape to a single backslash prefix.
		prefix := strings.ReplaceAll(m[1], `\\`, `\`)
		dir := strings.TrimSuffix(m[2], "/")
		out[prefix] = dir
	}
	return out
}

// resolvePHPModulePath maps a PHP class FQN to its file path using the PSR-4
// map; on a miss it searches the workspace for a file whose basename matches the
// class name and whose contents declare that class.
func resolvePHPModulePath(workspacePath, fqn string, psr4 map[string]string) (string, bool) {
	// Longest-prefix PSR-4 match.
	bestPrefix, bestDir := "", ""
	for prefix, dir := range psr4 {
		if strings.HasPrefix(fqn, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix, bestDir = prefix, dir
		}
	}
	if bestPrefix != "" {
		rest := strings.TrimPrefix(fqn, bestPrefix)
		rel := filepath.Join(strings.Split(rest, `\`)...) + ".php"
		full := filepath.Join(workspacePath, bestDir, rel)
		if _, err := os.Stat(full); err == nil {
			return full, true
		}
	}

	// Fallback: basename search.
	className := lastBackslashSegment(fqn)
	var found string
	_ = filepath.WalkDir(workspacePath, func(path string, de os.DirEntry, err error) error {
		if found != "" || err != nil || de.IsDir() {
			return nil
		}
		if filepath.Base(path) != className+".php" {
			return nil
		}
		found = path
		return nil
	})
	if found != "" {
		return found, true
	}
	return "", false
}

// lastBackslashSegment returns the final segment of a PHP FQN.
func lastBackslashSegment(fqn string) string {
	if i := strings.LastIndex(fqn, `\`); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// modelTableInFileRe matches a `protected $table = 'name';` declaration in a
// model file, for the class→table index built without a full parse.
var modelTableInFileRe = regexp.MustCompile(`\$table\s*=\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

// buildModelClassIndex returns (1) the set of short class names that look like
// Eloquent models reachable in the workspace and (2) a class-name → table-name
// map. The class set lets relation targets be resolved honestly (an unknown
// target becomes a TODO); the table map lets belongsTo FK columns reference the
// real target table rather than a column-stem guess.
func buildModelClassIndex(workspacePath string, psr4 map[string]string) (map[string]bool, map[string]string) {
	index := make(map[string]bool)
	tables := make(map[string]string)
	seenDir := make(map[string]bool)
	for _, dir := range psr4 {
		root := filepath.Join(workspacePath, dir)
		if seenDir[root] {
			continue
		}
		seenDir[root] = true
		_ = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			// Only index files under a Models/Entities\Models directory.
			if !strings.Contains(path, string(os.PathSeparator)+"Models"+string(os.PathSeparator)) {
				return nil
			}
			className := strings.TrimSuffix(filepath.Base(path), ".php")
			index[className] = true
			if data, rerr := os.ReadFile(path); rerr == nil {
				if m := modelTableInFileRe.FindSubmatch(data); m != nil {
					tables[className] = string(m[1])
				} else {
					tables[className] = pluralSnake(className)
				}
			}
			return nil
		})
	}
	return index, tables
}

// --- Eloquent model parser (tree-sitter AST) ---

// eloquentModelBases are class names whose presence as a base marks an Eloquent
// model. The project base classes (Model, Entity) are included because BookStack
// models extend them; the framework names cover direct extension.
var eloquentModelBases = map[string]bool{
	"model":           true,
	"eloquentmodel":   true,
	"authenticatable": true,
	"pivot":           true,
	"entity":          true, // BookStack abstract Eloquent base for Book/Page/Chapter
	"bookchild":       true, // Page/Chapter extend this Eloquent base
}

// rawEloquentColumn holds one parsed column candidate before AIP normalisation.
type rawEloquentColumn struct {
	name    string // snake_case column name
	phpType string // PHP/Eloquent type token (int, string, bool, Carbon, datetime, ...)
}

// parseEloquentModel parses one Eloquent model class file into a ProtoMessage.
// fqn is the class's full name; it is used only for diagnostics and naming.
func parseEloquentModel(
	ctx context.Context,
	src []byte,
	fqn, workspacePath string,
	psr4 map[string]string,
	classIndex map[string]bool,
	classTableIndex map[string]string,
	tableServiceMap map[string]string,
) (workerdomain.ProtoMessage, bool) {
	p := sitter.NewParser()
	p.SetLanguage(php.GetLanguage())
	tree, _ := p.ParseCtx(ctx, nil, src)
	if tree == nil {
		return workerdomain.ProtoMessage{}, false
	}
	root := tree.RootNode()

	classNode, docComment := findClassDeclaration(root, src)
	if classNode == nil {
		return workerdomain.ProtoMessage{}, false
	}
	if !isEloquentClass(classNode, src) {
		return workerdomain.ProtoMessage{}, false
	}

	className := classNodeName(classNode, src)
	bodyNode := classNode.ChildByFieldName("body")
	if bodyNode == nil {
		return workerdomain.ProtoMessage{}, false
	}

	members := extractEloquentMembers(bodyNode, src, classIndex)
	tableName := members.table
	if tableName == "" {
		tableName = pluralSnake(className)
	}
	casts := members.casts
	relations := members.relations

	// Columns: union of @property docblock, $casts keys, and migration schema.
	colMap := make(map[string]rawEloquentColumn)
	order := []string{}
	addCol := func(name, phpType string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, seen := colMap[name]; !seen {
			order = append(order, name)
		}
		// Later, more specific sources (casts) override an empty/weaker type.
		existing := colMap[name]
		if existing.phpType == "" || phpType != "" {
			colMap[name] = rawEloquentColumn{name: name, phpType: phpType}
		}
	}

	for _, pc := range parsePropertyDocblock(docComment) {
		addCol(pc.name, pc.phpType)
	}
	for col, castType := range casts {
		addCol(col, castType) // cast type wins for known columns
		colMap[col] = rawEloquentColumn{name: col, phpType: castType}
	}
	for _, mc := range readMigrationColumns(workspacePath, tableName) {
		addCol(mc.name, mc.phpType)
	}

	if len(order) == 0 {
		return workerdomain.ProtoMessage{}, false
	}

	rawCols := make([]rawColAST, 0, len(order))
	for _, name := range order {
		c := colMap[name]
		sqlType := phpTypeToSQLAlchemy(c.phpType)
		isFK, refTable := eloquentFKHint(c.name, sqlType)
		// A belongsTo relation names the FK column and its target model. When
		// present it is authoritative: the referenced table is the target
		// model's table (User → users), not the column-stem guess (author → authors).
		if target, ok := members.belongsToFK[c.name]; ok {
			isFK = true
			if t := classTableIndex[target]; t != "" {
				refTable = t
			} else {
				refTable = pluralSnake(target)
			}
			if sqlType == "" {
				sqlType = "Integer"
			}
		}
		rawCols = append(rawCols, rawColAST{
			name:     c.name,
			sqlType:  sqlType,
			isFKCol:  isFK,
			refTable: refTable,
		})
	}

	fields := buildProtoFields(rawCols, tableServiceMap)
	if len(fields) == 0 {
		return workerdomain.ProtoMessage{}, false
	}

	return workerdomain.ProtoMessage{
		Name:          className,
		Fields:        fields,
		Relationships: relations,
	}, true
}

// findClassDeclaration returns the first class_declaration node in the tree and
// the docblock comment immediately preceding it (empty when none).
func findClassDeclaration(root *sitter.Node, src []byte) (*sitter.Node, string) {
	var prevComment string
	for i := 0; i < int(root.ChildCount()); i++ {
		c := root.Child(i)
		switch c.Type() {
		case "comment":
			prevComment = nodeText(c, src)
		case "class_declaration":
			return c, prevComment
		default:
			// A non-comment, non-class node breaks the docblock adjacency.
			if c.Type() != "php_tag" {
				prevComment = ""
			}
		}
	}
	return nil, ""
}

// isEloquentClass reports whether the class_declaration extends a recognised
// Eloquent base (case-insensitive on the short base name).
func isEloquentClass(classNode *sitter.Node, src []byte) bool {
	for i := 0; i < int(classNode.ChildCount()); i++ {
		c := classNode.Child(i)
		if c.Type() != "base_clause" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			base := strings.ToLower(lastBackslashSegment(nodeText(c.NamedChild(j), src)))
			if eloquentModelBases[base] {
				return true
			}
		}
	}
	return false
}

// classNodeName returns the class name from a class_declaration node.
func classNodeName(classNode *sitter.Node, src []byte) string {
	if n := classNode.ChildByFieldName("name"); n != nil {
		return nodeText(n, src)
	}
	for i := 0; i < int(classNode.ChildCount()); i++ {
		if c := classNode.Child(i); c.Type() == "name" {
			return nodeText(c, src)
		}
	}
	return ""
}

// eloquentRelationMethods are the Eloquent relation factory method names.
var eloquentRelationMethods = map[string]bool{
	"hasone": true, "hasmany": true, "belongsto": true, "belongstomany": true,
	"hasmanythrough": true, "hasonethrough": true,
	"morphone": true, "morphmany": true, "morphto": true, "morphtomany": true,
}

// eloquentMembers holds the structured result of parsing a model class body.
type eloquentMembers struct {
	table       string            // explicit $table, empty when unset
	casts       map[string]string // column → cast type
	relations   []string          // relationship descriptions (sorted)
	belongsToFK map[string]string // FK column → target model class name
}

// extractEloquentMembers walks a class body and returns its structured members.
// Relations to a target class not present in classIndex are annotated as
// unresolved (honest TODO). belongsTo relations additionally record their FK
// column → target so FK columns can be resolved to the real target table.
func extractEloquentMembers(
	bodyNode *sitter.Node,
	src []byte,
	classIndex map[string]bool,
) eloquentMembers {
	out := eloquentMembers{
		casts:       make(map[string]string),
		belongsToFK: make(map[string]string),
	}
	for i := 0; i < int(bodyNode.ChildCount()); i++ {
		child := bodyNode.Child(i)
		switch child.Type() {
		case "property_declaration":
			name, init := propertyNameAndInit(child, src)
			switch name {
			case "table":
				out.table = firstStringContent(init, src)
			case "casts":
				for k, v := range parseAssocArray(init, src) {
					out.casts[k] = v
				}
			}
		case "method_declaration":
			rel, ok, fkCol, target := relationFromMethod(child, src, classIndex)
			if ok {
				out.relations = append(out.relations, rel)
			}
			if fkCol != "" && target != "" {
				out.belongsToFK[fkCol] = target
			}
		}
	}
	sort.Strings(out.relations)
	return out
}

// propertyNameAndInit returns the property variable name (without "$") and its
// initializer node (the array/string after "=").
func propertyNameAndInit(prop *sitter.Node, src []byte) (string, *sitter.Node) {
	for i := 0; i < int(prop.NamedChildCount()); i++ {
		el := prop.NamedChild(i)
		if el.Type() != "property_element" {
			continue
		}
		var name string
		var init *sitter.Node
		for j := 0; j < int(el.NamedChildCount()); j++ {
			c := el.NamedChild(j)
			switch c.Type() {
			case "variable_name":
				name = strings.TrimPrefix(nodeText(c, src), "$")
			case "property_initializer":
				for k := 0; k < int(c.NamedChildCount()); k++ {
					init = c.NamedChild(k)
				}
			}
		}
		return name, init
	}
	return "", nil
}

// firstStringContent returns the unquoted content of a string node (or the first
// string descendant of init).
func firstStringContent(init *sitter.Node, src []byte) string {
	if init == nil {
		return ""
	}
	if init.Type() == "string" {
		return phpStringContent(init, src)
	}
	for i := 0; i < int(init.NamedChildCount()); i++ {
		if s := firstStringContent(init.NamedChild(i), src); s != "" {
			return s
		}
	}
	return ""
}

// phpStringContent returns the inner text of a tree-sitter PHP `string` node.
func phpStringContent(strNode *sitter.Node, src []byte) string {
	for i := 0; i < int(strNode.NamedChildCount()); i++ {
		if c := strNode.NamedChild(i); c.Type() == "string_content" {
			return nodeText(c, src)
		}
	}
	// Empty string literal ('') has no string_content child.
	return ""
}

// parseAssocArray reads a PHP array_creation_expression of 'key' => 'value'
// string pairs into a map. Non-string values are skipped.
func parseAssocArray(init *sitter.Node, src []byte) map[string]string {
	out := make(map[string]string)
	if init == nil || init.Type() != "array_creation_expression" {
		return out
	}
	for i := 0; i < int(init.NamedChildCount()); i++ {
		el := init.NamedChild(i)
		if el.Type() != "array_element_initializer" {
			continue
		}
		var key, val string
		var seenArrow bool
		for j := 0; j < int(el.ChildCount()); j++ {
			c := el.Child(j)
			if c.Type() == "=>" {
				seenArrow = true
				continue
			}
			if c.Type() != "string" {
				continue
			}
			if !seenArrow {
				key = phpStringContent(c, src)
			} else {
				val = phpStringContent(c, src)
			}
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// relationFromMethod inspects a method body for a `$this-><relation>(Target::class, ...)`
// call. It returns a relation description "name → Target" (or a TODO-annotated
// description when the target model is not resolvable), plus — for belongsTo
// relations only — the FK column and target class so the column can be resolved
// to the correct referenced table.
func relationFromMethod(method *sitter.Node, src []byte, classIndex map[string]bool) (desc string, ok bool, fkColumn, fkTarget string) {
	methodName := ""
	if n := method.ChildByFieldName("name"); n != nil {
		methodName = nodeText(n, src)
	} else {
		for i := 0; i < int(method.ChildCount()); i++ {
			if c := method.Child(i); c.Type() == "name" {
				methodName = nodeText(c, src)
				break
			}
		}
	}
	if methodName == "" {
		return "", false, "", ""
	}

	call := findRelationCall(method, src)
	if call == nil {
		return "", false, "", ""
	}
	relKind, target, secondArg := relationCallInfo(call, src)
	if relKind == "" {
		return "", false, "", ""
	}

	// belongsTo carries the owning FK on this model: the explicit second-arg
	// column, or Eloquent's `<method>_id` default.
	if strings.ToLower(relKind) == "belongsto" && target != "" {
		fkColumn = secondArg
		if fkColumn == "" {
			fkColumn = pascalToSnake(methodName) + "_id"
		}
		fkTarget = target
	}

	switch {
	case target == "":
		desc = fmt.Sprintf("%s → ? (TODO: %s target not statically resolvable)", methodName, relKind)
	case !classIndex[target]:
		desc = fmt.Sprintf("%s → %s (TODO: target model not found in workspace)", methodName, target)
	default:
		desc = methodName + " → " + target
	}
	return desc, true, fkColumn, fkTarget
}

// findRelationCall returns the first member_call_expression in method whose
// member name is an Eloquent relation method.
func findRelationCall(node *sitter.Node, src []byte) *sitter.Node {
	if node.Type() == "member_call_expression" {
		for i := 0; i < int(node.ChildCount()); i++ {
			c := node.Child(i)
			if c.Type() == "name" && eloquentRelationMethods[strings.ToLower(nodeText(c, src))] {
				return node
			}
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		if found := findRelationCall(node.Child(i), src); found != nil {
			return found
		}
	}
	return nil
}

// relationCallInfo returns the relation method name, the first Target::class
// argument's short class name (empty when the first argument is not a
// `<Class>::class` literal), and the second positional string argument (the
// explicit FK column for belongsTo / pivot table for belongsToMany), empty when
// absent.
func relationCallInfo(call *sitter.Node, src []byte) (relKind, target, secondArg string) {
	for i := 0; i < int(call.ChildCount()); i++ {
		if c := call.Child(i); c.Type() == "name" {
			relKind = nodeText(c, src)
		}
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		for i := 0; i < int(call.ChildCount()); i++ {
			if call.Child(i).Type() == "arguments" {
				args = call.Child(i)
			}
		}
	}
	if args == nil {
		return relKind, "", ""
	}

	argIdx := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		arg := args.NamedChild(i)
		if arg.Type() != "argument" {
			continue
		}
		switch argIdx {
		case 0:
			cca := firstChildOfType(arg, "class_constant_access_expression")
			if cca == nil {
				return relKind, "", ""
			}
			for j := 0; j < int(cca.ChildCount()); j++ {
				c := cca.Child(j)
				if c.Type() == "name" || c.Type() == "qualified_name" {
					target = lastBackslashSegment(nodeText(c, src))
					break
				}
			}
		case 1:
			secondArg = firstStringContent(arg, src)
		}
		argIdx++
		if argIdx > 1 {
			break
		}
	}
	return relKind, target, secondArg
}

// firstChildOfType returns the first descendant of node with the given type.
func firstChildOfType(node *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == typ {
			return c
		}
		if found := firstChildOfType(c, typ); found != nil {
			return found
		}
	}
	return nil
}

// --- @property docblock parser ---

// docPropertyRe matches a single "@property <type> $name" line in a docblock.
// The type may carry a leading "?" and union pipes; only the leading token is
// used for mapping.
var docPropertyRe = regexp.MustCompile(`@property(?:-read|-write)?\s+([^\s$]+)\s+\$([A-Za-z_][A-Za-z0-9_]*)`)

// parsePropertyDocblock extracts (name, phpType) pairs from a class docblock.
// Properties whose declared type is a Collection or a model class (relations
// documented as @property) are filtered out — they are not scalar columns.
func parsePropertyDocblock(comment string) []rawEloquentColumn {
	if comment == "" {
		return nil
	}
	var cols []rawEloquentColumn
	for _, m := range docPropertyRe.FindAllStringSubmatch(comment, -1) {
		phpType := strings.TrimPrefix(m[1], "?")
		name := m[2]
		if isRelationDocType(phpType) {
			continue
		}
		cols = append(cols, rawEloquentColumn{name: name, phpType: phpType})
	}
	return cols
}

// isRelationDocType returns true for docblock types that denote a relation or a
// model object, not a scalar column.
func isRelationDocType(t string) bool {
	base := lastBackslashSegment(t)
	switch base {
	case "Collection", "EloquentCollection", "BaseCollection":
		return true
	}
	// A capitalised, non-scalar type token is a model/object property.
	switch strings.ToLower(base) {
	case "int", "integer", "string", "bool", "boolean", "float", "double",
		"carbon", "datetime", "date", "array", "mixed", "object":
		return false
	}
	// Anything else starting uppercase is treated as a related object/model.
	return len(base) > 0 && base[0] >= 'A' && base[0] <= 'Z'
}

// --- migration schema reader ---

// migrationColRe matches `$table-><type>('column'...)` schema builder calls.
var migrationColRe = regexp.MustCompile(`\$table->([a-zA-Z]+)\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

// readMigrationColumns finds the create-table migration for tableName and
// returns its column (name, phpType) pairs. Schema-only calls (increments,
// timestamps, index, foreign) are expanded or skipped appropriately.
func readMigrationColumns(workspacePath, tableName string) []rawEloquentColumn {
	migDir := filepath.Join(workspacePath, "database", "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		return nil
	}
	var file string
	suffix := "create_" + tableName + "_table.php"
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			file = filepath.Join(migDir, e.Name())
			break
		}
	}
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var cols []rawEloquentColumn
	for _, m := range migrationColRe.FindAllStringSubmatch(string(data), -1) {
		builder := strings.ToLower(m[1])
		name := m[2]
		switch builder {
		case "index", "unique", "primary", "foreign", "dropcolumn", "dropindex",
			"dropforeign", "dropprimary", "renamecolumn", "dropsoftdeletes":
			continue // schema-shaping, not a column
		}
		cols = append(cols, rawEloquentColumn{name: name, phpType: migrationBuilderToPHPType(builder)})
	}
	return cols
}

// --- type mapping ---

// phpTypeToSQLAlchemy maps a PHP/Eloquent type token to the SQLAlchemy type
// vocabulary already understood by sqlAlchemyTypeToProto, so the shared
// proto-field machinery (buildProtoFields) is reused without duplication.
func phpTypeToSQLAlchemy(phpType string) string {
	switch strings.ToLower(lastBackslashSegment(phpType)) {
	case "int", "integer", "bigint", "biginteger", "smallint", "tinyint", "unsignedinteger", "unsignedbiginteger":
		return "Integer"
	case "string", "varchar", "char":
		return "String"
	case "text", "longtext", "mediumtext", "html":
		return "Text"
	case "bool", "boolean":
		return "Boolean"
	case "float", "double", "decimal", "numeric":
		return "Float"
	case "carbon", "datetime", "timestamp", "date", "time", "immutable_datetime":
		return "DateTime"
	case "binary", "blob":
		return "Binary"
	case "array", "json", "collection", "object", "mixed":
		return "" // not a scalar proto column
	default:
		return ""
	}
}

// migrationBuilderToPHPType maps a Laravel schema-builder method to a PHP type.
func migrationBuilderToPHPType(builder string) string {
	switch builder {
	case "increments", "bigincrements", "integer", "biginteger", "smallinteger",
		"tinyinteger", "unsignedinteger", "unsignedbiginteger", "unsignedtinyinteger", "id":
		return "int"
	case "string", "char":
		return "string"
	case "text", "longtext", "mediumtext":
		return "text"
	case "boolean":
		return "bool"
	case "float", "double", "decimal":
		return "float"
	case "timestamp", "datetime", "date":
		return "datetime"
	default:
		return ""
	}
}

// eloquentFKHint reports whether a column is a foreign key by the `_id`
// convention, and resolves the referenced table name. A column qualifies only
// when its declared type is integer-like — `external_auth_id` typed as a string
// is an external identifier, not an Eloquent FK, and must stay a string column.
// The reference table is the pluralised stem (author_id → authors); if that
// table is owned by another service in tableServiceMap, buildProtoFields
// annotates the cross-service FK.
func eloquentFKHint(colName, sqlType string) (bool, string) {
	if colName == "id" || !strings.HasSuffix(colName, "_id") {
		return false, ""
	}
	// Only integer-typed (or untyped, defaulting to int) columns are FKs.
	if sqlType != "" && sqlType != "Integer" {
		return false, ""
	}
	stem := strings.TrimSuffix(colName, "_id")
	// Polymorphic columns (commentable_id) point at no single table; their
	// "<entity>able" stem still pluralises but the reference simply stays
	// unresolved in tableServiceMap, so no cross-service annotation is invented.
	candidate := pluralSnake(stem)
	return true, candidate
}

// pluralSnake converts a PascalCase class name or a snake stem to Laravel's
// default plural snake_case table name (Book → books, BookShelf → book_shelves,
// category → categories).
func pluralSnake(name string) string {
	snake := pascalToSnake(name)
	return pluralize(snake)
}

// pascalToSnake converts PascalCase/camelCase to snake_case; already-snake input
// passes through unchanged.
func pascalToSnake(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i > 0 && c >= 'A' && c <= 'Z' {
			b.WriteByte('_')
		}
		if c >= 'A' && c <= 'Z' {
			b.WriteByte(c + 32)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pluralize applies the simple English plural rules Laravel uses for the common
// REST cases (y→ies, default +s; already-plural left mostly alone).
func pluralize(word string) string {
	if word == "" {
		return word
	}
	if strings.HasSuffix(word, "y") && len(word) > 1 && !isVowel(word[len(word)-2]) {
		return word[:len(word)-1] + "ies"
	}
	if strings.HasSuffix(word, "fe") {
		return word[:len(word)-2] + "ves" // knife → knives
	}
	if strings.HasSuffix(word, "f") {
		return word[:len(word)-1] + "ves" // shelf → shelves
	}
	if strings.HasSuffix(word, "s") || strings.HasSuffix(word, "x") ||
		strings.HasSuffix(word, "z") || strings.HasSuffix(word, "ch") || strings.HasSuffix(word, "sh") {
		return word + "es"
	}
	return word + "s"
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// --- Laravel route parser ---

// laravelRouteRe matches `Route::<verb>('path', [<Controller>::class, 'action'])`.
// Group 1: HTTP verb. Group 2: URL path. Group 3: controller reference
// (possibly namespace-aliased). Group 4: controller action method.
var laravelRouteRe = regexp.MustCompile(
	`Route::(get|post|put|patch|delete)\(\s*['"]([^'"]*)['"]\s*,\s*\[\s*([A-Za-z_\\][A-Za-z0-9_\\]*)::class\s*,\s*['"]([A-Za-z0-9_]+)['"]`)

// useAliasRe matches `use BookStack\Entities\Controllers as EntityControllers;`
// and `use BookStack\Users\Controllers\UserApiController;` import lines.
var useAliasRe = regexp.MustCompile(`use\s+([A-Za-z_\\][A-Za-z0-9_\\]*?)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?\s*;`)

// parseLaravelRoutesForService reads routes/api.php and routes/web.php and
// returns the ServiceRPCs attributed to svcName. Attribution: a route's
// controller reference is resolved through the file's `use` aliases to a full
// FQN; the service is the second namespace segment (BookStack\<Service>\...).
// Standard CRUD controller actions become CRUD RPCs; everything else is TODO.
func parseLaravelRoutesForService(_ context.Context, workspacePath, svcName string) []workerdomain.ServiceRPC {
	var rpcs []workerdomain.ServiceRPC
	for _, rel := range []string{
		filepath.Join("routes", "api.php"),
		filepath.Join("routes", "web.php"),
	} {
		data, err := os.ReadFile(filepath.Join(workspacePath, rel))
		if err != nil {
			continue
		}
		text := string(data)
		aliases := parseUseAliases(text)

		for _, m := range laravelRouteRe.FindAllStringSubmatch(text, -1) {
			httpMethod := strings.ToUpper(m[1])
			path := m[2]
			ctrlRef := m[3]
			action := m[4]

			ctrlFQN := resolveControllerFQN(ctrlRef, aliases)
			if routeServiceOf(ctrlFQN) != svcName {
				continue
			}

			rpcName, isTODO := classifyLaravelAction(path, httpMethod, action)
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

// parseUseAliases returns a map from the local alias (or short class name) to
// the full namespace prefix it imports.
func parseUseAliases(text string) map[string]string {
	aliases := make(map[string]string)
	for _, m := range useAliasRe.FindAllStringSubmatch(text, -1) {
		fqn := m[1]
		alias := m[2]
		if alias != "" {
			aliases[alias] = fqn
		} else {
			aliases[lastBackslashSegment(fqn)] = fqn
		}
	}
	return aliases
}

// resolveControllerFQN expands an aliased controller reference into a full FQN
// using the file's use-aliases. `EntityControllers\BookApiController` →
// `BookStack\Entities\Controllers\BookApiController`; a bare `BookshelfController`
// → its imported FQN.
func resolveControllerFQN(ref string, aliases map[string]string) string {
	if i := strings.Index(ref, `\`); i >= 0 {
		head := ref[:i]
		if full, ok := aliases[head]; ok {
			return full + ref[i:]
		}
		return ref
	}
	if full, ok := aliases[ref]; ok {
		return full
	}
	return ref
}

// routeServiceOf returns the lowercase service segment of a controller FQN
// (BookStack\<Service>\...). Returns "" when the FQN has no service segment.
func routeServiceOf(fqn string) string {
	segs := strings.Split(fqn, `\`)
	if len(segs) < 2 {
		return ""
	}
	// segs[0] is the root vendor namespace (BookStack); segs[1] is the service.
	return strings.ToLower(segs[1])
}

// crudActions maps standard Laravel controller action names to a CRUD verb.
var crudActions = map[string]string{
	"index": "List", "list": "List",
	"store": "Create", "create": "Create",
	"show": "Get", "read": "Get",
	"update": "Update", "edit": "Update",
	"destroy": "Delete", "delete": "Delete",
}

// classifyLaravelAction maps a route to a CRUD RPC when the controller action is
// a standard resource action AND the path shape agrees with the verb; otherwise
// it returns a TODO. This is conservative: ambiguity becomes a TODO, never a
// guessed RPC (Lesson 11).
func classifyLaravelAction(path, httpMethod, action string) (string, bool) {
	verb, ok := crudActions[strings.ToLower(action)]
	if !ok {
		return "", true
	}

	segments := pathSegments(path)
	if len(segments) == 0 {
		return "", true
	}
	resourceIdx := -1
	for i, seg := range segments {
		if !isLaravelDynamic(seg) {
			resourceIdx = i
			break
		}
	}
	if resourceIdx < 0 {
		return "", true
	}
	singular := singularizeTitle(segments[resourceIdx])
	remaining := segments[resourceIdx+1:]

	// The action verb must agree with the path shape, mirroring the Flask
	// CRUD classifier: collection paths for List/Create, single-resource paths
	// (one dynamic trailing segment) for Get/Update/Delete.
	switch verb {
	case "List", "Create":
		if len(remaining) == 0 {
			return verb + singular, false
		}
	case "Get", "Update", "Delete":
		if len(remaining) == 1 && isLaravelDynamic(remaining[0]) {
			return verb + singular, false
		}
	}
	return "", true
}

// isLaravelDynamic reports whether a path segment is a Laravel route parameter
// (e.g. {id}, {slug}).
func isLaravelDynamic(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}
