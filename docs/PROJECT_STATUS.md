# Project Status — evidence-based implementation ledger

**Review date:** 2026-10-20.  
**Implementation baseline reviewed:** `517eaa1291f9aef7eaf5756d3f8c3100b5cca035` (P1 capability/provider and preset completion plus authorization/matching hardening); the earlier baseline was `3df147e9d1b65a0daf3f4dc5fc245b13a4406dbe`.
**Maintenance note:** Documentation commits will advance HEAD beyond this baseline; this is *not* an assertion that the later documentation-only HEAD has been deployed or verified.

See [MASTER_PLAN.md](MASTER_PLAN.md), [FEATURE_REQUESTS.md](FEATURE_REQUESTS.md), [DECISIONS.md](DECISIONS.md).

## Legend

- **Implemented (source reviewed):** code/config exists in the reviewed commit, not necessarily proved in every real deployment.
- **Automated test present:** a test/CI stage exists; not proof that its latest run passed.
- **Externally reported:** another reviewer reports a test passing but this ledger has not independently inspected run logs.
- **Open:** verified inconsistency or work not yet implemented.
- **Proposed:** product idea or design, not a release promise.

## Implemented in reviewed source

- **Administrator version visibility and advisory release checks:** the running `APP_VERSION` is visible in the top bar on desktop and mobile. Administrators can view cached stable-release metadata and notes at **Admin → Application updates**; Core checks at most every 12 hours and never performs an upgrade. The admin-only `/api/v1/system/update` endpoint and `UPDATE_CHECK_URL` deployment override are documented. Portainer Git pull/redeploy remains the update mechanism.

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
| P1 capability/provider selection and presets | `server/internal/capability/`, `server/internal/presets/`, additive `/api/v1/capabilities`, `/api/v1/capabilities/resolve`, and `/api/v1/presets`; automatic jobs resolve through canonical providers while legacy profiles remain the execution compatibility boundary |

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

- P1 capability/provider migration and saved presets: implemented in the completion commit. The remaining release gate is remote CI and browser evidence; the deployment continues to retain legacy profile IDs as a migration boundary.
- Metadata-based automation conditions, richer media controls, and engine-specific optional GPU execution.
- Document Creator: the first Build editor now provides non-destructive page ordering, thumbnails, ordered preview, and browser-local PDF generation for raster image pages; server-side PDF/Office expansion and merging, saved/expiring projects, and fidelity-tested exporter output remain planned.
- Visual Automations blocks/branches and broader blueprint system.
- Versioned extension installation/compatibility/rollback and Immich connector.
- New brand/logo, domain-specific SEO/publication and rate-limited public demo.

## P4 progress in the current worktree (not deployment evidence)

- The Worker now performs an explicit allowlist-only acceleration probe for
  Intel VAAPI/Quick Sync paths, AMD VAAPI paths, and configured NVIDIA devices.
  It records verified device/driver diagnostics in the durable Worker
  heartbeat, exposes them to administrators through Operations, and keeps CPU
  as the deterministic fallback. The base Compose deployment remains
  CPU-only; no privileged or unrestricted device mapping was added.
- Hardware selection deliberately excludes unverified HDR-preserving paths and
  reports the CPU fallback reason. Engine-specific FFmpeg arguments,
  codec-level support, performance/fidelity benchmarks, and real deployment
  verification remain open.

Evidence: `server/internal/acceleration/`, the Worker heartbeat migration in
`server/internal/jobs/`, `docs/architecture/hardware-acceleration-p4.md`,
and the focused acceleration/jobs tests.

## P5 progress in the current worktree (not deployment evidence)

- The Web image includes a static `/about/` discovery page with accurate
  Convert, Compress, Build, Inspect, and Automate descriptions, repository and
  sign-in links, responsive markup, and no external analytics or scripts.
- The authenticated SPA shell is marked `noindex,nofollow`; `robots.txt`
  allows only the public information page. No new brand, canonical domain,
  sitemap publication, or public conversion demo has been selected.

Evidence: `web/public/about/index.html`, `web/public/robots.txt`,
`web/index.html`, and `docs/architecture/public-discoverability-p5.md`.

## P2 progress in the current worktree (not deployment evidence)

The P2 scope is implemented and source-verified in this worktree:

- Compression profiles can declare a numeric target-size option plus an
  adaptive quality option. Core freezes the quality range and attempt bound in
  the immutable execution specification.
- The Worker performs a bounded quality search, validates each candidate's
  type/signature/size, and refuses to publish an oversized or infeasible result.
  JPEG XL compression is the first official catalog consumer; the existing
  duration/bitrate video target-size path remains separate.
- The UI describes adaptive quality target sizing correctly, and tests cover a
  real FFmpeg media target, deterministic adaptive-quality behavior, catalog
  manifest validation, and the frontend typecheck/build.
- Compression batches can now declare `combinedTargetSizeMb`. Core resolves
  compatible profiles and freezes proportional per-file target allocations in
  the durable batch item requests, with hard minimum-budget rejection and
  best-effort handling when profile maxima are lower than the requested cap.
- The FFmpeg compression profile declares explicit stream selection, safe
  metadata/chapter preservation, resolution/HDR policies, and dynamic-HDR
  behavior. The Worker shares one bounded typed media probe with target-size
  bitrate calculation and rejects output that loses selected streams, audio
  layout, comparable metadata, video dimensions, HDR signaling, bit depth,
  static HDR side data, or configured dynamic-HDR payloads.
- Core exposes a bounded ownership-checked upload analysis endpoint and the
  Web UI provides an Inspector workspace for image, document, and media
  summaries. The Inspector now uses a scan-only multipart endpoint: the source
  is held in an ephemeral file, never added to conversion storage, and removed
  after the scan. Analysis reuses the shared probe and returns bounded full
  metadata, including sensitive tags when present, to the authorized caller.
- An explicit same-format metadata-removal endpoint now queues a separate
  conversion result through the selected profile's typed `metadata=-1` option;
  the source upload is retained and the normal job idempotency, quota, and
  output validation paths remain in force.
- The first Build slice adds a validated page-manifest package and responsive
  editor for add, reorder, rotate, duplicate, remove, undo/redo, thumbnails,
  ordered preview, browser-local raster-image PDF generation, source
  preparation, and manifest download. Server-side PDF/Office expansion and
  merging plus saved projects remain open.
  Native image metadata sources are retained as raw bounded APP/chunk/box
  payloads alongside normalized EXIF/XMP/IPTC/ICC/GPS fields; media scans
  request ffprobe private data/programs, and PDF scans include bounded
  first/last windows so tail Info/XMP properties are not silently missed.
- Successful jobs expose input/output savings, and batch allocations remain
  independently frozen per item for deterministic retry behavior.

Evidence: `server/internal/worker/runner.go`,
`server/internal/analysis/service.go`, `web/src/features/Inspector.tsx`,
`catalog/targets/profiles/ffmpeg-compression-linux-amd64.json`,
`docs/architecture/compression-p2.md`, and the focused/full package test runs
performed in this worktree. Production deployment and broad codec-specific
HDR10+/Dolby Vision compatibility remain unverified release risks.

## Immediate implementation queue

**Next:** obtain remote CI evidence, tighten the Worker compatibility grants after schema review, and execute the owner-controlled trust-root ceremony when release ownership is ready. Keep the deployment classified as trusted single tenant/small team until those gates and the public-demo threat model are accepted.

The 2026-10-20 local release pass rebuilt the pinned Core, Worker, and Web images, migrated the managed Worker login, passed the Go suite/vet, upload ingress, Compose security, catalog, FFmpeg matrix, and document runtime unit checks. The document HTTP stage stopped on disposable PostgreSQL `ENOSPC`, and browser/restore was deliberately not started below its 8 GiB free-space preflight; both remain remote-CI gates.

The status ledger should be updated whenever a significant task is implemented **and verified**. Record the completion SHA, test URLs, compatibility impacts, completed/requested feature IDs and explicit outstanding risks. P0 release gates remain open: remote CI evidence, the owner-controlled TUF trust-root ceremony/publication, the document HTTP stage that stopped on disposable PostgreSQL `ENOSPC`, and browser/restore gates below their 8 GiB preflight.
