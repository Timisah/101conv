# Deploying 101conv with Portainer

The root `compose.yaml` is the production deployment. It builds and runs the React/Nginx web app, Go Core, restricted Go Worker, and PostgreSQL in the same stack. The web app is published on port `5656` by default; only Nginx exposes a host port. The Core API remains private behind Nginx.

## Update an existing Git stack

Use the existing Portainer stack. Keep its repository, branch, and stack name; do not add a second stack. Java stacks already use Compose path `compose.yaml`. Prior Go stacks using `compose.go.yaml` can keep that path because it remains a compatibility symlink to the same root Compose file.

The Nginx web service deliberately keeps the old Java Compose service ID, `101conv`. Compose therefore replaces that existing service container and releases its port during the stack update instead of leaving the Java container behind as an orphan. Before updating:

If a separate parallel Go stack is still running and publishes the same `PORT`, stop that stack's web service before redeploying this stack. Preserve its volumes and data directory; do not remove them during the cutover.

1. Back up the existing `DATA_PATH` directory and, if this stack has already run the Go deployment, back up its PostgreSQL, uploads, and results named volumes using your normal Docker backup procedure. Do not remove volumes or enable any “prune volumes” option.
2. Keep the current `DATA_PATH` and `PORT` stack variables. The Java application used `DATA_PATH` for its H2 database and user files. The new stack leaves those files in place and stores Go management state under `DATA_PATH/go-data` by default. It does not import Java accounts, H2 history, or Java converter configuration.
3. Create the new Go state directory on the Docker host and grant the Go runtime UID/GID `65532:65532` access. For example, if the existing value is `/mnt/truenas/101conv`:

   ```sh
   sudo mkdir -p /mnt/truenas/101conv/go-data/engines
   sudo chown -R 65532:65532 /mnt/truenas/101conv/go-data
   ```

   This only creates a sibling subdirectory and does not change ownership or content of the existing Java data. If the stack already has Go engine state in another location, set the optional `GO_DATA_PATH` variable to that exact existing directory instead; back it up first and do not point it at the Java data directory.
4. In the existing stack's Environment variables table, preserve `DATA_PATH`, `PORT`, and any existing Go database settings. Add `POSTGRES_PASSWORD` and `ENGINE_ADMIN_TOKEN` if they are not already set. See the table below. For a previously deployed Go stack, reuse the exact database name, user, and password that were used with its existing `101conv-go_*` volumes; changing environment values does not rotate credentials inside an existing PostgreSQL volume.
5. In the same stack, choose **Pull and redeploy** (or the equivalent update action for your Portainer version), keep its current Compose path, and enable image building if Portainer presents that option. The Compose build has `no_cache: true` for the web and Go images, so the updated Git checkout rebuilds the frontend assets instead of reusing a stale frontend layer. Portainer must be using a standalone Docker endpoint that supports Compose builds; Swarm stack deploy does not build Dockerfiles.

The update stops/replaces the old Java container to release the same host port, then starts the new services. Expect brief downtime. Open the same address and port afterward, for example `http://<docker-host>:5656/`. Log in with `ENGINE_ADMIN_TOKEN`; Java user accounts are not migrated.

### Stack variables

| Variable | Required | Purpose |
|---|---:|---|
| `DATA_PATH` | Yes | Existing host data directory. Kept for compatibility with the Java deployment and used as the parent of the new Go state directory. |
| `POSTGRES_PASSWORD` | Yes | Strong PostgreSQL password. Use the existing value when reusing a Go PostgreSQL volume. |
| `ENGINE_ADMIN_TOKEN` | Yes | Strong shared administrator token for the Go UI and management API. This is new for Java-only installations. |
| `PORT` | No | Published web port; defaults to `5656`. Keep the current value to preserve the existing URL. |
| `GO_DATA_PATH` | No | Go state/engine host directory. Defaults to `${DATA_PATH}/go-data`. Set only to reuse an existing Go data directory. |
| `POSTGRES_DB` | No | Defaults to `101conv`; preserve the old value when reusing Go database volumes. |
| `POSTGRES_USER` | No | Defaults to `101conv`; preserve the old value when reusing Go database volumes. |
| `APP_VERSION` | No | Version label used for catalog compatibility; defaults to `0.1.0`. |

Legacy Java-only variables such as `SERVER_PORT`, `APP_DATA_DIR`, `APP_USER_CONFIG_FILE`, `SPRING_DATASOURCE_URL`, and `SPRING_SERVLET_MULTIPART_LOCATION` are no longer consumed by the default application. Keep their old values documented for rollback, but they are not required by the Go stack. `PORT` and `DATA_PATH` remain supported.

## New installation

Create the host directories before deploying. The `DATA_PATH` directory can retain the existing Java UID if you are migrating; the Go directory must be writable by UID/GID `65532:65532`:

```sh
sudo mkdir -p /mnt/storage/apps/101conv/go-data/engines
sudo chown -R 65532:65532 /mnt/storage/apps/101conv/go-data
```

In Portainer, create one Git stack using repository `https://github.com/Timisah/101conv`, branch `refs/heads/main`, and Compose path `compose.yaml`. Set `DATA_PATH`, `POSTGRES_PASSWORD`, and `ENGINE_ADMIN_TOKEN` in the Environment variables table. Leave `PORT` unset for port `5656`. The Go data bind must exist; Compose will fail safely if it does not.

## Storage and rollback

The root Compose project keeps the existing Portainer stack name unchanged. Docker derives the PostgreSQL, uploads, and results volume names from that stack name, so those volumes remain attached when the same stack is redeployed. The Compose update does not declare volume deletion and never migrates, overwrites, or removes Java files. The Go state defaults to the separate `DATA_PATH/go-data` directory. Java data stays available on the host for manual inspection or rollback, but no automatic Java-to-Go data importer currently exists.

To roll back, restore the previous Compose definition from Git (or redeploy the earlier commit) and keep all data paths and named volumes. Do not remove the Go named volumes or the `go-data` folder during rollback. Java accounts, H2 job history, and Java profile definitions remain in the old directory but are not visible in the new interface.

Portainer Git stacks run Docker builds from the selected commit. The Compose file disables build cache for the web image to ensure the React app is regenerated from the latest checkout. Automatic updates still depend on Portainer detecting or being asked to pull the new Git commit; a running container does not update merely because a commit exists.
