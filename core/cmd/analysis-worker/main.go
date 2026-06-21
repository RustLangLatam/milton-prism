package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"

	services "milton_prism/core/internal/svc"
	workerapp "milton_prism/core/worker/analysis/application"
	workerdomain "milton_prism/core/worker/analysis/domain"
	workeradapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	workermigrability "milton_prism/core/worker/analysis/infrastructure/migrability"
	workerjobs "milton_prism/core/worker/analysis/jobs"
	decompapp "milton_prism/core/worker/decomposition/application"
	decompadapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	decompjobs "milton_prism/core/worker/decomposition/jobs"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

func main() {
	log.InitLogger("analysis-worker")

	cfg, err := config.LoadMicroserviceCfg(config.TokenRoleValidator, nil)
	if err != nil {
		log.Fatalf("Failed load cfg: %v", err)
	}

	if err := cfg.ValidateWithFlags(config.RequiredFields{
		RequireCache:   true,
		RequireMongoDb: true,
	}); err != nil {
		log.Fatalf("Failed validate cfg: %v", err)
	}

	newServices, err := services.NewServicesFromConfig(cfg)
	if err != nil {
		log.Fatalf("Failed initialize services: %v", err)
	}

	mongoClient := newServices.Mongo().GetClient()
	analysisDB := newServices.Mongo().GetDatabase()
	migrationDB := mongoClient.Database("milton_prism_migration")
	repoDB := mongoClient.Database("milton_prism_repository")
	writer := workeradapters.NewMongoSummaryWriter(analysisDB, migrationDB)
	credentialReader := workeradapters.NewMongoCredentialReader(repoDB)

	// Shared Asynq client used by all in-process enqueuers.
	redisOpt := asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%s", cfg.Cache.Host, cfg.Cache.Port),
		Password: cfg.Cache.RequirePass,
	}
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	decompEnqueuer := workeradapters.NewAsynqDecomposeEnqueuer(asynqClient)

	// Decomposition pipeline — all stages live (D1–D4).
	mongoGraphLoader := decompadapters.NewMongoGraphLoader(analysisDB)
	decompPipeline := decompapp.NewPipeline(
		mongoGraphLoader,
		decompadapters.NewPHPAwareInfraDetector(),
	).
		WithClusterer(decompadapters.NewLouvainClusterer()).
		WithAllocator(decompadapters.NewDeterministicPrefixAllocator()).
		WithSummaryLoader(mongoGraphLoader).
		WithAcquirer(decompadapters.NewGitWorkspaceAcquirer("")).
		WithContractDeriver(decompadapters.NewContractDeriverHub()).
		WithPlanWriter(decompadapters.NewMongoPlanWriter(migrationDB)).
		WithArtifactStore(decompadapters.NewMongoArtifactStore(migrationDB))

	// M2 — Migrability assessor: wired only when ANTHROPIC_API_KEY is available.
	// One LLM call per decomposition run (~cents); skipped gracefully when absent.
	if modelClient, mcErr := workeradapters.NewAnthropicModelClient(nil); mcErr == nil {
		decompPipeline.WithAssessor(decompapp.NewAssessor(modelClient))
		log.Infof("analysis-worker: migrability assessor wired (ANTHROPIC_API_KEY present)")
	} else {
		log.Infof("analysis-worker: migrability assessor disabled — %v", mcErr)
	}
	decompHandler := decompjobs.NewDecomposeJobHandler(decompPipeline)

	// Analysis pipeline — all stages wired, including the decompose trigger.
	pipeline := workerapp.NewPipeline(writer).
		WithDecomposeEnqueuer(decompEnqueuer).
		WithCredentialReader(credentialReader).
		WithAcquirer(workeradapters.NewGitSourceAcquirer("")).
		WithDetector(workeradapters.NewEnryLanguageDetector()).
		WithParser(workerdomain.EcosystemNpm, workeradapters.NewNpmManifestParser()).
		WithParser(workerdomain.EcosystemMaven, workeradapters.NewMavenManifestParser()).
		WithParser(workerdomain.EcosystemComposer, workeradapters.NewComposerManifestParser()).
		WithParser(workerdomain.EcosystemPyPI, workeradapters.NewPyPIManifestParser()).
		WithParser(workerdomain.EcosystemNuGet, workeradapters.NewNuGetManifestParser()).
		WithParser(workerdomain.EcosystemRubyGems, workeradapters.NewRubyGemsManifestParser()).
		WithResolver(workeradapters.NewHTTPVersionResolver(http.DefaultClient)).
		WithScanner(workeradapters.NewOSVVulnerabilityScanner(http.DefaultClient)).
		WithFrameworkDetector(workeradapters.NewFileSystemFrameworkDetector()).
		WithDatabaseDetector(workeradapters.NewDatabaseDetector()).
		WithSecurityScanner(workeradapters.NewSecurityScanner()).
		WithBranchSHAResolver(workeradapters.ResolveRemoteBranchSHA)

	// Tier-2 (dependency graph + module cards): Python and PHP analyzers registered;
	// other stacks are holes — the registry returns nil for unregistered languages.
	registry := workerapp.NewLanguageAnalyzerRegistry()
	registry.Register(workeradapters.NewPythonLanguageAnalyzer())
	registry.Register(workeradapters.NewPHPLanguageAnalyzer())
	pipeline.WithGraphBuilder(registry).
		WithCardProvider(registry).
		WithClassifier(workeradapters.NewLanguageAwareClassifier()).
		WithMigrabilityScorer(workermigrability.NewLouvainMigrabilityScorer()).
		// Intake gate (guards 5 & 7): the supported-language set is derived from the
		// registered analyzers so the "unsupported language" warning stays in lockstep
		// with the actually-wired Tier-2 analyzers (today: PHP, Python).
		WithSupportedLanguages(registry.Languages()...)

	analysisHandler := workerjobs.NewAnalysisJobHandler(pipeline)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 5,
		Queues:      map[string]int{"analysis": 1},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(workerjobs.TaskTypeAnalysisRun, analysisHandler.ProcessTask)
	mux.HandleFunc(decompjobs.TaskTypeDecomposeRun, decompHandler.ProcessTask)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Infof("analysis-worker: starting redis=%s:%s", cfg.Cache.Host, cfg.Cache.Port)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(mux)
	}()

	select {
	case <-ctx.Done():
		log.Infof("analysis-worker: shutting down")
		srv.Shutdown()
	case err := <-errCh:
		if err != nil {
			log.Fatalf("analysis-worker: server error: %v", err)
		}
	}
}
