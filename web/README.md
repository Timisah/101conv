# 101conv web application

The React application is served by the Nginx `web` service in the root `compose.yaml`. It proxies `/api/`, `/healthz`, and `/readyz` to the private Go Core. Only the web service publishes a host port (`PORT`, default `5656`). Persistent Core state defaults to `${DATA_PATH}/go-data`; retain the current `DATA_PATH`, optional `GO_DATA_PATH`, and named database/file volumes when updating a deployment. See the [Portainer deployment guide](../docs/deployment/portainer.md).

## Local development

```sh
npm ci
npm run dev
npm run lint
npm run build
```

The browser verification script `../scripts/phase5-browser-smoke.sh` provisions a temporary PostgreSQL/Core/Worker stack, serves the frontend through Nginx, and uses Chromium to submit and download an actual conversion.

## Supported workflows

- Administer users, groups, reusable roles, granular permissions, API keys, and explicit per-user/group quotas from Users & Access. The UI displays effective permission grants and their role/group/user origins, monthly usage, bulk-action results, and the server-side enforcement is authoritative.
- Convert or compress supported files, view job history, monitor jobs, cancel them, and download completed results.
- Manage shared engines and separately typed conversion/compression profiles. Browse repositories and review trust/install previews before installing packages.
- Use responsive desktop/mobile layouts and the system-aware light/dark theme.

Authentication uses HttpOnly browser sessions. `ENGINE_ADMIN_TOKEN` is never sent back to the browser. User/group/role and API-key operations are protected by the current effective server permissions. Changes to a user's role, groups, permissions, or enabled state are reloaded for authorization on each API request.
