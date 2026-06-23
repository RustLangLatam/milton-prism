package adapters

import (
	"context"
	"errors"
	"fmt"

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

type artifactDocMinimal struct {
	ServiceName      string `bson:"service_name"`
	ProtoContent     string `bson:"proto_content"`
	BoundarySpec     string `bson:"boundary_spec"`
	Incomplete       bool   `bson:"incomplete"`
	IncompleteReason string `bson:"incomplete_reason"`
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
	if len(migDoc.TargetBytes) > 0 {
		var tc migrationv1.TargetConfig
		if err := proto.Unmarshal(migDoc.TargetBytes, &tc); err == nil {
			lang = tc.GetLanguage()
			transport = tc.GetInterServiceTransport()
			authOverride = tc.GetTargetAuthScheme()
		}
	}
	protocol := protocolLabel(transport)
	profile, promptRef := profileAndPromptForLanguage(lang, transport)

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
		services[i] = ports.ServiceSpec{
			Name:               a.ServiceName,
			ErrorPrefix:        prefixByName[a.ServiceName],
			ProtoContent:       a.ProtoContent,
			BoundarySpec:       a.BoundarySpec,
			Incomplete:         a.Incomplete,
			IncompleteReason:   a.IncompleteReason,
			GeneratorPromptRef: promptRef,
			Protocol:           protocol,
			AuthScheme:         authScheme,
			AuthSignatureAlg:   authSigAlg,
		}
	}

	return &ports.GenerationPackage{
		MigrationID:   migrationID,
		OutputProfile: profile,
		Protocol:      protocol,
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
	default:
		if transport == migrationv1.Transport_TRANSPORT_HTTP {
			return "go", "docs/prism/milton-prism-service-generator-prompt-go-http.md"
		}
		return "go", "docs/prism/milton-prism-service-generator-prompt.md"
	}
}
