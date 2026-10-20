#!/usr/bin/env bash
set -euo pipefail
[[ $# -ge 1 && $# -le 2 ]] || { echo 'Usage: scripts/test-restore.sh BACKUP_DIRECTORY [--conversion-smoke]' >&2; exit 2; }
conversion_smoke=0
if [[ ${2:-} == --conversion-smoke ]]; then conversion_smoke=1; elif [[ $# -eq 2 ]]; then echo "Unknown restore-test option: $2" >&2; exit 2; fi
: "${SMOKE_IMAGE:?Run this from phase5-browser-smoke.sh so Core/Worker use the tested image}"
: "${BROWSER_IMAGE:?Run this from phase5-browser-smoke.sh so the isolated API check can run}"
docker image inspect "$SMOKE_IMAGE" >/dev/null
docker image inspect "$BROWSER_IMAGE" >/dev/null
backup_dir="$(realpath "$1")"
"$(dirname "$0")/verify-backup.sh" "$backup_dir"
restore_init_script="$(realpath "$(dirname "$0")/../db/init/01-worker-role.sh")"
original_project="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("project", ""))' "$backup_dir/manifest.json")"
[[ -n "$original_project" ]] || { echo 'Backup manifest does not record its Compose project identity.' >&2; exit 1; }
suffix="$(openssl rand -hex 6)"
project="101conv-restore-test-$suffix"
volume_prefix="$project"
check_dir="$(mktemp -d)"
chmod 0755 "$check_dir"
if [[ -n "${RESTORE_ARTIFACT_DIR:-}" ]]; then mkdir -p "$RESTORE_ARTIFACT_DIR/checks"; fi
compose_file="$check_dir/compose.yaml"
restore_password="$(openssl rand -hex 24)"
restore_worker_password="$(openssl rand -hex 24)"
restore_admin_token="restore-test-$(openssl rand -hex 24)"
created_volumes=()
export RESTORE_GO_VOLUME="$volume_prefix-go" RESTORE_UPLOADS_VOLUME="$volume_prefix-uploads" RESTORE_RESULTS_VOLUME="$volume_prefix-results" RESTORE_POSTGRES_VOLUME="$volume_prefix-postgres"
export RESTORE_PASSWORD="$restore_password" RESTORE_WORKER_USER=101conv_worker RESTORE_WORKER_PASSWORD="$restore_worker_password" RESTORE_INIT_SCRIPT="$restore_init_script" RESTORE_ADMIN_TOKEN="$restore_admin_token" RESTORE_IMAGE="$SMOKE_IMAGE"
cleanup() {
  status=$?
  if [[ "$status" -ne 0 && -n "${RESTORE_ARTIFACT_DIR:-}" ]]; then
    docker compose -p "$project" -f "$compose_file" logs --no-color --tail 200 > "$RESTORE_ARTIFACT_DIR/restore-compose.log" 2>&1 || true
    RESTORE_REDACT_TOKEN="$RESTORE_ADMIN_TOKEN" RESTORE_REDACT_PASSWORD="$RESTORE_PASSWORD" RESTORE_REDACT_WORKER_PASSWORD="$RESTORE_WORKER_PASSWORD" python3 - "$RESTORE_ARTIFACT_DIR/restore-compose.log" <<'PY'
import os,sys
from pathlib import Path
p=Path(sys.argv[1]); text=p.read_text(errors='replace')
for value in (os.environ.get('RESTORE_REDACT_TOKEN',''),os.environ.get('RESTORE_REDACT_PASSWORD',''),os.environ.get('RESTORE_REDACT_WORKER_PASSWORD','')):
    if value: text=text.replace(value,'[REDACTED]')
p.write_text(text)
PY
    cp -a "$check_dir/." "$RESTORE_ARTIFACT_DIR/checks/" 2>/dev/null || true
    printf 'exit_status=%s\ncompose_project=%s\n' "$status" "$project" > "$RESTORE_ARTIFACT_DIR/run.txt"
  fi
  docker compose -p "$project" -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
  for volume in "${created_volumes[@]}"; do
    docker volume rm "$volume" >/dev/null 2>&1 || true
  done
  rm -rf "$check_dir"
  exit "$status"
}
trap cleanup EXIT
if [[ -n "$(docker ps -aq --filter "label=com.docker.compose.project=$project")" ]]; then
  echo "Refusing to reuse existing Compose project identity $project." >&2
  exit 1
fi
for volume in "$RESTORE_GO_VOLUME" "$RESTORE_UPLOADS_VOLUME" "$RESTORE_RESULTS_VOLUME" "$RESTORE_POSTGRES_VOLUME"; do
  if docker volume inspect "$volume" >/dev/null 2>&1; then
    echo "Refusing to overwrite existing Docker volume $volume." >&2
    exit 1
  fi
  docker volume create "$volume" >/dev/null
  created_volumes+=("$volume")
done
# Archives are extracted only inside named test volumes, never onto the host or a live volume.
for part in go uploads results; do
  archive="$part.tar"
  [[ "$part" == go ]] && archive='go-data.tar'
  docker run --rm -i --network none --user 0:0 --volume "$volume_prefix-$part:/restore" --entrypoint tar postgres:17-alpine -C /restore -xf - < "$backup_dir/$archive"
done
cat >"$compose_file" <<'YAML'
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: 101conv
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${RESTORE_PASSWORD:?}
      POSTGRES_WORKER_USER: ${RESTORE_WORKER_USER:?}
      POSTGRES_WORKER_PASSWORD: ${RESTORE_WORKER_PASSWORD:?}
    volumes:
      - restored-postgres:/var/lib/postgresql/data
      - ${RESTORE_INIT_SCRIPT:?}:/docker-entrypoint-initdb.d/01-worker-role.sh:ro
    networks: [private]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d 101conv"]
      interval: 2s
      timeout: 2s
      retries: 30
    mem_limit: 512m
    cpus: 1.0
    pids_limit: 128
    restart: "no"
  core:
    image: ${RESTORE_IMAGE:?}
    environment:
      APP_VERSION: restore-smoke
      PORT: 5656
      DATA_PATH: /var/lib/101conv
      UPLOADS_PATH: /var/lib/101conv/uploads
      RESULTS_PATH: /var/lib/101conv/results
      DATABASE_URL: host=postgres port=5432 user=postgres password=${RESTORE_PASSWORD} dbname=101conv sslmode=disable
      ENGINE_ADMIN_TOKEN: ${RESTORE_ADMIN_TOKEN}
    volumes:
      - restored-go:/var/lib/101conv
      - restored-uploads:/var/lib/101conv/uploads
      - restored-results:/var/lib/101conv/results
    networks: [private]
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "/101conv", "healthcheck"]
      interval: 2s
      timeout: 3s
      retries: 30
      start_period: 10s
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777
    cap_drop: [ALL]
    pids_limit: 128
    mem_limit: 512m
    cpus: 1.0
    restart: "no"
  worker:
    image: ${RESTORE_IMAGE:?}
    command: ["worker"]
    environment:
      DATABASE_URL: host=postgres port=5432 user=${RESTORE_WORKER_USER} password=${RESTORE_WORKER_PASSWORD} dbname=101conv sslmode=disable
      ENGINE_ROOT: /var/lib/101conv/engines
      UPLOADS_PATH: /var/lib/101conv/uploads
      RESULTS_PATH: /var/lib/101conv/results
      WORK_PATH: /work
      CONVERSION_TIMEOUT: 2m
      MAX_OUTPUT_BYTES: 209715200
      JOB_LEASE: 45s
    volumes:
      - restored-go:/var/lib/101conv:ro
      - restored-uploads:/var/lib/101conv/uploads:ro
      - restored-results:/var/lib/101conv/results
    networks: [private]
    depends_on:
      postgres:
        condition: service_healthy
      core:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "/101conv", "worker-health"]
      interval: 2s
      timeout: 3s
      retries: 30
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=32m,mode=1777
      - /work:rw,nosuid,nodev,noexec,size=1g,mode=0700,uid=65532,gid=65532
    cap_drop: [ALL]
    pids_limit: 64
    mem_limit: 768m
    cpus: 1.0
    restart: "no"
volumes:
  restored-go: { external: true, name: "${RESTORE_GO_VOLUME}" }
  restored-uploads: { external: true, name: "${RESTORE_UPLOADS_VOLUME}" }
  restored-results: { external: true, name: "${RESTORE_RESULTS_VOLUME}" }
  restored-postgres: { external: true, name: "${RESTORE_POSTGRES_VOLUME}" }
networks:
  private:
    internal: true
YAML
compose=(docker compose -p "$project" -f "$compose_file")
"${compose[@]}" config --quiet
"${compose[@]}" up -d postgres
ready=false
postgres_id="$("${compose[@]}" ps -q postgres)"
for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$postgres_id" 2>/dev/null || true)"
  [[ "$health" == healthy ]] && { ready=true; break; }
  sleep 1
done
"$ready" || { "${compose[@]}" logs --no-color postgres; echo 'Isolated restore database did not become ready.' >&2; exit 1; }
"${compose[@]}" exec -T postgres pg_restore -U postgres -d 101conv --no-owner --no-privileges --exit-on-error < "$backup_dir/postgres.dump"
# Check consistency against the restored files before the application starts.
"${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT id FROM conv_uploads WHERE original_deleted_at IS NULL AND original_missing_at IS NULL ORDER BY id' > "$check_dir/uploads.txt"
"${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT result_key FROM conv_jobs WHERE result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL ORDER BY result_key' > "$check_dir/results.txt"
"${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT id FROM conv_uploads WHERE original_deleted_at IS NOT NULL ORDER BY id' > "$check_dir/expired-uploads.txt"
"${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT result_key FROM conv_jobs WHERE result_key IS NOT NULL AND result_deleted_at IS NOT NULL ORDER BY result_key' > "$check_dir/expired-results.txt"
chmod 0644 "$check_dir/uploads.txt" "$check_dir/results.txt" "$check_dir/expired-uploads.txt" "$check_dir/expired-results.txt"
docker run --rm --network none --user 0:0 --volume "$RESTORE_UPLOADS_VOLUME:/uploads:ro" --volume "$RESTORE_RESULTS_VOLUME:/results:ro" --volume "$check_dir:/checks:ro" --entrypoint sh postgres:17-alpine -c '
  while IFS= read -r id; do [ -f "/uploads/$id" ] || { echo "Missing original for $id" >&2; exit 1; }; done < /checks/uploads.txt
  while IFS= read -r key; do [ -f "/results/$key" ] || { echo "Missing result for $key" >&2; exit 1; }; done < /checks/results.txt
  while IFS= read -r id; do [ ! -e "/uploads/$id" ] || { echo "Expired original still exists for tombstone $id" >&2; exit 1; }; done < /checks/expired-uploads.txt
  while IFS= read -r key; do [ ! -e "/results/$key" ] || { echo "Expired result still exists for tombstone $key" >&2; exit 1; }; done < /checks/expired-results.txt
'
bad_jobs="$("${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT count(*) FROM conv_jobs j LEFT JOIN conv_uploads u ON u.id=j.upload_id WHERE u.id IS NULL')"
[[ "$bad_jobs" == 0 ]] || { echo "Restore contains $bad_jobs jobs without upload metadata." >&2; exit 1; }
bad_specs="$("${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c "SELECT count(*) FROM conv_jobs WHERE execution->>'engineId' IS NULL OR execution->>'engineVersion' IS NULL")"
[[ "$bad_specs" == 0 ]] || { echo "Restore contains $bad_specs jobs without pinned engine execution specifications." >&2; exit 1; }
users="$("${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT count(*) FROM conv_users')"
jobs="$("${compose[@]}" exec -T postgres psql -U postgres -d 101conv -At -c 'SELECT count(*) FROM conv_jobs')"
expired_uploads="$(wc -l < "$check_dir/expired-uploads.txt")"
expired_results="$(wc -l < "$check_dir/expired-results.txt")"
"${compose[@]}" up -d core worker
wait_health() {
  local service="$1" id status
  id="$("${compose[@]}" ps -q "$service")"
  for _ in $(seq 1 60); do
    status="$(docker inspect --format '{{.State.Health.Status}}' "$id" 2>/dev/null || true)"
    [[ "$status" == healthy ]] && return 0
    [[ "$status" == unhealthy ]] && break
    sleep 1
  done
  "${compose[@]}" logs --no-color --tail 80 "$service" >&2
  echo "Restored $service did not become healthy." >&2
  return 1
}
wait_health core
wait_health worker
if "${compose[@]}" ps --services --filter status=running | grep -q '^web$'; then
  echo 'Restore test unexpectedly started the public web service.' >&2
  exit 1
fi
for service in postgres core worker; do
  container_id="$("${compose[@]}" ps -q "$service")"
  docker inspect "$container_id" --format '{{json .HostConfig.PortBindings}}' | python3 -c 'import json,sys; ports=json.load(sys.stdin); assert not ports or all(not bindings for bindings in ports.values()), ports'
done
# Probe the restored application from its private network. The optional fixture
# conversion is separate from generic restore consistency checks.
docker run --rm -i --network "$project"_private -e RESTORE_ADMIN_TOKEN -e RESTORE_ORIGINAL_PROJECT="$original_project" -e RESTORE_CONVERSION_SMOKE="$conversion_smoke" -e RESTORE_EXPIRED_UPLOADS="$expired_uploads" -e RESTORE_EXPIRED_RESULTS="$expired_results" "$BROWSER_IMAGE" node - <<'NODE'
const assert = require('node:assert/strict');
const base = 'http://core:5656';
const headers = { Authorization: 'Bearer ' + process.env.RESTORE_ADMIN_TOKEN };
async function get(path) {
  const response = await fetch(base + path, { headers });
  if (!response.ok) throw new Error(`${path} returned ${response.status}: ${(await response.text()).slice(0,500)}`);
  return response.json();
}
(async () => {
  const [users, engines, profiles, jobs] = await Promise.all([
    get('/api/v1/users'), get('/api/v1/engines'), get('/api/v1/profiles'), get('/api/v1/jobs?limit=100')
  ]);
  assert.ok(users.users.length > 0, 'restored account records are visible');
  assert.ok(engines.engines.length > 0, 'restored engine records are visible');
  assert.ok(profiles.profiles.length > 0, 'restored profile records are visible');
  assert.ok(jobs.total > 0, 'restored job history is visible');
  assert.ok(jobs.jobs.every(job => job.id && job.profileId && job.profileVersion), 'restored history retains its profile/version references');
  let convertedJobID='';
  if (process.env.RESTORE_CONVERSION_SMOKE === '1') {
  const expired = jobs.jobs.find(job => job.resultState === 'expired');
  assert.ok(expired, 'restored job history retains an expired-result tombstone');
  const expiredResult = await fetch(base + '/api/v1/jobs/' + expired.id + '/result', { headers });
  assert.equal(expiredResult.status, 410, 'expired result remains unavailable after restore');
  const profile = profiles.profiles.find(entry => entry.definition?.id === 'ppm-pgm-grayscale');
  assert.ok(profile, 'restored fixture conversion profile is available');
  assert.ok(engines.engines.some(entry => entry.installation?.engineId === 'ppm-pgm'), 'restored fixture engine is installed');
  const source = Buffer.from('P3\n2 1\n255\n255 0 0 0 255 0\n');
  const form = new FormData();
  form.append('file', new Blob([source]), 'restore-smoke.ppm');
  const uploaded = await fetch(base + '/api/v1/uploads', { method: 'POST', headers, body: form });
  if (uploaded.status !== 201) throw new Error(`restored app accepts upload: ${uploaded.status} ${(await uploaded.text()).slice(0,500)}`);
  const upload = await uploaded.json();
  const submitted = await fetch(base + '/api/v1/jobs', { method: 'POST', headers: { ...headers, 'Content-Type': 'application/json' }, body: JSON.stringify({ uploadId: upload.id, profileId: profile.definition.id, targetFormat: 'pgm', idempotencyKey: 'restore-smoke-conversion-001' }) });
  if (submitted.status !== 202) throw new Error(`restored app queues conversion: ${submitted.status} ${(await submitted.text()).slice(0,500)}`);
  const { job: initial } = await submitted.json();
  let job = initial;
  for (let i = 0; i < 60 && !['succeeded','failed','cancelled'].includes(job.state); i++) {
    await new Promise(resolve => setTimeout(resolve, 1000));
    const response = await fetch(base + '/api/v1/jobs/' + job.id, { headers });
    assert.equal(response.status, 200);
    job = (await response.json()).job;
  }
  assert.equal(job.state, 'succeeded', `restored conversion completed: ${job.error || job.state}`);
  const result = await fetch(base + '/api/v1/jobs/' + job.id + '/result', { headers });
  assert.equal(result.status, 200);
  assert.match(await result.text(), /^P2\n2 1\n255\n/);
  convertedJobID=job.id;
  }
  console.log(`Isolated restore application passed: ${users.users.length} users, ${engines.engines.length} engines, ${profiles.profiles.length} profiles, ${jobs.total} jobs, ${process.env.RESTORE_EXPIRED_UPLOADS} expired-original and ${process.env.RESTORE_EXPIRED_RESULTS} expired-result tombstones. ${convertedJobID?'Fixture conversion '+convertedJobID+' succeeded.':'Fixture conversion was not requested.'} Original Compose project: ${process.env.RESTORE_ORIGINAL_PROJECT}.`);
})().catch(error => { console.error(error); process.exit(1); });
NODE
