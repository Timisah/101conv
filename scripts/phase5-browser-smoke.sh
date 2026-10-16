#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_root="$(mktemp -d)"
export COMPOSE_PROJECT_NAME="101conv-phase5-smoke-$$"
export SMOKE_IMAGE="101conv-phase5-smoke:$$"
export WEB_IMAGE="101conv-web:phase5-smoke-$$"
export BUILDX_CONFIG="$tmp_root/buildx"
export DATA_PATH="$tmp_root/data"
export POSTGRES_PASSWORD="phase5-smoke-$(date +%s)-$RANDOM"
export ENGINE_ADMIN_TOKEN="phase5-admin-$(date +%s)-$RANDOM"
port_num="${SMOKE_PORT:-15656}"
export PORT="127.0.0.1:$port_num"
export SMOKE_BASE="http://127.0.0.1:$port_num"
export BROWSER_IMAGE="101conv-browser-smoke:local"
mkdir -p "$DATA_PATH/imports" "$DATA_PATH/engines" "$DATA_PATH/uploads" "$DATA_PATH/results" "$BUILDX_CONFIG" "$tmp_root/state/uploads" "$tmp_root/state/results"
chmod 0777 "$DATA_PATH" "$DATA_PATH/imports" "$DATA_PATH/engines" "$DATA_PATH/uploads" "$DATA_PATH/results"
chmod 0700 "$tmp_root/state/uploads" "$tmp_root/state/results"

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" logs --no-color --tail 80 || true
  fi
  docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker run --rm --user 65532:65532 -v "$DATA_PATH:/cleanup" --entrypoint /bin/sh postgres:17-alpine -c 'find /cleanup -mindepth 1 -delete' >/dev/null 2>&1 || true
  docker image rm -f "$SMOKE_IMAGE" "$WEB_IMAGE" "$BROWSER_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$tmp_root"
  exit "$status"
}
trap cleanup EXIT

proxy_cert_mount=()
if [[ -n "${CODEX_PROXY_CERT:-}" && -r "$CODEX_PROXY_CERT" ]]; then
  proxy_cert_mount=(-v "$CODEX_PROXY_CERT:/tmp/proxy-ca.pem:ro")
fi
docker run --rm --user "$(id -u):$(id -g)" -e GOMODCACHE=/go/pkg/mod -e GOCACHE=/tmp/go-cache -v /tmp/101conv-gomod-cache:/go/pkg/mod -v "$repo_root/server:/src" -v "$tmp_root:/out" "${proxy_cert_mount[@]}" -w /src golang:1.27.2-alpine sh -c '
  if [ -f /tmp/proxy-ca.pem ]; then cat /etc/ssl/certs/ca-certificates.crt /tmp/proxy-ca.pem >/tmp/trust.pem; export SSL_CERT_FILE=/tmp/trust.pem; fi
  CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/101conv ./cmd/101conv
  CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/data/imports/ppm-pgm ./testdata/ppm-pgm
'
cat >"$tmp_root/Dockerfile" <<'DOCKER'
FROM scratch
COPY 101conv /101conv
COPY --chown=65532:65532 state /var/lib/101conv
WORKDIR /var/lib/101conv
USER 65532:65532
ENTRYPOINT ["/101conv"]
DOCKER
docker build -t "$SMOKE_IMAGE" "$tmp_root"
if [[ -n "${CODEX_PROXY_CERT:-}" && -r "$CODEX_PROXY_CERT" ]]; then
  docker build --secret id=proxy_ca,src="$CODEX_PROXY_CERT" -t "$WEB_IMAGE" "$repo_root/web"
else
  docker build -t "$WEB_IMAGE" "$repo_root/web"
fi
cat >"$tmp_root/compose.override.yaml" <<YAML
services:
  core:
    build: null
    image: ${SMOKE_IMAGE}
  worker:
    build: null
    image: ${SMOKE_IMAGE}
  web:
    build: null
    image: ${WEB_IMAGE}
YAML

docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" up -d
core_container="$(docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" ps -q core)"
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$core_container" 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$core_container")" == "healthy" ]]
docker build -f "$repo_root/web/Dockerfile.e2e" -t "$BROWSER_IMAGE" "$repo_root/web"
docker run --rm --shm-size=1g --network "${COMPOSE_PROJECT_NAME}_web-core" -e SMOKE_BASE=http://web:8080 -e ENGINE_ADMIN_TOKEN -e CHROMIUM_PATH=/usr/bin/chromium -e NO_PROXY=web,core,localhost,127.0.0.1 -e no_proxy=web,core,localhost,127.0.0.1 -v "$repo_root/web/node_modules:/app/node_modules:ro" -v "$repo_root/web/smoke.cjs:/app/smoke.cjs:ro" -w /app "$BROWSER_IMAGE" node smoke.cjs
printf 'Phase 5 browser conversion smoke test passed.\n'
