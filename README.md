# 101conv

101conv is a self-hosted file conversion application. The default production stack is defined in [`compose.yaml`](compose.yaml) and runs the React/Nginx web app, Go Core, restricted conversion Worker, and PostgreSQL.

## Deploy with Portainer

Use one Portainer Git stack for `https://github.com/Timisah/101conv`, branch `main`, Compose path `compose.yaml`. For an existing Java installation, follow the [in-place migration and rollback guide](docs/deployment/portainer.md) before pulling and redeploying. It preserves the Java data directory and existing Go named volumes; it does not import Java accounts, history, or converter definitions.

Set these variables in the Portainer stack:

- `DATA_PATH`: existing directory on the Docker host. The default Go state location is its `go-data` subdirectory; prepare that directory for UID/GID `65532:65532` as described in the guide.
- `POSTGRES_PASSWORD`: a strong database password.
- `ENGINE_ADMIN_TOKEN`: a strong administrator token for browser login and management APIs.
- `PORT`: optional published port, default `5656`.

Only the web service publishes a host port. The Go Core API is private behind Nginx. Portainer must build Compose services from the Git checkout; the web service rebuilds without build cache when redeployed so updated frontend files are served. Existing deployments should keep their current `DATA_PATH` and `PORT` values.

The frontend and versioned API are documented in [`web/README.md`](web/README.md) and [`docs/api/v1/openapi.yaml`](docs/api/v1/openapi.yaml). For the real browser conversion smoke test, see [`scripts/phase5-browser-smoke.sh`](scripts/phase5-browser-smoke.sh).
