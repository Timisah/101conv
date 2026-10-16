# Go replacement backend

This is the start of the replacement service, developed alongside the existing application until the migration gates in [`../docs/architecture/target-system.md`](../docs/architecture/target-system.md) pass.

Requirements: Go 1.24 or newer.

```sh
cd server
go test ./...
go run ./cmd/101conv
```

The service listens on port `5656` by default. Set `PORT` to change it and `APP_VERSION` to set the release used for catalog compatibility checks. `/healthz` is the liveness endpoint, `/readyz` reports the running version, and `/api/v1/catalogs` exposes the preconfigured 101conv Official Catalog source. No profiles or third-party converters are installed on a fresh instance.

Build the minimal static container from this directory with `docker build -t 101conv-next .`. Run it with `docker run --rm -p 5656:5656 101conv-next`. This image is an architecture smoke test, not the replacement deployment stack; PostgreSQL, durable jobs, and the separated Worker are not wired into it yet.

## Local engine/profile management (Go service)

Management APIs require a persistent `DATA_PATH` and a strong `ENGINE_ADMIN_TOKEN` environment variable. They accept local/manual engine binaries only; repository-based remote binary installation remains disabled until signed metadata verification is implemented. See [`../docs/architecture/phase2-engine-profile-management.md`](../docs/architecture/phase2-engine-profile-management.md) for routes, request behavior, storage, and migration limits. In Docker, persist `/data/101conv` and provide the admin token.
