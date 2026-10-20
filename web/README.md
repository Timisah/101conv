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
- Inspect an owned upload with bounded content-derived metadata, privacy-aware location redaction, and sanitized JSON export.
- Start a Build document with non-destructive page ordering, rotation, duplication, removal, undo/redo, and a prepared manifest handoff.
- Manage shared engines and separately typed conversion/compression profiles. Browse repositories and review trust/install previews before installing packages.
- Use responsive desktop/mobile layouts and the system-aware light/dark theme.

Authentication uses HttpOnly browser sessions. `ENGINE_ADMIN_TOKEN` is never sent back to the browser. User/group/role and API-key operations are protected by the current effective server permissions. Changes to a user's role, groups, permissions, or enabled state are reloaded for authorization on each API request.

## Responsive UI checks

With the Vite dev server running, run `npm run test:ui` in another terminal.
The Chromium checks use deterministic API fixtures to verify desktop, tablet,
and phone layouts, upload selection and submission, guest batch limits, theme
switching, mobile navigation, and sign-in errors. They do not run a converter.
Set `UI_BASE` to test another frontend URL, `CHROMIUM_PATH` to select Chromium,
and `UI_ARTIFACT_DIR` to change the screenshot directory (default: `/tmp/101conv-ui`).
The Docker browser suite above verifies real server and worker integration.

## Appearance

The default theme follows the operating system, including live changes. Choose
System, Always light, or Always dark in **Account & API keys → Appearance** or
with the header theme picker. Explicit preferences are saved in this browser;
they are not synchronized through the user account. Values saved by the old
two-way toggle are ignored so existing browsers start with System.

A small same-origin head script applies the resolved theme, native color scheme,
and browser/status-bar color before React loads. The mobile header accounts for
the device's top safe area. Run `UI_BASE=http://127.0.0.1:4173 npm run test:theme`
against `npm run build && npm run preview -- --port 4173` to verify initialization,
OS changes, explicit overrides, persistence, cross-tab updates, and browser
metadata. These Chromium checks do not emulate the native status bar of a
physical iPhone or Android device.
