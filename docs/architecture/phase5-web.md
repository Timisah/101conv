# Phase 5 — web application

Phase 5 adds a standalone React + TypeScript application in `web/` and serves it with Nginx in the Go Compose deployment. The deployment migration now makes this the default application in the root `compose.yaml`, replacing the Java runtime in the default stack while retaining Java source and the old bind-mounted data for rollback. The browser and Core share only the private `web-core` network. Core retains its outbound network for repository metadata, and the Worker remains confined to the database network plus its existing read-only engine/upload and writable result mounts.

The public port remains configurable with `PORT`, default `5656`. It publishes Nginx on that port; Core listens on private port 5656 and is no longer published. Existing Go data path and named volumes are unchanged. For remote administration, terminate TLS at a trusted reverse proxy and have it overwrite `X-Forwarded-Proto: https`; the opaque cookie is marked Secure when TLS reaches Core or this trusted forwarded header is set.

## Experiences

- Convert is the homepage. It supports drag/drop and regular file selection, up to 20 files in a batch, up to the backend 100 MiB per file, detected file extensions, only enabled profile/target combinations that support every selected input, upload and asynchronous job submission, active progress, cancellation from history, and result downloads.
- Jobs & history is backed by the durable PostgreSQL job table. It filters by status and searches profile, extensions, and job ID. Status is refreshed every five seconds. There is no retry API, so the UI does not show a retry button.
- Installed converters separates profiles and engines. It displays profile-supported extensions, versions, dependency/capability summaries, enabled state, allows profile enable/disable and removal, lets an administrator choose among installed compatible engine versions, and confirms engine removal.
- Marketplace browses only enabled, trusted TUF catalogs, presents a preview bound to the exact verified plan and dependency set before installation, distinguishes installed packages, and supports profile update previews. The UI explicitly explains that the bundled Official catalog has no provisioned trust root; it does not list that catalog as installable. Incompatible signed profiles and repositories lacking trust are labeled.
- Administration exposes repository trust states, root pinning, refresh, enable/disable, removal, the public Core health/readiness checks, and honest limitations for Worker health/storage controls.

## Browser authentication

The shared administrator passphrase remains `ENGINE_ADMIN_TOKEN`, but React never receives or stores it. Login POSTs it to `POST /api/v1/auth/session`; the Go API compares it in constant time and returns an opaque random token in an HttpOnly, SameSite Strict cookie. Only a hash of that token is held in memory. Cookie-authenticated writes must include a same-origin Origin and pass Fetch Metadata checks. The bearer API continues to work for current scripts and external API clients. Sessions expire after 12 hours and are invalidated when Core restarts. This phase does not add user accounts, per-user job ownership, roles, or API-key management.

## Backend limits reflected in the UI

Profiles expose fixed argument arrays, not per-job options, so the conversion workspace does not invent options. Batch conversion submits one durable job per file. Jobs cannot be retried through the current API. The API does not expose a dedicated Worker health check, storage controls, or update preview for engine packages; these are shown as unavailable. Engine updates can be installed as separately versioned compatible installations but the UI does not promise an upgrade workflow without an engine-update plan endpoint. No user/API-key administration is shown because the backend has no such feature.

## API additions

- `GET|POST|DELETE /api/v1/auth/session` supports browser login, state, and logout.
- `GET /api/v1/jobs?status=&q=&limit=&offset=` lists durable jobs for history. Search wildcard characters are escaped; status and pagination are bounded.
- Existing versioned management, repository, upload, status, cancel, and download endpoints remain the frontend's business-logic source.
- Nginx proxies `/api/`, `/healthz`, and `/readyz`. OpenAPI documents bearer and browser-session authentication.

## Validation

The React production build and TypeScript check use `cd web && npm run build && npm run lint`. `scripts/phase5-browser-smoke.sh` builds the Go service and real PPM→PGM converter, launches Core/Worker/PostgreSQL/Nginx in a unique test Compose project, registers an engine/profile fixture, and uses a headless Chromium session over the private Compose network to log in, upload a file, submit it, wait for completion, download and inspect the converted PGM pixels, and verify mobile navigation/no horizontal overflow. The E2E browser image is test-only and not used in deployment.

The job-list query and administrator-cookie tests are in the Go suite. Docker's behavior in this environment was verified through that actual Compose flow; remote reverse-proxy TLS behavior depends on the production proxy configuration and still needs deployment-host verification.

## Remaining integration gaps

- Authentication is one shared admin passphrase, with no user identity or per-user authorization. Anyone holding it has full management and job access.
- Cookie sessions are held in memory and do not survive Core restart. Run a single Core instance behind TLS; multi-replica sessions need shared storage or an equivalent design.
- Configure a trusted TLS proxy for remote use. The direct published Compose port is HTTP for local/self-hosted setup and cannot protect credentials over an untrusted network.
- Worker health, disk/retention settings, per-job options, retries, user/API-key management, engine update plans, and official repository trust provisioning remain backend work.
- The inherited browser smoke validates representative desktop and mobile widths and a real conversion, not an exhaustive cross-browser/device accessibility audit.
