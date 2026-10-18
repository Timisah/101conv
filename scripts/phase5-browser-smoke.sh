#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
required_gib="${SMOKE_MIN_FREE_GIB:-8}"
if [[ ! "$required_gib" =~ ^[1-9][0-9]*$ || "$required_gib" -lt 8 ]]; then
  echo 'Browser/container verification requires at least 8 GiB free; SMOKE_MIN_FREE_GIB may raise but cannot lower that safety threshold.' >&2
  exit 2
fi
required_kib=$((required_gib * 1024 * 1024))
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo 'Browser/container verification not started: a reachable Docker daemon is required. No containers, volumes, or images were changed.' >&2
  exit 2
fi
if ! command -v openssl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo 'Browser/container verification not started: Docker Compose v2 is required. No test resources were created.' >&2
  exit 2
fi
docker_root="$(docker info --format '{{.DockerRootDir}}')"
docker_context="${DOCKER_CONTEXT:-$(docker context show 2>/dev/null || echo default)}"
if [[ -n "${DOCKER_CONTEXT:-}" ]]; then
  docker_endpoint="$(docker context inspect "$DOCKER_CONTEXT" --format '{{(index .Endpoints "docker").Host}}' 2>/dev/null || true)"
elif [[ -n "${DOCKER_HOST:-}" ]]; then
  docker_endpoint="$DOCKER_HOST"
else
  docker_endpoint="$(docker context inspect "$docker_context" --format '{{(index .Endpoints "docker").Host}}' 2>/dev/null || true)"
fi
local_docker="false"
case "$docker_endpoint" in unix://*|npipe://*) local_docker="true" ;; esac
required_paths=("$repo_root" "${TMPDIR:-/tmp}")
if [[ -n "${SMOKE_EXPORT_RESTORE_DIR:-}" ]]; then
  artifact_parent="$(dirname "$SMOKE_EXPORT_RESTORE_DIR")"
  while [[ ! -d "$artifact_parent" && "$artifact_parent" != / ]]; do artifact_parent="$(dirname "$artifact_parent")"; done
  required_paths+=("$artifact_parent")
fi
for required_path in "${required_paths[@]}"; do
  available_kib="$(df -Pk "$required_path" | awk 'NR==2 {print $4}')"
  if [[ -z "$available_kib" || "$available_kib" -lt "$required_kib" ]]; then
    echo "Browser/container verification not started: $required_path has ${available_kib:-unknown} KiB free; at least ${required_kib} KiB (${required_gib} GiB) is required for isolated PostgreSQL, Core/Worker, web, and browser images. Free space on this filesystem; the minimum may not be lowered. No test containers, volumes, or images were changed." >&2
    exit 2
  fi
done
available_mem_kib="$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)"
if [[ -r /sys/fs/cgroup/memory.max && -r /sys/fs/cgroup/memory.current ]]; then
  cgroup_limit="$(cat /sys/fs/cgroup/memory.max)"
  cgroup_used="$(cat /sys/fs/cgroup/memory.current)"
  if [[ "$cgroup_limit" =~ ^[0-9]+$ ]]; then
    cgroup_available_kib=$(((cgroup_limit - cgroup_used) / 1024))
    (( cgroup_available_kib < available_mem_kib )) && available_mem_kib="$cgroup_available_kib"
  fi
fi
if [[ "$local_docker" == "true" ]]; then
  docker_disk_kib="$(df -Pk "$docker_root" 2>/dev/null | awk 'NR==2 {print $4}' || true)"
  if [[ -z "$docker_disk_kib" || "$docker_disk_kib" -lt "$required_kib" ]]; then
    echo "Browser/container verification not started: Docker data path $docker_root has ${docker_disk_kib:-unknown} KiB free; at least ${required_kib} KiB (${required_gib} GiB) is required. No test resources were created." >&2
    exit 2
  fi
else
  docker_disk_gib="${SMOKE_DOCKER_FREE_GIB:-}"
  if [[ ! "$docker_disk_gib" =~ ^[0-9]+$ || "$docker_disk_gib" -lt "$required_gib" ]]; then
    echo "Browser/container verification not started: Docker context '$docker_context' is remote ($docker_endpoint), so its free space cannot be measured from this host. Set SMOKE_DOCKER_FREE_GIB to the independently measured free space on the daemon host (at least $required_gib GiB); this reports capacity and does not lower the threshold. No test resources were created." >&2
    exit 2
  fi
fi
if [[ "$local_docker" != "true" ]]; then
  docker_mem_mib="${SMOKE_DOCKER_AVAILABLE_MEM_MIB:-}"
  if [[ ! "$docker_mem_mib" =~ ^[0-9]+$ || "$docker_mem_mib" -lt 2048 ]]; then
    echo "Browser/container verification not started: Docker context '$docker_context' is remote, so daemon free memory cannot be measured here. Set SMOKE_DOCKER_AVAILABLE_MEM_MIB to independently measured available memory (at least 2048 MiB). No test resources were created." >&2
    exit 2
  fi
elif [[ -n "$available_mem_kib" && "$available_mem_kib" -lt 2097152 ]]; then
  echo "Browser/container verification not started: only ${available_mem_kib} KiB memory is available; at least 2097152 KiB is required for Chromium and the conversion Worker. No test resources were created." >&2
  exit 2
fi
if [[ "$local_docker" == "true" ]]; then
  docker_mem_bytes="$(docker info --format '{{.MemTotal}}' 2>/dev/null || true)"
  if [[ ! "$docker_mem_bytes" =~ ^[0-9]+$ || "$docker_mem_bytes" -lt 2147483648 ]]; then
    echo "Browser/container verification not started: Docker daemon reports ${docker_mem_bytes:-unknown} bytes total memory; at least 2147483648 bytes (2 GiB) is required. No test resources were created." >&2
    exit 2
  fi
fi
if [[ "$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)" -lt 2 ]]; then
  echo 'Browser/container verification warning: fewer than two CPUs are visible; the suite may be very slow.' >&2
fi
echo "Browser/container preflight passed: ${required_gib} GiB free-space threshold and required Docker services are available."
echo "Docker context: $docker_context; daemon storage: $docker_root"
df -h "$repo_root"
df -h "${TMPDIR:-/tmp}"
if [[ "$local_docker" == "true" ]]; then df -h "$docker_root"; else echo "Remote Docker disk capacity (operator measured): ${docker_disk_gib} GiB free"; fi
echo "Available memory: $((available_mem_kib / 1024)) MiB; CPUs: $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)."
docker system df
run_id="${SMOKE_RUN_ID:-$(openssl rand -hex 8)}"
run_id="${run_id//[^a-zA-Z0-9_.-]/-}"
tmp_root="$(mktemp -d)"
export COMPOSE_PROJECT_NAME="101conv-phase5-smoke-$run_id"
export SMOKE_IMAGE="101conv-phase5-smoke:$run_id"
export WEB_IMAGE="101conv-web:phase5-smoke-$run_id"
export DATA_PATH="$tmp_root/data"
export GO_DATA_PATH="$tmp_root/go-data"
export POSTGRES_PASSWORD="phase5-smoke-$(date +%s)-$RANDOM"
export POSTGRES_USER=101conv
export POSTGRES_DB=101conv
export ENGINE_ADMIN_TOKEN="phase5-admin-$(date +%s)-$RANDOM"
port_num="${SMOKE_PORT:-15656}"
export PORT="127.0.0.1:$port_num"
export SMOKE_BASE="http://127.0.0.1:$port_num"
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
export BROWSER_IMAGE="101conv-browser-smoke:$run_id"
export BROWSER_CONTAINER="${COMPOSE_PROJECT_NAME}-browser"
export CLEANUP_BROWSER_CONTAINER="${COMPOSE_PROJECT_NAME}-cleanup-browser"
export CRASH_CHECK_CONTAINER="${COMPOSE_PROJECT_NAME}-worker-crash-check"
export CRASH_WAIT_CONTAINER="${COMPOSE_PROJECT_NAME}-worker-crash-wait"
export SMOKE_OUTPUT_DIR=/smoke-output
if [[ -n "${SMOKE_ARTIFACT_DIR:-}" ]]; then mkdir -p "$SMOKE_ARTIFACT_DIR"; fi
cat > "$tmp_root/compose.env" <<ENV
DATA_PATH=$DATA_PATH
GO_DATA_PATH=$GO_DATA_PATH
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
POSTGRES_USER=$POSTGRES_USER
POSTGRES_DB=$POSTGRES_DB
ENGINE_ADMIN_TOKEN=$ENGINE_ADMIN_TOKEN
PORT=$PORT
ENV
chmod 0600 "$tmp_root/compose.env"
compose() {
  docker compose --env-file "$tmp_root/compose.env" -p "$COMPOSE_PROJECT_NAME" -f "$repo_root/compose.yaml" -f "$tmp_root/compose.override.yaml" "$@"
}
mkdir -p "$tmp_root/smoke-output"
if [[ -n "${SMOKE_EXPORT_RESTORE_DIR:-}" ]]; then
  mkdir -p "$SMOKE_EXPORT_RESTORE_DIR"
  if [[ -n "$(find "$SMOKE_EXPORT_RESTORE_DIR" -mindepth 1 -print -quit)" ]]; then
    echo "Restore artifact directory must be empty: $SMOKE_EXPORT_RESTORE_DIR" >&2
    exit 2
  fi
fi

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    if docker inspect "$BROWSER_CONTAINER" >/dev/null 2>&1; then
      docker cp "$BROWSER_CONTAINER:/smoke-output/." "$tmp_root/smoke-output/" >/dev/null 2>&1 || true
      docker logs "$BROWSER_CONTAINER" > "$tmp_root/browser.log" 2>&1 || true
    fi
    if docker inspect "$CLEANUP_BROWSER_CONTAINER" >/dev/null 2>&1; then
      docker logs "$CLEANUP_BROWSER_CONTAINER" > "$tmp_root/cleanup-browser.log" 2>&1 || true
    fi
    compose logs --no-color --tail 200 > "$tmp_root/compose.log" 2>&1 || true
    redact_file "$tmp_root/compose.log" || true
    if [[ -f "$tmp_root/browser.log" ]]; then redact_file "$tmp_root/browser.log" || true; fi
    if [[ -f "$tmp_root/cleanup-browser.log" ]]; then redact_file "$tmp_root/cleanup-browser.log" || true; fi
    if [[ -f "$tmp_root/worker-crash-recovery.log" ]]; then redact_file "$tmp_root/worker-crash-recovery.log" || true; fi
    if [[ -n "${SMOKE_ARTIFACT_DIR:-}" ]]; then
      mkdir -p "$SMOKE_ARTIFACT_DIR"
      cp -a "$tmp_root/smoke-output" "$SMOKE_ARTIFACT_DIR/" 2>/dev/null || true
      cp -a "$tmp_root/compose.log" "$SMOKE_ARTIFACT_DIR/" 2>/dev/null || true
      cp -a "$tmp_root/browser.log" "$SMOKE_ARTIFACT_DIR/" 2>/dev/null || true
      cp -a "$tmp_root/cleanup-browser.log" "$SMOKE_ARTIFACT_DIR/" 2>/dev/null || true
      cp -a "$tmp_root/worker-crash-recovery.log" "$SMOKE_ARTIFACT_DIR/" 2>/dev/null || true
      printf 'exit_status=%s\nrun_id=%s\n' "$status" "$run_id" > "$SMOKE_ARTIFACT_DIR/run.txt"
    fi
    cat "$tmp_root/compose.log" >&2 || true
  fi
  docker rm -f "$BROWSER_CONTAINER" "$CLEANUP_BROWSER_CONTAINER" >/dev/null 2>&1 || true
  docker rm -f "$CRASH_CHECK_CONTAINER" "$CRASH_WAIT_CONTAINER" >/dev/null 2>&1 || true
  running_ids="$(compose ps -q 2>/dev/null || true)"
  if [[ -n "$running_ids" ]]; then
    echo 'Container resource snapshot before cleanup:'
    docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.PIDs}}' $running_ids || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm -f "$SMOKE_IMAGE" "$WEB_IMAGE" "$BROWSER_IMAGE" >/dev/null 2>&1 || true
  if [[ "$status" -ne 0 && "${SMOKE_KEEP_FAILED:-0}" == 1 ]]; then
    echo "Failed smoke test files retained at $tmp_root" >&2
  else
    rm -rf "$tmp_root"
  fi
  df -h "$repo_root"
  if [[ "$local_docker" == "true" ]]; then df -h "$docker_root"; fi
  memory_after_kib="$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)"
  echo "Available memory after cleanup: $((memory_after_kib / 1024)) MiB."
  docker system df || true
  exit "$status"
}
trap cleanup EXIT

redact_file() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  SMOKE_REDACT_TOKEN="$ENGINE_ADMIN_TOKEN" SMOKE_REDACT_PASSWORD="$POSTGRES_PASSWORD" python3 - "$file" <<'PY'
import os,re,sys
from pathlib import Path
p=Path(sys.argv[1]); s=p.read_text(errors='replace')
for secret in (os.environ.get('SMOKE_REDACT_TOKEN',''),os.environ.get('SMOKE_REDACT_PASSWORD',''),'smoke-test-password-123','smoke-user-password-123'):
    if secret: s=s.replace(secret,'[REDACTED]')
s=re.sub(r'(?i)(authorization[\s:=\"\']+)(?:bearer\s+)?[^\s,\"\']+',r'\1[REDACTED]',s)
s=re.sub(r'(?i)((?:password|token|api[_-]?key)[\s:=\"\']+)[^\s,\"\']+',r'\1[REDACTED]',s)
p.write_text(s)
PY
}

proxy_cert_mount=()
if [[ -n "${CODEX_PROXY_CERT:-}" && -r "$CODEX_PROXY_CERT" ]]; then
  proxy_cert_mount=(--secret "id=proxy_ca,src=$CODEX_PROXY_CERT")
fi
proxy_build_args=()
for proxy_variable in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  if [[ -n "${!proxy_variable:-}" ]]; then proxy_build_args+=(--build-arg "$proxy_variable=${!proxy_variable}"); fi
done
docker build "${proxy_cert_mount[@]}" "${proxy_build_args[@]}" -f "$repo_root/server/Dockerfile.smoke" -t "$SMOKE_IMAGE" "$repo_root/server"
if [[ -n "${CODEX_PROXY_CERT:-}" && -r "$CODEX_PROXY_CERT" ]]; then
  docker build --secret id=proxy_ca,src="$CODEX_PROXY_CERT" "${proxy_build_args[@]}" -t "$WEB_IMAGE" "$repo_root/web"
else
  docker build "${proxy_build_args[@]}" -t "$WEB_IMAGE" "$repo_root/web"
fi
cat >"$tmp_root/compose.override.yaml" <<YAML
services:
  core:
    build: null
    image: ${SMOKE_IMAGE}
    volumes:
      - smoke-go-data:/var/lib/101conv
  worker:
    build: null
    image: ${SMOKE_IMAGE}
    environment:
      WORKER_CONCURRENCY: ${SMOKE_WORKER_CONCURRENCY:-3}
      WORKER_BATCH_CONCURRENCY: ${SMOKE_WORKER_BATCH_CONCURRENCY:-2}
      JOB_LEASE: 5s
    volumes: !override
      - smoke-go-data:/var/lib/101conv:ro
      - uploads:/var/lib/101conv/uploads:ro
      - results:/var/lib/101conv/results
  web:
    build: null
    image: ${WEB_IMAGE}
volumes:
  smoke-go-data:
YAML

compose up -d
core_container="$(compose ps -q core)"
web_container="$(compose ps -q web)"
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
docker run --rm --network none --volumes-from "$core_container:ro" --entrypoint df postgres:17-alpine -P -B1 /var/lib/101conv/uploads /var/lib/101conv/results
docker run --rm --network none --user 65532:65532 --volumes-from "$core_container" --entrypoint sh postgres:17-alpine -ec '
  for name in uploads results; do
    path="/var/lib/101conv/$name"
    test -d "$path" && test -w "$path" || { echo "Core cannot write $path as UID:GID 65532:65532" >&2; exit 1; }
    marker="$path/.101conv-smoke-write-check-$$"
    : > "$marker"
    rm "$marker"
  done
  test -d /var/lib/101conv/engines
  test -r /var/lib/101conv/imports/ppm-pgm
  echo "Core storage checks passed as UID:GID 65532:65532."
'
worker_container="$(compose ps -q worker)"
docker run --rm --network none --user 65532:65532 --volumes-from "$worker_container" --entrypoint sh postgres:17-alpine -ec '
  test -d /var/lib/101conv/engines
  test -r /var/lib/101conv/engines
  test -r /var/lib/101conv/imports/ppm-pgm
  if test -w /var/lib/101conv/engines; then
    echo "Worker engine directory must be read-only." >&2
    exit 1
  fi
  echo "Worker can read the shared engine tree and fixture; its engine tree is read-only."
'
docker inspect "$web_container" | python3 -c 'import json,sys; c=json.load(sys.stdin)[0]; p=c["HostConfig"]["PortBindings"]["5656/tcp"]; assert p and p[0]["HostPort"] == sys.argv[1], p' "$port_num"
docker build "${proxy_cert_mount[@]}" "${proxy_build_args[@]}" -f "$repo_root/web/Dockerfile.e2e" -t "$BROWSER_IMAGE" "$repo_root/web"
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
if docker run --name "$BROWSER_CONTAINER" --shm-size=1g --network "${COMPOSE_PROJECT_NAME}_web-core" \
  -e SMOKE_BASE=http://web:5656 -e SMOKE_OUTPUT_DIR=/smoke-output -e ENGINE_ADMIN_TOKEN \
  -e CHROMIUM_PATH=/usr/bin/chromium -e NO_PROXY=web,core,localhost,127.0.0.1 \
  -e no_proxy=web,core,localhost,127.0.0.1 "$BROWSER_IMAGE" node smoke.cjs >"$tmp_root/browser.log" 2>&1; then
  browser_status=0
else
  browser_status=$?
fi
if docker inspect "$BROWSER_CONTAINER" >/dev/null 2>&1; then
  docker cp "$BROWSER_CONTAINER:/smoke-output/." "$tmp_root/smoke-output/" >/dev/null 2>&1 || true
  docker rm "$BROWSER_CONTAINER" >/dev/null
fi
redact_file "$tmp_root/browser.log"
cat "$tmp_root/browser.log"
[[ "$browser_status" -eq 0 ]] || exit "$browser_status"
worker_crash_log="$tmp_root/worker-crash-recovery.log"
if docker run --name "$CRASH_CHECK_CONTAINER" --network "${COMPOSE_PROJECT_NAME}_web-core" \
  -e ENGINE_ADMIN_TOKEN -e NO_PROXY=web,localhost,127.0.0.1 -e no_proxy=web,localhost,127.0.0.1 "$BROWSER_IMAGE" node - <<'NODE' >"$worker_crash_log" 2>&1; then
const assert = require('node:assert/strict');
const base='http://web:5656';
const headers={Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN};
(async()=>{
 const form=new FormData();form.append('file',new Blob(['P3\n2 1\n255\n255 0 0 0 255 0\n']), 'worker-recovery.ppm');
 const uploadResponse=await fetch(base+'/api/v1/uploads',{method:'POST',headers,body:form});
 if(uploadResponse.status!==201)throw new Error('Upload failed: '+uploadResponse.status+' '+(await uploadResponse.text()));
 const upload=await uploadResponse.json();
 const submit=await fetch(base+'/api/v1/jobs',{method:'POST',headers:{...headers,'Content-Type':'application/json'},body:JSON.stringify({uploadId:upload.id,profileId:'ppm-pgm-slow',targetFormat:'pgm',idempotencyKey:'worker-crash-recovery-001'})});
 if(submit.status!==202)throw new Error('Job submission failed: '+submit.status+' '+(await submit.text()));
 let job=(await submit.json()).job;
 for(let i=0;i<30&&job.state!=='running';i++){await new Promise(resolve=>setTimeout(resolve,100));job=(await fetch(base+'/api/v1/jobs/'+job.id,{headers}).then(r=>r.json())).job;}
 assert.equal(job.state,'running','worker claimed the crash-recovery fixture');
 console.log(job.id);
})().catch(error=>{console.error(error);process.exit(1)});
NODE
  crash_job_id="$(tail -n 1 "$worker_crash_log")"
  [[ "$crash_job_id" =~ ^[0-9a-f-]{36}$ ]] || { echo 'Worker crash fixture did not provide a valid job ID.' >&2; exit 1; }
  worker_container="$(compose ps -q worker)"
  docker kill "$worker_container" >/dev/null
  compose up -d worker
  for _ in $(seq 1 60); do
    worker_health="$(docker inspect --format '{{.State.Health.Status}}' "$worker_container" 2>/dev/null || true)"
    [[ "$worker_health" == healthy ]] && break
    sleep 1
  done
  [[ "$worker_health" == healthy ]] || { compose logs --no-color --tail 80 worker >&2; echo 'Worker did not recover after the forced container crash.' >&2; exit 1; }
  if docker run --name "$CRASH_WAIT_CONTAINER" --network "${COMPOSE_PROJECT_NAME}_web-core" \
    -e ENGINE_ADMIN_TOKEN -e SMOKE_JOB_ID="$crash_job_id" "$BROWSER_IMAGE" node - <<'NODE' >>"$worker_crash_log" 2>&1; then
const assert=require('node:assert/strict');
const base='http://web:5656';const headers={Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN};
(async()=>{let job;for(let i=0;i<60;i++){const r=await fetch(base+'/api/v1/jobs/'+process.env.SMOKE_JOB_ID,{headers});assert.equal(r.status,200);job=(await r.json()).job;if(['succeeded','failed','cancelled'].includes(job.state))break;await new Promise(resolve=>setTimeout(resolve,1000));}assert.equal(job.state,'succeeded','expired lease was recovered after Worker restart: '+JSON.stringify(job));const result=await fetch(base+'/api/v1/jobs/'+job.id+'/result',{headers});assert.equal(result.status,200);assert.match(await result.text(),/^P2\n/);console.log('Worker recovered job after container crash and produced a valid result.');})().catch(error=>{console.error(error);process.exit(1)});
NODE
    docker rm "$CRASH_WAIT_CONTAINER" >/dev/null 2>&1 || true
  else
    docker rm "$CRASH_WAIT_CONTAINER" >/dev/null 2>&1 || true
    cat "$worker_crash_log" >&2
    exit 1
  fi
  redact_file "$worker_crash_log"
  cat "$worker_crash_log"
else
  cat "$worker_crash_log" >&2
  exit 1
fi
docker rm "$CRASH_CHECK_CONTAINER" >/dev/null 2>&1 || true
retention_job_id="$(cat "$tmp_root/smoke-output/retention-job-id")"
[[ "$retention_job_id" =~ ^[0-9a-f-]{36}$ ]] || { echo 'Browser smoke did not provide a valid result job ID.' >&2; exit 1; }
retention_update="$(printf "UPDATE conv_jobs SET updated_at=now()-interval '2 days' WHERE id='%s';\n" "$retention_job_id" | compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB")"
[[ "$retention_update" == *'UPDATE 1'* ]] || { echo 'Could not age the test result for retention cleanup verification.' >&2; exit 1; }
if docker run --name "$CLEANUP_BROWSER_CONTAINER" --shm-size=1g --network "${COMPOSE_PROJECT_NAME}_web-core" \
  -e SMOKE_BASE=http://web:5656 -e RETENTION_JOB_ID="$retention_job_id" -e ENGINE_ADMIN_TOKEN \
  -e CHROMIUM_PATH=/usr/bin/chromium -e NO_PROXY=web,core,localhost,127.0.0.1 \
  -e no_proxy=web,core,localhost,127.0.0.1 "$BROWSER_IMAGE" node cleanup-smoke.cjs >"$tmp_root/cleanup-browser.log" 2>&1; then
  cleanup_browser_status=0
else
  cleanup_browser_status=$?
fi
docker rm "$CLEANUP_BROWSER_CONTAINER" >/dev/null 2>&1 || true
redact_file "$tmp_root/cleanup-browser.log"
cat "$tmp_root/cleanup-browser.log"
[[ "$cleanup_browser_status" -eq 0 ]] || exit "$cleanup_browser_status"
if [[ "${SMOKE_BACKUP_TEST:-0}" == 1 ]]; then
  BACKUP_OMIT_ENV=1 "$repo_root/scripts/backup.sh" "$tmp_root/backups" "$COMPOSE_PROJECT_NAME"
  backup_dir="$(find "$tmp_root/backups" -mindepth 1 -maxdepth 1 -type d -name '101conv-backup-*' -print -quit)"
  [[ -n "$backup_dir" ]]
  "$repo_root/scripts/test-restore.sh" "$backup_dir" --conversion-smoke
fi
if [[ -n "${SMOKE_EXPORT_RESTORE_DIR:-}" ]]; then
  BACKUP_OMIT_ENV=1 "$repo_root/scripts/backup.sh" "$tmp_root/backups" "$COMPOSE_PROJECT_NAME"
  backup_dir="$(find "$tmp_root/backups" -mindepth 1 -maxdepth 1 -type d -name '101conv-backup-*' -print -quit)"
  [[ -n "$backup_dir" ]]
  cp -a "$backup_dir" "$SMOKE_EXPORT_RESTORE_DIR/backup"
  printf '%s\n%s\n' "$SMOKE_IMAGE" "$BROWSER_IMAGE" > "$SMOKE_EXPORT_RESTORE_DIR/images.txt"
  docker image save "$SMOKE_IMAGE" "$BROWSER_IMAGE" | gzip -1 > "$SMOKE_EXPORT_RESTORE_DIR/images.tar.gz"
  chmod 0600 "$SMOKE_EXPORT_RESTORE_DIR/images.txt" "$SMOKE_EXPORT_RESTORE_DIR/images.tar.gz"
fi
compose restart postgres core worker
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$core_container" 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  sleep 2
done
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$core_container")" == "healthy" ]]
persisted_batch_id="$(cat "$tmp_root/smoke-output/batch-id")"
[[ "$persisted_batch_id" =~ ^[0-9a-f-]{36}$ ]] || { echo 'Browser smoke did not provide a valid batch ID.' >&2; exit 1; }
docker run --rm --network "${COMPOSE_PROJECT_NAME}_web-core" -e ENGINE_ADMIN_TOKEN -e SMOKE_BATCH_ID="$persisted_batch_id" "$BROWSER_IMAGE" node - <<'NODE'
const assert = require('node:assert/strict');
(async () => {
  const base = 'http://web:5656';
  const headers = { Authorization: 'Bearer ' + process.env.ENGINE_ADMIN_TOKEN };
  const response = await fetch(base + '/api/v1/jobs?limit=100', { headers });
  assert.equal(response.status, 200);
  const data = await response.json();
  assert.ok(data.total >= 1);
  const batchResponse = await fetch(base + '/api/v1/batches/' + process.env.SMOKE_BATCH_ID, { headers });
  assert.equal(batchResponse.status, 200, 'batch status persists across application restart');
  const batch = (await batchResponse.json()).batch;
  assert.equal(batch.succeeded, 20, 'all 20 mixed-format items remain successful after restart');
  assert.equal(batch.total, 20);
  const job = data.jobs.find(item => item.state === 'succeeded');
  assert.ok(job, 'completed job persisted across restart');
  const result = await fetch(base + '/api/v1/jobs/' + job.id + '/result', { headers });
  assert.equal(result.status, 200);
  assert.match(await result.text(), /^P2\n/);
})().catch(error => { console.error(error); process.exit(1); });
NODE
printf 'Go/React browser conversion smoke test passed.\n'
