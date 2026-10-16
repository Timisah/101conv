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

The signed `catalog/index.json` format is schema version 1, with optional app-version bounds and `engines`/`profiles` entries. Entries identify a manifest target; engine manifest targets contain `{"engine": <engine.Definition>}` and profile manifests contain `{"profile": <profile.Definition>}`. Engine artifacts use a logical target path. An optional upstream GitHub release URL is repeated in the signed TUF target custom metadata; its signed length/hash must agree with the manifest and downloaded bytes.

Inspection reads metadata only; it never runs install scripts or binaries. `tar.gz`, `tar.xz`, `tar.lz`, and `zip` artifacts are supported. Extraction rejects traversal, absolute paths, links, devices, unsupported entries, excessive file counts, and excessive expanded size. XZ/LZip dictionaries are capped at 128 MiB; compressed artifacts are capped at 1 GiB and expanded contents at 2 GiB. Only declared executable files are copied to private versioned directories.

Repository status distinguishes `configured`, `ready`, `disabled`, `trust_error`, `expired`, and general `error`; `lastError` is bounded and intended for an authenticated administrator. This is operational status, not a separate identity assertion: the configured repository name is an administrator-entered publisher label. The actual trust anchor is the pinned root fingerprint.

## Official catalog signing and release procedure

The Go catalog manifests and a `catalog-repo` provisioning/publishing command are present. `server/internal/repositorymanager/official-root.json` remains empty in this checkout, so the official repository is intentionally reported as `trust_unavailable`; no production identity or signed production metadata was created in this task.

The explicit `go run ./cmd/catalog-repo init` command creates an Ed25519 key per TUF role, a public root, and the application embed file. It refuses to overwrite non-empty outputs. The default root threshold is one maintainer; the root key should be moved to offline custody after initialization. The project owner must run the command on a trusted machine, confirm key custody and the printed fingerprint, and protect the Targets/Snapshot/Timestamp signing keys. `go run ./cmd/catalog-repo publish` verifies each upstream package against its declared size and SHA-256, constructs TUF targets metadata (including signed upstream URLs), checks each role signature against the root, increments versions, and writes snapshot/timestamp metadata. The timestamp expires in seven days. Private signing keys are never needed by the runtime or committed to Git.

The Go Core embeds the initial public root. When the owner-provisioned root and signed metadata are reviewed and published, the bundled source is verified by default; users will not have to manually pin the official repository. Until that happens, an administrator can explicitly opt into unsigned installation from the built-in GitHub catalog in Administration. This checks HTTPS, manifest package digests, size, compatibility, and safe extraction, but does not authenticate the publisher; production trust should move to TUF. No GitHub credentials are required to create local metadata, but publishing the root and manifests to `main` still requires an authorized repository update. Future root rotation must remain sequential under TUF; the embedded root is the original trust anchor and must not be silently replaced.

## Migration, remaining risks, and Phase 4 proposal

Existing Phase 2 JSON state and managed files remain readable. Managed installation paths remain relative to `DATA_PATH`; legacy absolute managed paths are normalized on load. Repository state fields are additive; old configured community repositories migrate as enabled. Installation plans intentionally do not survive restart and must be previewed again. The Java app and data are untouched; Java profile/history import is still not implemented.

TUF verifies authenticity relative to the root but does not sandbox converter execution. Community admins explicitly accept publisher code; no restricted Worker exists yet. The JSON storage lock is local-filesystem coordination and does not make network filesystems or multiple independent data mounts safe. Large packages use memory proportional to compressed artifact size. The repository HTTP fetcher has a 30-second timeout, but the underlying go-tuf fetcher API does not accept request contexts, so caller cancellation cannot stop an in-flight metadata fetch immediately.

Proposed Phase 4 scope: implement a restricted conversion Worker with resource limits and no network by default; connect durable conversion jobs and the web UI to the versioned API; add backup/restore and an explicit Java-data importer. Keep repository signing/publishing as a separate release-engineering workflow. Phase 4 implementation has not started.
