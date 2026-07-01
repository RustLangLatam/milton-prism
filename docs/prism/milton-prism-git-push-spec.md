# Milton Prism — Git Push + Repo Pre-flight (spec + tasks)

The last link of the cycle: deliver the reviewed generated code to the client's git. Plus the repo connection pre-flight (detect public/private, validate connection, show the token input only when needed) so the user doesn't fumble credentials.

The generated code already lives in MongoDB staging (`generation_file_artifacts`) and is reviewable in the UI. This makes "reviewed code → client's git" real, closing generate → review → **deliver**.

Two parts: **Part A** = frozen decisions. **Part B** = tasks. One task at a time, gate block between each. Ambiguity/impossibility → STOP note in `docs/prism/git-push-blockers.md`.

---

# PART A — Frozen decisions

## A.1 Two distinct credential moments — read vs write
Reading (clone) and writing (push) are different accesses with different scopes. A repo can be public to read but require a write-scoped token to push. The system treats them separately:
- **Clone (read):** at migration creation. Public repo → no token needed. Private → read token.
- **Push (write):** at publish time. Always needs a write token (even if the repo was public for reading).

This is the core insight driving both the pre-flight and the push.

## A.2 Pre-flight repo probe (read side) — at migration creation
Before cloning, probe the source repo: is it reachable? public or private? Show the clone-token input **only if the repo is private**. If public, no token UI — one less thing for the user to get wrong. Validate the connection (and the token, if given) before kicking off the clone, so a bad URL or bad token fails immediately with a clear message instead of mid-clone.

## A.3 Push destination — user-provided target repo (configurable)
The microservices are a different structure than the monolith, so pushing onto a new branch of the monolith repo mixes old and new awkwardly. v1: the user provides a **target repo URL** (a new, empty repo they created beforehand) + a write token, and Prism pushes the generated monorepo there. Auto-creating the repo via the provider's API (GitHub/GitLab) is a future hole (A.9) — for v1 the user creates the empty repo, we fill it.

## A.4 Write credentials — provided at push time, never stored
The write token is supplied by the user at the moment of publishing, used for that push operation, and **never persisted**. Storing write-scoped tokens is a security liability. (Consistent with the existing "tokens → httpOnly / don't persist" hole.) Separate from the clone read-token of A.2.

## A.5 Owner — the repository service does the git operations
The repository service is the architectural owner of git (it already has the git stub). The worker must NOT do git directly (noted when designing persistence). The push flow: read artifacts from MongoDB staging → repository service performs clone/init of target + commit + push.

## A.6 Trigger — explicit human approval after review
The push happens AFTER the human reviews the generated code (the review UI already exists) and explicitly chooses to publish. NOT automatic on generation. This is the whole point of the MongoDB staging + review gate (validated by the agent-variance finding: autonomous output needs human review before it touches the client's repo).

## A.7 Source — MongoDB staging
The push reads the generated files from `generation_file_artifacts` (already persisted, already exposed via `GetGenerationArtifacts`). No regeneration, no tokens spent on the model.

## A.8 State — PUSHED on success, stays READY (retryable) on failure
`MIGRATION_STATE_PUSHED` already exists. On a successful push: `READY → PUSHED`. On push failure (bad credentials, network, conflict): the migration **stays in READY** (the code is safe in staging, the push is retryable) and surfaces a legible failure reason — NOT a silent hang, NOT a misleading FAILED. Consistent with the "no state lies" principle.

## A.9 Holes
- Auto-create the target repo via provider API (GitHub/GitLab) — v1 requires a pre-existing empty repo.
- Token storage / OAuth app integration — v1 is push-time tokens only.
- Branch strategy / incremental re-push — v1 is a clean push to an empty target.
- Multi-provider nuances (GitHub vs GitLab vs Bitbucket auth) — start with the common case.

---

# PART B — Tasks (one at a time)

**Gate block (CGO_ENABLED=1):** `buf lint` · `go build ./...` · `go vet ./...` · `go test ./core/...` (+ `vite build` for frontend tasks).

### Task P0 — Pre-flight repo probe (read side)
Backend: a probe that, given a repo URL (+ optional read token), reports reachable? public/private? auth-valid? without cloning. Expose it (RPC + gateway binding). Frontend (`NewMigrationPage`): on entering the repo URL, call the probe; show the clone-token input ONLY if the repo is private; validate before allowing "Start". Clear messages for unreachable / bad-token. Validate against a public repo (no token prompt), a private repo (token prompt), and a bad URL (clear error). Gate block. No tokens spent on the model (this is git/HTTP probing, not LLM).

### Task P1 — Implement the git push in the repository service
Replace the git stub with a real push: given a target repo URL + write token + a set of files (path/content), the repository service initializes/clones the target, writes the files preserving structure, commits (a traceable message), and pushes. Write token used at call time, never logged or stored. Handle failure cleanly (bad token, conflict, network) with a typed error + legible reason. Unit-test the git operations against a local/temp git remote (no real remote needed, no tokens). Gate block. STOP for review — this is the first real git-write operation; confirm it's clean and credentials are handled safely before wiring.

### Task P2 — Wire the publish flow
A `PublishMigration` (or similar) operation: reads the artifacts from `generation_file_artifacts`, calls the repository service push (P1) with the user-provided target + write token, and on success transitions `READY → PUSHED`; on failure stays `READY` with a legible reason (A.8). Idempotent/retryable. Gate block. No tokens spent (use stored artifacts from migration 10018; for the actual remote, a temp/local git target).

### Task P3 — Frontend publish UI
In `READY` (after the review panel): a "Publish to git" action that collects the target repo URL + write token, calls `PublishMigration`, shows push progress, and on success shows the `PUSHED` confirmation (with the target repo link). On failure: show the reason and keep the migration in READY (retryable) — the code stays available (review + download still work). The write token field is not persisted (cleared after use). Validate the flow against a test target repo. `vite build` clean.

---

## Standing rules
- Part A frozen. Read credentials (clone) and write credentials (push) are separate moments (A.1).
- Write token: push-time only, never stored, never logged (A.4).
- The repository service owns git ops; the worker never does git directly (A.5).
- Push only after explicit human approval post-review (A.6).
- Push failure → stays READY retryable with reason, never a silent hang or misleading FAILED (A.8).
- Holes (A.9) get a marked stop/report, never a guess.
- STOP for review after P1 (first real git-write).
- No model tokens spent anywhere in this spec — it's git/HTTP, not LLM. Use migration 10018's stored artifacts + a temp/local git target for validation.
