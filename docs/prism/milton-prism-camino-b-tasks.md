# Milton Prism — Camino B: Autonomous Generation (spec + tasks)

Two parts. **Part A** = frozen decisions (do not redesign). **Part B** = sequenced tasks. Run one task at a time, gate block between each, validate against the real Conduit migration. If a decision in Part A is ambiguous or impossible, STOP and write a note in `docs/prism/camino-b-blockers.md` instead of guessing.

Composes with the Architecture Canon. This turns the manual "copy the block into Claude Code" (Camino A) into an automated "Generate" that runs in the backend.

---

# PART A — Frozen decisions

## A.1 What Camino B does
When a migration enters `GENERATING`, a worker generates the planned services **autonomously**: for each service it spins an isolated container, places the inputs, invokes a coding agent that writes the service and runs the gates, captures the result, and records per-service status. No human pastes anything.

## A.2 Agent invocation — Claude Code headless
The worker invokes **Claude Code in headless / non-interactive mode** as a subprocess inside the container — it reuses the existing agentic loop (file edits, running `go build`/`go test`, reading errors, retrying) instead of reimplementing it against a raw model API. The generator prompt (`docs/prism/milton-prism-service-generator-prompt.md`) is the agent's instructions; its built-in self-verification loop runs the gates.

> **Verify before building (A.2 depends on external product details that change):** the exact way to invoke Claude Code non-interactively (flags, SDK, output/stream format, how it signals completion, how/if it reports token usage) must be checked against current Claude Code documentation, not assumed. Task B0 does this first.

## A.3 Isolation — ephemeral container per generation, from day one
Each service generation runs in an **ephemeral container** with the toolchain (Go, buf, Claude Code) and the model credentials injected at runtime. This is NOT optional and NOT deferred: the agent executes code derived from an untrusted third-party repo (which may carry the very vulnerabilities Prism reports) plus arbitrary agent commands. Running that unisolated in the worker process is a security exposure. The container has resource limits (CPU, memory) and restricted network; it is destroyed after the generation.

> This container is also the foundation of the verification sandbox (the next hole). Building isolation now advances both.

## A.4 Cost and limits (defaults — adjustable later)
- **Wall-clock timeout per service:** default 15 minutes; on timeout, mark the service failed.
- **Iteration discipline:** rely on the generator prompt's own "same gate failure 3× → stop and report" rule; the worker enforces the wall-clock timeout as the hard backstop.
- **Token budget:** if Claude Code headless reports usage, record it per service and enforce a per-service cap; if it does not expose usage, mark token-budget enforcement as a hole (B0 determines this).
- **Concurrency:** cap how many service containers run in parallel (default 2) to bound cost and resource use.

## A.5 Failure handling — degrade per service
A service that does not reach green gates within the limits is marked `failed` with the captured reason; the migration **continues** with the other services. The migration's final state reflects partial success (e.g. `READY` with a per-service status list showing which succeeded/failed), never a hard crash. Same degrade-and-report discipline used throughout Prism.

## A.6 Output of generation
Generated service code is persisted per (migration, service) — reuse/extend the `design_artifacts` approach or a `generated_services` store. The actual git push to a branch/new repo remains the repository service's job (its git client is still a stub — that's a separate hole). Camino B produces and persists the code; pushing is downstream.

## A.7 Credentials
Model credentials for Claude Code are injected into the container **at runtime as secrets**, never baked into the image and never logged. Document where they come from in `docs/prism/camino-b-setup.md`.

## A.8 Holes (marked, not built here)
- Full system sandbox (run the generated services together and verify behavior) — next hole; the A.3 container is its base.
- Git push of the result — repository service's stubbed git client.
- Token-budget enforcement if Claude Code doesn't expose usage (B0 decides).

---

# PART B — Tasks (one at a time, gate block between each)

**Gate block (Go side, CGO_ENABLED=1):**
```
buf lint
go build ./...
go vet ./...
go test ./core/...
```

### Task B0 — Inspection: how to invoke Claude Code headless (NO code)
Before building anything, determine and report:
1. The current, documented way to run Claude Code non-interactively / headless: the command/flags or SDK, how to pass a prompt and a working directory, the output format, and how it signals success/failure on completion.
2. Whether it reports token/cost usage in a machine-readable way (decides A.4 token enforcement).
3. What must be present in a container image to run it (runtime, auth mechanism).
Check current Claude Code documentation — do not assume from memory. Report findings and STOP. Do not write code.

### Task B1 — ContainerRunner (ephemeral isolated container)
Implement a `ContainerRunner` port + adapter that: builds/uses an image with the toolchain (Go, buf, Claude Code per B0), starts an ephemeral container with CPU/memory limits and restricted network, mounts/places a workspace, runs a command capturing stdout/stderr/exit, and tears down on completion (even on failure). Validation: spin a container, run `go version` + `buf --version` + the Claude Code version check from B0, confirm teardown leaves nothing. Gate block.

### Task B2 — AgentInvoker (Claude Code headless inside the container)
Implement an `AgentInvoker` port + adapter that, inside a B1 container, places the generation inputs (the generator prompt, the Canon, the active language profile, the service's boundary spec, the derived `.proto`), injects model credentials as a runtime secret (A.7), invokes Claude Code headless (per B0) with the generator prompt as instructions and the workspace as working dir, and captures the result: did the agent's self-verification gates pass, and the generated file list. Enforce the A.4 wall-clock timeout. Validation: run it for the Conduit `articles` service and confirm it produces a service that passes its gates inside the container — compare to the manually generated `articles` from Camino A (equivalent quality). Gate block.

### Task B3 — Generation worker (wire GENERATING → autonomous generation)
When `approveDesign`/state reaches `GENERATING`, enqueue generation work. A generation worker, per service in the plan (respecting the A.4 concurrency cap): runs B1+B2, captures per-service status (`generating`/`verifying`/`done`/`failed` + gate results + reason), persists the generated code (A.6), and degrades per service (A.5). When all services are processed, advance the migration state to reflect partial success with a per-service status list. Idempotent: re-running reads persisted status, regenerates only what's not `done`. Validation: a Conduit migration in `GENERATING` autonomously generates all three services; show the per-service status and that `articles` matches Camino A quality. Gate block.

### Task B4 — Frontend: live generation status
Replace the manual "copy block" GENERATING screen (or keep it as a fallback toggle) with a live per-service generation view: each service shows its status (`generating`/`verifying`/`done`/`failed`), gate results, and the failure reason if failed. Poll for updates as in the analysis screen. Validation: watch the three Conduit services generate live. `vite build` clean.

### Task B5 — End-to-end validation on Conduit
Run a full migration on the real Conduit from `GENERATING` autonomously to completion. Report: per-service status (all three done, or which failed and why), that the generated `articles` passes the same gates and conformance self-audit as the Camino A manual run, the captured cost/usage if available, and the final migration state. Gate block.

---

## Standing rules
- Part A frozen. Ambiguity/impossibility → STOP note in `docs/prism/camino-b-blockers.md`.
- Isolation (A.3) is mandatory from B1 — never run the agent or generated code unisolated.
- Credentials injected at runtime as secrets, never baked or logged (A.7).
- Degrade per service, never crash the migration (A.5).
- Validate every step against the real Conduit migration; `articles` from Camino A is the quality reference.
- Holes (A.8) get a marked stop/report, never a guess.
- One task per turn; gate block between each; STOP for review after B0, B2, and B5.
