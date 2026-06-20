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

# System dependencies:
#   git           — VCS operations inside the workspace
#   nodejs / npm  — required by Claude Code CLI
#   curl          — buf binary download
#   bash          — shell for agent commands (claude --bare uses bash internally)
#   ca-certificates — HTTPS to api.anthropic.com, proxy.golang.org, buf.build
RUN apk add --no-cache \
      git \
      nodejs \
      npm \
      curl \
      bash \
      ca-certificates

# buf CLI — downloaded directly from GitHub releases for the correct arch.
# Used for: buf lint, buf generate (gate block inside the container).
RUN ARCH=$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/arm64/') && \
    curl -fsSL \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${ARCH}" \
      -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

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
