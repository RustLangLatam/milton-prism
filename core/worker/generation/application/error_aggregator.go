package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
)

// errorAggregatorPath is the workspace-relative path of the shared gateway
// error aggregator. Agents must NOT generate this file — the pipeline owns it.
const errorAggregatorPath = "pkg/gateway/common/error/message_error.go"

// errorAggregatorService is the synthetic service name used to key the
// pipeline-assembled aggregator artifact. No real service uses this name.
const errorAggregatorService = "__pipeline__"

// errorMessagesVarRE matches Go declarations of the form:
//
//	var fooErrorMessages = map[string]string{
var errorMessagesVarRE = regexp.MustCompile(`(?m)^var (\w+ErrorMessages)\s*=`)

// extractErrorVarNames returns all <X>ErrorMessages variable names declared in
// content — one per per-service gateway error file.
func extractErrorVarNames(content string) []string {
	matches := errorMessagesVarRE.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// scanExistingErrorVarNames walks the monorepo's pkg/gateway/common/error/
// directory and collects every *ErrorMessages variable name found in
// *_errors.go files (excluding message_error.go itself). Returns an empty
// map — not an error — when the directory does not exist, which is expected
// in test environments where monorepoRoot is a placeholder path.
func scanExistingErrorVarNames(monorepoRoot string) map[string]struct{} {
	dir := filepath.Join(monorepoRoot, "pkg", "gateway", "common", "error")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return make(map[string]struct{})
	}
	result := make(map[string]struct{})
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_errors.go") || name == "message_error.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, varName := range extractErrorVarNames(string(data)) {
			result[varName] = struct{}{}
		}
	}
	return result
}

// buildMessageErrorGo generates the complete pkg/gateway/common/error/message_error.go
// from the union of all *ErrorMessages variable names discovered across the monorepo.
// The static body (ErrorMessage struct, HandlerErrorMessage, formatters) is fixed;
// only lookupErrorMessage changes as services are added.
func buildMessageErrorGo(varNames map[string]struct{}) string {
	sorted := make([]string, 0, len(varNames))
	for name := range varNames {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	w := &strings.Builder{}

	// Static preamble — never changes.
	fmt.Fprint(w, "package message_error\n\n")
	fmt.Fprint(w, "import (\n")
	fmt.Fprint(w, "\t\"encoding/json\"\n")
	fmt.Fprint(w, "\t\"strings\"\n")
	fmt.Fprint(w, "\t\"unicode\"\n\n")
	fmt.Fprint(w, "\t\"github.com/grpc-ecosystem/grpc-gateway/v2/runtime\"\n")
	fmt.Fprint(w, "\t\"golang.org/x/text/cases\"\n")
	fmt.Fprint(w, "\t\"golang.org/x/text/language\"\n")
	fmt.Fprint(w, "\t\"google.golang.org/grpc/status\"\n")
	fmt.Fprint(w, ")\n\n")
	fmt.Fprint(w, "// ErrorMessage represents an error message with details, status code, and title.\n")
	fmt.Fprint(w, "type ErrorMessage struct {\n")
	fmt.Fprint(w, "\tDetail string `json:\"detail,omitempty\"` // Human-readable error message\n")
	fmt.Fprint(w, "\tStatus int    `json:\"status,omitempty\"` // HTTP status code\n")
	fmt.Fprint(w, "\tTitle  string `json:\"title,omitempty\"`  // Short, human-readable error title\n")
	fmt.Fprint(w, "}\n\n")
	fmt.Fprint(w, "// Error returns the JSON representation of the ErrorMessage.\n")
	fmt.Fprint(w, "func (e *ErrorMessage) Error() string {\n")
	fmt.Fprint(w, "\terrStr, _ := json.Marshal(e)\n")
	fmt.Fprint(w, "\treturn string(errStr)\n")
	fmt.Fprint(w, "}\n\n")
	fmt.Fprint(w, "// HandlerErrorMessage converts a gRPC status to an ErrorMessage.\n")
	fmt.Fprint(w, "func HandlerErrorMessage(st status.Status) ErrorMessage {\n")
	fmt.Fprint(w, "\tstatusCode := runtime.HTTPStatusFromCode(st.Code())\n")
	fmt.Fprint(w, "\tdetail := st.Message()\n\n")
	fmt.Fprint(w, "\t// Split the detail to extract the error code and the associated message\n")
	fmt.Fprint(w, "\tparts := strings.SplitN(detail, \": \", 2)\n")
	fmt.Fprint(w, "\tcode := parts[0]  // Assume the code is the first part before \": \"\n")
	fmt.Fprint(w, "\tmessage := detail // Default to using the whole detail as the message\n")
	fmt.Fprint(w, "\tif len(parts) > 1 {\n")
	fmt.Fprint(w, "\t\tmessage = formatErrorMessage(parts[1])\n")
	fmt.Fprint(w, "\t}\n\n")
	fmt.Fprint(w, "\t// Try to get a custom error message based on the extracted error code\n")
	fmt.Fprint(w, "\tif customMsg, ok := lookupErrorMessage(code); ok {\n")
	fmt.Fprint(w, "\t\tmessage = customMsg\n")
	fmt.Fprint(w, "\t}\n\n")
	fmt.Fprint(w, "\treturn ErrorMessage{\n")
	fmt.Fprint(w, "\t\tDetail: message,\n")
	fmt.Fprint(w, "\t\tStatus: statusCode,\n")
	fmt.Fprint(w, "\t\tTitle:  st.Code().String(),\n")
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "}\n\n")
	fmt.Fprint(w, "// formatErrorMessage converts Failure_X_Y style messages into readable form.\n")
	fmt.Fprint(w, "//\n")
	fmt.Fprint(w, "// Examples:\n")
	fmt.Fprint(w, "//\n")
	fmt.Fprint(w, "//\tformatErrorMessage(\"Failure_Missing_Identifier\") → \"Failure missing identifier.\"\n")
	fmt.Fprint(w, "//\tformatErrorMessage(\"Failure_Company_Not_Found\")  → \"Failure company not found.\"\n")
	fmt.Fprint(w, "func formatErrorMessage(msg string) string {\n")
	fmt.Fprint(w, "\tparts := strings.Split(msg, \"_\")\n")
	fmt.Fprint(w, "\tfor i, part := range parts {\n")
	fmt.Fprint(w, "\t\tif containsInternalUppercase(part) {\n")
	fmt.Fprint(w, "\t\t\tparts[i] = part\n")
	fmt.Fprint(w, "\t\t} else {\n")
	fmt.Fprint(w, "\t\t\tparts[i] = strings.ToLower(part)\n")
	fmt.Fprint(w, "\t\t}\n")
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "\tif len(parts) > 0 {\n")
	fmt.Fprint(w, "\t\tparts[0] = cases.Title(language.Und).String(parts[0])\n")
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "\treturn strings.Join(parts, \" \") + \".\"\n")
	fmt.Fprint(w, "}\n\n")
	fmt.Fprint(w, "// containsInternalUppercase checks if a word contains uppercase letters other than the first letter.\n")
	fmt.Fprint(w, "func containsInternalUppercase(word string) bool {\n")
	fmt.Fprint(w, "\tfor i, r := range word {\n")
	fmt.Fprint(w, "\t\tif i > 0 && unicode.IsUpper(r) {\n")
	fmt.Fprint(w, "\t\t\treturn true\n")
	fmt.Fprint(w, "\t\t}\n")
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "\treturn false\n")
	fmt.Fprint(w, "}\n\n")

	// Dynamic part — lookupErrorMessage references all discovered maps.
	fmt.Fprint(w, "// lookupErrorMessage searches for an API-friendly message by error code across all service maps.\n")
	fmt.Fprint(w, "func lookupErrorMessage(code string) (string, bool) {\n")
	fmt.Fprint(w, "\tmaps := []map[string]string{\n")
	for _, name := range sorted {
		fmt.Fprintf(w, "\t\t%s,\n", name)
	}
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "\tfor _, m := range maps {\n")
	fmt.Fprint(w, "\t\tif msg, ok := m[code]; ok {\n")
	fmt.Fprint(w, "\t\t\treturn msg, true\n")
	fmt.Fprint(w, "\t\t}\n")
	fmt.Fprint(w, "\t}\n")
	fmt.Fprint(w, "\treturn \"\", false\n")
	fmt.Fprint(w, "}\n")

	return w.String()
}

// assembleErrorAggregator builds pkg/gateway/common/error/message_error.go from
// the union of:
//   - *ErrorMessages variable names found in existing *_errors.go files on disk
//     (platform services that are always present in the monorepo)
//   - *ErrorMessages variable names extracted from newly generated *_errors.go
//     artifacts persisted by each successfully completed service agent
//
// The result is persisted as a single "__pipeline__" service artifact, so
// PublishMigration sees exactly one canonical message_error.go — eliminating
// the MIG211 shared-file conflict that occurs when multiple agents each write
// their own version of the aggregator.
//
// This method never returns an error: failures are logged as warnings and the
// migration continues. A missing aggregator degrades the gateway's friendly-
// message lookup (codes fall back to raw Failure_X_Y formatting) but does not
// block generation or publishing.
func (p *Pipeline) assembleErrorAggregator(ctx context.Context, migrationID uint64, pkg *ports.GenerationPackage, final []workerdomain.ServiceGenerationRecord) {
	doneSet := make(map[string]bool, len(final))
	for _, r := range final {
		if r.Status == workerdomain.ServiceStatusDone {
			doneSet[r.ServiceName] = true
		}
	}

	// Step 1: collect variable names from existing platform/service files on disk.
	// This covers auth, db, common, identity, repository, migration, analysis, and
	// any service that was generated in a prior migration and is already in the repo.
	varNames := scanExistingErrorVarNames(p.monorepoRoot)

	// Step 2: overlay with newly generated service artifacts (not yet on disk).
	for _, svc := range pkg.Services {
		if !doneSet[svc.Name] {
			continue
		}
		artifacts, err := p.store.ListArtifacts(ctx, migrationID, svc.Name)
		if err != nil {
			applog.Warningf("generation-worker: aggregator list artifacts service=%s: %v", svc.Name, err)
			continue
		}
		for _, a := range artifacts {
			base := filepath.Base(a.Path)
			if !strings.HasSuffix(base, "_errors.go") || base == "message_error.go" {
				continue
			}
			for _, name := range extractErrorVarNames(string(a.Content)) {
				varNames[name] = struct{}{}
			}
		}
	}

	if len(varNames) == 0 {
		applog.Warningf("generation-worker: aggregator found no error variable names — skipping message_error.go assembly migration_id=%d", migrationID)
		return
	}

	content := buildMessageErrorGo(varNames)
	if err := p.store.UpsertArtifacts(ctx, migrationID, errorAggregatorService, []workerdomain.FileArtifact{
		{Path: errorAggregatorPath, Content: []byte(content)},
	}); err != nil {
		applog.Warningf("generation-worker: aggregator persist message_error.go migration_id=%d: %v", migrationID, err)
		return
	}
	applog.Infof("generation-worker: aggregator assembled message_error.go maps=%d migration_id=%d", len(varNames), migrationID)
}
