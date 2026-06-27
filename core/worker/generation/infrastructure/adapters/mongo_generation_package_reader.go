package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// analysisSummariesDBName is the database the analysis service owns. The generation
// worker connects to the migration DB; it reaches across the shared client to read
// the linked summary's detected auth scheme (read-only). Kept in one place so the
// cross-service read is explicit.
const analysisSummariesDBName = "milton_prism_analysis"

var _ ports.GenerationPackageReader = (*MongoGenerationPackageReader)(nil)

// MongoGenerationPackageReader assembles the generation package from the
// migrations and design_artifacts collections. It mirrors
// migration.Service.GetGenerationPackage without going through gRPC,
// which lets the generation worker stay decoupled from the migration service.
type MongoGenerationPackageReader struct {
	migrations *mongo.Collection
	artifacts  *mongo.Collection
}

// NewMongoGenerationPackageReader returns a reader backed by db.
func NewMongoGenerationPackageReader(db *mongo.Database) *MongoGenerationPackageReader {
	return &MongoGenerationPackageReader{
		migrations: db.Collection("migrations"),
		artifacts:  db.Collection("design_artifacts"),
	}
}

type migrationDocMinimal struct {
	Identifier        uint64 `bson:"identifier"`
	PlanBytes         []byte `bson:"plan_bytes,omitempty"`
	TargetBytes       []byte `bson:"target_bytes,omitempty"`
	AnalysisSummaryID uint64 `bson:"analysis_summary_id,omitempty"`
}

// analysisSummaryAuthDoc reads only the auth-scheme blob from an analysis summary.
type analysisSummaryAuthDoc struct {
	AuthSchemeDetectionBytes []byte `bson:"auth_scheme_detection_bytes,omitempty"`
}

// analysisSummaryDatabaseDoc reads only the database-detection blob from an
// analysis summary. Used to resolve Auto (TARGET_DATABASE_UNSPECIFIED) generation
// against the engine the source actually used.
type analysisSummaryDatabaseDoc struct {
	DatabaseDetectionBytes []byte `bson:"database_detection_bytes,omitempty"`
}

type artifactDocMinimal struct {
	ServiceName      string              `bson:"service_name"`
	ProtoContent     string              `bson:"proto_content"`
	BoundarySpec     string              `bson:"boundary_spec"`
	Incomplete       bool                `bson:"incomplete"`
	IncompleteReason string              `bson:"incomplete_reason"`
	SourceFiles      []sourceFileDoc     `bson:"source_files,omitempty"`
}

// sourceFileDoc decodes one captured original source file persisted by the
// decomposition stage in the design_artifact (source_files). It is mapped to a
// ports.SourceFile so the generation prompt can carry the logic to port.
type sourceFileDoc struct {
	Path    string   `bson:"path"`
	Lang    string   `bson:"lang"`
	Role    string   `bson:"role"`
	Content string   `bson:"content"`
	Symbols []string `bson:"symbols,omitempty"`
}

func (r *MongoGenerationPackageReader) ReadPackage(ctx context.Context, migrationID uint64) (*ports.GenerationPackage, error) {
	var migDoc migrationDocMinimal
	err := r.migrations.FindOne(ctx, bson.M{"identifier": migrationID, "delete_time": nil}).Decode(&migDoc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("generation-package: migration %d not found", migrationID)
		}
		return nil, fmt.Errorf("generation-package: find migration %d: %w", migrationID, err)
	}

	// Decode plan to build the error-prefix index by service name.
	prefixByName := make(map[string]string)
	if len(migDoc.PlanBytes) > 0 {
		var plan migrationv1.RestructurePlan
		if err := proto.Unmarshal(migDoc.PlanBytes, &plan); err != nil {
			return nil, fmt.Errorf("generation-package: unmarshal plan: %w", err)
		}
		for _, svc := range plan.GetServices() {
			prefixByName[svc.GetName()] = svc.GetErrorPrefix()
		}
	}

	// Decode target config to determine the output profile, protocol and prompt.
	lang := migrationv1.TargetLanguage_TARGET_LANGUAGE_UNSPECIFIED
	transport := migrationv1.Transport_TRANSPORT_UNSPECIFIED
	authOverride := analysisv1.AuthScheme_AUTH_SCHEME_UNSPECIFIED
	dbOverride := migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED
	httpFramework := migrationv1.HttpFramework_HTTP_FRAMEWORK_UNSPECIFIED
	if len(migDoc.TargetBytes) > 0 {
		var tc migrationv1.TargetConfig
		if err := proto.Unmarshal(migDoc.TargetBytes, &tc); err == nil {
			lang = tc.GetLanguage()
			transport = tc.GetInterServiceTransport()
			authOverride = tc.GetTargetAuthScheme()
			dbOverride = tc.GetDatabase()
			httpFramework = tc.GetHttpFramework()
		}
	}
	protocol := protocolLabel(transport)
	profile, promptRef := profileAndPromptForLanguage(lang, transport)

	// Derive the effective HTTP framework the generated router/handlers are built
	// on. The sub-axis only applies to HTTP — for gRPC it is the empty string and
	// the frameworkSection prompt block is omitted. For HTTP it is the canonicalised
	// framework token (an unset/legacy migration degrades to the language default
	// "net_http", a no-op block — no regression). Read from the persisted (already
	// canonicalised at creation) TargetConfig.http_framework.
	framework := frameworkLabel(transport, httpFramework)
	applog.Infof("generation-worker: generation package framework migration_id=%d protocol=%s framework=%s",
		migrationID, protocol, framework)

	// Resolve the effective persistence engine the generated services must target:
	// the per-migration override (TargetConfig.database) wins; otherwise — for Auto
	// (UNSPECIFIED) — the engine detected in the linked analysis summary, mapped
	// twin to resolveAuthScheme (POSTGRESQL→postgres, MYSQL→mysql, else mongodb).
	// Best-effort: a missing link or cross-DB read failure degrades to "mongodb"
	// (the original path) — generation never fails for lack of a database signal.
	store := r.resolveDatabase(ctx, dbOverride, migDoc.AnalysisSummaryID)
	applog.Infof("generation-worker: generation package store migration_id=%d store=%s", migrationID, store)

	// Resolve the effective authentication scheme the generated service must
	// implement: the per-migration override (TargetConfig.target_auth_scheme) wins;
	// otherwise the scheme detected in the linked analysis summary. Best-effort: a
	// missing link or a cross-DB read failure degrades to "none" — generation never
	// fails for lack of an auth signal. Mirrors migration.Service.GetGenerationPackage.
	authScheme, authSigAlg := r.resolveAuthScheme(ctx, authOverride, migDoc.AnalysisSummaryID)
	applog.Infof("generation-worker: generation package auth migration_id=%d scheme=%s sig=%s",
		migrationID, authScheme, authSigAlg)

	// Read per-service design artifacts.
	cur, err := r.artifacts.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("generation-package: find artifacts migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)
	var artDocs []artifactDocMinimal
	if err := cur.All(ctx, &artDocs); err != nil {
		return nil, fmt.Errorf("generation-package: decode artifacts migration_id=%d: %w", migrationID, err)
	}
	if len(artDocs) == 0 {
		return nil, fmt.Errorf("generation-package: no design artifacts for migration %d", migrationID)
	}

	services := make([]ports.ServiceSpec, len(artDocs))
	for i, a := range artDocs {
		sourceToPort := make([]ports.SourceFile, 0, len(a.SourceFiles))
		var domainN, testN int
		for _, sf := range a.SourceFiles {
			if strings.TrimSpace(sf.Content) == "" {
				continue
			}
			sourceToPort = append(sourceToPort, ports.SourceFile{
				Path:    sf.Path,
				Lang:    sf.Lang,
				Role:    sf.Role,
				Content: sf.Content,
				Symbols: sf.Symbols,
			})
			switch sf.Role {
			case "test":
				testN++
			default:
				domainN++
			}
		}
		applog.Infof("generation-worker: generation package source_to_port migration_id=%d service=%s domain=%d test=%d",
			migrationID, a.ServiceName, domainN, testN)
		services[i] = ports.ServiceSpec{
			Name:               a.ServiceName,
			ErrorPrefix:        prefixByName[a.ServiceName],
			ProtoContent:       a.ProtoContent,
			BoundarySpec:       a.BoundarySpec,
			Incomplete:         a.Incomplete,
			IncompleteReason:   a.IncompleteReason,
			GeneratorPromptRef: promptRef,
			Protocol:           protocol,
			HTTPFramework:      framework,
			AuthScheme:         authScheme,
			AuthSignatureAlg:   authSigAlg,
			Store:              store,
			SourceToPort:       sourceToPort,
		}
	}

	return &ports.GenerationPackage{
		MigrationID:   migrationID,
		OutputProfile: profile,
		Protocol:      protocol,
		HTTPFramework: framework,
		Store:         store,
		Services:      services,
	}, nil
}

// resolveAuthScheme returns the effective auth scheme token + JWT signature alg.
// The override wins; otherwise the scheme detected in the linked analysis summary
// (read cross-DB, read-only). Degrades to "none" on any miss. v1 generates "jwt"
// and "none"; other tokens flow through for the prompt's honest note.
func (r *MongoGenerationPackageReader) resolveAuthScheme(ctx context.Context, override analysisv1.AuthScheme, summaryID uint64) (scheme, sigAlg string) {
	if override != analysisv1.AuthScheme_AUTH_SCHEME_UNSPECIFIED {
		return authSchemeToken(override), ""
	}
	if summaryID == 0 {
		return "none", ""
	}
	analysisDB := r.migrations.Database().Client().Database(analysisSummariesDBName)
	var doc analysisSummaryAuthDoc
	err := analysisDB.Collection("analysis_summaries").
		FindOne(ctx, bson.M{"identifier": summaryID}).Decode(&doc)
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			applog.Warningf("generation-worker: auth-scheme summary read failed summary_id=%d: %v", summaryID, err)
		}
		return "none", ""
	}
	if len(doc.AuthSchemeDetectionBytes) == 0 {
		return "none", ""
	}
	var wrapper analysisv1.AnalysisSummary
	if err := proto.Unmarshal(doc.AuthSchemeDetectionBytes, &wrapper); err != nil {
		applog.Warningf("generation-worker: auth-scheme unmarshal failed summary_id=%d: %v", summaryID, err)
		return "none", ""
	}
	asd := wrapper.GetAuthSchemeDetection()
	if asd == nil {
		return "none", ""
	}
	return authSchemeToken(asd.GetScheme()), asd.GetSignatureAlg()
}

// resolveDatabase returns the effective persistence engine label
// ("mongodb"|"postgres"|"mysql") the generated services must target. It is the
// store twin of resolveAuthScheme: the per-migration override wins; otherwise —
// for Auto (TARGET_DATABASE_UNSPECIFIED) — the engine detected in the linked
// analysis summary (read cross-DB, read-only). Degrades to "mongodb" on any miss
// (the original path) so generation never fails for lack of a database signal.
// The mapping mirrors the blueprint: POSTGRESQL→postgres, MYSQL→mysql,
// MONGODB/non-generable→mongodb.
func (r *MongoGenerationPackageReader) resolveDatabase(ctx context.Context, override migrationv1.TargetDatabase, summaryID uint64) string {
	if override != migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED {
		return databaseStoreToken(override)
	}
	if summaryID == 0 {
		return "mongodb"
	}
	analysisDB := r.migrations.Database().Client().Database(analysisSummariesDBName)
	var doc analysisSummaryDatabaseDoc
	err := analysisDB.Collection("analysis_summaries").
		FindOne(ctx, bson.M{"identifier": summaryID}).Decode(&doc)
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			applog.Warningf("generation-worker: database-detection summary read failed summary_id=%d: %v", summaryID, err)
		}
		return "mongodb"
	}
	if len(doc.DatabaseDetectionBytes) == 0 {
		return "mongodb"
	}
	var wrapper analysisv1.AnalysisSummary
	if err := proto.Unmarshal(doc.DatabaseDetectionBytes, &wrapper); err != nil {
		applog.Warningf("generation-worker: database-detection unmarshal failed summary_id=%d: %v", summaryID, err)
		return "mongodb"
	}
	dd := wrapper.GetDatabaseDetection()
	if dd == nil || dd.GetUnknown() {
		return "mongodb"
	}
	// The first deterministically detected engine is the primary store. Map it to a
	// generation target; auxiliary engines (e.g. Redis) are ignored for the store.
	return detectedEngineStore(dd.GetEngines())
}

// detectedEngineStore maps the detected primary engine to a generation store
// label. Mirrors the blueprint Auto mapping: PostgreSQL→postgres, MySQL→mysql,
// everything else (MongoDB, SQLite, SQLServer, Oracle, Redis, none)→mongodb (the
// original, always-generable path). The first SQL/Mongo engine in the list wins;
// Redis-only or empty degrades to mongodb.
func detectedEngineStore(engines []analysisv1.DatabaseEngine) string {
	for _, e := range engines {
		switch e {
		case analysisv1.DatabaseEngine_DATABASE_ENGINE_POSTGRESQL:
			return "postgres"
		case analysisv1.DatabaseEngine_DATABASE_ENGINE_MYSQL:
			return "mysql"
		case analysisv1.DatabaseEngine_DATABASE_ENGINE_MONGODB:
			return "mongodb"
		}
	}
	return "mongodb"
}

// databaseStoreToken maps a TargetDatabase override to its lowercase store label.
// Mirrors migration.Service.storeLabel; UNSPECIFIED canonicalises to "mongodb".
func databaseStoreToken(d migrationv1.TargetDatabase) string {
	switch d {
	case migrationv1.TargetDatabase_TARGET_DATABASE_POSTGRES:
		return "postgres"
	case migrationv1.TargetDatabase_TARGET_DATABASE_MARIADB:
		return "mysql"
	default:
		return "mongodb"
	}
}

// authSchemeToken maps an AuthScheme enum to its lowercase canonical token. Mirrors
// migration.Service.authSchemeToken; UNSPECIFIED and NONE both canonicalise to "none".
func authSchemeToken(s analysisv1.AuthScheme) string {
	switch s {
	case analysisv1.AuthScheme_AUTH_SCHEME_JWT:
		return "jwt"
	case analysisv1.AuthScheme_AUTH_SCHEME_OAUTH2:
		return "oauth2"
	case analysisv1.AuthScheme_AUTH_SCHEME_SESSION_COOKIE:
		return "session_cookie"
	case analysisv1.AuthScheme_AUTH_SCHEME_API_KEY:
		return "api_key"
	case analysisv1.AuthScheme_AUTH_SCHEME_BASIC:
		return "basic"
	default:
		return "none"
	}
}

// protocolLabel maps a Transport to the worker-side protocol label
// ("grpc" | "http"). UNSPECIFIED canonicalises to "grpc" (the platform default),
// mirroring the migration service's CreateMigration canonicalisation.
func protocolLabel(t migrationv1.Transport) string {
	switch t {
	case migrationv1.Transport_TRANSPORT_HTTP:
		return "http"
	default:
		return "grpc"
	}
}

// frameworkLabel maps a (Transport, HttpFramework) to the worker-side HTTP
// framework token consumed by workspace.frameworkSection. The sub-axis only
// applies to HTTP: for gRPC it returns "" (the field is ignored and no framework
// block is injected). For HTTP it maps the enum to its token; NET_HTTP and (a
// legacy/unset) UNSPECIFIED both canonicalise to "net_http" — the Go HTTP default,
// which frameworkSection treats as a no-op so the established net/http behaviour
// is unchanged. MUST stay in lockstep with frameworkSection and the domain
// supportedHttpFrameworkByLanguage matrix.
func frameworkLabel(t migrationv1.Transport, fw migrationv1.HttpFramework) string {
	if t != migrationv1.Transport_TRANSPORT_HTTP {
		return ""
	}
	switch fw {
	case migrationv1.HttpFramework_HTTP_FRAMEWORK_GO_GIN:
		return "gin"
	case migrationv1.HttpFramework_HTTP_FRAMEWORK_GO_ECHO:
		return "echo"
	case migrationv1.HttpFramework_HTTP_FRAMEWORK_GO_CHI:
		return "chi"
	case migrationv1.HttpFramework_HTTP_FRAMEWORK_GO_FIBER:
		return "fiber"
	default: // NET_HTTP or UNSPECIFIED → the Go HTTP-native default (no-op block)
		return "net_http"
	}
}

// profileAndPromptForLanguage maps a migration's (TargetLanguage, Transport) to
// the output profile label and generator prompt the generation worker must use.
// It is the worker-side counterpart of the migration application's
// outputProfileLabel / generatorPromptRef and MUST stay in lockstep with them
// (and with the worker's promptProfileBindings). The transport selects the prompt
// per (profile, protocol): Go + HTTP, Python + HTTP, Node + HTTP and Rust + HTTP
// use their dedicated HTTP-native prompts; every other generable cell uses the
// gRPC prompt.
// An unset/unsupported language defaults to Go; an unset transport canonicalises
// to gRPC.
func profileAndPromptForLanguage(lang migrationv1.TargetLanguage, transport migrationv1.Transport) (profile, promptRef string) {
	switch lang {
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "python", "docs/prism/milton-prism-service-generator-prompt-python-http.md"
		}
		return "python", "docs/prism/milton-prism-service-generator-prompt-python.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "node", "docs/prism/milton-prism-service-generator-prompt-node-http.md"
		}
		return "node", "docs/prism/milton-prism-service-generator-prompt-node.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "rust", "docs/prism/milton-prism-service-generator-prompt-rust-http.md"
		}
		return "rust", "docs/prism/milton-prism-service-generator-prompt-rust.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_JAVA:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "java", "docs/prism/milton-prism-service-generator-prompt-java-http.md"
		}
		return "java", "docs/prism/milton-prism-service-generator-prompt-java.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_RUBY:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "ruby", "docs/prism/milton-prism-service-generator-prompt-ruby-http.md"
		}
		return "ruby", "docs/prism/milton-prism-service-generator-prompt-ruby.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_CSHARP:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "csharp", "docs/prism/milton-prism-service-generator-prompt-csharp-http.md"
		}
		return "csharp", "docs/prism/milton-prism-service-generator-prompt-csharp.md"
	case migrationv1.TargetLanguage_TARGET_LANGUAGE_CPP:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "cpp", "docs/prism/milton-prism-service-generator-prompt-cpp-http.md"
		}
		return "cpp", "docs/prism/milton-prism-service-generator-prompt-cpp.md"
	default:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "go", "docs/prism/milton-prism-service-generator-prompt-go-http.md"
		}
		return "go", "docs/prism/milton-prism-service-generator-prompt.md"
	}
}
