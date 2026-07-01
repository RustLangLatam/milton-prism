# Git Push + Pre-flight — Blockers / Validation Gaps

This file records honest stop-notes and partial-validation gaps for the
git-push frontier (spec: `milton-prism-git-push-spec.md`). It is NOT a list of
failures to hide — it is the "what I could not validate" ledger (Lesson 11).

---

## Task P0 — Pre-flight repo probe (read side) — 2026-06-21

**Status:** implemented, gate-green, validated over real HTTP for the public and
bad-URL cases. One validation gap below; not a code blocker.

### Gap 1 — private repo + valid read token: not fully validated (Lesson 11)

The probe's three live HTTP outcomes were validated against the running `local`
stack (gateway :8083, JWT EdDSA + keydb session):

- PUBLIC repo, no token → `reachable=true, visibility=PUBLIC, authValid=true`. ✅ validated
  (`https://github.com/octocat/Hello-World`).
- Bad / unreachable URL → `reachable=false` + legible message, no 500. ✅ validated.
- Bad token against a private-or-missing repo → `reachable=true,
  visibility=PRIVATE, authValid=false` + "Token was rejected…". ✅ validated.

**What was NOT validated:** the *happy* private path — a real PRIVATE repo with a
*valid* read token returning `visibility=PRIVATE, authValid=true`. No private repo
+ working read token was available in this environment, so this branch
(`http.StatusUnauthorized/Forbidden` → re-probe with token → `200 OK`) was
exercised only by unit tests (`TestProbeSourceRepository_PrivateWithValidToken`
with a stub git client), not over a real remote. The code path is symmetric with
the validated bad-token path (same `httpProbe` call, only the remote's status
differs), so confidence is high, but a real-remote confirmation is still owed
before calling the private-happy-path field-proven.

### Provider nuance (not a blocker, recorded for the frontend)

GitHub returns HTTP **404** for a private repo when no/insufficient credentials
are sent — deliberately, to avoid leaking the existence of private repos. The
probe therefore cannot distinguish "private, needs token" from "genuinely does
not exist" without a token. v1 behavior: with no token, a 404 is reported as
`visibility=PRIVATE, authValid=false` (i.e. "looks private — supply a token").
This is the spec-aligned choice (A.2: show the token input when the repo looks
private). The frontend should treat PRIVATE+!authValid as "prompt for a read
token"; if the token is then rejected with "not found", the URL is likely wrong.
This matches A.9's "multi-provider nuances — start with the common case".

---

## Frente 2, sub-fase 2 — write-side PRE-FLIGHT / DRY-RUN — 2026-06-21

**Status:** implemented, gate-green (buf lint / go build / go vet / go test ./core/...
all pass with CGO_ENABLED=1). Validated end-to-end against a **real local git
smart-HTTP server** (git-http-backend over `httptest`, no network, no real
remote, no model tokens). **HARD STOP honored: no real push to any remote was
performed.** The push itself (P1) is not enabled by this work.

### What the write-side pre-flight validates today (no real push)

`GitClient.PreflightTarget(ctx, targetURL, writeToken)` →
`domain.TargetPreflightResult{Reachable, CanPush, Empty, ErrorMessage}`. It does
NOT push: it (1) GETs the smart-HTTP **receive-pack** discovery endpoint
(`info/refs?service=git-receive-pack`) — the write-side equivalent of the P0
read probe — to confirm reachability and whether the write token is accepted for
push (200 = can push, 401/403 = token rejected, 404 = target not pre-created),
and (2) lists refs (ls-remote) to confirm the target is **empty** (A.3). go-git
v5.19.1 `PushOptions` has **no DryRun field**, so a literal `git push --dry-run`
is not available in-process; the receive-pack-discovery + ls-remote pair is the
correct non-mutating substitute and is what was implemented.

Validated branches (real local git server, `git_client_preflight_test.go`):
- empty target, valid push → `Reachable=true, CanPush=true, Empty=true`. OK
- non-empty target (one seeded commit) → `Empty=false`. OK
- bad write token (basic-auth gated server) → `Reachable=true, CanPush=false`,
  legible "token rejected for push". OK
- unreachable host / invalid URL → `Reachable=false`, legible message, no panic. OK
- **non-mutation proof**: after a pre-flight the bare target still has no HEAD —
  pre-flight writes nothing. OK

### Gap 2 — migration 10018 has NO persisted artifacts in this environment (Lesson 11)

The spec (A.7 / Task P2) names migration **10018** as the artifact source for the
`assertNoSecrets` sweep. In the running `local` stack
(`mongodb://admin:***@localhost:27017`, rs0), **`generation_file_artifacts` is
empty (0 documents total, 0 for migration_id=10018)**. Migration 10018 does not
exist here; the only migrations present are 2,3,4,5, and only `design_artifacts`
(migrations 2 & 4, 20 docs) carry generated-ish payload. The 10018 per-file
payload referenced by the spec lived in a prior/other environment and is not
reproducible here.

**assertNoSecrets evidence (what WAS run, grep=0):**
- Function located: `assertNoSecrets(content, label)` in
  `core/services/migration/application/assembler/assembler_config.go` (checks the
  `knownSecrets` dev-credential set). NOT relaxed.
- A payload-wide gate `AssertPayloadNoSecrets([]File)` was added (same package,
  same `knownSecrets`) so the **entire** push payload — not just synthesised
  config templates — is swept before any push. Unit-proven: clean payload → nil
  (grep=0); planted secret → caught, error names the path, never echoes the value
  (`assembler_secrets_test.go`).
- Real persisted-payload sweep over **all** artifact-bearing collections in the
  live `milton_prism_migration` DB (`generation_file_artifacts`,
  `generation_results`, `design_artifacts`): **TOTAL secret-hits = 0 (grep=0)**.
  Since 10018 has no artifacts, the only real payload available to sweep was the
  20 `design_artifacts` of migrations 2 & 4 → grep=0. Named honestly per the gate:
  the assert is grep=0 over the real persisted payload that exists; the
  10018-specific set is unavailable, not skipped.

### Gap 3 — live replica set not reachable by hostname from the host

The `local` rs0 advertises its member as `mongodb:27017` (docker-internal name),
so the Go driver, after seeding on `localhost`, fails host-side with
`lookup mongodb ... no such host`. The `*_10018` integration test therefore
cannot run from the host even with `MONGO_URI` set; it must run inside the docker
network (or with a host alias `mongodb → 127.0.0.1`). The mock-based
`TestPublishMigration_Integration_CleanArtifacts` passes (no live data needed).
Not a code blocker — an environment/topology note.

### Security note (out of scope, flagged) — leaked API key in Makefile

`milton-prism/Makefile` line 32 is a commented-out string that looks like a real
Anthropic API key (`#sk-ant-api03-...`). This is a real secret in a tracked file.
Outside this task's edit scope (write-side pre-flight), but it should be rotated
and scrubbed from history. Not touched here.

### Readiness for P1 (real push) — conditions still owed

Today the write-side is dry-run-complete: reachability, push-permission, and
empty-target are all validatable without pushing, and the payload secret-sweep is
in place and grep=0. Before P1 (real push) is enabled, the following must hold,
none of which this sub-phase performed:
1. **Human confirmation + a clean write credential** (A.4 / A.6) — the spec's
   explicit STOP-for-review after the first real git-write. NOT done here.
2. A **real, pre-created empty target repo** with a valid write-scoped token, to
   confirm the receive-pack happy path over a real provider (only the local
   smart-HTTP server was exercised here; real-provider auth nuances are A.9).
3. A **10018 (or equivalent) artifact set persisted** so the publish flow pushes a
   real reviewed payload (Gap 2). With no artifacts the publish flow correctly
   refuses rather than pushing nothing.
4. Wiring `PreflightTarget` into `PublishMigration` as a guard **before** the push
   (so a non-empty / no-push-permission target fails fast in READY, A.8) — the
   primitive exists and is owned by the repository service (A.5), but the publish
   orchestration still calls `PushFiles` directly; adding the pre-flight call is a
   P1/P2 wiring step, deliberately NOT done in this dry-run sub-phase.
