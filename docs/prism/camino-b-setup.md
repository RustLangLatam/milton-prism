# Camino B — Setup and Operational Guide

Infrastructure reference for Camino B autonomous generation. Covers the generation agent image, network policy, credential injection, and validation procedures.

---

## Generation agent image

### Location

`infra/generation-agent/Dockerfile`

### Contents

| Tool | Purpose | Source |
|---|---|---|
| Go 1.23 | `go build`, `go vet`, `go test` (gate block) | `golang:1.23-alpine` |
| buf | `buf lint`, `buf generate` (gate block) | GitHub releases |
| git | workspace VCS operations | Alpine apk |
| Node.js + npm | Claude Code CLI runtime | Alpine apk |
| Claude Code CLI | `claude --bare -p "..." --output-format json` | npm `@anthropic-ai/claude-code` |

### Build

From the repo root:

```bash
docker build -t milton-prism-generation-agent:latest \
    -f infra/generation-agent/Dockerfile .
```

Build takes 3–5 minutes on first run (npm install pulls Claude Code and its dependencies). Subsequent builds use the layer cache.

### Version pins

- Go: `1.23` (change the `FROM` line to update)
- buf: controlled by `ARG BUF_VERSION` (default `1.50.0`)
- Claude Code: resolved at build time by npm; pin with `@anthropic-ai/claude-code@<version>` for reproducible builds

---

## Credential injection (A.7)

Model credentials are **never** baked into the image and **never** logged. The `DockerContainerRunner` passes them as environment variables at container start time.

Required variable:

| Variable | Value | Source |
|---|---|---|
| `ANTHROPIC_API_KEY` | Anthropic API key (`sk-ant-api03-...`) | Runtime secret |

The generation worker reads this from its own environment (or a secrets manager) and passes it in `RunRequest.Env`. The `RunRequest.Env` slice is never written to any log.

For local development, add to the worker's `config.toml` or export in the shell. For production, inject via Docker secrets or a secrets manager.

---

## Network policy

### Isolated Docker network: `prism-generation`

Generation containers run in a dedicated bridge network `prism-generation`, **separate from** `milton-prism-network` and `cache-network` (the project's internal networks).

**What this provides:**
- ✅ Internet egress via host NAT — required for three endpoints:
  - `api.anthropic.com:443` — Claude Code model calls
  - `proxy.golang.org:443`, `sum.golang.org:443` — Go module downloads
  - `buf.build:443` — buf schema registry
- ✅ No DNS resolution of internal service hostnames — generation containers cannot reach `mongodb`, `keydb-replica-0`, or any internal gRPC service by container name
- ⚠️ IP-based access to internal containers is theoretically possible if IPs are known — see hardening note below

**Hardening (future):** Add iptables egress rules to restrict outbound traffic to the three CIDRs above only. This eliminates the IP-based access vector entirely.

### Creating the network

```bash
docker network create --driver bridge prism-generation
```

`DockerContainerRunner.EnsureNetwork` handles this idempotently at worker startup.

---

## Headless invocation (from B0)

The generation worker invokes Claude Code with:

```bash
claude --bare -p "<combined prompt>" --output-format json
```

- `--bare`: skips `~/.claude/` and `.claude/` reads — container is stateless
- `--output-format json`: structured response with `result`, `total_cost_usd`, `usage`
- Exit code 0 = success; 1 = failure; 2 = hook blocked

Token usage is available in `total_cost_usd` (client-side estimate, not authoritative billing). The worker records it per service for monitoring.

---

## Validation procedure (B1)

After building the image, run the integration tests:

```bash
CGO_ENABLED=1 go test -v -tags integration -timeout 5m \
    ./core/worker/generation/infrastructure/container/...
```

Expected: all five tests pass, including `TestRun_TeardownOnTimeout` and `TestRun_NetworkIsolation`. Confirm `docker ps` shows no leftover containers after the run.

---

## Resource limits (A.4 defaults)

| Limit | Default | Location |
|---|---|---|
| Wall-clock timeout per service | 15 min | `RunRequest.Timeout` in B3 worker |
| CPU quota | 50% of 1 CPU (50 000 µs / 100 000 µs period) | `RunRequest.CPUQuota` |
| Memory | 1 GiB | `RunRequest.MemoryBytes` |
| Concurrency | 2 containers | B3 worker semaphore |

Adjust in the generation worker configuration (B3).
