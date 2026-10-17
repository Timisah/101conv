# 101conv

101conv is a self-hosted file conversion application. The supported application is a React web client, Go Core and restricted Worker, and PostgreSQL, deployed together from [`compose.yaml`](compose.yaml).

## New installation

On a Docker host with Compose and OpenSSL, run:

```sh
git clone https://github.com/Timisah/101conv.git && cd 101conv && ./scripts/install.sh
```

The installer creates private, random database and bootstrap credentials in `.env`, prepares `data/go-data`, and starts the existing Compose stack. Open `http://<docker-host>:5656/`, enter the bootstrap passphrase from `.env`, and create the first administrator account. Keep `.env` private and include it in your secure configuration backup. The setup page checks writable storage and shows the path to installing a first profile; it does not download converter binaries without your action. PostgreSQL, uploads, and results use Docker named volumes. The local `data/go-data` bind contains engine binaries, profile/repository management state, and trust settings. Worker scratch files use a nonpersistent 1 GiB tmpfs. The installer refuses to adopt an existing `data/go-data` without explicit configuration.

## Deploy with Portainer

Create or update one Git stack from `https://github.com/Timisah/101conv`, branch `main`, Compose path `compose.yaml`. The web interface is published on port `5656` by default. Only the web service publishes a host port; Core and PostgreSQL stay private.

Set these variables in the Portainer stack's Environment variables table:

- `DATA_PATH`: host directory whose `go-data` subdirectory contains Go management state and installed engines. For an existing installation, retain its current value. The directory `${DATA_PATH}/go-data` must already exist and be writable by UID/GID `65532:65532`.
- `POSTGRES_PASSWORD`: strong PostgreSQL password. When retaining an existing database volume, keep its original database credentials.
- `ENGINE_ADMIN_TOKEN`: strong administrator login/API token.
- `PORT`: optional published web port; defaults to `5656`.

Optionally set `GO_DATA_PATH` to the exact existing Go data directory instead of using `${DATA_PATH}/go-data`. Do not delete or replace the PostgreSQL, uploads, results, or Go data storage when updating the stack. Compose builds the frontend and backend from the checked-out commit without build cache, while HTML revalidation makes the newly built interface visible after deployment.

For a new Portainer deployment, create the Go data directory before deploying and supply a random `ENGINE_ADMIN_TOKEN` of at least 24 characters for first-run claim. For an existing installation, preserve the current path and named volumes. See the [Portainer deployment guide](docs/deployment/portainer.md) for storage, variables, and update details.

Administrators can inspect storage estimates, configure manual or time-based cleanup of **unsubmitted uploads only**, preview deletions, and check Worker/database readiness in the Storage and Operations views. Other originals and results remain indefinitely. A verified, stopped-writer full backup and isolated restore test are available in [`docs/deployment/backup.md`](docs/deployment/backup.md). The [Milestone A implementation map](docs/architecture/maturity-milestone-a.md) distinguishes completed work from the remaining product roadmap.

The browser uses the versioned API documented in [`docs/api/v1/openapi.yaml`](docs/api/v1/openapi.yaml). Frontend development instructions are in [`web/README.md`](web/README.md); the browser conversion smoke test is [`scripts/phase5-browser-smoke.sh`](scripts/phase5-browser-smoke.sh).
