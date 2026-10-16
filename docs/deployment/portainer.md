# Deploying 101conv with Portainer

Use one Portainer Git stack with repository `https://github.com/Timisah/101conv`, branch `main`, and Compose path `compose.yaml`. The stack builds React/Nginx, Go Core, and the restricted Go Worker, and runs PostgreSQL. Nginx publishes host port `5656` by default; Core and PostgreSQL have no public ports.

## Existing stack update

Keep the current stack name, Git repository, branch, and `DATA_PATH`, `GO_DATA_PATH`, `PORT`, PostgreSQL credentials, and named volumes. Set the Compose path to `compose.yaml` if needed, pull the new commit, and redeploy/build the stack. Do not enable volume pruning or delete host directories. This deployment uses a `web` Compose service; if an older stack leaves an orphaned frontend container after updating, inspect it and remove only that stopped container after confirming it is no longer serving the port. Do not remove any volumes.

The existing Go storage layout remains supported: by default the Go state directory is `${DATA_PATH}/go-data`, or the exact directory selected by `GO_DATA_PATH`. It holds management state and installed engines. The Compose bind uses `create_host_path: false`; prepare the directory before deployment and ensure it is writable by UID/GID `65532:65532`. PostgreSQL, uploads, and results remain in named volumes and are not deleted by this Compose file.

Portainer variables:

| Variable | Required | Purpose |
|---|---:|---|
| `DATA_PATH` | Yes, unless `GO_DATA_PATH` is set | Parent of the existing `go-data` directory. Keep its current value during updates. |
| `GO_DATA_PATH` | No | Exact persistent Go state directory; takes precedence over `${DATA_PATH}/go-data`. |
| `POSTGRES_PASSWORD` | Yes | PostgreSQL password. Reuse the value associated with an existing database volume. |
| `ENGINE_ADMIN_TOKEN` | Yes | Strong administrator login/API token. |
| `PORT` | No | Published web port; default `5656`. |
| `POSTGRES_DB` | No | Database name; default `101conv`. Keep existing value when reusing a volume. |
| `POSTGRES_USER` | No | Database user; default `101conv`. Keep existing value when reusing a volume. |
| `APP_VERSION` | No | Application/catalog compatibility version; default `0.1.0`. |

For a new installation, create the directory first, for example:

```sh
sudo mkdir -p /mnt/storage/apps/101conv/data/go-data/engines
sudo chown -R 65532:65532 /mnt/storage/apps/101conv/data/go-data
```

Set `DATA_PATH=/mnt/storage/apps/101conv/data` in Portainer. For an existing install, use its real data path and preserve all existing directories and named volumes. Never point `GO_DATA_PATH` at a directory containing unrelated application data.

After redeployment, visit `http://<docker-host>:5656/` (or the configured `PORT`). The Compose build disables cached layers for application and web images so the built files correspond to the selected Git commit. Portainer must build Compose services from the Git repository; a running stack does not update until Portainer pulls and redeploys a commit.

## Data and migration boundary

This release runs only the Go/React application. Account, profile, job, and engine data are stored in PostgreSQL, named uploads/results volumes, and the Go data directory. Those stores are retained by keeping the same stack and storage configuration. No automatic importer exists for data from other application formats; any such data remains untouched on disk and is not used by this application. This guide does not perform data deletion or volume migration.
