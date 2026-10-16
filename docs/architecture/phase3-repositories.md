# Phase 3: trusted repositories and installation plans

## API and installation flow

Repository and converter-management routes require `Authorization: Bearer $ENGINE_ADMIN_TOKEN` and are under `/api/v1`:

- `GET /repositories` lists configured sources with `enabled`, trust, last refresh, and the latest verification error.
- `POST /repositories` adds a community or local TUF source. The request includes `id`, `name`, `url`, `kind`, Base64 `trustedRoot`, its exact-byte SHA-256 `fingerprint`, and `approved: true`. Administrators must compare the fingerprint through an independent channel before approving it.
- `PATCH /repositories/{id}/enabled` enables or disables a source. Disabling waits for active repository operations, prevents new browse/installation work, and invalidates outstanding plans.
- `POST /repositories/{id}/refresh` refreshes metadata and updates the source status/error.
- `DELETE /repositories/{id}` removes the source and its local TUF cache. A failed cache removal is reported and the source remains configured.
- `GET /repositories/{id}/catalog` browses verified definitions, including profile versions incompatible with this application version.
- `GET /repositories/{id}/profiles/{profileID}/preview` creates an install plan. For updates use `/update-preview`. The response includes a random `planId`, expiration time, catalog digest, exact profile/engine versions, reuse decisions, publisher label, pinned trust fingerprint, licenses, dependencies, capabilities, and planned changes.
- `POST /repositories/{id}/profiles/{profileID}/install` and `PUT /repositories/{id}/profiles/{profileID}` require JSON `{"planId":"…"}` from the matching preview. The plan expires after ten minutes and is process-local; after restart, preview again.
- `GET /repositories/{id}/engines/{engineID}/{version}/preview` previews an engine release. Its corresponding `/install` endpoint also requires the returned `planId`.

Plans bind the exact signed metadata files, catalog/manifests, artifact target hashes and lengths, selected engine releases, and the management-state snapshot. Installation refreshes and verifies one TUF snapshot, rejects changed metadata or changed management state, downloads artifacts through that same updater snapshot, and verifies the signed artifact hash. It never re-resolves to a different version after preview. Installing/updating a profile stages all missing engines first, then commits those engine records and the profile record in one JSON state update. File cleanup failures are returned with the original failure rather than discarded. Incomplete engine staging and uncommitted version directories are removed during restart recovery.

Profile engine pins remain stable until the user requests an update or selects another compatible installed version. Dependency selection prefers the existing valid pin; a new profile selects the highest compatible installed version, then the highest compatible repository version. Shared engines remain one installation. Uninstalling a pinned version moves profiles to an available compatible release and disables only profiles with no alternative when dependency resolution is explicitly requested.

Errors use `{"error":{"code":"…","message":"…"}}`. Relevant codes include `signature_failure`, `expired_metadata`, `metadata_rollback`, `stale_preview`, `preview_expired`, `repository_disabled`, `missing_dependency`, `incompatible_version`, `unsafe_package`, `conflict`, and `installation_interrupted`.

`DATA_PATH` contains the engine state, repository approvals, TUF cache, and managed binaries. `ENGINE_ALLOWED_PATHS` is the OS path-list of administrator-approved manual import roots (default: `DATA_PATH/imports`). Repository package staging is private to the service. A lifetime lock permits only one process to write the current JSON stores.

## Supported repository format and trust behavior

The client uses the maintained `github.com/theupdateframework/go-tuf/v2` v2.4.2 implementation (Apache-2.0). Remote sources must use HTTPS and same-origin redirects; local sources use hostless `file:` URLs. Both require an explicit admin approval and independently verified SHA-256 pin of the initial TUF root. The TUF client enforces signatures and role thresholds, expiration, sequential root rotation, rollback protection, and target length/hash checks. Root rotations preserve the initial bootstrap fingerprint and are accepted only through TUF's signed sequential rotation flow.

The signed `catalog/index.json` format is schema version 1, with optional app-version bounds and `engines`/`profiles` entries. Entries identify a manifest target; engine manifest targets contain `{"engine": <engine.Definition>}` and profile manifests contain `{"profile": <profile.Definition>}`. Engine artifacts are logical target paths, never arbitrary URLs. The signed manifest digest must agree with TUF target metadata and downloaded bytes.

Inspection reads metadata only; it never runs install scripts or binaries. Only `tar.gz` and `zip` artifacts are supported. Extraction rejects traversal, absolute paths, links, devices, unsupported entries, excessive file counts, and excessive expanded size. Only declared executable files are copied to private versioned directories. The current per-artifact compressed limit is 1 GiB and expanded limit is 2 GiB; deployments should account for memory and storage capacity.

Repository status distinguishes `configured`, `ready`, `disabled`, `trust_error`, `expired`, and general `error`; `lastError` is bounded and intended for an authenticated administrator. This is operational status, not a separate identity assertion: the configured repository name is an administrator-entered publisher label. The actual trust anchor is the pinned root fingerprint.

## Official catalog signing and release procedure

The existing official catalog is unsigned JSON and has no official TUF root. The service therefore marks the bundled source `trust_unavailable` and will not install from it. No production key was generated, silently trusted, or committed.

Before enabling official installation, the project owner must approve and provision an official signing identity and publication workflow:

1. Create an organization-owned TUF root in a controlled key ceremony. Use a threshold root role with keys held by separate custodians, preferably offline or in hardware-backed custody. Keep private keys out of Git, CI artifacts, the application image, and the running service.
2. Create Targets, Snapshot, and Timestamp roles with least-privilege keys. Protect online signing through a dedicated hardened release workflow/HSM; scope target signing to approved release artifacts and metadata.
3. Generate and review TUF metadata using maintained TUF repository tooling. Publish `root.json`, versioned root rotations, `timestamp.json`, `snapshot.json`, `targets.json`, `catalog/index.json`, manifests, and packages atomically. Set short metadata expirations appropriate to the release cadence, retain rollback state, and verify the published repository with the same client library before release.
4. Publish the initial root fingerprint through an independent release channel, pin the root bytes in a reviewed application release, and document a multi-person recovery/rotation process. Never replace the root pin through the ordinary repository-add endpoint.
5. Add a CI release job only after the owner approves the official publisher, key custodians, hosting location, and credential/HSM access. The runtime repository manager remains a read/install client and never receives signing credentials.

**Approval blocker:** enabling the official catalog requires the project owner's decision on custodians/threshold, a key ceremony, and authorized release-hosting/signing credentials. Those decisions and credentials are not available in this workspace, so only the workflow is documented. The official source must remain unavailable until a reviewed root and signed metadata are published.

## Migration, remaining risks, and Phase 4 proposal

Existing Phase 2 JSON state and managed files remain readable. Managed installation paths remain relative to `DATA_PATH`; legacy absolute managed paths are normalized on load. Repository state fields are additive; old configured community repositories migrate as enabled. Installation plans intentionally do not survive restart and must be previewed again. The Java app and data are untouched; Java profile/history import is still not implemented.

TUF verifies authenticity relative to the root but does not sandbox converter execution. Community admins explicitly accept publisher code; no restricted Worker exists yet. The JSON storage lock is local-filesystem coordination and does not make network filesystems or multiple independent data mounts safe. Large packages use memory proportional to compressed artifact size. The repository HTTP fetcher has a 30-second timeout, but the underlying go-tuf fetcher API does not accept request contexts, so caller cancellation cannot stop an in-flight metadata fetch immediately.

Proposed Phase 4 scope: implement a restricted conversion Worker with resource limits and no network by default; connect durable conversion jobs and the web UI to the versioned API; add backup/restore and an explicit Java-data importer. Keep repository signing/publishing as a separate release-engineering workflow. Phase 4 implementation has not started.
