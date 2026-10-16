# 101conv

101conv is a self-hosted file conversion application. The supported application is a React web client, Go Core and restricted Worker, and PostgreSQL, deployed together from [`compose.yaml`](compose.yaml).

## Deploy with Portainer

Create or update one Git stack from `https://github.com/Timisah/101conv`, branch `main`, Compose path `compose.yaml`. The web interface is published on port `5656` by default. Only the web service publishes a host port; Core and PostgreSQL stay private.

Set these variables in the Portainer stack's Environment variables table:

- `DATA_PATH`: host directory whose `go-data` subdirectory contains Go management state and installed engines. For an existing installation, retain its current value. The directory `${DATA_PATH}/go-data` must already exist and be writable by UID/GID `65532:65532`.
- `POSTGRES_PASSWORD`: strong PostgreSQL password. When retaining an existing database volume, keep its original database credentials.
- `ENGINE_ADMIN_TOKEN`: strong administrator login/API token.
- `PORT`: optional published web port; defaults to `5656`.

Optionally set `GO_DATA_PATH` to the exact existing Go data directory instead of using `${DATA_PATH}/go-data`. Do not delete or replace the PostgreSQL, uploads, results, or Go data storage when updating the stack. Compose builds the frontend and backend from the checked-out commit without build cache, while HTML revalidation makes the newly built interface visible after deployment.

For a new deployment, create the Go data directory before deploying. For an existing installation, preserve the current path and named volumes. See the [Portainer deployment guide](docs/deployment/portainer.md) for storage, variables, and update details.

The browser uses the versioned API documented in [`docs/api/v1/openapi.yaml`](docs/api/v1/openapi.yaml). Frontend development instructions are in [`web/README.md`](web/README.md); the browser conversion smoke test is [`scripts/phase5-browser-smoke.sh`](scripts/phase5-browser-smoke.sh).
