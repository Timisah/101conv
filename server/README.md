# Go replacement backend

This is the start of the replacement service, developed alongside the existing application until the migration gates in [`../docs/architecture/target-system.md`](../docs/architecture/target-system.md) pass.

Requirements: Go 1.25 or newer.

```sh
cd server
go test ./...
go run ./cmd/101conv
```

The service listens on port `5656` by default. Set `PORT` to change it and `APP_VERSION` to set the release used for catalog compatibility checks. `/healthz` is the liveness endpoint, `/readyz` reports the running version, and `/api/v1/catalogs` exposes the preconfigured 101conv Official Catalog source. No profiles or third-party converters are installed on a fresh instance.

Build the minimal static container from this directory with `docker build -t 101conv-next .`. Run it with `docker run --rm -p 5656:5656 101conv-next`. This image is an architecture smoke test, not the replacement deployment stack; PostgreSQL, durable jobs, and the separated Worker are not wired into it yet.

## Local engine/profile management (Go service)

Management APIs require a persistent `DATA_PATH` and a strong `ENGINE_ADMIN_TOKEN` environment variable. Manual engine operations are restricted to administrator-approved paths. See [`../docs/architecture/phase2-engine-profile-management.md`](../docs/architecture/phase2-engine-profile-management.md) for the engine/profile lifecycle and migration limits. In Docker, persist `/data/101conv` and provide the admin token.

## Repository trust and installation

Repository browsing, previews, and package installation are in [`../docs/architecture/phase3-repositories.md`](../docs/architecture/phase3-repositories.md). Provide a strong `ENGINE_ADMIN_TOKEN`. By default manual engine imports are restricted to `DATA_PATH/imports`; set `ENGINE_ALLOWED_PATHS` to an OS path-list when administrators need other host directories. Community and local catalog sources require a TUF root whose SHA-256 fingerprint is independently verified and explicitly approved. The current official catalog has no published TUF root, so official remote installs remain disabled. Managed converters are not sandboxed; do not expose conversion execution until a restricted Worker is implemented.
