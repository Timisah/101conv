# Go Core and Worker

The Go modular monolith provides the versioned HTTP API, authentication and access policy, converter/profile/repository managers, durable PostgreSQL jobs, and a restricted conversion Worker. Requirements: Go 1.25 or newer.

```sh
cd server
go test ./...
go run ./cmd/101conv
```

The Core listens on port `5656` by default. `/healthz` is liveness and `/readyz` is readiness. Set `DATABASE_URL` for PostgreSQL, `DATA_PATH` for persistent state, and `ENGINE_ADMIN_TOKEN` for initial administrator access. The production Compose stack builds the Core, React/Nginx web, and Worker and publishes only the web service on host port `5656` by default.

Repository trust, package formats, and installation plans are described in [`../docs/architecture/phase3-repositories.md`](../docs/architecture/phase3-repositories.md). API schemas are in [`../docs/api/v1/openapi.yaml`](../docs/api/v1/openapi.yaml). The browser conversion smoke test is [`../scripts/phase5-browser-smoke.sh`](../scripts/phase5-browser-smoke.sh).
