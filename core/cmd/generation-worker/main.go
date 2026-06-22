package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	services "milton_prism/core/internal/svc"
	genapp "milton_prism/core/worker/generation/application"
	genadapters "milton_prism/core/worker/generation/infrastructure/adapters"
	genagent "milton_prism/core/worker/generation/infrastructure/agent"
	gencontainer "milton_prism/core/worker/generation/infrastructure/container"
	genjobs "milton_prism/core/worker/generation/jobs"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

func main() {
	log.InitLogger("generation-worker")

	cfg, err := config.LoadMicroserviceCfg(config.TokenRoleValidator, nil)
	if err != nil {
		log.Fatalf("generation-worker: load cfg: %v", err)
	}
	if err := cfg.ValidateWithFlags(config.RequiredFields{
		RequireCache:   true,
		RequireMongoDb: true,
	}); err != nil {
		log.Fatalf("generation-worker: validate cfg: %v", err)
	}

	newServices, err := services.NewServicesFromConfig(cfg)
	if err != nil {
		log.Fatalf("generation-worker: init services: %v", err)
	}

	db := newServices.Mongo().GetDatabase()

	// Infrastructure adapters.
	packageReader := genadapters.NewMongoGenerationPackageReader(db)
	store := genadapters.NewMongoGenerationStore(db)
	stateUpdater := genadapters.NewMongoMigrationStateUpdater(db)

	// Docker host selection (Camino B, pending B2):
	//   - PRISM_DOCKER_HOST unset  → spawn ephemeral containers on the LOCAL daemon
	//                                (default; /var/run/docker.sock or DOCKER_HOST).
	//   - PRISM_DOCKER_HOST set     → spawn them on a REMOTE daemon over tcp:// with
	//                                optional mutual TLS (PRISM_DOCKER_TLS_CA/CERT/KEY).
	dockerCfg := gencontainer.RemoteConfigFromEnv()
	runner, err := gencontainer.NewDockerContainerRunnerWithConfig(dockerCfg)
	if err != nil {
		log.Fatalf("generation-worker: docker runner: %v", err)
	}
	invoker := genagent.NewClaudeAgentInvoker(runner)

	// Optional: share GOPATH module cache with the container to avoid re-downloading.
	if goModCache := os.Getenv("PRISM_GO_MOD_CACHE"); goModCache != "" {
		invoker = invoker.WithGoModCache(goModCache)
	}

	// Required when running inside Docker (DooD): the workspace temp dir must be
	// a host path visible to the Docker daemon so it can bind-mount workspaces
	// into ephemeral containers. Set to "" (OS default) when running on the host.
	if workspaceDir := os.Getenv("PRISM_WORKSPACE_PATH"); workspaceDir != "" {
		invoker = invoker.WithWorkspaceTempDir(workspaceDir)
	}

	monorepoRoot := os.Getenv("PRISM_MONOREPO_PATH")
	if monorepoRoot == "" {
		log.Fatalf("generation-worker: PRISM_MONOREPO_PATH is required")
	}

	concurrency := int64(2)
	if s := os.Getenv("PRISM_GENERATION_CONCURRENCY"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			concurrency = n
		}
	}

	pipeline := genapp.NewPipeline(packageReader, store, stateUpdater, invoker, monorepoRoot).
		WithConcurrency(concurrency)

	// Auth mode: PRISM_AGENT_AUTH=apikey (default, production) | subscription (testing).
	//
	//   apikey       — injects ANTHROPIC_API_KEY into every agent container; billed per-token.
	//   subscription — mounts OAuth credentials at $HOME/.claude inside agent containers;
	//                  ANTHROPIC_API_KEY is intentionally absent from agent containers so
	//                  Claude Code uses the subscription plan, not the API billing path.
	//
	// Credentials are runtime secrets and are never logged anywhere in the call chain (A.7).
	agentAuth := os.Getenv("PRISM_AGENT_AUTH")
	if agentAuth == "" {
		agentAuth = "apikey"
	}
	switch agentAuth {
	case "apikey":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			log.Fatalf("generation-worker: PRISM_AGENT_AUTH=apikey requires ANTHROPIC_API_KEY")
		}
		pipeline = pipeline.WithAPIKey(apiKey)
		log.Infof("generation-worker: auth=apikey")
	case "subscription":
		// HOST path of the ~/.claude directory — the Docker daemon uses this to
		// bind-mount the live credentials directory into each agent container.
		// Must be the HOST path (not a container-internal path) because the mount
		// source is resolved by the Docker daemon from the host filesystem (DooD).
		credDir := os.Getenv("PRISM_CLAUDE_DIR")
		if credDir == "" {
			log.Fatalf("generation-worker: PRISM_AGENT_AUTH=subscription requires PRISM_CLAUDE_DIR (HOST path of ~/.claude)")
		}
		pipeline = pipeline.WithCredDir(credDir)
		log.Infof("generation-worker: auth=subscription claude_dir=%s", credDir)
	default:
		log.Fatalf("generation-worker: PRISM_AGENT_AUTH must be 'apikey' or 'subscription', got %q", agentAuth)
	}

	handler := genjobs.NewGenerationJobHandler(pipeline)

	redisOpt := asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%s", cfg.Cache.Host, cfg.Cache.Port),
		Password: cfg.Cache.RequirePass,
	}
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: int(concurrency),
		Queues:      map[string]int{"generation": 1},
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(genjobs.TaskTypeGenerationRun, handler.ProcessTask)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Infof("generation-worker: starting redis=%s:%s monorepo=%s", cfg.Cache.Host, cfg.Cache.Port, monorepoRoot)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(mux)
	}()

	select {
	case <-ctx.Done():
		log.Infof("generation-worker: shutting down")
		srv.Shutdown()
	case err := <-errCh:
		if err != nil {
			log.Fatalf("generation-worker: server error: %v", err)
		}
	}
}
