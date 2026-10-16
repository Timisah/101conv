#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_root="$(mktemp -d)"
export COMPOSE_PROJECT_NAME="101conv-phase5-smoke-$$"
export SMOKE_IMAGE="101conv-phase5-smoke:$$"
export WEB_IMAGE="101conv-web:phase5-smoke-$$"
export BUILDX_CONFIG="$tmp_root/buildx"
export DATA_PATH="$tmp_root/data"
export GO_DATA_PATH="$tmp_root/go-data"
export POSTGRES_PASSWORD="phase5-smoke-$(date +%s)-$RANDOM"
export ENGINE_ADMIN_TOKEN="phase5-admin-$(date +%s)-$RANDOM"
port_num="${SMOKE_PORT:-15656}"
export PORT="127.0.0.1:$port_num"
export SMOKE_BASE="http://127.0.0.1:$port_num"
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
export BROWSER_IMAGE="101conv-browser-smoke:$$"
mkdir -p "$GO_DATA_PATH/imports" "$GO_DATA_PATH/engines" "$BUILDX_CONFIG" "$tmp_root/state/uploads" "$tmp_root/state/results"
chmod 0777 "$GO_DATA_PATH" "$GO_DATA_PATH/imports" "$GO_DATA_PATH/engines"
chmod 0700 "$tmp_root/state/uploads" "$tmp_root/state/results"
mkdir -p "$DATA_PATH"

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" logs --no-color --tail 80 || true
  fi
  docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  if [[ -d "$GO_DATA_PATH" ]]; then
    docker run --rm --user 65532:65532 --volume "$GO_DATA_PATH:/cleanup" --entrypoint /bin/sh postgres:17-alpine -c 'find /cleanup -mindepth 1 -delete' >/dev/null 2>&1 || true
  fi
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
  CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/go-data/imports/ppm-pgm ./testdata/ppm-pgm
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

docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" up -d
core_container="$(docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" ps -q core)"
web_container="$(docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" ps -q web)"
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$core_container" 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$core_container")" == "healthy" ]]
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$web_container" 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$web_container")" == "healthy" ]]
docker inspect "$web_container" | python3 -c 'import json,sys; c=json.load(sys.stdin)[0]; p=c["HostConfig"]["PortBindings"]["5656/tcp"]; assert p and p[0]["HostPort"] == sys.argv[1], p' "$port_num"
docker build -f "$repo_root/web/Dockerfile.e2e" -t "$BROWSER_IMAGE" "$repo_root/web"
docker run --rm --network "${COMPOSE_PROJECT_NAME}_web-core" "$BROWSER_IMAGE" node - <<'NODE'
const assert = require('node:assert/strict');
(async () => {
  const base = 'http://web:5656';
  const response = await fetch(base + '/');
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('cache-control'), 'no-cache, must-revalidate');
  const html = await response.text();
  const asset = html.match(/(?:src|href)="([^"]+\/assets\/[^"]+)"/);
  assert.ok(asset, 'HTML references a fingerprinted asset');
  const assetResponse = await fetch(new URL(asset[1], base));
  assert.equal(assetResponse.status, 200);
  assert.equal(assetResponse.headers.get('cache-control'), 'public, max-age=31536000, immutable');
})().catch(error => { console.error(error); process.exit(1); });
NODE
docker run --rm --shm-size=1g --network "${COMPOSE_PROJECT_NAME}_web-core" -e SMOKE_BASE=http://web:5656 -e ENGINE_ADMIN_TOKEN -e CHROMIUM_PATH=/usr/bin/chromium -e NO_PROXY=web,core,localhost,127.0.0.1 -e no_proxy=web,core,localhost,127.0.0.1 -v "$repo_root/web/node_modules:/app/node_modules:ro" -v "$repo_root/web/smoke.cjs:/app/smoke.cjs:ro" -w /app "$BROWSER_IMAGE" node smoke.cjs
docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" restart postgres core worker
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$core_container" 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$core_container")" == "healthy" ]]
docker run --rm --network "${COMPOSE_PROJECT_NAME}_web-core" -e ENGINE_ADMIN_TOKEN "$BROWSER_IMAGE" node - <<'NODE'
const assert = require('node:assert/strict');
(async () => {
  const base = 'http://web:5656';
  const headers = { Authorization: 'Bearer ' + process.env.ENGINE_ADMIN_TOKEN };
  const response = await fetch(base + '/api/v1/jobs?limit=100', { headers });
  assert.equal(response.status, 200);
  const data = await response.json();
  assert.ok(data.total >= 1);
  const job = data.jobs.find(item => item.state === 'succeeded');
  assert.ok(job, 'completed job persisted across restart');
  const result = await fetch(base + '/api/v1/jobs/' + job.id + '/result', { headers });
  assert.equal(result.status, 200);
  assert.match(await result.text(), /^P2\n/);
})().catch(error => { console.error(error); process.exit(1); });
NODE
printf 'Go/React browser conversion smoke test passed.\n'
