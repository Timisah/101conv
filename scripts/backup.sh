#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo 'Usage: scripts/backup.sh BACKUP_PARENT_DIRECTORY [COMPOSE_PROJECT_NAME]' >&2
  exit 2
fi
command -v docker >/dev/null || { echo 'Docker is required.' >&2; exit 1; }
command -v python3 >/dev/null || { echo 'Python 3 is required.' >&2; exit 1; }
dest_parent="$(mkdir -p "$1" && realpath "$1")"
project="${2:-101conv-go}"
container() {
  local service="$1" ids
  ids="$(docker ps -aq --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=$service")"
  [[ "$(wc -w <<< "$ids")" -eq 1 ]] || { echo "Expected exactly one $service container in project $project" >&2; return 1; }
  printf '%s' "$ids"
}
core="$(container core)"
worker="$(container worker)"
web="$(container web)"
postgres="$(container postgres)"
for id in "$core" "$worker" "$web" "$postgres"; do
  [[ "$(docker inspect -f '{{.State.Running}}' "$id")" == true ]] || { echo 'All stack containers must be running before backup.' >&2; exit 1; }
done
go_data="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/101conv"}}{{.Source}}{{end}}{{end}}' "$core")"
if [[ -n "$go_data" ]]; then
  case "$dest_parent/" in "$go_data/"*) echo 'Backup destination must be outside the live application data directory.' >&2; exit 1;; esac
fi
umask 077
stage="$(mktemp -d "$dest_parent/.101conv-incomplete-XXXXXXXX")"
restart_required=false
cleanup() {
  status=$?
  if "$restart_required"; then
    docker start "$core" >/dev/null || true
    docker start "$worker" "$web" >/dev/null || true
  fi
  if [[ $status -ne 0 ]]; then
    echo "Backup failed; incomplete files remain at $stage for inspection." >&2
  fi
  exit "$status"
}
trap cleanup EXIT
restart_required=true
docker stop "$web" "$worker" "$core" >/dev/null

# The application writers are stopped while pg_dump and every file archive are
# captured. PostgreSQL stays up and is never exposed on a host port.
docker exec "$postgres" sh -c 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec pg_dump -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$stage/postgres.dump"
docker run --rm --network none --volumes-from "$core:ro" --entrypoint tar postgres:17-alpine -C /var/lib/101conv --exclude=./uploads --exclude=./results -cf - . > "$stage/go-data.tar"
docker run --rm --network none --volumes-from "$core:ro" --entrypoint tar postgres:17-alpine -C /var/lib/101conv/uploads -cf - . > "$stage/uploads.tar"
docker run --rm --network none --volumes-from "$core:ro" --entrypoint tar postgres:17-alpine -C /var/lib/101conv/results -cf - . > "$stage/results.tar"

BACKUP_STAGE="$stage" BACKUP_PROJECT="$project" python3 - <<'PY'
import datetime, hashlib, json, os
from pathlib import Path
root=Path(os.environ['BACKUP_STAGE'])
files={}
for name in ('postgres.dump','go-data.tar','uploads.tar','results.tar'):
    path=root/name
    digest=hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''):
            digest.update(block)
    files[name]={'sha256':digest.hexdigest(),'bytes':path.stat().st_size}
manifest={'formatVersion':1,'application':'101conv','scope':'full','project':os.environ['BACKUP_PROJECT'],'createdAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'writersStopped':True,'files':files}
(root/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
PY
"$(dirname "$0")/verify-backup.sh" "$stage"
final="$dest_parent/101conv-backup-$(date -u +%Y%m%dT%H%M%SZ)"
[[ ! -e "$final" ]] || { echo "Backup destination already exists: $final" >&2; exit 1; }
mv "$stage" "$final"
stage="$final"
echo "Verified full backup: $final"
