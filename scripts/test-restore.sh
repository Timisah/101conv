#!/usr/bin/env bash
set -euo pipefail
[[ $# -eq 1 ]] || { echo 'Usage: scripts/test-restore.sh BACKUP_DIRECTORY' >&2; exit 2; }
backup_dir="$(realpath "$1")"
"$(dirname "$0")/verify-backup.sh" "$backup_dir"
suffix="$(openssl rand -hex 6)"
prefix="101conv-restore-test-$suffix"
db_container="$prefix-postgres"
check_dir="$(mktemp -d)"
# The checker runs with all capabilities dropped. It needs to traverse only
# this temporary directory, which contains identifiers but no file contents.
chmod 0755 "$check_dir"
cleanup() {
  status=$?
  docker rm -f "$db_container" >/dev/null 2>&1 || true
  for part in go uploads results; do docker volume rm "$prefix-$part" >/dev/null 2>&1 || true; done
  rm -rf "$check_dir"
  exit "$status"
}
trap cleanup EXIT
for part in go uploads results; do docker volume create "$prefix-$part" >/dev/null; done
for part in go uploads results; do
  archive="$part.tar"
  [[ "$part" == go ]] && archive='go-data.tar'
  docker run --rm -i --network none --cap-drop ALL --volume "$prefix-$part:/restore" --entrypoint tar postgres:17-alpine -C /restore -xf - < "$backup_dir/$archive"
done
restore_password="$(openssl rand -hex 24)"
docker run -d --rm --network none --name "$db_container" -e POSTGRES_PASSWORD="$restore_password" -e POSTGRES_DB=101conv postgres:17-alpine >/dev/null
ready=false
for _ in $(seq 1 40); do
  if docker exec "$db_container" pg_isready -U postgres -d 101conv >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
"$ready" || { echo 'Isolated restore database did not become ready.' >&2; exit 1; }
docker exec -i "$db_container" pg_restore -U postgres -d 101conv --no-owner --no-privileges < "$backup_dir/postgres.dump"
docker exec "$db_container" psql -U postgres -d 101conv -At -c 'SELECT id FROM conv_uploads ORDER BY id' > "$check_dir/uploads.txt"
docker exec "$db_container" psql -U postgres -d 101conv -At -c "SELECT result_key FROM conv_jobs WHERE result_key IS NOT NULL ORDER BY result_key" > "$check_dir/results.txt"
chmod 0644 "$check_dir/uploads.txt" "$check_dir/results.txt"
docker run --rm --network none --cap-drop ALL --volume "$prefix-uploads:/uploads:ro" --volume "$prefix-results:/results:ro" --volume "$check_dir:/checks:ro" --entrypoint sh postgres:17-alpine -c '
  while IFS= read -r id; do [ -f "/uploads/$id" ] || { echo "Missing original for $id" >&2; exit 1; }; done < /checks/uploads.txt
  while IFS= read -r key; do [ -f "/results/$key" ] || { echo "Missing result for $key" >&2; exit 1; }; done < /checks/results.txt
'
users="$(docker exec "$db_container" psql -U postgres -d 101conv -At -c 'SELECT count(*) FROM conv_users')"
jobs="$(docker exec "$db_container" psql -U postgres -d 101conv -At -c 'SELECT count(*) FROM conv_jobs')"
profiles="$(docker run --rm --network none --volume "$prefix-go:/go:ro" --entrypoint sh postgres:17-alpine -c 'test -f /go/management.json && echo present || echo inspect' 2>/dev/null || true)"
echo "Isolated restore passed: $users accounts, $jobs jobs; original and result references exist. Go state volume: $profiles."
