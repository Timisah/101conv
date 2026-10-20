#!/usr/bin/env bash
set -euo pipefail

# Read-only audit for an existing deployment. Source the deployment environment
# first when the stack uses non-default database names or a custom role name.
worker_user="${POSTGRES_WORKER_USER:-101conv_worker}"
db_user="${POSTGRES_USER:-101conv}"
db_name="${POSTGRES_DB:-101conv}"
[[ "$worker_user" =~ ^[A-Za-z0-9_]+$ ]] || {
  echo 'POSTGRES_WORKER_USER must contain only letters, digits, and underscores.' >&2
  exit 2
}

row="$(docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -AtF '|' \
  --username "$db_user" --dbname "$db_name" \
  -c "SELECT rolname, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, has_schema_privilege(rolname, 'public', 'CREATE'), has_database_privilege(rolname, current_database(), 'TEMPORARY') FROM pg_roles WHERE rolname = '$worker_user'")"

expected="$worker_user|f|f|f|f|f|f|f"
if [[ "$row" != "$expected" ]]; then
  echo "Worker role audit failed: expected $expected, got ${row:-<missing>}" >&2
  exit 1
fi
echo "Worker role $worker_user is non-superuser and has no database/schema creation or temporary-table privileges."
