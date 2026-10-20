# Deploying 101conv with Portainer

Use one Portainer Git stack with repository `https://github.com/Timisah/101conv`, branch `main`, and Compose path `compose.yaml`. The stack builds React/Nginx, Go Core, and the restricted Go Worker, and runs PostgreSQL. Nginx publishes host port `5656` by default; Core and PostgreSQL have no public ports.

## HTTPS reverse proxy

Put the bundled `web` service behind the public HTTPS proxy, or terminate TLS in front of the published web port. The outer proxy must set the public `Host`, overwrite `X-Forwarded-Proto` with `https`, and preserve request bodies and the `X-101conv-Request: guest-session` header. It must not append client-controlled `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Real-IP`, or `X-Forwarded-For` values. Keep Core's `5656` port private; the bundled Nginx hop overwrites address headers and marks forwarded requests for Core.

Direct trusted-LAN HTTP is supported when users open the published web port directly. Cookies are then intentionally not marked `Secure`. HTTPS deployments receive `Secure; SameSite=Strict` host-only session cookies. If an outer proxy rewrites or removes `Origin`, `Referer`, Fetch Metadata, or the guest request marker, browser mutations will be rejected with a CSRF error until that proxy is configured to preserve them. This is a configuration failure, not a reason to broadly allow missing or mismatched origins.

The application ignores `X-Forwarded-Host` for origin decisions and ignores client-supplied address headers for login throttling unless the request was marked by the bundled proxy. This protects the default deployment from spoofed forwarding headers. A custom ingress must be a trusted boundary and must overwrite forwarded values before passing requests to `web`.

## Existing stack update

Keep the current stack name, Git repository, branch, and `DATA_PATH`, `GO_DATA_PATH`, `PORT`, PostgreSQL credentials, and named volumes. Set the Compose path to `compose.yaml` if needed, pull the new commit, and redeploy/build the stack. Do not enable volume pruning or delete host directories. This deployment uses a `web` Compose service; if an older stack leaves an orphaned frontend container after updating, inspect it and remove only that stopped container after confirming it is no longer serving the port. Do not remove any volumes.

The existing Go storage layout remains supported: by default the Go state directory is `${DATA_PATH}/go-data`, or the exact directory selected by `GO_DATA_PATH`. It holds management state and installed engines. The Compose bind uses `create_host_path: false`; prepare the directory before deployment and ensure it is writable by UID/GID `65532:65532`. PostgreSQL, uploads, and results remain in named volumes and are not deleted by this Compose file.

Portainer variables:

| Variable | Required | Purpose |
|---|---:|---|
| `DATA_PATH` | Yes, unless `GO_DATA_PATH` is set | Parent of the existing `go-data` directory. Keep its current value during updates. |
| `GO_DATA_PATH` | No | Exact persistent Go state directory; takes precedence over `${DATA_PATH}/go-data`. |
| `POSTGRES_PASSWORD` | Yes | PostgreSQL password. Reuse the value associated with an existing database volume. |
| `POSTGRES_WORKER_USER` | No for existing stacks; generated for new installs | Dedicated Worker database login; default `101conv_worker`. Keep this value stable after migration. |
| `POSTGRES_WORKER_PASSWORD` | Yes for new installs; required before an existing Worker switch | Separate strong password for the Worker login. Existing stacks must run the additive migration below before restarting Worker with this value. |
| `ENGINE_ADMIN_TOKEN` | Yes | Strong administrator login/API token. |
| `PORT` | No | Published web port; default `5656`. |
| `POSTGRES_DB` | No | Database name; default `101conv`. Keep existing value when reusing a volume. |
| `POSTGRES_USER` | No | Database user; default `101conv`. Keep existing value when reusing a volume. |
| `APP_VERSION` | No | Application/catalog compatibility version; default `0.1.0`. |
| `WORKER_CONCURRENCY` | No | Concurrent conversion processes per Worker container; default `1`, maximum `8`. Keep low on small hosts. Operations shows the configured slots and current active jobs. |
| `WORKER_BATCH_CONCURRENCY` | No | Maximum simultaneous running jobs from one batch across the shared queue; default `2`, maximum `20`. The worker process concurrency is still the global limit. |
| `AUTOMATION_HOST_PATH` | No | Host directory mounted into Core at `/mounts`; default `${DATA_PATH}/automation-mounts`. Use an existing Docker-visible directory containing folders administrators intend to register. |

For a new installation, create the directory first, for example:

```sh
sudo mkdir -p /mnt/storage/apps/101conv/data/go-data/engines
sudo chown -R 65532:65532 /mnt/storage/apps/101conv/data/go-data
```

Run these commands on the Docker host, using your actual path. If the bind is an NFS mount with root squashing, set ownership on the NFS server instead. The browser can test whether its mounted directory is writable, but it cannot change host mounts or permissions.

Set `DATA_PATH=/mnt/storage/apps/101conv/data` in Portainer. For an existing install, use its real data path and preserve all existing directories and named volumes. Never point `GO_DATA_PATH` at a directory containing unrelated application data.

## Mounted folders

The Compose stack binds `AUTOMATION_HOST_PATH` (or `${DATA_PATH}/automation-mounts`) to `/mounts` in **Core only**. It does not add the host folder to the Worker, expose Docker, or allow the web browser to create host mounts. Before registering folders, create the host directories and grant the Core container UID/GID `65532:65532` read access; grant write access only where output destinations are required. Example on Ubuntu:

```sh
sudo mkdir -p /mnt/truenas/media/incoming /mnt/truenas/media/converted
sudo chown -R 65532:65532 /mnt/truenas/media
```

Set `AUTOMATION_HOST_PATH=/mnt/truenas/media` in the Portainer Environment variables table, then redeploy. If using an NFS mount, configure its server-side UID/GID or ACLs instead of relying on client-side `chown`. In **Admin → Mounted drives**, register `/mounts/incoming` as read-only and `/mounts/converted` as read/write. The access setting controls 101conv's workflow behavior; the Docker mount must also permit the corresponding operation. Use a distinct host directory dedicated to 101conv rather than exposing `/`, `/mnt`, or another broad filesystem root.

After redeployment, visit `http://<docker-host>:5656/` (or the configured `PORT`). The Compose build disables cached layers for application and web images so the built files correspond to the selected Git commit. Portainer must build Compose services from the Git repository; a running stack does not update until Portainer pulls and redeploys a commit.

For a fresh instance, use a random `ENGINE_ADMIN_TOKEN` of at least 24 characters. The browser asks for that bootstrap passphrase to claim the initial administrator account. Keep the value private: the original token login remains valid for compatibility after setup. If a previous deployment already has accounts, onboarding is marked complete during migration and they continue to sign in. If a previous token-only deployment has no accounts, its existing token can claim the initial account; the existing data remains untouched. The setup review checks only container-visible paths; it cannot create Docker mounts or change ownership on your Ubuntu host.

New local installs can instead use [`scripts/install.sh`](../../scripts/install.sh), which creates `.env` and the data path. Existing Portainer stacks should keep their current environment table and stack identity. Review [backup and isolated restore instructions](backup.md) before upgrades; the backup destination must be outside live storage.

For an existing database volume, take a backup, add `POSTGRES_WORKER_USER` and a new strong `POSTGRES_WORKER_PASSWORD` to the stack environment, then run the migration from a checkout on the Docker host before restarting or recreating Worker:

```sh
set -a; . ./.env; set +a
scripts/migrate-worker-db-role.sh
```

If the stack is managed only through Portainer, run the same script from an administrative shell with the Compose project selected and the two variables exported. The migration is additive, preserves the Core credentials and data, and grants the Worker application-table DML needed by the current job implementation. Verify the result with [`scripts/verify-worker-db-role.sh`](../../scripts/verify-worker-db-role.sh). New installs run the equivalent init hook automatically.

## Data and migration boundary

This release runs only the Go/React application. Account, profile, job, and engine data are stored in PostgreSQL, named uploads/results volumes, and the Go data directory. Those stores are retained by keeping the same stack and storage configuration. No automatic importer exists for data from other application formats; any such data remains untouched on disk and is not used by this application. This guide does not perform data deletion or volume migration.

### Office and PDF runtime (D.2)

Rebuild the Worker with `server/Dockerfile.worker` from the current Compose
stack before installing the Office or PDF profiles. Core keeps its existing
image. No new port or mount is required. The Worker includes pinned LibreOffice,
Poppler, runtime dependencies and fallback fonts. See
[document deployment and runtime details](../architecture/documents-d2.md).
