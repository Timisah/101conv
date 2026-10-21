# Mounted drives and automation model (C.1)

## Storage boundary

Docker/Portainer mounts `AUTOMATION_HOST_PATH` into Core at `/mounts`; `AUTOMATION_MOUNT_ROOTS` is the allowlist of container-visible roots. Core rejects paths outside those roots, non-absolute paths, missing folders, and symlink escapes. A drive registration has a stable random ID, label, container path, access mode, health, available capacity when measurable, and reference count. Renaming a drive preserves its ID. Removing a registration referenced by a current automation revision is rejected.

Only Core receives the folder mount. The conversion Worker continues to see only its existing engine, upload, and result mounts. Drive registration does not create a Docker mount or change host permissions. For safe operation, keep inputs read-only at the host mount/ACL level when possible and grant output access only to folders intended for generated files.

## Declarative definitions and revisions

An automation revision contains a folder-scan or manual trigger, a stable input-drive ID, bounded interval/stability/concurrency values, declarative file filters/conditions, a conversion or compression profile ID and exact profile version, target format, destination-drive ID, and a non-overwriting output policy. AND/OR/NOT condition trees are bounded to 8 levels and 32 nodes; they can compare extension, filename glob, and byte size. Unknown operators and executable expressions are rejected.

Saving edits inserts a new immutable `conv_automation_revisions` row and switches the automation's active revision. Each submitted job is created through the existing job service with the revision's exact profile version; the existing job execution spec pins the resolved engine version and arguments. Omitting the profile version on create, blueprint installation, or update resolves the exact installed version before inserting the immutable revision. Before submission, an unavailable pinned profile blocks the run rather than selecting a replacement. Once committed, the job keeps its resolved engine/arguments even if the installed profile changes. Existing jobs are not rewritten by later edits.

C.1.1 upgrades are additive. Historical definitions and runs remain readable. A legacy active revision with an empty profile version is disabled with an actionable diagnostic; saving an edit creates a new pinned revision, then the owner can enable it. Historical definitions are never retroactively filled with today's installed version. Already submitted legacy jobs retain their execution spec and can still publish when no destination candidate exists. If a legacy destination candidate already exists, publication is blocked for review: without an old receipt Core cannot tell a foreign file from an unacknowledged C.1 publication, and will not adopt it or create a duplicate. Legacy `retry_wait` records are blocked for review because the original submission request was not durable; they are not silently resubmitted with current settings. No upload, result, account, drive, or automation history is deleted by migration.

The PostgreSQL tables are created idempotently at Core startup:

- `conv_mounted_drives` stores stable drive IDs and registration state.
- `conv_automations` stores ownership, active revision, pause/block status, and scan timestamps.
- `conv_automation_revisions` stores immutable definitions and built-in block version pins.
- `conv_automation_scans` stores input identity, scan generation, inventory/baseline completion, page cursor, and consumed request revision.
- `conv_automation_baseline` stores the complete initial filename inventory for a generation.
- `conv_automation_runs` stores per-file observation, match/skip, retry, job, output, and error history.
- `conv_generated_files` tracks output content fingerprints to prevent output loops.

## Scanning and file publication

The current trigger uses bounded top-level directory polling, not recursive traversal or an event watcher. Each scan examines at most 250 entries and 512 MiB and submits at most 20 files. A file must be a regular non-symlink file and remain size/mtime stable for its configured interval before its contents are hashed and copied into the normal upload service. An unchanged input-drive/name/content fingerprint is submitted once. A lexical page cursor lets later scans reach files beyond the first 250 entries. Listing names for inventory/pagination still enumerates the top-level directory in memory; metadata inspection and content reads are bounded per scan.

Baseline semantics are explicit:

- **First enable or manual scan:** before processing entries, capture the complete current filename inventory in one database transaction. The default records those matching files as `skipped_existing`. Names are used rather than filesystem timestamps, so future-dated files cannot bypass the baseline. Inventory completion and page completion are separate. A crash during inventory commits no partial inventory; a crash during a page retains the inventory and committed observations, and replays the unfinished page safely. `last_scan` advances only after a successful page.
- **Ordinary edit:** retain the baseline for the same input drive/path. Files that existed at baseline but only match the edited filter later are still skipped unless explicitly requested. Observed runs retain their original revision snapshot; accepted jobs never adopt later edits.
- **Input-drive or registered-path change:** invalidate the scan generation in the editing transaction and capture a new baseline. Even A → B → A edits between scans invalidate the old baseline. Existing contents are skipped by default. A consumed opt-in from a previous path does not carry across a drive-path edit.
- **Pause/resume:** retain the baseline and observations. Files newly arriving while paused remain eligible after resume. Pausing stops scheduled discovery; it does not cancel accepted submissions, running jobs, or publication. Manual Run now deliberately scans even while paused. A manual trigger is never polled automatically.
- **Explicit one-time request:** save a new revision with `processExistingOnNextScan=true`. This creates a fresh inventory/generation even after earlier scans. Existing matching names enter normal stability observation; generated content and previously submitted identical inputs are still excluded. The request is consumed when the initial inventory page traversal finishes, and pending observations can execute afterwards. Each newly saved revision with this flag is a new request; the editor defaults the checkbox to off on edit/duplicate. Historical JSON retains the requested flag while mutable scan state records its consumption.
- **Legacy upgrade:** a non-null C.1 `last_scan` is not proof of a completed baseline. Migration seeds a conservative fresh inventory without replaying any potentially consumed existing-file opt-in. An unscanned legacy record retains its initial explicit request. Files arriving during an interrupted inventory may be conservatively included in the resumed baseline; safety takes priority over automatic catch-up.

A skipped filename whose observed metadata subsequently changes is observed again. Removing/reintroducing an identical name/content never creates another job. Users should treat preserved timestamps and rapidly reused filenames conservatively and use an explicit request when intentional catch-up is required.

Automation concurrency is 1 by default and bounded to 8 per automation. Submissions honor the owner's current permissions and quotas. Submission preparation persists the run snapshot and source digest before uploading, then persists `upload_id` before job submission. One stable per-run idempotency key reconnects a committed job after a lost response, before consulting mutable profile/engine state. An interruption between upload commit and recording `upload_id` may leave an unsubmitted upload for the normal storage/retention tools; it cannot create a second job. Submission failures retry at most three times with a 30-second delay, reusing the durable upload/request. Conversion retries belong to the existing Worker's bounded attempts on that job's frozen execution spec; automation does not manufacture new jobs for failed conversions. Missing originals, unavailable drives, disabled profiles, or incompatible versions produce actionable history rather than replacements.

After a successful job has a registered result, Core rechecks that the output registration exists, is enabled, still has `read_write` access, resolves to the snapshotted approved path, and accepts an actual write probe. A database share lock serializes registration edits/removal against publication. File operations use an opened directory root, preventing symlink escapes. A failed authorization or storage check becomes a blocked run with a diagnostic.

Core syncs a staged file and retains a deterministic per-run hard-link receipt. It journals the candidate output name and content digest before making a no-overwrite hard link, then syncs the directory. On restart, the receipt must share an inode and digest with the journaled output before recovery records success. Equal content in a pre-existing user file is insufficient. This applies to `create`, `skip_existing`, and `unique`; a crash after publication cannot be reclassified as skipped or produce another unique output. Generated-file bookkeeping and success commit together before removing the receipt. Recovery of an already-published output records success even if write authorization was revoked afterwards; it performs no new publication. Pending runs keep their receipt if database acknowledgement fails. Blocked runs may retain a hidden `.101conv-receipt-*` file for inspection; do not delete receipts of pending runs. The destination filesystem must support this same-filesystem atomic publication. Output names are unique by default; `skip_existing` and create-only behavior are also available. Replacement is not implemented. Inputs are retained. Generated content fingerprints stop folder loops.

The dry-run API returns `scope=current_file_matches_only`. The UI labels it **Current-file match preview**, displays truncation, and explains that stability, baseline treatment, duplicate/generated contents, permissions, and concurrency are execution constraints. It is not a promise that a displayed match will run.

## API

The versioned endpoints are documented in [`../api/v1/openapi.yaml`](../api/v1/openapi.yaml):

- `GET/POST /api/v1/drives`, `PUT/DELETE /api/v1/drives/{id}`, `POST /api/v1/drives/test`
- `GET /api/v1/automation-blocks`, `GET /api/v1/blueprints`, `POST /api/v1/blueprints/{id}/install`
- `GET /api/v1/extensions`, `POST /api/v1/extensions/install`, `POST /api/v1/extensions/{id}/activate`, `POST /api/v1/extensions/{id}/rollback` (administrator-only declarative lifecycle)
- `GET/POST /api/v1/automations`, `GET/PUT/DELETE /api/v1/automations/{id}`
- `PATCH /api/v1/automations/{id}/enabled`, `POST .../dry-run`, `POST .../run`, `GET .../history`

Drive, automation, read, and execute permissions are enforced by the Core on every route. Browser sessions are same-origin protected; API keys are not accepted for management routes. Guest identities have no management permissions.

## Blueprint and extension compatibility contracts

Built-in blueprints are versioned definitions. Installing one copies its selected configuration into a new, independently editable automation; changing a built-in blueprint later does not mutate installed automations. Current blueprints are **Automatic Folder Conversion** and **Automatic Compression**.

`ExtensionManifest` is a declarative contract with package ID, publisher, package version, extension API range, optional external application/version, permissions/capabilities, block definitions, dependencies, integrity metadata, and configuration schema version. `SelectCompatibleExtension` chooses the newest semver release whose declared extension API range contains the running API version; incompatible newest releases do not hide older compatible ones.

P6 adds the first visual/extension lifecycle slice:

- Every new revision stores a bounded `graph` alongside the C.1 flat definition. Graph nodes pin a built-in or extension block ID/version and contain typed JSON configuration only; edges are acyclic, reachable from one trigger, and bounded to 32 nodes/64 edges. Core projects the graph into the existing folder-scan executor, so queued jobs still use the immutable profile/engine specification and older definitions remain readable. The Web editor presents the trigger → condition → action → output flow on narrow screens and keeps the detailed controls available as progressive disclosure.
- `conv_automation_extensions` retains every submitted declarative manifest release. Administrators can install a manifest, activate a compatible release, and roll back to the previous retained compatible release through `/api/v1/extensions`. Incompatible releases remain visible and cannot replace an active version. Active extension blocks are added to the typed block catalog; automation revisions retain their own block/version snapshot.
- Extension manifests are metadata-only in this slice. Core never downloads, loads, or executes package code, arbitrary commands, external application probes, or user expressions. A future constrained connector service must add signed package retrieval, permissions/secrets isolation, configuration migration, and external-version health checks before those capabilities are enabled.

## Operational limitations

- Polling currently handles only files directly in the registered directory, not nested trees.
- There is no filesystem event watcher; scan interval controls detection latency.
- Current inspection uses filesystem metadata and content hashing. Network filesystems with unstable metadata may need longer stability windows.
- The output filesystem must support atomic hard links. Cross-device replacement and overwrite are intentionally unsupported.
- PostgreSQL and app-managed files are included in normal backups. Arbitrary host-mounted folders remain administrator-owned backup sources.
- This automation subsystem is Core-managed; only the converter process remains in the isolated Worker. A future milestone may split ingestion/publication into a narrower service if threat analysis requires it.
