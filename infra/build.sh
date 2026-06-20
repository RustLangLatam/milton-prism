#!/usr/bin/env bash
# build.sh — Compile all Milton Prism backend binaries into infra/bin/.
#
# Usage:
#   ./build.sh              # build all services + gateway
#   ./build.sh identity     # build only services matching "identity"
#
# Binaries land in infra/bin/, which is the Docker build context used by
# all environment docker-compose files.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$(cd "$(dirname "$0")" && pwd)/bin"
mkdir -p "$BIN_DIR"
CMD_DIR="$REPO_ROOT/core/cmd"

VERSION="1.0.0-dev"
BUILD_TIME="$(date +%Y-%m-%dT%H:%M:%S%z)"
CONFIG_PACKAGE="milton_prism/pkg/config"
LDFLAGS="-X '${CONFIG_PACKAGE}.Version=${VERSION}' -X '${CONFIG_PACKAGE}.BuildTime=${BUILD_TIME}' -s -w"

declare -A SERVICES=(
  ["identity-services"]="identity_service"
  ["repository-services"]="repository_service"
  ["generation-worker"]="generation_worker"
)

# Workers and services that require CGO (tree-sitter, etc.) — built with static linking.
# migration-services and analysis-services pull in tree-sitter transitively via
# their migrability assessor adapters (PHPAwareInfraDetector).
declare -A WORKERS=(
  ["analysis-worker"]="analysis_worker"
  ["analysis-services"]="analysis_service"
  ["migration-services"]="migration_service"
)

GATEWAY_SVC="milton-prism-gateway"
GATEWAY_BIN="milton_prism_gateway"
GATEWAY_CMD="$REPO_ROOT/api-gateway/cmd/milton-prism-gateway"

build_service() {
  local svc="$1"
  local bin="$2"
  local src_dir="$CMD_DIR/$svc"

  if [ ! -d "$src_dir" ]; then
    echo "  SKIP  $svc (directory not found)"
    return
  fi

  echo -n "  BUILD $svc → $bin ... "
  local output
  if output=$(cd "$src_dir" && CGO_ENABLED=0 GOOS=linux go build -ldflags "$LDFLAGS" -o "$BIN_DIR/$bin" . 2>&1); then
    echo "OK"
  else
    echo "FAILED"
    echo "$output" | sed 's/^/    /'
  fi
}

build_worker() {
  local svc="$1"
  local bin="$2"
  local src_dir="$CMD_DIR/$svc"

  if [ ! -d "$src_dir" ]; then
    echo "  SKIP  $svc (directory not found)"
    return
  fi

  echo -n "  BUILD $svc → $bin (CGO static) ... "
  local output
  if output=$(cd "$src_dir" && CGO_ENABLED=1 GOOS=linux go build -ldflags "$LDFLAGS -extldflags=-static" -o "$BIN_DIR/$bin" . 2>&1); then
    echo "OK"
  else
    echo "FAILED"
    echo "$output" | sed 's/^/    /'
  fi
}

echo "=== Milton Prism backend build ==="
echo "Repo:    $REPO_ROOT"
echo "Output:  $BIN_DIR"
echo ""

FILTER="${1:-}"

for svc in "${!SERVICES[@]}"; do
  bin="${SERVICES[$svc]}"
  if [ -z "$FILTER" ] || [[ "$svc" == *"$FILTER"* ]]; then
    build_service "$svc" "$bin"
  fi
done

for svc in "${!WORKERS[@]}"; do
  bin="${WORKERS[$svc]}"
  if [ -z "$FILTER" ] || [[ "$svc" == *"$FILTER"* ]]; then
    build_worker "$svc" "$bin"
  fi
done

if [ -z "$FILTER" ] || [[ "$GATEWAY_SVC" == *"$FILTER"* ]]; then
  if [ -d "$GATEWAY_CMD" ]; then
    echo -n "  BUILD $GATEWAY_SVC → $GATEWAY_BIN ... "
    gw_output=""
    if gw_output=$(cd "$GATEWAY_CMD" && CGO_ENABLED=0 GOOS=linux go build -ldflags "$LDFLAGS" -o "$BIN_DIR/$GATEWAY_BIN" . 2>&1); then
      echo "OK"
    else
      echo "FAILED"
      echo "$gw_output" | sed 's/^/    /'
    fi
  fi
fi

echo ""
echo "Done. Binaries in: $BIN_DIR"
echo "Next: docker compose -f environments/<env>/docker-compose.yaml up --build -d"
