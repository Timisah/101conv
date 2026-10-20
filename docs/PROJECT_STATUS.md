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
| P1 canonical capability/provider read model (first slice) | `server/internal/capability/`, `docs/architecture/capabilities-p1.md`, additive `/api/v1/capabilities` and `/api/v1/capabilities/resolve`; legacy profiles, preferences, jobs and automations remain the compatibility boundary |

### Automated verification present

CI workflow includes Go/PostgreSQL tests, Go race/vet, React checks/build, pinned FFmpeg image verification, Docker browser acceptance, isolated restore, and the new `document-conversion` runtime verification stage. A separate reviewer reported local successful checks for frontend, Go, Compose and document runtime, but this ledger has **not independently established latest GitHub Actions run status**. Report the exact run URL/result before marking CI green.

## Confirmed issues at reviewed HEAD — address before general public launch

1. **[P0] Nginx upload size mismatch — fixed and ingress-verified.** The continuation working tree based on `5cfcbac` sets `client_max_body_size 1025m`, matching Core's 1 GiB plus 1 MiB multipart request bound. On 2026-10-20, `bash scripts/verify-upload-proxy.sh` passed Nginx config validation, forwarded an actual 1 GiB multipart file and a 106 MiB chunked file to an isolated byte-counting upstream, and rejected a declared request above 1025 MiB with HTTP 413. The same change was built into and exercised by the managed Docker deployment; request buffering remains disabled, existing proxy send/read timeouts remain 650 seconds, and client-body idle timeout remains Nginx's default 60 seconds. Core quotas/auth/storage behavior is unchanged. The test is added to CI; no remote CI result or outer production proxy configuration is claimed. No data/schema migration; rollback by restoring the previous ingress config (which restores its smaller limit).
2. **[P0] Static `/readyz` — fixed, unit-verified, and deployment-verified.** `/healthz` is now process liveness. `/readyz` checks PostgreSQL schema access and Core-managed storage, returns HTTP 503 with safe diagnostics when unavailable, and returns HTTP 200 for a fresh unconfigured instance so setup can proceed before a Worker or converter profile exists. On 2026-10-20, the managed Docker deployment reported all four application dependencies healthy; direct in-network checks returned HTTP 200 for `/healthz` and `/readyz`, and `/api/v1/setup/status` returned the expected incomplete-setup response. `/api/v1/operations` remains the authenticated source for Worker/profile `readyToConvert` diagnostics. A live dependency outage/recovery and remote CI run have not been independently verified.
3. **[P0] Embedded compatibility catalog / documentation drift — fixed and locally verified.** `catalog/targets/catalog/index.json` is now the authoritative release list. `scripts/sync-compatibility-catalog.py` validates all target manifest identities, generates `catalog/index.json` and `server/internal/catalog/official.json`, preserves the legacy `catalog/profiles/` paths, and runs in CI. The derived indexes include the managed LibreOffice/Poppler document profiles and compression profiles; README and catalog-format documentation now describe the same set. Remote CI and signed production publication remain outstanding.
4. **[P0 pre-public-release] No pinned official trust root — ceremony prepared, publication outstanding.** `server/internal/repositorymanager/official-root.json` remains empty by design. `docs/security/trust-root-ceremony.md` records offline custody, independent fingerprint verification, rotation, recovery, and publication steps. Production signing keys and root metadata require an owner-controlled release operation; no AI should create or check in signing secrets automatically.

## Risks/decisions needing targeted audits

- **Worker isolation:** `scripts/verify-compose-security.sh` and live inspection confirm an unprivileged Worker with only the internal database network, read-only engine/input mounts, writable results, dropped capabilities, read-only root, and CPU/memory/process bounds. The managed deployment now uses a dedicated non-superuser Worker database role with compatibility DML grants; existing installations must run the explicit additive migration. The Worker still shares the application result volume, and hostile-parser/public-demo threat-model work remains open.
- **Five-character password policy:** intentional compatibility behavior in current auth code; assess risk, throttling, warnings and rollout without breaking accounts. Owner approval for a policy change.
- **Mutable image/base dependencies:** production Compose and build-stage references are now pinned to recorded digests, with exact document package revisions. Verify those digests in the remote CI/build environment and retain rollback images. `sslmode=disable` operates on the isolated Docker DB network; risk depends on deployment, not an automatic universal defect.
- **`no-new-privileges` absent by design:** documented compatibility behavior with snap-packaged Docker; retest host variants and consider opt-in hardening, not blind change.
- **Real hardware/device support, full HDR preservation, image target-size and combined batch budgets:** not yet verified as general product features.
- **Frontend modularity:** `web/src/App.tsx` remains large; recent feature extraction is partial. Split by workspace/service rather than another broad rewrite.

## Planned, not yet implemented as generally available

- Full P1 capability/provider migration: the canonical read model and deterministic resolver are implemented as an additive first slice; novice UI migration, immutable job-resolution adapter, and final provider precedence policy remain open.
- User-created Convert/Compress presets, personal/shared defaults and compatibility-safe migration.
- Full file analysis / metadata Inspector and metadata-based conditions.
- Rich image target size + combined batch maximum, bounded adaptive compression, advanced media options.
- HDR-preserving re-encoding with output checks; optional GPU workers and device probing.
- Visual multi-format Document Creator with per-page reorder/rotation, save/resume and exports.
- Visual Automations blocks/branches and broader blueprint system.
- Versioned extension installation/compatibility/rollback and Immich connector.
- New brand/logo, static public pages/SEO and rate-limited public demo.

## Immediate implementation queue

**Next:** obtain remote CI evidence, tighten the Worker compatibility grants after schema review, and execute the owner-controlled trust-root ceremony when release ownership is ready. Keep the deployment classified as trusted single tenant/small team until those gates and the public-demo threat model are accepted.

The 2026-10-20 local release pass rebuilt the pinned Core, Worker, and Web images, migrated the managed Worker login, passed the Go suite/vet, upload ingress, Compose security, catalog, FFmpeg matrix, and document runtime unit checks. The document HTTP stage stopped on disposable PostgreSQL `ENOSPC`, and browser/restore was deliberately not started below its 8 GiB free-space preflight; both remain remote-CI gates.

The status ledger should be updated whenever a significant task is implemented **and verified**. Record new SHA, test URLs, compatibility impacts, completed/requested feature IDs and explicit outstanding risks. Do not rewrite this baseline as though those future tests had already run.
