# Phase 3: trusted repositories and catalog installation

## API

All repository and install-management routes require `Authorization: Bearer $ENGINE_ADMIN_TOKEN` and live under `/api/v1`:

- `GET /repositories` lists sources and their trust state.
- `POST /repositories` adds a community or local TUF source. The body contains `id`, `name`, `url`, `kind`, Base64 `trustedRoot`, `fingerprint` (SHA-256 of the exact root bytes), and `approved: true`. The administrator must obtain and compare the fingerprint through an independent channel before approving it.
- `DELETE /repositories/{id}` removes a configured source and its TUF metadata cache. The bundled official source cannot be removed.
- `GET /repositories/{id}/catalog` browses signed engine and profile manifests without installing or executing package contents.
- `GET /repositories/{id}/profiles/{profileID}/preview` shows the configured publisher label, pinned root fingerprint/trust state, profile license, engine version ranges, required capabilities, installed engine reuse, and planned installs.
- `POST /repositories/{id}/profiles/{profileID}/install` installs a profile; `PUT /repositories/{id}/profiles/{profileID}` updates it.
- `POST /repositories/{id}/engines/{engineID}/{version}/install` explicitly installs or updates an engine release.

The existing manual engine and profile endpoints remain available. `DATA_PATH` stores engine state, repository approvals, trusted metadata, and managed binaries. `ENGINE_ALLOWED_PATHS` is an OS path-list of administrator-approved directories for manual/local imports; the default is `DATA_PATH/imports`. Repository package staging is private to the application and does not use the manual import path policy.

## Repository format and trust

The client uses `github.com/theupdateframework/go-tuf/v2` v2.4.2 (Apache-2.0) for the TUF update workflow. Remote community sources require HTTPS; local sources use a `file:` URL. Both require explicit administrator approval and an out-of-band SHA-256 pin of the supplied TUF root. go-tuf validates signed root/timestamp/snapshot/targets metadata, expiration, sequential root rotation, rollback state in the persistent metadata cache, and target lengths/hashes. Requests have timeouts, reject HTTPS downgrade redirects, and serialize access to each source's TUF cache.

TUF target `catalog/index.json` is JSON with `schemaVersion: 1`, optional application-version bounds, and `engines`/`profiles` arrays. Each item gives `id`, `version`, `name`, and a `manifestTarget`. Engine targets contain `{"engine": <engine.Definition>}`; profile targets contain `{"profile": <profile.Definition>}`. For repository engines, `engine.artifact.url` is a logical TUF target path (not an arbitrary URL), `sha256` must match both the signed TUF target and downloaded bytes, and `format` must be `tar.gz` or `zip`.

Package inspection only reads signed manifests and archives. Extraction rejects traversal, absolute paths, symlinks, hard links, devices, unsupported formats, excessive file counts, and excessive expanded size. Only declared executable files are copied into a private versioned engine directory; no install scripts or downloaded binaries are run. Engine/profile state commits after files are staged and synced. Profiles retain explicit engine-version pins; installing a newer engine does not switch a profile automatically. An administrator can select a compatible version through `PUT /profiles/{id}/engines/{engineID}` and can roll back by selecting the prior still-installed version. Removing a version re-pins active profiles to a remaining compatible version, and only profiles with no alternative are disabled when dependency resolution is explicitly requested.

Signatures establish metadata and artifact authenticity relative to the pinned root. They do not sandbox converter processes. The Worker remains a separate security boundary.

## Current official source and migration

The repository's current official catalog is ordinary unsigned JSON, and the upstream project does not publish a TUF root/metadata set. This release therefore reports the official source as `unavailable` for trusted repository installation. It does not fabricate or self-pin a signing key. The older read-only catalog endpoint remains for compatibility and is still marked unverified. A project release must publish a root and begin signing the index, manifests, and artifacts before official catalog installation can be enabled.

Community and local sources work when their maintainers publish the documented TUF layout. Existing Phase 2 JSON state remains readable; managed install paths are now persisted relative to `DATA_PATH`, with absolute Phase 2 paths normalized on load. The exclusive storage lock rejects a second process using the same data directory. The Java application's files and routes are untouched, and Java profile data is still not automatically imported into Go. Manual binaries outside `DATA_PATH/imports` require an explicit `ENGINE_ALLOWED_PATHS` entry.

## Remaining risks and Phase 4 proposal

The initial official trust root is blocked on upstream key provisioning and signed catalog publication. TUF cache integrity depends on the owner-only persistent data directory. Package-size limits still permit large downloads and should be tuned to deployment quotas. Community approval means the administrator accepts that publisher's converter code; it does not make the code safe to execute. The Go service still has no restricted conversion Worker, PostgreSQL job pipeline, browser UI integration, or Java data importer.

Proposed Phase 4 work is to add the restricted Worker and durable conversion jobs, then migrate the web UI and provide a backupable importer for Java profiles/history/files. No Phase 4 implementation is included here.
