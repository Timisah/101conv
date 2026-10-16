# 101conv web application

The React application is served by the Nginx web role in the root `compose.yaml` (service ID `101conv` preserves in-place replacement of the old Java container). Nginx listens on port `5656` inside the container, and Portainer publishes it on `PORT` (default `5656`). It proxies `/api/`, `/healthz`, and `/readyz` to the private Core service. Only the web role publishes a host port. Set `DATA_PATH`, `POSTGRES_PASSWORD`, and `ENGINE_ADMIN_TOKEN` as stack environment variables as described in the [Portainer migration and deployment guide](../docs/deployment/portainer.md). Existing Java data remains under `DATA_PATH`; new Go management state defaults to `DATA_PATH/go-data`.

## Local development

```sh
npm ci
npm run dev
```

`npm run build` performs the TypeScript check and creates the production bundle. `npm run lint` runs the TypeScript checker. The browser verification script `../scripts/phase5-browser-smoke.sh` provisions a temporary PostgreSQL/Core/Worker stack, serves the frontend through Nginx, and uses Chromium to submit and download an actual conversion.

## Supported UI workflows

- Administrator login through the server's HttpOnly, SameSite Strict session cookie. The configured `ENGINE_ADMIN_TOKEN` is accepted as a passphrase but is never placed in browser storage or returned to the browser.
- Uploads up to the backend's 100 MiB input limit, compatible enabled-profile selection, asynchronous conversion, cancellation, status monitoring, job history search/filtering, and result download.
- Engine/profile inventory, profile enable/disable/removal, engine removal with confirmation, and compatible installed engine-version selection.
- Trusted repository browsing, signed package previews and install confirmation, profile update previews, repository refresh/enable/disable/removal, and trust-root pinning.
- Theme toggle with system-preference default and responsive desktop/mobile navigation.

The backend currently exposes one shared administrator credential, not user accounts, roles, or API-key management. Conversion profiles have fixed argument templates, so the UI has no per-job options. The API has no retry endpoint, so retry is not presented. Worker-specific health, Storage controls, Engine update previews, and trusted bundled-official trust-root provisioning are not exposed; corresponding UI states say so instead of showing fake controls. Engine binaries may be installed once and shared, while profile versions can be updated via a reviewed repository plan.

For remote administration, terminate TLS at a trusted reverse proxy and configure it to replace `X-Forwarded-Proto` with `https`. The session cookie becomes Secure when the Core receives TLS or that trusted forwarded-protocol header. The in-memory session registry logs out sessions when Core restarts; a single Core instance is expected for this deployment.
