# React web client

The React and TypeScript application in `web/` is served by Nginx in the root Compose stack. The `web` service listens on container port `5656`; Compose publishes the configurable `PORT` (default `5656`). Core and PostgreSQL remain private. HTML responses revalidate so a browser sees new frontend builds; fingerprinted assets may be cached.

## User experiences

- **Convert and Compress:** upload supported files, choose a compatible profile/output, submit durable jobs, monitor status, cancel jobs, and download results. Compression is a distinct workflow and selects the source extension by default when supported.
- **Jobs and history:** filter/search owned jobs and inspect status and results.
- **Converter setup:** administrators manage engines, conversion profiles, compression profiles, and repositories as separate views. Multiple profiles can share one engine install.
- **Users & Access:** administrators can manage users, groups, reusable roles, granular permissions, API-key access, explicit quota inheritance/limits, usage, and bulk changes. Effective permission origins are displayed for inspection.
- **Account:** users inspect permissions and quota usage and manage their own scoped API keys/password.
- **Administration:** repository trust and system-level settings are separate from account access management.

## Authentication and policy

The configured administrator token is accepted as a passphrase but is never stored or returned by the browser. User passwords are Argon2id hashes. Browser sessions use HttpOnly, SameSite cookies. API keys are stored as hashes and combine endpoint scopes with current account permissions. The Core reloads user policy for authenticated requests so changed group membership or account state takes effect immediately.

The API exposes roles, groups, memberships, user permission overrides, and `limited`/`unlimited`/`inherit` quota modes. Group denial wins over a grant from another group; a direct user override takes precedence over inherited role and group rules. Usage reservations are serialized in PostgreSQL. Administrative changes are written to the audit table.

## Validation

`cd web && npm run lint && npm run build` runs TypeScript checking and produces the production bundle. `scripts/phase5-browser-smoke.sh` runs a real conversion through temporary Compose services and headless Chromium, including mobile layout checks. PostgreSQL-backed account, permission, quota, and API authorization tests run when `101CONV_TEST_DATABASE_URL` is set.

## Remaining work

Profile-by-profile grants, API-key expiry, and multi-factor identity are not implemented. Browser sessions are process-local and do not survive Core restart. Audit records are retained in PostgreSQL, but the UI does not yet provide an audit browser/export. The Storage view currently covers estimates and abandoned-upload retention; full lifecycle policy remains a follow-up. Full backup and isolated restore tools exist, while scheduling and production restore orchestration do not. Container isolation still requires verification on each deployment host.
