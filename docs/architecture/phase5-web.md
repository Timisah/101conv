# Phase 5 — web application

Phase 5 adds a standalone React + TypeScript application in `web/` and serves it with Nginx in the Go Compose deployment. The deployment migration now makes this the default application in the root `compose.yaml`, replacing the Java runtime in the default stack while retaining Java source and the old bind-mounted data for rollback. Nginx listens on container port `5656`; Compose publishes only the configurable host port `PORT` (default `5656`) on the non-internal web ingress bridge. Core retains its outbound network for repository metadata, and the Worker remains confined to the internal database network plus its existing read-only engine/upload and writable result mounts.

The public port remains configurable with `PORT`, default `5656`. It publishes Nginx on that port; Core listens on private port 5656 and is no longer published. Existing Go data path and named volumes are unchanged. For remote administration, terminate TLS at a trusted reverse proxy and have it overwrite `X-Forwarded-Proto: https`; the opaque cookie is marked Secure when TLS reaches Core or this trusted forwarded header is set.

## Experiences

- Convert is the homepage. It supports drag/drop and regular file selection, up to 20 files in a batch, up to the backend 100 MiB per file, detected file extensions, only enabled profile/target combinations that support every selected input, upload and asynchronous job submission, active progress, cancellation from history, and result downloads.
- Compress is a separate workspace backed by durable `compress` jobs. It uses profile-declared controls, defaults to the original extension when supported, allows another compatible output extension, and offers FFmpeg audio/video target-size compression plus JPEG XL quality controls.
- Jobs & history is backed by the durable PostgreSQL job table. It filters by status and searches profile, extensions, and job ID. Status is refreshed every five seconds. There is no retry API, so the UI does not show a retry button.
- Installed converters separates profiles and engines. It displays profile-supported extensions, versions, dependency/capability summaries, enabled state, allows profile enable/disable and removal, lets an administrator choose among installed compatible engine versions, and confirms engine removal.
- Marketplace browses the public GitHub-hosted Official catalog before its TUF root is provisioned. An administrator can explicitly approve unsigned installs in Administration; cards and confirmation dialogs label this state, and the UI explains that hashes do not authenticate the publisher. Trusted TUF catalogs retain the verified preview/install flow. Untrusted community catalogs remain excluded from browsing.
- Administration exposes repository trust states, root pinning, refresh, enable/disable, removal, user accounts, per-user permissions and quotas, the public Core health/readiness checks, and honest limitations for Worker health/storage controls.
- Account & API keys shows a user's current permissions and monthly usage, allows scoped-key creation/revocation, and supports password changes. A new key is shown once.

## Browser authentication

The shared administrator passphrase remains `ENGINE_ADMIN_TOKEN`, but React never receives or stores it. Login POSTs it to `POST /api/v1/auth/session`; regular users sign in with a username and password. Passwords are Argon2id-hashed in PostgreSQL. Both receive an opaque random token in an HttpOnly, SameSite Strict cookie, with only a hash held in memory. Cookie-authenticated writes must include a same-origin Origin and pass Fetch Metadata checks. Existing administrator bearer clients continue to work. User API keys use the `101c_` prefix, are stored as hashes, and can only invoke their selected conversion/job scopes. Sessions expire after 12 hours and are invalidated when Core restarts.

The administrator creates regular accounts and sets `canConvert`, `canReadJobs`, per-file maximum, monthly job count, and monthly uploaded bytes. Uploads and jobs are owned by the authenticated identity; users cannot read another account's jobs or results. Existing rows migrate to the legacy administrator owner. Monthly upload and job reservations are serialized in PostgreSQL. See [permissions and API keys](permissions-and-api-keys.md) for quota semantics and API-client examples.

## Backend limits reflected in the UI

Profiles expose fixed conversion argument arrays and declarative compression options, validated server-side before jobs are queued. A profile places compression flags with the `{compression.args}` marker, allowing its author to keep engine-specific output argument ordering valid. Batch operations submit one durable job per file. Jobs cannot be retried through the current API. The API does not expose a dedicated Worker health check, storage controls, or update preview for engine packages; these are shown as unavailable. Engine updates can be installed as separately versioned compatible installations but the UI does not promise an upgrade workflow without an engine-update plan endpoint. Target-size compression uses a duration-based bitrate estimate; jobs fail instead of returning a result above the requested cap.

## API additions

- `GET|POST|DELETE /api/v1/auth/session` supports browser login, state, and logout.
- `GET /api/v1/me`, `/me/password`, and `/me/api-keys` expose the current user's settings and scoped credential lifecycle. A raw API key is returned only by the create response.
- `GET|POST /api/v1/users`, `PATCH|DELETE /api/v1/users/{id}` are administrator-only. DELETE disables the user and revokes keys while retaining job history.
- User API keys are accepted as bearer credentials for scoped conversion and job routes. `ENGINE_ADMIN_TOKEN` remains the all-powerful global administrator credential.
- `GET /api/v1/jobs?status=&q=&limit=&offset=` lists durable jobs for history. Search wildcard characters are escaped; status and pagination are bounded.
- `POST /api/v1/compressions` accepts the same upload/profile/idempotency fields as job submission, plus profile-declared string option values, and stores the job with `mode: compress`.
- Existing versioned management, repository, upload, status, cancel, and download endpoints remain the frontend's business-logic source.
- Nginx proxies `/api/`, `/healthz`, and `/readyz`. OpenAPI documents bearer and browser-session authentication.

## Validation

The React production build and TypeScript check use `cd web && npm run build && npm run lint`. `scripts/phase5-browser-smoke.sh` builds the Go service and real PPM→PGM converter, launches Core/Worker/PostgreSQL/Nginx in a unique test Compose project, registers an engine/profile fixture, and uses a headless Chromium session over the private Compose network to log in, upload a file, submit it, wait for completion, download and inspect the converted PGM pixels, and verify mobile navigation/no horizontal overflow. The E2E browser image is test-only and not used in deployment.

The job-list query and administrator-cookie tests are in the Go suite. Database-backed auth, quota, owner-isolation, and API-scope integration tests run when `101CONV_TEST_DATABASE_URL` is set. Docker's behavior in this environment was verified through that actual Compose flow; remote reverse-proxy TLS behavior depends on the production proxy configuration and still needs deployment-host verification.

## Remaining integration gaps

- The user model is only administrator/regular user with two account permissions; there are no teams, custom roles, profile-specific rights, or concurrent-job limits.
- API keys are currently revocable but do not expire automatically. Password reset does not yet invalidate other browser sessions.
- Cookie sessions are held in memory and do not survive Core restart. Run a single Core instance behind TLS; multi-replica sessions need shared storage or an equivalent design.
- Configure a trusted TLS proxy for remote use. The direct published Compose port is HTTP for local/self-hosted setup and cannot protect credentials over an untrusted network.
- Worker health, disk/retention settings, per-job options, retries, engine update plans, and official repository trust provisioning remain backend work.
- The inherited browser smoke validates representative desktop and mobile widths and a real conversion, not an exhaustive cross-browser/device accessibility audit.
