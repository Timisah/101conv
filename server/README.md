# Go replacement backend

This is the start of the replacement service, developed alongside the existing application until the migration gates in [`../docs/architecture/target-system.md`](../docs/architecture/target-system.md) pass.

Requirements: Go 1.25 or newer.

```sh
cd server
go test ./...
go run ./cmd/101conv
```

The service listens on port `5656` by default. Set `PORT` to change it and `APP_VERSION` to set the release used for catalog compatibility checks. `/healthz` is the liveness endpoint, `/readyz` reports the running version, and `/api/v1/catalogs` exposes the preconfigured 101conv Official Catalog source.

Build the minimal static container from this directory with `docker build -t 101conv-next .`. The root [`../compose.yaml`](../compose.yaml) is the production React/Nginx web, Go Core, restricted Worker, and PostgreSQL deployment. It replaces the old Java runtime in the default stack while leaving Java source and host data available for rollback. Follow [`../docs/deployment/portainer.md`](../docs/deployment/portainer.md) to migrate an existing stack safely. The Go state and engines default to `GO_DATA_PATH` or `DATA_PATH/go-data`; prepare that directory writable by UID/GID `65532:65532`. Configure `POSTGRES_PASSWORD` and `ENGINE_ADMIN_TOKEN` in Portainer's stack environment table. `PORT` defaults to `5656` and is configurable. The stack creates persistent named volumes for PostgreSQL, uploads, and results.

## Local engine/profile management (Go service)

Management APIs require a persistent `DATA_PATH` and a strong `ENGINE_ADMIN_TOKEN` environment variable. Manual engine operations are restricted to administrator-approved paths. See [`../docs/architecture/phase2-engine-profile-management.md`](../docs/architecture/phase2-engine-profile-management.md) for the engine/profile lifecycle and migration limits. In Docker, persist `/data/101conv` and provide the admin token.

## Repository trust and installation

Repository browsing, status/refresh/enable controls, immutable previews, and package installation are documented in [`../docs/architecture/phase3-repositories.md`](../docs/architecture/phase3-repositories.md). The public Official catalog is fetched from GitHub for browsing while unsigned. An administrator may explicitly approve unsigned official installs from the Administration UI; this checks package hashes and safe extraction but does not authenticate publisher identity. Trusted repositories use TUF and require the matching short-lived `planId` returned by preview. Provide a strong `ENGINE_ADMIN_TOKEN`. By default manual engine imports are restricted to `DATA_PATH/imports`; set `ENGINE_ALLOWED_PATHS` to an OS path-list when administrators need other host directories. Community and local catalog sources require a TUF root whose SHA-256 fingerprint is independently verified and explicitly approved.

## Conversion API and Worker

The Core and Worker require PostgreSQL via `DATABASE_URL`. The Core stores an upload, resolves an enabled installed profile to a managed engine and validated argv template, and persists a job. The separate Worker claims jobs with PostgreSQL leases and executes the pinned managed engine version in a private attempt directory. Clients cannot provide executable paths or command lines. The Worker receives a read-only engine mount, read-only uploads, a writable result volume, and a bounded no-exec tmpfs workspace. It has no public network, Docker socket, or repository trust-store mount.

The browser uses an opaque HttpOnly administrator session cookie; existing API clients can continue using the bearer token. This is a single shared administrator identity, not a multi-user deployment. The upload, job-list, and management APIs are documented in [`../docs/api/v1/openapi.yaml`](../docs/api/v1/openapi.yaml). React assets are built from `web/` during the root Compose deployment; only Nginx is exposed at the configurable host port (default `5656`), and the Core is private. To verify the actual browser conversion path with Chromium and isolated containers, run `../scripts/phase5-browser-smoke.sh` with Docker Compose available.

See [`../docs/architecture/phase5-web.md`](../docs/architecture/phase5-web.md) for the current frontend, auth model, API coverage, test results, and integration gaps, and [`../docs/architecture/phase4-conversion-worker.md`](../docs/architecture/phase4-conversion-worker.md) for the underlying durable job and Worker design. The scratch runtime requires self-contained/static converter binaries; many system FFmpeg builds depend on dynamic libraries and need an explicit runtime image decision before deployment.
