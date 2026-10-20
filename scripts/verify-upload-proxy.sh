#!/usr/bin/env bash
# Isolated ingress test: no application data, published ports, or converter needed.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
run_id="upload-proxy-$$"
cleanup() {
    docker rm -f "$run_id-web" "$run_id-core" >/dev/null 2>&1 || true
    docker network rm "$run_id" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create "$run_id" >/dev/null
docker run -d --name "$run_id-core" --network "$run_id" --network-alias core \
    --mount "type=bind,src=$root/scripts/upload-proxy-check.py,dst=/check.py,readonly" \
    python:3.12-alpine python /check.py serve >/dev/null
docker run -d --name "$run_id-web" --network "$run_id" --network-alias web \
    --mount "type=bind,src=$root/web/nginx.conf,dst=/etc/nginx/conf.d/default.conf,readonly" \
    nginx:1.27-alpine >/dev/null
docker exec "$run_id-web" nginx -t
docker exec "$run_id-core" python /check.py check
