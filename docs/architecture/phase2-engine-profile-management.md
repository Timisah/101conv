# Phase 2: local engine and profile management

The Go service now manages durable engine installations and conversion profiles.
The Java service and its existing converter configuration remain unchanged.

## Lifecycle and API

Management routes are under `/api/v1` and require `Authorization: Bearer <ENGINE_ADMIN_TOKEN>`:

- `GET /engines`, `POST /engines/install` install a local package directory, `PUT /engines/{id}` install a new immutable engine version, `POST /engines/manual` register administrator-specified executable paths, and `DELETE /engines/{id}/{version}` uninstall an engine.
- `GET /profiles`, `POST /profiles` install a manual profile, `PUT /profiles/{id}` update it, `PATCH /profiles/{id}/enabled` enable/disable it, `DELETE /profiles/{id}` uninstall it, and `GET /profiles/{id}/engines` resolve its installed dependencies.

An install request supplies an engine definition and a local `sourceDirectory`; every executable must be explicitly named by a safe relative path. Files are copied into private versioned storage by staging, syncing, and atomic rename. Repository URLs and archives are rejected. A profile install resolves engine ID, compatible version range, and required capabilities before it is made active. Resolution chooses the highest compatible installed version, allowing profiles with different constraints to share binaries.

Engine versions are immutable and can coexist during updates. Profile compatibility ranges decide which version resolves. Uninstall returns a conflict while an enabled profile can use the target engine. `?resolveDependencies=true` explicitly disables those profiles in the same durable state update before removing the engine.

Persistent state is stored in `DATA_PATH/management.json`; managed executables live under `DATA_PATH/engines`. Docker sets `DATA_PATH=/data/101conv` and prepares that directory for the unprivileged runtime user. Mount this directory on persistent storage. For local development, the default is `./data`. Set a long random `ENGINE_ADMIN_TOKEN` before enabling management; if it is absent, management APIs return `503`.

## Security and migration limits

Only local/manual installs are enabled. Signed repository verification is still absent, so the service will not download or extract remote engine binaries. Managed installs reject symlinks and unsafe paths, copy only declared executable files, and expose an installation only after filesystem and state writes succeed. Startup removes abandoned staging/orphaned version directories and fails if persisted managed engine data is missing. Administrator-provided manual paths are recorded but not copied or owned.

No existing Java `data/profiles` directories or profile metadata are automatically imported. The Phase 1 legacy adapter remains available as a translation utility, but operators must register converted definitions through the new API. No current Java route or on-disk data is changed. The Go API is not yet used by the existing Java UI or conversion worker; this phase provides management and dependency resolution only.
