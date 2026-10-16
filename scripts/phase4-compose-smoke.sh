#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_root="$(mktemp -d)"
export BUILDX_CONFIG="$tmp_root/buildx"
export COMPOSE_PROJECT_NAME="101conv-phase4-smoke-$$"
export SMOKE_IMAGE="101conv-phase4-smoke:$$"
export DATA_PATH="$tmp_root/data"
export POSTGRES_PASSWORD="phase4-smoke-$(date +%s)-$RANDOM"
export ENGINE_ADMIN_TOKEN="phase4-admin-$(date +%s)-$RANDOM"
export PORT="${SMOKE_PORT:-15656}"
export JOB_LEASE=3s
export CONVERSION_TIMEOUT=8s
export MAX_OUTPUT_BYTES=65536
mkdir -p "$DATA_PATH/imports"
mkdir -p "$BUILDX_CONFIG"
chmod 755 "$tmp_root" "$DATA_PATH/imports"
chmod 0777 "$DATA_PATH"

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" logs --no-color || true
  fi
  docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  if [[ -d "$DATA_PATH" ]]; then
    docker run --rm --user 0:0 --volume "$DATA_PATH:/cleanup" --entrypoint /bin/sh postgres:17-alpine -c 'find /cleanup -mindepth 1 -delete' >/dev/null 2>&1 || true
  fi
  docker image rm -f "$SMOKE_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$tmp_root"
  exit "$status"
}
trap cleanup EXIT

go_bin="${GO_BIN:-$(command -v go || true)}"
if [[ -z "$go_bin" ]]; then
  printf 'go toolchain not found; set GO_BIN to the Go 1.27 executable\n' >&2
  exit 1
fi
(cd "$repo_root/server" && GOTOOLCHAIN=local GOCACHE="${GOCACHE:-$tmp_root/go-cache}" GOPATH="${GOPATH:-$tmp_root/go-path}" "$go_bin" build -o "$DATA_PATH/imports/ppm-pgm" ./testdata/ppm-pgm)
chmod 755 "$DATA_PATH/imports/ppm-pgm"
(cd "$repo_root/server" && GOTOOLCHAIN=local GOCACHE="${GOCACHE:-$tmp_root/go-cache}" GOPATH="${GOPATH:-$tmp_root/go-path}" CGO_ENABLED=0 GOOS=linux "$go_bin" build -trimpath -o "$tmp_root/101conv" ./cmd/101conv)
chmod 0755 "$tmp_root/101conv"
cat >"$tmp_root/Dockerfile" <<'DOCKER'
FROM scratch
COPY 101conv /101conv
COPY --chown=65532:65532 state /var/lib/101conv
WORKDIR /var/lib/101conv
USER 65532:65532
ENTRYPOINT ["/101conv"]
DOCKER
mkdir -p "$tmp_root/state/uploads" "$tmp_root/state/results"
chmod 0700 "$tmp_root/state/uploads" "$tmp_root/state/results"
docker build -t "$SMOKE_IMAGE" "$tmp_root"
cat >"$tmp_root/compose.override.yaml" <<'YAML'
services:
  core:
    build: null
    image: ${SMOKE_IMAGE}
  worker:
    build: null
    image: ${SMOKE_IMAGE}
YAML
docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" up -d
worker_container="$(docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" ps -q worker)"
docker inspect "$worker_container" | python3 -c 'import json,sys; c=json.load(sys.stdin)[0]; h=c["HostConfig"]; n=c["NetworkSettings"]["Networks"]; m={x["Destination"]:x for x in c["Mounts"]}; assert h["ReadonlyRootfs"] is True; assert "ALL" in h["CapDrop"]; assert any("no-new-privileges" in x for x in h["SecurityOpt"]); assert h["PidsLimit"] <= 64; assert 0 < h["Memory"] <= 1073741824; assert 0 < h["NanoCpus"] <= 1000000000; assert len(n)==1 and next(iter(n)).endswith("_worker-db"); assert m["/var/lib/101conv/engines"]["RW"] is False; assert m["/var/lib/101conv/uploads"]["RW"] is False; assert m["/var/lib/101conv/results"]["RW"] is True'

base="http://127.0.0.1:$PORT"
auth=(-H "Authorization: Bearer $ENGINE_ADMIN_TOKEN")
submit_profile() {
  profile_id="$1"
  source_file="$2"
  key="$3"
  upload_json="$(curl -fsS -X POST "${auth[@]}" -F "file=@$source_file" "$base/api/v1/uploads")"
  upload_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$upload_json")"
  request_file="$tmp_root/request-$key.json"
  cat >"$request_file" <<JSON
{"uploadId":"$upload_id","profileId":"$profile_id","targetFormat":"pgm","idempotencyKey":"$key"}
JSON
  response="$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$request_file" "$base/api/v1/jobs")"
  python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["id"])' <<<"$response"
}
unauthorized="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -F 'file=@/dev/null;filename=auth.ppm' "$base/api/v1/uploads")"
[[ "$unauthorized" == 401 ]]
for _ in $(seq 1 60); do
  if curl -fsS "$base/readyz" >/dev/null; then break; fi
  sleep 2
done
curl -fsS "$base/readyz" >/dev/null

cat >"$tmp_root/engine.json" <<'JSON'
{"definition":{"schemaVersion":1,"id":"ppm-pgm","version":"1.0.0","name":"PPM to PGM test converter","platform":"linux-amd64","capabilities":["image.ppm.decode","image.pgm.encode"],"executables":{"ppm-pgm":"ppm-pgm"}},"sourceDirectory":"/var/lib/101conv/imports"}
JSON
curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/engine.json" "$base/api/v1/engines/install" >/dev/null

cat >"$tmp_root/profile.json" <<'JSON'
{"definition":{"schemaVersion":1,"id":"ppm-pgm-grayscale","version":"1.0.0","name":"PPM to grayscale PGM","engines":[{"engineId":"ppm-pgm","versions":{"minimum":"1.0.0","maximumExclusive":"2.0.0"},"capabilities":["image.ppm.decode","image.pgm.encode"]}],"rules":[{"engineId":"ppm-pgm","executable":"ppm-pgm","sources":["ppm"],"targets":["pgm"],"arguments":["--input","{source}","--output","{target}"]}]}}
JSON
curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/profile.json" "$base/api/v1/profiles" >/dev/null

cat >"$tmp_root/slow-profile.json" <<'JSON'
{"definition":{"schemaVersion":1,"id":"ppm-pgm-slow","version":"1.0.0","name":"Delayed PPM test profile","engines":[{"engineId":"ppm-pgm","versions":{"minimum":"1.0.0","maximumExclusive":"2.0.0"},"capabilities":["image.ppm.decode","image.pgm.encode"]}],"rules":[{"engineId":"ppm-pgm","executable":"ppm-pgm","sources":["ppm"],"targets":["pgm"],"arguments":["--sleep-ms","15000","--input","{source}","--output","{target}"]}]}}
JSON
curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/slow-profile.json" "$base/api/v1/profiles" >/dev/null
cat >"$tmp_root/large-profile.json" <<'JSON'
{"definition":{"schemaVersion":1,"id":"ppm-pgm-large-output","version":"1.0.0","name":"Large-output limit test profile","engines":[{"engineId":"ppm-pgm","versions":{"minimum":"1.0.0","maximumExclusive":"2.0.0"},"capabilities":["image.ppm.decode","image.pgm.encode"]}],"rules":[{"engineId":"ppm-pgm","executable":"ppm-pgm","sources":["ppm"],"targets":["pgm"],"arguments":["--output-bytes","1000000","--input","{source}","--output","{target}"]}]}}
JSON
curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/large-profile.json" "$base/api/v1/profiles" >/dev/null
cat >"$tmp_root/crash-profile.json" <<'JSON'
{"definition":{"schemaVersion":1,"id":"ppm-pgm-recovery","version":"1.0.0","name":"Worker recovery test profile","engines":[{"engineId":"ppm-pgm","versions":{"minimum":"1.0.0","maximumExclusive":"2.0.0"},"capabilities":["image.ppm.decode","image.pgm.encode"]}],"rules":[{"engineId":"ppm-pgm","executable":"ppm-pgm","sources":["ppm"],"targets":["pgm"],"arguments":["--sleep-ms","5000","--input","{source}","--output","{target}"]}]}}
JSON
curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/crash-profile.json" "$base/api/v1/profiles" >/dev/null
curl -fsS "${auth[@]}" "$base/api/v1/engines" | python3 -c 'import json,sys; assert len(json.load(sys.stdin)["engines"]) == 1'

printf 'P3\n2 1\n255\n255 0 0 0 255 0\n' >"$tmp_root/source.ppm"
upload="$(curl -fsS -X POST "${auth[@]}" -F "file=@$tmp_root/source.ppm;type=image/x-portable-pixmap" "$base/api/v1/uploads")"
upload_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$upload")"
cat >"$tmp_root/job.json" <<JSON
{"uploadId":"$upload_id","profileId":"ppm-pgm-grayscale","targetFormat":"pgm","idempotencyKey":"phase4-smoke-$(date +%s)-$RANDOM"}
JSON
submitted="$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/job.json" "$base/api/v1/jobs")"
job_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["id"])' <<<"$submitted")"
duplicate="$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/job.json" "$base/api/v1/jobs")"
python3 -c 'import json,sys; x=json.load(sys.stdin); assert x["duplicate"] is True and x["job"]["id"] == sys.argv[1]' "$job_id" <<<"$duplicate"

for _ in $(seq 1 60); do
  detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$job_id")"
  state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$detail")"
  case "$state" in
    succeeded) break ;;
    failed|cancelled) printf '%s\n' "$detail" >&2; exit 1 ;;
  esac
  sleep 1
done
if [[ "$state" != succeeded ]]; then
  printf 'conversion did not finish: %s\n' "$detail" >&2
  exit 1
fi
curl -fsS "${auth[@]}" "$base/api/v1/jobs/$job_id/result" -o "$tmp_root/result.pgm"
grep -q '^P2$' "$tmp_root/result.pgm"
grep -q '^2 1$' "$tmp_root/result.pgm"
grep -q '76 150' "$tmp_root/result.pgm"

# Unsupported mappings are rejected before job creation, and malformed PPM is
# reported as a converter/input failure instead of producing a false artifact.
unsupported_upload="$(curl -fsS -X POST "${auth[@]}" -F "file=@$tmp_root/source.ppm" "$base/api/v1/uploads")"
unsupported_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$unsupported_upload")"
cat >"$tmp_root/unsupported.json" <<JSON
{"uploadId":"$unsupported_id","profileId":"ppm-pgm-grayscale","targetFormat":"png","idempotencyKey":"phase4-unsupported-$(date +%s)-$RANDOM"}
JSON
unsupported_status="$(curl -sS -o "$tmp_root/unsupported-response.json" -w '%{http_code}' -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/unsupported.json" "$base/api/v1/jobs")"
[[ "$unsupported_status" == 422 ]]
printf 'P3\n1 1\n255\ninvalid pixel\n' >"$tmp_root/invalid.ppm"
invalid_upload="$(curl -fsS -X POST "${auth[@]}" -F "file=@$tmp_root/invalid.ppm" "$base/api/v1/uploads")"
invalid_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$invalid_upload")"
cat >"$tmp_root/invalid.json" <<JSON
{"uploadId":"$invalid_id","profileId":"ppm-pgm-grayscale","targetFormat":"pgm","idempotencyKey":"phase4-invalid-$(date +%s)-$RANDOM"}
JSON
invalid_job="$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data-binary "@$tmp_root/invalid.json" "$base/api/v1/jobs")"
invalid_job_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["id"])' <<<"$invalid_job")"
for _ in $(seq 1 60); do
  invalid_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$invalid_job_id")"
  invalid_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$invalid_detail")"
  [[ "$invalid_state" == failed ]] && break
  sleep 1
done
[[ "$invalid_state" == failed ]]

cancel_id="$(submit_profile ppm-pgm-slow "$tmp_root/source.ppm" "phase4-cancel-$(date +%s)-$RANDOM")"
for _ in $(seq 1 30); do
  cancel_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$cancel_id")"
  cancel_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$cancel_detail")"
  [[ "$cancel_state" == running ]] && break
  sleep 1
done
[[ "$cancel_state" == running ]]
curl -fsS -X POST "${auth[@]}" "$base/api/v1/jobs/$cancel_id/cancel" >/dev/null
for _ in $(seq 1 20); do
  cancel_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$cancel_id")"
  cancel_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$cancel_detail")"
  [[ "$cancel_state" == cancelled ]] && break
  sleep 1
done
[[ "$cancel_state" == cancelled ]]

timeout_id="$(submit_profile ppm-pgm-slow "$tmp_root/source.ppm" "phase4-timeout-$(date +%s)-$RANDOM")"
for _ in $(seq 1 30); do
  timeout_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$timeout_id")"
  timeout_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$timeout_detail")"
  [[ "$timeout_state" == failed ]] && break
  sleep 1
done
[[ "$timeout_state" == failed ]]

large_id="$(submit_profile ppm-pgm-large-output "$tmp_root/source.ppm" "phase4-large-$(date +%s)-$RANDOM")"
for _ in $(seq 1 30); do
  large_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$large_id")"
  large_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$large_detail")"
  [[ "$large_state" == failed ]] && break
  sleep 1
done
[[ "$large_state" == failed ]]

recovery_id="$(submit_profile ppm-pgm-recovery "$tmp_root/source.ppm" "phase4-recovery-$(date +%s)-$RANDOM")"
for _ in $(seq 1 30); do
  recovery_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$recovery_id")"
  recovery_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$recovery_detail")"
  [[ "$recovery_state" == running ]] && break
  sleep 1
done
[[ "$recovery_state" == running ]]
worker_container="$(docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" ps -q worker)"
docker kill "$worker_container" >/dev/null
docker compose -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.go.yaml" -f "$tmp_root/compose.override.yaml" up -d worker >/dev/null
for _ in $(seq 1 30); do
  recovery_detail="$(curl -fsS "${auth[@]}" "$base/api/v1/jobs/$recovery_id")"
  recovery_state="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["state"])' <<<"$recovery_detail")"
  recovery_attempt="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["job"]["attempt"])' <<<"$recovery_detail")"
  [[ "$recovery_state" == succeeded ]] && break
  [[ "$recovery_state" == failed ]] && { printf '%s\n' "$recovery_detail" >&2; exit 1; }
  sleep 1
done
[[ "$recovery_state" == succeeded && "$recovery_attempt" -ge 2 ]]
printf 'Phase 4 Compose integration passed: conversion, invalid/unsupported input, auth, idempotency, cancellation, timeout, output limit, worker crash recovery; success job %s\n' "$job_id"
