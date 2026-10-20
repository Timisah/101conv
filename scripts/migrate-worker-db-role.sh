#!/usr/bin/env bash
set -euo pipefail

# Additive migration for an existing PostgreSQL volume. It changes only the
# Worker login and grants; Core keeps the existing migration-capable role.
worker_user="${POSTGRES_WORKER_USER:-101conv_worker}"
worker_password="${POSTGRES_WORKER_PASSWORD:?Set POSTGRES_WORKER_PASSWORD before migrating the existing database}"
[[ "$worker_user" =~ ^[A-Za-z0-9_]+$ ]] || { echo 'POSTGRES_WORKER_USER must contain only letters, digits, and underscores.' >&2; exit 2; }
[[ "$worker_password" != *$'\n'* && "$worker_password" != *$'\r'* ]] || { echo 'POSTGRES_WORKER_PASSWORD must not contain a newline.' >&2; exit 2; }

# psql variables are not expanded inside a dollar-quoted DO body. Escape the
# values as SQL string literals before sending the migration to psql. The role
# name is constrained above and is still passed through format('%I', ...) as
# an identifier in the database.
worker_user_sql=$(printf '%s' "$worker_user" | sed "s/'/''/g")
worker_password_sql=$(printf '%s' "$worker_password" | sed "s/'/''/g")

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER:-101conv}" --dbname "${POSTGRES_DB:-101conv}" <<SQL
DO \$\$
DECLARE
    worker_name text := '$worker_user_sql';
    worker_password text := '$worker_password_sql';
BEGIN
    IF worker_name = '' OR worker_password = '' THEN
        RAISE EXCEPTION 'worker role credentials must not be empty';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = worker_name) THEN
        EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', worker_name, worker_password);
    ELSE
        EXECUTE format('ALTER ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', worker_name, worker_password);
    END IF;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), worker_name);
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I', worker_name);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %I', worker_name);
    EXECUTE format('GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO %I', worker_name);
    EXECUTE format('REVOKE CREATE ON SCHEMA public FROM %I', worker_name);
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM %I', current_database(), worker_name);
    -- TEMPORARY is granted to PUBLIC by default, so remove that inherited
    -- privilege as well. Core is a migration-capable superuser and does not
    -- need an explicit grant here.
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', current_user, worker_name);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO %I', current_user, worker_name);
END
\$\$;
SQL

echo "Worker database role $worker_user is ready. Restart the Worker with the matching POSTGRES_WORKER_* environment variables."
