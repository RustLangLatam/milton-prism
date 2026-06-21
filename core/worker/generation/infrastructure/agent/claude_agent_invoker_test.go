//go:build integration

// Integration test for ClaudeAgentInvoker (B2).
//
// Requirements:
//   - Docker daemon accessible at DOCKER_HOST or /var/run/docker.sock
//   - Generation agent image built:
//     docker build -t milton-prism-generation-agent:latest \
//     -f infra/generation-agent/Dockerfile .
//   - ANTHROPIC_API_KEY set in the environment
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-ant-... \
//	  CGO_ENABLED=1 go test -v -tags integration -timeout 25m \
//	  ./core/worker/generation/infrastructure/agent/...

package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/infrastructure/agent"
	"milton_prism/core/worker/generation/infrastructure/container"
	"milton_prism/core/worker/generation/ports"
)

// articlesTypesProto is the AIP-compliant types proto for the articles service.
// Sourced from protobuf/proto/milton_prism/types/articles/v1/articles.proto — the
// refined contract produced during Camino A and stored in the monorepo.
const articlesTypesProto = `syntax = "proto3";

package milton_prism.types.articles.v1;

import "google/api/field_behavior.proto";
import "google/protobuf/timestamp.proto";
import "openapiv3/annotations.proto";

option cc_enable_arenas = true;
option csharp_namespace = "MiltonPrism.Types.Articles.V1";
option go_package = "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1;articlesv1";
option java_multiple_files = true;
option java_outer_classname = "ArticlesProtoV1";
option java_package = "com.miltonprism.types.articles.v1";
option objc_class_prefix = "MPR";

enum ArticleState {
  ARTICLE_STATE_UNSPECIFIED = 0;
  ARTICLE_STATE_ACTIVE = 1;
  ARTICLE_STATE_DELETED = 2;
}

enum TagState {
  TAG_STATE_UNSPECIFIED = 0;
  TAG_STATE_ACTIVE = 1;
  TAG_STATE_DELETED = 2;
}

message Article {
  option (openapi.v3.schema) = {description: "An article resource."};

  uint64 identifier = 1 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Unique numeric identifier."}
  ];
  ArticleState state = 2 [(openapi.v3.property) = {description: "Article lifecycle state."}];
  string slug = 3 [(openapi.v3.property) = {description: "URL-safe slug."}];
  string title = 4 [(openapi.v3.property) = {description: "Article title."}];
  string description = 5 [(openapi.v3.property) = {description: "Short description."}];
  string body = 6 [(openapi.v3.property) = {description: "Article body (Markdown)."}];
  uint64 author_identifier = 7 [
    (google.api.field_behavior) = REQUIRED,
    (openapi.v3.property) = {description: "Profile-service identifier of the author."}
  ];
  google.protobuf.Timestamp create_time = 8 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Creation timestamp."}
  ];
  google.protobuf.Timestamp update_time = 9 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Last-updated timestamp."}
  ];
  google.protobuf.Timestamp delete_time = 10 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Soft-delete timestamp; null while active."}
  ];
  google.protobuf.Timestamp purge_time = 11 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Hard-delete timestamp; null until purged."}
  ];
}

message Tag {
  option (openapi.v3.schema) = {description: "A tag resource."};

  uint64 identifier = 1 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Unique numeric identifier."}
  ];
  TagState state = 2 [(openapi.v3.property) = {description: "Tag lifecycle state."}];
  string tagname = 3 [(openapi.v3.property) = {description: "Tag label."}];
  google.protobuf.Timestamp delete_time = 4 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Soft-delete timestamp."}
  ];
  google.protobuf.Timestamp purge_time = 5 [
    (google.api.field_behavior) = OUTPUT_ONLY,
    (openapi.v3.property) = {description: "Hard-delete timestamp."}
  ];
}

message ArticlesFilter {
  optional uint64 author_identifier = 1 [(openapi.v3.property) = {description: "Filter by author profile identifier."}];
  optional string slug = 2 [(openapi.v3.property) = {description: "Filter by slug."}];
  optional ArticleState state = 3 [(openapi.v3.property) = {description: "Filter by state."}];
}`

// articlesServiceProto is the AIP-compliant service proto for the articles service.
const articlesServiceProto = `syntax = "proto3";

package milton_prism.services.articles.v1;

import "google/api/annotations.proto";
import "google/api/field_behavior.proto";
import "google/protobuf/empty.proto";
import "google/protobuf/field_mask.proto";
import "milton_prism/types/articles/v1/articles.proto";
import "milton_prism/types/pagination/v1/pagination.proto";
import "milton_prism/types/query_params/v1/query_params.proto";
import "openapiv3/annotations.proto";

option cc_enable_arenas = true;
option csharp_namespace = "MiltonPrism.Services.Articles.V1";
option go_package = "milton_prism/pkg/pb/gen/milton_prism/services/articles/v1;articlessvcv1";
option java_multiple_files = true;
option java_outer_classname = "ArticlesServiceProtoV1";
option java_package = "com.miltonprism.services.articles.v1";
option objc_class_prefix = "MPR";

service ArticleService {
  rpc CreateArticle(CreateArticleRequest) returns (milton_prism.types.articles.v1.Article) {
    option (google.api.http) = {
      post: "/v1/articles"
      body: "article"
    };
  }
  rpc GetArticle(GetArticleRequest) returns (milton_prism.types.articles.v1.Article) {
    option (google.api.http) = {get: "/v1/articles/{identifier}"};
  }
  rpc ListArticles(ListArticlesRequest) returns (ListArticlesResponse) {
    option (google.api.http) = {get: "/v1/articles"};
  }
  rpc UpdateArticle(UpdateArticleRequest) returns (milton_prism.types.articles.v1.Article) {
    option (google.api.http) = {
      patch: "/v1/articles/{article.identifier}"
      body: "article"
    };
  }
  rpc DeleteArticle(DeleteArticleRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = {delete: "/v1/articles/{identifier}"};
  }
  rpc GetTag(GetTagRequest) returns (milton_prism.types.articles.v1.Tag) {
    option (google.api.http) = {get: "/v1/tags/{identifier}"};
  }
  rpc ListTags(ListTagsRequest) returns (ListTagsResponse) {
    option (google.api.http) = {get: "/v1/tags"};
  }
}

message CreateArticleRequest {
  milton_prism.types.articles.v1.Article article = 1 [(google.api.field_behavior) = REQUIRED];
}
message GetArticleRequest {
  uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED];
}
message ListArticlesRequest {
  milton_prism.types.articles.v1.ArticlesFilter filter = 1;
  milton_prism.types.query_params.v1.PageQueryParams page_params = 2;
}
message ListArticlesResponse {
  repeated milton_prism.types.articles.v1.Article articles = 1;
  milton_prism.types.pagination.v1.Pagination pagination = 2;
}
message UpdateArticleRequest {
  milton_prism.types.articles.v1.Article article = 1 [(google.api.field_behavior) = REQUIRED];
  google.protobuf.FieldMask update_mask = 2 [(google.api.field_behavior) = REQUIRED];
}
message DeleteArticleRequest {
  uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED];
}
message GetTagRequest {
  uint64 identifier = 1 [(google.api.field_behavior) = REQUIRED];
}
message ListTagsRequest {
  milton_prism.types.query_params.v1.PageQueryParams page_params = 1;
}
message ListTagsResponse {
  repeated milton_prism.types.articles.v1.Tag tags = 1;
  milton_prism.types.pagination.v1.Pagination pagination = 2;
}`

// articlesBoundarySpec is the generator-prompt-formatted boundary spec for articles.
const articlesBoundarySpec = `service: articles
module: milton_prism
resources:
  - name: Article
    proto_type: "milton_prism/types/articles/v1.Article"
    soft_delete: true
  - name: Tag
    proto_type: "milton_prism/types/articles/v1.Tag"
    soft_delete: true
rpcs:
  - CreateArticle
  - GetArticle
  - ListArticles
  - UpdateArticle
  - DeleteArticle
  - GetTag
  - ListTags
store: mongodb
needs_transaction: true
error_prefix: "ART"
inter_service_deps:
  - profiles
auth: required`

// monorepoRoot returns the absolute path to the monorepo root, derived from
// this test file's location at compile time.
func monorepoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// this file: core/worker/generation/infrastructure/agent/<file>.go
	// 5 levels up → monorepo root
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", ".."))
}

// credentialSource holds whichever auth mechanism is available.
type credentialSource struct {
	// apiKey is set when ANTHROPIC_API_KEY is in the environment.
	apiKey string
	// sessionCredDir is the HOST path of ~/.claude when no direct API key is
	// available (claude.ai OAuth session). Bind-mounted into the container so
	// Claude Code always sees the live token state.
	sessionCredDir string
}

// resolveCredentialSource locates the best available auth mechanism.
// Priority: ANTHROPIC_API_KEY env var → ~/.claude directory (OAuth session).
// Returns an empty credentialSource if nothing is found.
func resolveCredentialSource() credentialSource {
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return credentialSource{apiKey: k}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return credentialSource{}
	}
	credDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(filepath.Join(credDir, ".credentials.json")); err == nil {
		return credentialSource{sessionCredDir: credDir}
	}
	return credentialSource{}
}

func newTestInvoker(t *testing.T) (*agent.ClaudeAgentInvoker, credentialSource) {
	t.Helper()
	cred := resolveCredentialSource()
	if cred.apiKey == "" && cred.sessionCredDir == "" {
		t.Skip("no model credential found (ANTHROPIC_API_KEY or ~/.claude/.credentials.json) — skipping B2 integration test")
	}
	r, err := container.NewDockerContainerRunner()
	require.NoError(t, err, "docker runner init")

	inv := agent.NewClaudeAgentInvoker(r)

	// Mount the host's Go module cache so the container doesn't re-download
	// all dependencies during buf generate / go build.
	if goModCache := goModCachePath(); goModCache != "" {
		inv = inv.WithGoModCache(goModCache)
		t.Logf("go mod cache mount: %s", goModCache)
	}

	return inv, cred
}

// goModCachePath returns the host's Go module cache path, or "" if unavailable.
func goModCachePath() string {
	// GOMODCACHE env takes priority; fall back to GOPATH/pkg/mod.
	if p := os.Getenv("GOMODCACHE"); p != "" {
		return p
	}
	if p := os.Getenv("GOPATH"); p != "" {
		return filepath.Join(p, "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

// TestInvoke_Articles_GatesPass validates the full B2 path: workspace
// preparation, Claude Code headless invocation, and self-verification gates.
//
// This is the core Camino B validation against the Conduit articles service.
// Expected outcome: all gates green (buf lint, go build, go vet, go test).
func TestInvoke_Articles_GatesPass(t *testing.T) {
	inv, cred := newTestInvoker(t)

	root := monorepoRoot()
	t.Logf("monorepo root: %s", root)
	if cred.sessionCredDir != "" {
		t.Logf("auth: session credentials dir (%s)", cred.sessionCredDir)
	} else {
		t.Logf("auth: ANTHROPIC_API_KEY")
	}

	combinedProto := "// File: protobuf/proto/milton_prism/types/articles/v1/articles.proto\n" +
		articlesTypesProto +
		"\n\n// File: protobuf/proto/milton_prism/services/articles/v1/articles_service.proto\n" +
		articlesServiceProto

	req := ports.InvokeRequest{
		ServiceName:           "articles",
		ErrorPrefix:           "ART",
		ProtoContent:          combinedProto,
		BoundarySpec:          articlesBoundarySpec,
		GeneratorPromptRef:    "docs/prism/milton-prism-service-generator-prompt.md",
		OutputProfile:         "go",
		APIKey:                cred.apiKey,
		SessionCredentialsDir: cred.sessionCredDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	result, err := inv.Invoke(ctx, root, req)

	// Log key metrics regardless of outcome.
	t.Logf("exit_code=%d gates_passed=%v total_cost_usd=%.4f input_tokens=%d output_tokens=%d",
		result.ExitCode, result.GatesPassed, result.TotalCostUSD,
		result.InputTokens, result.OutputTokens)
	t.Logf("generated_files=%d", len(result.GeneratedFiles))

	if len(result.GeneratedFiles) > 0 {
		sorted := make([]string, len(result.GeneratedFiles))
		copy(sorted, result.GeneratedFiles)
		sort.Strings(sorted)
		t.Logf("generated:\n  %s", strings.Join(sorted, "\n  "))
	}

	if result.RawResult != "" {
		tail := result.RawResult
		if len(tail) > 1500 {
			tail = "...\n" + tail[len(tail)-1500:]
		}
		t.Logf("agent result (tail):\n%s", tail)
	}

	if !result.GatesPassed && result.FailureReason != "" {
		t.Logf("failure reason:\n%s", result.FailureReason)
	}

	require.NoError(t, err, "Invoke must not return a Go error")
	assert.Equal(t, 0, result.ExitCode, "claude exit code must be 0 (all gates green)")
	assert.True(t, result.GatesPassed, "self-verification gates must pass")
	assert.NotEmpty(t, result.GeneratedFiles, "agent must create files in the workspace")
	assert.Positive(t, result.TotalCostUSD, "cost must be non-zero for a successful run")

	// Verify key hexagonal layer files were created.
	generated := strings.Join(result.GeneratedFiles, " ")
	assert.Contains(t, generated, filepath.Join("core", "services", "articles"),
		"core/services/articles/ must be created")
}
