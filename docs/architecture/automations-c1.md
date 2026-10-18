# Mounted drives and automation model (C.1)

## Storage boundary

Docker/Portainer mounts `AUTOMATION_HOST_PATH` into Core at `/mounts`; `AUTOMATION_MOUNT_ROOTS` is the allowlist of container-visible roots. Core rejects paths outside those roots, non-absolute paths, missing folders, and symlink escapes. A drive registration has a stable random ID, label, container path, access mode, health, available capacity when measurable, and reference count. Renaming a drive preserves its ID. Removing a registration referenced by a current automation revision is rejected.

Only Core receives the folder mount. The conversion Worker continues to see only its existing engine, upload, and result mounts. Drive registration does not create a Docker mount or change host permissions. For safe operation, keep inputs read-only at the host mount/ACL level when possible and grant output access only to folders intended for generated files.

## Declarative definitions and revisions

An automation revision contains a folder-scan or manual trigger, a stable input-drive ID, bounded interval/stability/concurrency values, declarative file filters/conditions, a conversion or compression profile ID and exact profile version, target format, destination-drive ID, and a non-overwriting output policy. AND/OR/NOT condition trees are bounded to 8 levels and 32 nodes; they can compare extension, filename glob, and byte size. Unknown operators and executable expressions are rejected.

Saving edits inserts a new immutable `conv_automation_revisions` row and switches the automation's active revision. Each submitted job is created through the existing job service with the revision's exact profile version; the existing job execution spec pins the resolved engine version and arguments. A queued job fails clearly if the selected profile version is no longer installed. Existing jobs are not rewritten by later edits.

The PostgreSQL tables are created idempotently at Core startup:

- `conv_mounted_drives` stores stable drive IDs and registration state.
- `conv_automations` stores ownership, active revision, pause/block status, and scan timestamps.
- `conv_automation_revisions` stores immutable definitions and built-in block version pins.
- `conv_automation_runs` stores per-file observation, match/skip, retry, job, output, and error history.
- `conv_generated_files` tracks output content fingerprints to prevent output loops.

## Scanning and file publication

The current trigger uses bounded top-level directory polling, not recursive traversal or an event watcher. Each scan examines at most 250 entries and 512 MiB and submits at most 20 files. A file must be a regular non-symlink file and remain size/mtime stable for its configured interval before its contents are hashed and copied into the normal upload service. An unchanged name/content fingerprint is processed once. By default, existing files are recorded as skipped on the first scan; the blueprint has an explicit first-scan option.

Automation concurrency is 1 by default and bounded to 8 per automation. Submissions honor the owner's current permissions and quotas. Retries use bounded backoff and the same pinned revision; a missing original, unavailable drive, disabled profile, or incompatible profile blocks the operation with history rather than selecting a replacement.

After a successful job has a registered result, Core writes it to a temporary file in the destination directory, syncs it, then publishes with a no-overwrite hard link. The destination filesystem must support this same-filesystem atomic publication. Output names are unique by default; `skip_existing` and create-only behavior are also available. Replacement is not implemented. Inputs are retained. Generated content fingerprints stop folder loops.

## API

The versioned endpoints are documented in [`../api/v1/openapi.yaml`](../api/v1/openapi.yaml):

- `GET/POST /api/v1/drives`, `PUT/DELETE /api/v1/drives/{id}`, `POST /api/v1/drives/test`
- `GET /api/v1/automation-blocks`, `GET /api/v1/blueprints`, `POST /api/v1/blueprints/{id}/install`
- `GET/POST /api/v1/automations`, `GET/PUT/DELETE /api/v1/automations/{id}`
- `PATCH /api/v1/automations/{id}/enabled`, `POST .../dry-run`, `POST .../run`, `GET .../history`

Drive, automation, read, and execute permissions are enforced by the Core on every route. Browser sessions are same-origin protected; API keys are not accepted for management routes. Guest identities have no management permissions.

## Blueprint and extension compatibility contracts

Built-in blueprints are versioned definitions. Installing one copies its selected configuration into a new, independently editable automation; changing a built-in blueprint later does not mutate installed automations. Current blueprints are **Automatic Folder Conversion** and **Automatic Compression**.

`ExtensionManifest` is a declarative contract with package ID, publisher, package version, extension API range, optional external application/version, permissions/capabilities, block definitions, dependencies, integrity metadata, and configuration schema version. `SelectCompatibleExtension` chooses the newest semver release whose declared extension API range contains the running API version; incompatible newest releases do not hide older compatible ones. No extension packages are downloaded, activated, or executed in C.1. There is no third-party connector runtime, marketplace extension UI, external application probing, migration runner, or rollback activation yet. Those require a separate constrained service and a future milestone.

## Operational limitations

- Polling currently handles only files directly in the registered directory, not nested trees.
- There is no filesystem event watcher; scan interval controls detection latency.
- Current inspection uses filesystem metadata and content hashing. Network filesystems with unstable metadata may need longer stability windows.
- The output filesystem must support atomic hard links. Cross-device replacement and overwrite are intentionally unsupported.
- PostgreSQL and app-managed files are included in normal backups. Arbitrary host-mounted folders remain administrator-owned backup sources.
- This automation subsystem is Core-managed; only the converter process remains in the isolated Worker. A future milestone may split ingestion/publication into a narrower service if threat analysis requires it.
