# Project Status — evidence-based implementation ledger

**Review date:** 2026-10-20.  
**Implementation baseline reviewed:** `6dc091cc0ae768e2a5c6fc8c5ceb84da5bea8d6f` on `main`.  
**Maintenance note:** Documentation commits will advance HEAD beyond this baseline; this is *not* an assertion that the later documentation-only HEAD has been deployed or verified.

See [MASTER_PLAN.md](MASTER_PLAN.md), [FEATURE_REQUESTS.md](FEATURE_REQUESTS.md), [DECISIONS.md](DECISIONS.md).

## Legend

- **Implemented (source reviewed):** code/config exists in the reviewed commit, not necessarily proved in every real deployment.
- **Automated test present:** a test/CI stage exists; not proof that its latest run passed.
- **Externally reported:** another reviewer reports a test passing but this ledger has not independently inspected run logs.
- **Open:** verified inconsistency or work not yet implemented.
- **Proposed:** product idea or design, not a release promise.

## Implemented in reviewed source

| Capability | Evidence and important constraints |
| --- | --- |
| Go Core, PostgreSQL jobs, isolated Worker, React/Nginx web | `compose.yaml`, `server/internal/jobs/`, `server/internal/worker/`; database/storage remain shared within the deployment trust boundary |
| Installer, first-run administrator, roles/permissions/API keys | README and auth/API code; current password policy permits five-character credentials |
| Guest conversion/compression, rate/storage quotas and expiry | `server/internal/auth/`, `server/internal/httpapi/session.go`; reverse-proxy behavior has had several hardening iterations |
| Durable job attempts, cancellation, retention, batch results and restore rehearsal | Job API and integration/restore scripts; actual production restore depends on deployment details |
| Versioned engine/profile repository, SHA-256 validation, TUF-capable manager | `server/internal/repositorymanager/`, `catalog/`; official signing root is still empty |
| FFmpeg audio/video, JPEG XL and FFmpeg image profile 1.1.0 | Catalog definitions and `scripts/verify-ffmpeg-image.sh`; runtime/encoder-specific limits apply |
| Office-to-PDF via managed LibreOffice runtime | Commit `6dc091c`, `docs/architecture/documents-d2.md`, engine/profile manifests; AMD64 first |
| PDF-to-image (one selected page) and text through managed Poppler | `docs/architecture/documents-d2.md`; not an all-pages PDF editor or OCR |
| Mounted Drives and bounded folder-scan Automations | `server/internal/automation/`, `docs/architecture/automations-c1.md`; Core-only registered mounts, worker does not mount input NAS paths |
| Improved mobile/desktop UI and system theme | Commits `e5e3c98` and `3fdccaa` with UI/theme smoke scripts |

### Automated verification present

CI workflow includes Go/PostgreSQL tests, Go race/vet, React checks/build, pinned FFmpeg image verification, Docker browser acceptance, isolated restore, and the new `document-conversion` runtime verification stage. A separate reviewer reported local successful checks for frontend, Go, Compose and document runtime, but this ledger has **not independently established latest GitHub Actions run status**. Report the exact run URL/result before marking CI green.

## Confirmed issues at reviewed HEAD — address before general public launch

1. **[P0] Nginx upload size mismatch.** `web/nginx.conf` sets `client_max_body_size 105m`. README advertises input ceiling of up to 1 GiB subject to quotas. The web ingress will reject larger requests before Core. Fix safely, align limits including multipart overhead, review proxy buffering/timeouts and test a real large upload.
2. **[P0] Static `/readyz`.** `server/internal/httpapi/httpapi.go` returns HTTP 200 with `ready` independent of dependencies. `operationsOverview` implements richer diagnostics. Define explicit `/healthz` liveness, Core readiness and separate conversion-ability signals; do not prevent first-time setup merely because no converter is installed.
3. **[P0] Embedded compatibility catalog / documentation drift.** `server/internal/catalog/official.json` and old catalog-format text do not reflect newly landed LibreOffice/Poppler profiles in the main target catalog. Distinguish compatibility catalog from authoritative repository, then unify/generate/index consistently with drift checks.
4. **[P0 pre-public-release] No pinned official trust root.** `server/internal/repositorymanager/official-root.json` is empty. Default browse-only and explicit unsigned approval are documented; production signing and secure root-key custody require a maintainer ceremony. No AI should create or check in signing secrets automatically.

## Risks/decisions needing targeted audits

- **Worker isolation:** Worker is unprivileged, network-restricted and CPU/memory bounded, but the shared database/result mounts are not a complete hostile-parser sandbox or multi-tenant isolation. Inspect least-privilege DB credentials, per-job FS exposure and threat model before an internet-accessible public demonstration.
- **Five-character password policy:** intentional compatibility behavior in current auth code; assess risk, throttling, warnings and rollout without breaking accounts. Owner approval for a policy change.
- **Mutable image/base dependencies:** audit build pinning and rollback evidence. Some Compose image references use tags (e.g. PostgreSQL) rather than digests. `sslmode=disable` operates on the isolated Docker DB network; risk depends on deployment, not an automatic universal defect.
- **`no-new-privileges` absent by design:** documented compatibility behavior with snap-packaged Docker; retest host variants and consider opt-in hardening, not blind change.
- **Real hardware/device support, full HDR preservation, image target-size and combined batch budgets:** not yet verified as general product features.
- **Frontend modularity:** `web/src/App.tsx` remains large; recent feature extraction is partial. Split by workspace/service rather than another broad rewrite.

## Planned, not yet implemented as generally available

- Unified canonical capability/provider resolver; UX no longer requires competing profile selection.
- User-created Convert/Compress presets, personal/shared defaults and compatibility-safe migration.
- Full file analysis / metadata Inspector and metadata-based conditions.
- Rich image target size + combined batch maximum, bounded adaptive compression, advanced media options.
- HDR-preserving re-encoding with output checks; optional GPU workers and device probing.
- Visual multi-format Document Creator with per-page reorder/rotation, save/resume and exports.
- Visual Automations blocks/branches and broader blueprint system.
- Versioned extension installation/compatibility/rollback and Immich connector.
- New brand/logo, static public pages/SEO and rate-limited public demo.

## Immediate implementation queue

**Next:** address P0 deployment/catalog correctness (upload ceiling, readiness, compatibility drift), with tests and docs. In parallel, prepare a maintainer-approved trusted-root publication plan. Only then implement the capability/provider/preset migration as the next major feature foundation.

The status ledger should be updated whenever a significant task is implemented **and verified**. Record new SHA, test URLs, compatibility impacts, completed/requested feature IDs and explicit outstanding risks. Do not rewrite this baseline as though those future tests had already run.
