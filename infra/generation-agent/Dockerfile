# Milton Prism — Generation Agent Image
#
# Toolchain container for Camino B autonomous service generation.
# Contains: Go, buf, git, Node.js, Claude Code CLI.
#
# Build (from repo root):
#   docker build -t milton-prism-generation-agent:latest \
#       -f infra/generation-agent/Dockerfile .
#
# The image carries NO model credentials. ANTHROPIC_API_KEY must be injected
# at container start time as an environment variable (A.7). Never bake
# credentials into the image and never log the Env slice that carries them.

FROM golang:1.25-alpine

LABEL org.opencontainers.image.title="milton-prism-generation-agent"
LABEL org.opencontainers.image.description="Milton Prism Camino B: autonomous service generation toolchain"

ARG BUF_VERSION=1.50.0

# protoc-gen-openapi — the RustLangLatam fork the platform uses for OpenAPI gen.
# Pinned to the commit that built the platform's host binary (matches the opts
# in protobuf/buf.docs.gen.yaml: depth/enum_type/default_response). The module
# path (github.com/RustLangLatam/protoc-gen-openapi) differs from the repo URL
# (github.com/RustLangLatam/openapi-generatos), so a proxy `go install
# <module>@<ver>` cannot resolve it — a full clone + checkout + `go install .`
# (the root main package) is the reliable install, producing a binary named
# `protoc-gen-openapi` (after the module path's last element).
ARG PROTOC_GEN_OPENAPI_REPO=https://github.com/RustLangLatam/openapi-generatos
ARG PROTOC_GEN_OPENAPI_REF=722ed85

# System dependencies:
#   git            — VCS operations inside the workspace
#   nodejs / npm   — required by Claude Code CLI AND by the Node (TypeScript)
#                    generation profile (npm install + tsc build gate)
#   curl           — buf binary download
#   bash           — shell for agent commands (claude --bare uses bash internally)
#   ca-certificates — HTTPS to api.anthropic.com, proxy.golang.org, buf.build
#   protobuf       — provides `protoc`, used by the Node profile's ts-proto codegen
#                    path (buf generate can also drive protoc-gen-ts_proto). The
#                    Node profile's PRIMARY path is the pure-npm @grpc/proto-loader
#                    (no protoc needed), so protoc is a robustness fallback only.
#                    REQUIRED by the Rust profile: tonic-build shells out to
#                    `protoc` for Prost/Tonic codegen in build.rs.
#   rust / cargo   — the Rust (Tonic) generation profile build gate is
#                    `cargo build`. Alpine's rust/cargo are musl-native, matching
#                    this musl base; no rustup download needed.
#   musl-dev gcc   — C toolchain rustc/cargo need to link the final binary and to
#                    build crates with C build scripts (e.g. ring/tls deps).
#   pkgconf openssl-dev — common -sys crate build inputs (kept minimal).
RUN apk add --no-cache \
      git \
      nodejs \
      npm \
      curl \
      bash \
      ca-certificates \
      protobuf \
      rust \
      cargo \
      musl-dev \
      gcc \
      pkgconf \
      openssl-dev

# buf CLI — downloaded directly from GitHub releases for the correct arch.
# Used for: buf lint, buf generate (gate block inside the container).
RUN ARCH=$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/arm64/') && \
    curl -fsSL \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${ARCH}" \
      -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

# protoc-gen-openapi — installed from the RustLangLatam fork so the deliverable
# OpenAPI matches the platform's conventions.
#
# The `package main` that produces the binary lives at the REPO ROOT (main.go),
# and go.mod there declares `module github.com/RustLangLatam/protoc-gen-openapi`,
# so `go install .` of the root package emits a binary named after the module's
# last path element: `protoc-gen-openapi`. It lands in /go/bin (already on PATH
# in golang images); buf resolves it via `local: protoc-gen-openapi`.
#
# We install the ROOT package only (`go install .`), NOT `./...`: the repo also
# carries an examples/ tree that may contain its own main packages — `./...`
# would try to build all of them (extra/failing builds, possibly extra binaries),
# making the result non-deterministic. `.` builds exactly the one binary we need.
#
# A FULL clone (no --depth 1) + `git checkout <ref>` is used so an arbitrary
# pinned ref (commit SHA, branch, or tag) always resolves. The previous form
# (`clone --depth 1` of the default branch, then `fetch --depth 1 origin <SHA>`,
# then `checkout <SHA>`) failed at build with exit 128: a shallow clone fetches
# only the tip of the default branch, and `fetch --depth 1 origin <SHA>` is not
# reliably supported by the GitHub server for an arbitrary commit SHA
# (uploadpack.allowReachableSHA1InWant/allowAnySHA1InWant is not guaranteed), so
# the commit never lands locally and the subsequent `checkout <SHA>` aborts with
# a git error (exit 128) — failing the image build and leaving the old image
# (without protoc-gen-openapi) in place. A full clone has every commit locally,
# so the checkout always succeeds.
#
# Two build-time pitfalls this RUN deliberately avoids:
#   1. The clone must NOT live under /tmp: Go refuses to use a go.mod located in
#      the system temp root (`go: warning: ignoring go.mod in system temp root
#      /tmp` → `go.mod file not found`), so we clone to /build/... instead.
#   2. The fork's go.sum is incomplete (missing entries for
#      google.golang.org/protobuf/...), so a plain `go install .` fails with
#      `missing go.sum entry for module providing package ...`. We run it with
#      GOFLAGS=-mod=mod so the install resolves and adds the missing sums from
#      the network.
#
# The build-time guard `command -v protoc-gen-openapi` makes a future regression
# (wrong module path / install target) fail the image build loudly.
RUN git clone "${PROTOC_GEN_OPENAPI_REPO}" /build/protoc-gen-openapi-src && \
    git -C /build/protoc-gen-openapi-src checkout "${PROTOC_GEN_OPENAPI_REF}" && \
    (cd /build/protoc-gen-openapi-src && GOFLAGS=-mod=mod go install .) && \
    rm -rf /build/protoc-gen-openapi-src && \
    command -v protoc-gen-openapi

# ── Rust (Tonic) profile dependency pre-warm ─────────────────────────────────
# Compiling a Tonic/Prost service from a cold cache downloads + compiles ~100
# crates, which alone can exceed the generation container's wall-clock budget.
# We pre-build a throwaway crate that depends on EXACTLY the crate set the Rust
# profile fixes (tonic/prost/tokio/mongodb/...), so the agent image ships with:
#   - the crate registry index + downloaded source crates (CARGO_HOME/registry)
#   - compiled rlibs for those deps in the dummy crate's target/ (reused by the
#     agent's build through the shared registry — source crates never re-download)
# CARGO_HOME is a world-readable shared path so the non-root `prism` agent user
# reuses the warm registry. The agent still compiles its own service crate, but
# the heavy, slow-to-fetch dependency graph is already present.
#
# Keep this dep list in lockstep with milton-prism-rust-profile.md §A.1. Pinned
# minor versions keep the cache deterministic across image rebuilds.
ENV CARGO_HOME=/usr/local/cargo
ENV PATH=$CARGO_HOME/bin:$PATH
RUN mkdir -p /build/prism-cargo-warm/src && \
    cd /build/prism-cargo-warm && \
    printf '%s\n' \
      '[package]' \
      'name = "prism_cargo_warm"' \
      'version = "0.0.0"' \
      'edition = "2021"' \
      '' \
      '[dependencies]' \
      'tonic = "0.12"' \
      'prost = "0.13"' \
      'tokio = { version = "1", features = ["rt-multi-thread", "macros"] }' \
      'mongodb = "3"' \
      'bson = "2"' \
      'async-trait = "0.1"' \
      'dotenvy = "0.15"' \
      'envy = "0.4"' \
      'serde = { version = "1", features = ["derive"] }' \
      'tracing = "0.1"' \
      'prost-types = "0.13"' \
      '' \
      '[build-dependencies]' \
      'tonic-build = "0.12"' \
      > Cargo.toml && \
    printf 'fn main() {}\n' > src/main.rs && \
    printf 'fn main() {}\n' > build.rs && \
    cargo build && \
    cargo build --release && \
    rm -rf /build/prism-cargo-warm && \
    chmod -R a+rX "$CARGO_HOME"

# Claude Code CLI — model credentials are injected at runtime, never in image.
# --bare mode (used at invocation time) skips reading ~/.claude/ and .claude/,
# making the container fully stateless.
RUN npm install -g @anthropic-ai/claude-code --no-fund --no-audit

# Suppress npm and Claude Code telemetry/update noise in container output.
ENV NPM_CONFIG_UPDATE_NOTIFIER=false
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
ENV CI=true

# Run as a non-root user so --dangerously-skip-permissions is accepted.
# uid=1000 matches the typical host uid; bind-mounted workspace dirs created
# by os.MkdirTemp on the host are therefore writable without an explicit chmod.
RUN addgroup -S prism && adduser -S -G prism -u 1000 prism

# Go build cache under /tmp so the non-root user can write.
# GOMODCACHE stays at /go/pkg/mod — the host read-only mount path is unchanged.
ENV GOCACHE=/tmp/go-build-cache
ENV GOMODCACHE=/go/pkg/mod
ENV HOME=/home/prism

USER prism
WORKDIR /workspace
