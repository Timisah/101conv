#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
args=()
if [[ -n "${CODEX_PROXY_CERT:-}" ]]; then args+=(--secret "id=proxy_ca,src=$CODEX_PROXY_CERT"); fi
python3 scripts/package-document-engines.py
# Versioned managed packages must match their committed manifests.
git diff --exit-code -- catalog/targets/packages catalog/targets/engines/libreoffice-linux-amd64.json catalog/targets/engines/poppler-linux-amd64.json
docker build "${args[@]}" -f server/Dockerfile.worker -t 101conv-document-worker:dev server
docker build "${args[@]}" -f server/Dockerfile.documents-test -t 101conv-document-test:dev .
docker run --rm --read-only --network none --cap-drop ALL --pids-limit 64 --memory 1g --cpus 1 \
 --tmpfs /tmp:rw,nosuid,nodev,noexec,size=512m,mode=1777 \
 --tmpfs /work:rw,nosuid,nodev,noexec,size=1g,mode=0700,uid=65532,gid=65532 \
 101conv-document-test:dev
# End-to-end HTTP, queue, guest isolation, batches, and folder automation.
# All service traffic stays on an internal Docker network.
run_id="101conv-documents-$$"
network="$run_id-net"
postgres="$run_id-db"
cleanup() {
 docker rm -f "$postgres" >/dev/null 2>&1 || true
 docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create --internal "$network" >/dev/null
docker run -d --name "$postgres" --network "$network" --network-alias postgres \
 -e POSTGRES_USER=test -e POSTGRES_PASSWORD=document-test-only -e POSTGRES_DB=test postgres:17-alpine >/dev/null
ready=false
for ((i=0;i<30;i++)); do
 if docker exec "$postgres" pg_isready -U test -d test >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
[[ "$ready" == true ]] || { echo 'Document test PostgreSQL failed to become ready' >&2; exit 1; }
docker run --rm --read-only --network "$network" --cap-drop ALL --pids-limit 64 --memory 1g --cpus 1 \
 --tmpfs /tmp:rw,nosuid,nodev,noexec,size=512m,mode=1777 \
 --tmpfs /work:rw,nosuid,nodev,noexec,size=1g,mode=0700,uid=65532,gid=65532 \
 --tmpfs /test-state:rw,exec,nosuid,nodev,size=128m,mode=0700,uid=65532,gid=65532 \
 -e 'TEST_DATABASE_URL=postgres://test:document-test-only@postgres:5432/test?sslmode=disable' \
 --entrypoint /tests/httpapi.test 101conv-document-test:dev -test.v -test.run TestDocumentJobPlatform
