// Package application contains the generation worker's orchestration logic.
package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"

	"golang.org/x/sync/semaphore"
)

const (
	defaultConcurrency  = 2
	maxServiceAttempts  = 3
	defaultRetryBackoff = 30 * time.Second
)

// rateLimitKeywords are substrings that indicate a transient, retriable failure
// (rate-limit or server overload). invokeErr != nil is always transient.
var rateLimitKeywords = []string{
	"rate limit", "rate_limit", "too many requests",
	"429", "overloaded", "server temporarily unavailable",
}

// Pipeline orchestrates autonomous service generation for one migration.
type Pipeline struct {
	packageReader ports.GenerationPackageReader
	store         ports.GenerationStore
	stateUpdater  ports.MigrationStateUpdater
	invoker       ports.AgentInvoker
	// openapiGen emits the deliverable's docs/openapi.yaml from the generated
	// service protos. Optional: when nil, the OpenAPI step is skipped (the
	// migration still completes; the deliverable just ships without the spec).
	openapiGen   ports.OpenAPIGenerator
	monorepoRoot string
	// Exactly one of apiKey or credDir is set at runtime (A.7).
	apiKey       string
	credDir      string
	concurrency  int64
	retryBackoff time.Duration
}

// NewPipeline constructs a Pipeline. monorepoRoot is the absolute path to the
// monorepo on the host filesystem — passed to AgentInvoker as workspaceBase.
func NewPipeline(
	packageReader ports.GenerationPackageReader,
	store ports.GenerationStore,
	stateUpdater ports.MigrationStateUpdater,
	invoker ports.AgentInvoker,
	monorepoRoot string,
) *Pipeline {
	return &Pipeline{
		packageReader: packageReader,
		store:         store,
		stateUpdater:  stateUpdater,
		invoker:       invoker,
		monorepoRoot:  monorepoRoot,
		concurrency:   defaultConcurrency,
		retryBackoff:  defaultRetryBackoff,
	}
}

// WithAPIKey sets the Anthropic API key for production auth (A.7).
// Callers MUST NOT log this value — it carries a runtime secret.
func (p *Pipeline) WithAPIKey(key string) *Pipeline { p.apiKey = key; return p }

// WithCredDir sets the HOST-side ~/.claude directory path for subscription auth (A.7 fallback).
func (p *Pipeline) WithCredDir(dir string) *Pipeline { p.credDir = dir; return p }

// WithOpenAPIGenerator wires the OpenAPI emitter used by the post-generation
// assembleOpenAPI step. When unset, the step is skipped.
func (p *Pipeline) WithOpenAPIGenerator(g ports.OpenAPIGenerator) *Pipeline {
	p.openapiGen = g
	return p
}

// WithConcurrency overrides the A.4 default concurrency cap (2).
func (p *Pipeline) WithConcurrency(n int64) *Pipeline { p.concurrency = n; return p }

// WithRetryBackoff overrides the base backoff between retry attempts (default 30s).
// Useful in tests to keep wall-clock time short.
func (p *Pipeline) WithRetryBackoff(d time.Duration) *Pipeline { p.retryBackoff = d; return p }

// sanitizeFailureReason produces the short, user-facing failure message that is
// safe to persist and expose. It delegates to the canonical domain implementation
// so the raw agent JSON blob (cost/session_id/usage/modelUsage) never reaches a
// user-visible field. The raw blob is logged server-side for diagnosis.
func sanitizeFailureReason(raw string) string {
	return workerdomain.SanitizeFailureReason(raw)
}

// isTransientError reports whether the failure is worth retrying.
// invokeErr != nil (infrastructure failure, context deadline) is always transient.
// A clean invoker run that fails gates is transient only when rate-limit keywords
// appear in the failure reason; all other gates failures are permanent.
func isTransientError(invokeErr error, failureReason string) bool {
	if invokeErr != nil {
		return true
	}
	lower := strings.ToLower(failureReason)
	for _, kw := range rateLimitKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// Run executes the generation pipeline for the given migration.
// A.5: per-service failures are recorded but never propagate — Run always
// returns nil unless the package cannot be loaded or the state cannot be
// advanced to READY.
func (p *Pipeline) Run(ctx context.Context, payload workerdomain.JobPayload) error {
	applog.Infof("generation-worker: starting migration_id=%d", payload.MigrationID)

	pkg, err := p.packageReader.ReadPackage(ctx, payload.MigrationID)
	if err != nil {
		return fmt.Errorf("read package: %w", err)
	}
	protocol := pkg.Protocol
	if protocol == "" {
		protocol = "grpc"
	}
	store := pkg.Store
	if store == "" {
		store = "mongodb"
	}
	applog.Infof("generation-worker: package loaded services=%d profile=%s protocol=%s store=%s", len(pkg.Services), pkg.OutputProfile, protocol, store)

	// Apply optional service filter: when provided, only the named services run.
	// Services not in the filter are silently skipped — they are neither marked
	// failed nor counted toward the final READY/FAILED decision.
	services := pkg.Services
	if len(payload.ServiceFilter) > 0 {
		filterSet := make(map[string]bool, len(payload.ServiceFilter))
		for _, name := range payload.ServiceFilter {
			filterSet[name] = true
		}
		filtered := make([]ports.ServiceSpec, 0, len(payload.ServiceFilter))
		for _, svc := range services {
			if filterSet[svc.Name] {
				filtered = append(filtered, svc)
			}
		}
		applog.Infof("generation-worker: service_filter applied total=%d selected=%d names=%v",
			len(services), len(filtered), payload.ServiceFilter)
		services = filtered
	}

	existing, err := p.store.ListRecords(ctx, payload.MigrationID)
	if err != nil {
		return fmt.Errorf("list existing records: %w", err)
	}
	doneSet := make(map[string]bool, len(existing))
	for _, r := range existing {
		if r.Status == workerdomain.ServiceStatusDone {
			doneSet[r.ServiceName] = true
		}
	}

	sem := semaphore.NewWeighted(p.concurrency)
	var wg sync.WaitGroup

	for _, svc := range services {
		if doneSet[svc.Name] {
			applog.Infof("generation-worker: service=%s already done, skipping (idempotence)", svc.Name)
			continue
		}
		if svc.Incomplete {
			// Profile hole: immediately mark failed without invoking the agent.
			if upsertErr := p.store.UpsertRecord(ctx, workerdomain.ServiceGenerationRecord{
				MigrationID:   payload.MigrationID,
				ServiceName:   svc.Name,
				Status:        workerdomain.ServiceStatusFailed,
				FailureReason: svc.IncompleteReason,
			}); upsertErr != nil {
				applog.Warningf("generation-worker: upsert incomplete service=%s: %v", svc.Name, upsertErr)
			}
			applog.Infof("generation-worker: service=%s skipped (incomplete): %s", svc.Name, svc.IncompleteReason)
			continue
		}

		svc := svc
		wg.Add(1)
		go func() {
			defer wg.Done()
			if acquireErr := sem.Acquire(ctx, 1); acquireErr != nil {
				_ = p.store.UpsertRecord(ctx, workerdomain.ServiceGenerationRecord{
					MigrationID:   payload.MigrationID,
					ServiceName:   svc.Name,
					Status:        workerdomain.ServiceStatusFailed,
					FailureReason: "context cancelled before generation started",
				})
				return
			}
			defer sem.Release(1)
			p.generateService(ctx, payload.MigrationID, pkg.OutputProfile, svc)
		}()
	}
	wg.Wait()

	// Determine final migration state by inspecting all persisted results.
	// READY only if every service is done; FAILED if any service failed (A.5).
	final, err := p.store.ListRecords(ctx, payload.MigrationID)
	if err != nil {
		return fmt.Errorf("list final records: %w", err)
	}

	// Assemble the shared gateway error aggregator from all successfully
	// generated service artifacts. Must run after wg.Wait() so every
	// service's *_errors.go artifact is already persisted in the store.
	// Go-only: message_error.go is a Go gateway file. Non-Go profiles (e.g.
	// Python, whose error mapping lives in python/shared/errors) must NOT get a
	// stray Go artifact — it would otherwise pollute both the deliverable and the
	// generated-code viewer for that migration.
	if pkg.OutputProfile == "" || pkg.OutputProfile == "go" {
		p.assembleErrorAggregator(ctx, payload.MigrationID, pkg, final)
	} else {
		applog.Infof("generation-worker: skipping Go error aggregator for profile=%s migration_id=%d", pkg.OutputProfile, payload.MigrationID)
	}

	// Emit the deliverable's docs/openapi.yaml from the generated service
	// protos. Profile-agnostic: the spec is derived from protos alone, so it
	// runs for every OutputProfile (Go, Python, any future one). Persisted as a
	// single __pipeline__ artifact so it flows into the deliverable unchanged.
	p.assembleOpenAPI(ctx, payload.MigrationID, pkg, final)

	anyFailed := false
	for _, r := range final {
		if r.Status == workerdomain.ServiceStatusFailed {
			anyFailed = true
			break
		}
	}

	if anyFailed {
		applog.Warningf("generation-worker: partial failure — advancing migration_id=%d to FAILED (check generation_results for per-service detail)", payload.MigrationID)
		if err := p.stateUpdater.MarkFailed(ctx, payload.MigrationID); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
	} else {
		applog.Infof("generation-worker: all services done — advancing migration_id=%d to READY", payload.MigrationID)
		if err := p.stateUpdater.MarkReady(ctx, payload.MigrationID); err != nil {
			return fmt.Errorf("mark ready: %w", err)
		}
	}
	return nil
}

// generateService runs the full B1+B2 flow for one service with per-attempt retry
// for transient failures (rate-limit, infrastructure). Permanent failures (gates
// not passed due to code quality) are never retried. A.5: generateService never panics.
func (p *Pipeline) generateService(ctx context.Context, migrationID uint64, profile string, spec ports.ServiceSpec) {
	if upsertErr := p.store.UpsertRecord(ctx, workerdomain.ServiceGenerationRecord{
		MigrationID: migrationID,
		ServiceName: spec.Name,
		Status:      workerdomain.ServiceStatusGenerating,
	}); upsertErr != nil {
		applog.Warningf("generation-worker: mark generating service=%s: %v", spec.Name, upsertErr)
	}

	req := ports.InvokeRequest{
		ServiceName:           spec.Name,
		ErrorPrefix:           spec.ErrorPrefix,
		ProtoContent:          spec.ProtoContent,
		BoundarySpec:          spec.BoundarySpec,
		GeneratorPromptRef:    spec.GeneratorPromptRef,
		OutputProfile:         profile,
		Protocol:              spec.Protocol,
		HTTPFramework:         spec.HTTPFramework,
		AuthScheme:            spec.AuthScheme,
		AuthSignatureAlg:      spec.AuthSignatureAlg,
		Store:                 spec.Store,
		SourceToPort:          spec.SourceToPort,
		APIKey:                p.apiKey,
		SessionCredentialsDir: p.credDir,
	}

	var (
		result    ports.InvokeResult
		invokeErr error
	)
	for attempt := 1; attempt <= maxServiceAttempts; attempt++ {
		if attempt > 1 {
			// Feed the PREVIOUS attempt's deterministic-gate failure back to the
			// agent so it fixes the exact red build/tests in place instead of
			// regenerating blind. Empty for a transient (rate-limit/infra) failure
			// where no verify output exists.
			req.PreviousVerifyStderr = result.VerifyStderr
			// Long backoff only for transient throttling; a red gate (compile/test
			// failure) is retried promptly — there is nothing to wait for.
			backoff := time.Duration(0)
			if isTransientError(invokeErr, result.RawFailureReason) {
				backoff = time.Duration(attempt-1) * p.retryBackoff
			}
			applog.Infof("generation-worker: retry service=%s attempt=%d/%d backoff=%s gateRed=%v",
				spec.Name, attempt, maxServiceAttempts, backoff, result.VerifyRan && !result.GatesPassed)
			if backoff > 0 {
				t := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					t.Stop()
					invokeErr = ctx.Err()
					goto persist
				case <-t.C:
				}
			} else if ctx.Err() != nil {
				invokeErr = ctx.Err()
				goto persist
			}
		}

		applog.Infof("generation-worker: generating service=%s attempt=%d/%d", spec.Name, attempt, maxServiceAttempts)
		result, invokeErr = p.invoker.Invoke(ctx, p.monorepoRoot, req)

		if invokeErr == nil && result.GatesPassed {
			break // success — deterministic gate green
		}
		// Every non-success is retryable up to maxServiceAttempts: a transient
		// (rate-limit/infra) failure, OR a deterministic-gate RED (the service did
		// not compile / its tests failed). On the next attempt the agent receives
		// the verify output and must fix it. After N reds → failed (NOT ready).
		if attempt < maxServiceAttempts {
			applog.Warningf("generation-worker: retryable failure service=%s attempt=%d/%d err=%v gatesPassed=%v reason=%q — retrying",
				spec.Name, attempt, maxServiceAttempts, invokeErr, result.GatesPassed, result.FailureReason)
		}
	}

persist:
	rec := workerdomain.ServiceGenerationRecord{
		MigrationID:              migrationID,
		ServiceName:              spec.Name,
		TotalCostUSD:             result.TotalCostUSD,
		InputTokens:              result.InputTokens,
		CacheCreationInputTokens: result.CacheCreationInputTokens,
		CacheReadInputTokens:     result.CacheReadInputTokens,
		OutputTokens:             result.OutputTokens,
		Model:                    result.Model,
		GeneratedFileCount:       len(result.FileArtifacts),
		AgentRawResult:           result.RawResult,
	}

	switch {
	case invokeErr != nil:
		rec.Status = workerdomain.ServiceStatusFailed
		// Sanitize the infrastructure error before persisting to the user-visible
		// field; log the full error server-side for diagnosis.
		rec.FailureReason = sanitizeFailureReason(invokeErr.Error())
		applog.Warningf("generation-worker: service=%s invoker error (raw, server-only): %v", spec.Name, invokeErr)
	case !result.GatesPassed:
		rec.Status = workerdomain.ServiceStatusFailed
		// result.FailureReason is already sanitized by the invoker; the raw blob
		// was logged server-side at the invoker boundary.
		rec.FailureReason = sanitizeFailureReason(result.FailureReason)
		applog.Warningf("generation-worker: service=%s gates failed reason=%q (raw server-only=%q)",
			spec.Name, result.FailureReason, result.RawFailureReason)
	default:
		rec.Status = workerdomain.ServiceStatusDone
		rec.GatesPassed = true
		applog.Infof("generation-worker: service=%s done cost=%.4f files=%d", spec.Name, rec.TotalCostUSD, rec.GeneratedFileCount)
	}

	if upsertErr := p.store.UpsertRecord(ctx, rec); upsertErr != nil {
		applog.Warningf("generation-worker: persist result service=%s: %v", spec.Name, upsertErr)
	}

	if len(result.FileArtifacts) > 0 {
		if upsertErr := p.store.UpsertArtifacts(ctx, migrationID, spec.Name, result.FileArtifacts); upsertErr != nil {
			applog.Warningf("generation-worker: persist artifacts service=%s: %v", spec.Name, upsertErr)
		}
	}
}
