# Portainer deployment — Go web application

This deployment is separate from the existing Java Spring stack documented in [Portainer deployment](portainer.md). Use the repository's `compose.go.yaml` in a Portainer Git stack. It starts the React/Nginx web service, Go Core, isolated Go Worker, and PostgreSQL. The Java `compose.yaml`, Java container, and Java data path are unchanged.

Set the following Stack environment variables in Portainer's Environment variables table:

| Variable | Required | Default / purpose |
| --- | --- | --- |
| `DATA_PATH` | Yes | Existing host directory for Go management state and managed engines. The Compose bind source must already exist. |
| `POSTGRES_PASSWORD` | Yes | Strong database password. |
| `ENGINE_ADMIN_TOKEN` | Yes | Strong shared administrator passphrase. The browser exchanges it for an HttpOnly session cookie; it is not included in frontend assets. |
| `PORT` | No | Published web port, default `5656`. The Go Core remains private on 5656. |

The stack continues to use named volumes for PostgreSQL, uploads, and results. Choose a Go `DATA_PATH` that can be mounted read/write by the Core and read-only by the Worker. Do not point it at the Java application's data directory. Portainer builds the React bundle and Go image from the same repository checkout.

The web service is the only published service. For access beyond a trusted local network, place it behind a TLS reverse proxy, and configure that proxy to replace `X-Forwarded-Proto` with `https`. Do not expose the Go Core port separately. The backend marks the browser cookie Secure when it receives TLS or the trusted forwarded-protocol header.

The Official Catalog is not installable until its trust root is provisioned. This interface reports that state explicitly. Community repository roots must be verified independently before pinning them.

For deployment issues or remaining backend limitations, see [Phase 5 web architecture](../architecture/phase5-web.md).
