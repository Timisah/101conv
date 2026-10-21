# Project Status — evidence-based implementation ledger

**Review date:** 2026-10-21.
**Implementation baseline reviewed:** focused CI follow-up commit `2b62674b90ba166f8667cda1a08aeeae0c6a17fd`, including the retention compatibility fix, browser acceptance synchronization, backup-artifact permission preservation, and restore-fixture compatibility fixes. The preceding feature baseline was `517eaa1291f9aef7eaf5756d3f8c3100b5cca035`.
**Remote CI evidence:** [CI run #98](https://github.com/Timisah/101conv/actions/runs/39585198155) passed every job, including Docker browser acceptance and isolated backup/restore conversion with a real post-restore conversion.
**Maintenance note:** Documentation commits will advance HEAD beyond this baseline; this ledger records the verified run above, not an assertion that every later documentation-only HEAD has been deployed.

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

CI workflow includes Go/PostgreSQL tests, Go race/vet, React checks/build, pinned FFmpeg image verification, Docker browser acceptance, isolated restore, and the `document-conversion` runtime verification stage. CI run #98 passed all of them; the browser job verified mobile guest conversion, ownership isolation, fair scheduling, batches, cancellation, retention, restart recovery and responsive layouts, and the dependent restore job verified private-volume restore plus a real conversion.

### Local stabilization reproduction — 2026-10-21

The same stabilization tree was independently reproduced in disposable containers: the full Go/PostgreSQL suite, focused retention round-trip regression, pinned-toolchain race/vet pass, frontend install/lint/build, public and UI smoke tests, catalog/Compose/upload-proxy checks, pinned FFmpeg matrix, document runtime, browser acceptance, backup export, and isolated restore conversion all passed. The browser fixture covered Document Creator preview/PDF generation, Inspector, guest limits, mobile navigation, fair scheduling, restart persistence, automation recovery after Core termination, and Worker crash recovery. The verified restore contained 2 users, 1 engine, 5 profiles, and 39 jobs, and completed a fixture conversion.

The repository records the remote result above. GitHub API access in the local verification environment returned HTTP 403, so the linked Actions log was not independently re-read during this session. The official trust root remains owner-controlled and unpublished; real JPEG/WebP/AVIF/JPEG XL provider conversions were not independently driven through the browser UI, and server-side PDF/Office merging remains planned.

## CI-verified changes — 2026-10-21

**Main CI #101 repair — verified.** [CI #101](https://github.com/Timisah/101conv/actions/runs/39589077454) on `58f0e75` failed only the Docker browser acceptance job (`guest can download its individual result`, `409 !== 200`; the dependent restore job was skipped). The stabilization merge had replaced the item-level wait in `web/smoke.cjs` with a check of the aggregate `succeeded` counter, so the individual-result download could race the job becoming downloadable. The race-free wait from `fix/ci-green-followup` was restored. [CI #106](https://github.com/Timisah/101conv/actions/runs/39595635761) on `ab7a093` passed all ten jobs, including Docker browser acceptance and isolated backup/restore conversion.

**Optional, blank-by-default maximum output size ([#9](https://github.com/Timisah/101conv/issues/9)) — implemented, CI #106 passed.** Previously the image-compression profiles shipped `targetSizeMb` with default `8`, so Core and the UI applied an 8 MB cap to anyone who never touched the control. Now:

- `OptionDefinition.optional` marks a numeric target-size option whose empty value means "no limit". Profile validation requires an empty default, a `targetQualityOption` that can run without a target, and rejects `optional` on conversion options. Job validation omits a blank optional value from the frozen options and, with no target, freezes the selected quality as `compressionFixedQuality`; the Worker encodes once at that quality. Explicit values keep their range/step checks and the existing bounded search (highest quality that fits). Presets accept a blank optional value.
- **FFmpeg Image Compression 1.1.0** and **JPEG XL Image Compression 1.1.0** are new immutable catalog releases with the optional blank default. The 1.0.0 manifests, queued jobs and pinned automations are unchanged; existing installations keep the 8 MB behavior until an administrator installs the new release (the update-versus-new-profile presentation is tracked by [#23](https://github.com/Timisah/101conv/issues/23)).
- The Compress screen shows `No limit` with an "optional" tag and accurate help text, and omits the blank value from requests and saved presets (`web/src/components/CompressionOptionField.tsx`, `withoutBlankOptionalValues`).

Still unverified from #9's regression list: a real FFmpeg/`cjxl` conversion of the new no-cap path (the Worker test uses the deterministic quality-converter fixture), non-monotonic encoder behavior, mixed batches and AVIF/JXL 2 MB output comparisons. The quality search assumes size grows monotonically with quality.

## Unified Jobs & history ([#24](https://github.com/Timisah/101conv/issues/24)) — implemented, CI-verified

[CI #107](https://github.com/Timisah/101conv/actions/runs/39597290879) on `cb8f7f3` passed all ten jobs, including the Docker browser acceptance suite with the rewritten selectors and the isolated restore conversion; `cb8f7f3` is on `main`. Not deployment evidence. The Jobs & history screen is now one filterable stream instead of a Batches section above a jobs table:

- **API (additive):** `GET /api/v1/history` returns standalone jobs and batch parents newest first, with `mode`, `type` (job/batch), `status` (including batch-only `partial`), `q` (IDs including a batch member's job ID, file names, profile IDs, formats), `from`/`to`, administrator-only `owner`, and paging. Batch member jobs are never top-level entries. Ownership is applied in SQL from the credential; non-administrators get 403 for `owner` and receive no owner fields. `GET /api/v1/jobs` and `/batches` and every existing URL are unchanged; a partial index on standalone jobs is added (`conv_jobs_standalone_history_idx`, `CREATE INDEX IF NOT EXISTS`). Documented in `docs/api/v1/openapi.yaml`.
- **UI:** new `web/src/features/history/` module (API client, URL-synced filter state, filters, entries, page). Filters live in the URL (bookmarkable), search is debounced, filters collapse behind one button on phones, batches show a progress bar, per-file results, savings and an expand affordance, open batches refresh while polling, and an administrator owner filter is offered. `/jobs` is the same route. `Jobs`, `PageHeading`, `StatePill`, `Empty` and formatting helpers were extracted from `App.tsx` (about 12 KB shorter).
- **Behavior change to note:** jobs created through the Convert/Compress screens are one-file batches, so they now appear as batch entries ("Conversion batch", one file) rather than as separate job rows; the old table duplicated every batch member. Standalone entries are jobs submitted directly through the API or automations.
- **Evidence run locally:** `go test ./...` with a local PostgreSQL 16 (new `TestHistoryGroupsBatchesFiltersAndIsolatesOwners` covering grouping, owner isolation, filters, search escaping, paging and invalid filters, plus guest history isolation in the existing HTTP integration test), `go vet`, frontend lint/build, `ui-smoke.cjs` (fixture-backed grouping, filter composition, URL state, reset, phone and 320 px layouts), `theme-smoke.cjs`, `public-smoke.cjs`.
- **Docker suite:** its job-row selectors (`tr`) were rewritten to expand the batch card and use batch item rows (`web/smoke.cjs`, `web/cleanup-smoke.cjs`) and passed in CI #107. Unmet from the issue: batch controls are preserved but not exercised at scale, no time-range UI beyond presets (24 h/7 d/30 d), and no history-wide per-owner pagination beyond 100,000 offsets.

## Administrator page UX pass — 2026-10-21 (working tree, awaiting CI)

Not deployment evidence. Reviewed every administrator screen against a real Core and PostgreSQL (local, no Docker) at desktop and 390 px, then fixed what was concretely wrong:

- **Navigation:** Profiles/Engines and the three converter tabs were all highlighted at once because `NavLink` adds `active` on any path match; the active state is now computed from the query string.
- **Operations** (`web/src/features/admin/OperationsPage.tsx`): a ready/not-ready banner, status tiles (Core, Database, Worker, Converters, Storage) with colored pills, issues with a link to the fix, job-state counts, worker cards and repository trust as pills; refreshes every 15 s while open.
- **Instance settings:** removed the stale "Not exposed by current API" rows (replaced by a link to Operations); the Anonymous access form is grouped into per-guest limits, whole-instance capacity and retention with units and hints (`AnonymousAccessForm.tsx`); the session-lifetime field and repository-trust card layout were aligned with the rest of the app.
- **Users & access:** page heading consistent with other pages, selects styled like the rest of the app, administrators show "Full administrator access" instead of every permission chip (others get a collapsible list), tabs show a scroll fade on phones.
- **Automations:** the create form shows everyday fields first and puts file matching and timing/output tuning behind two collapsible sections; the raw-JSON extension installer is collapsed under "Extension releases (advanced)". Inputs share the app's field styling.
- **Sizes and labels:** `bytes()` now scales to GB/TB ("28.8 GB", not "29447.7 MB"), and `pretty()` humanizes underscores ("Not started").

Verified locally: `tsc`, production build, `ui-smoke.cjs` (new assertions for Operations content/links, one active converter nav item and one active tab, no horizontal overflow at 390/320 px), `theme-smoke.cjs`, `public-smoke.cjs`. The Docker suite's automation step now opens the two new collapsible sections before filling those fields; unverified until CI. **Known gaps found but not fixed:** duplicate "FFmpeg Image Conversion" v1.0.0/v1.1.0 cards in the catalog (tracked by [#23](https://github.com/Timisah/101conv/issues/23)), the Storage and Profile-defaults pages were reviewed only for sizing/label fixes, Drives and Account were not changed, and no screen-reader pass was done.

## Simple compression controls and inline results ([#27](https://github.com/Timisah/101conv/issues/27)) — implemented in the working tree, awaiting CI

Not deployment evidence. Convert and Compress now keep progress and results on the page where work was started:

- **Inline results** (`web/src/features/results/`): after submitting, a compact summary follows the batch through queued/running/finished/partial states. A single file gets a labeled **Download result** (with output size and savings); a multi-file batch gets **Download all successful files (.zip)** once it has finished, plus an optional per-file list with per-file downloads. Failed files explain why and offer no download; expired or missing results are reported, never offered. A secondary "Open full history" link remains. It reuses the history module's batch API and download helpers, so authorization and result semantics are unchanged (guests use the same cookie).
- **Polling bounds:** the hook polls only while the batch can still change, waits while the tab is hidden, gives up after three consecutive failures and cancels on unmount; one download runs at a time so a double click cannot fetch twice. This replaces Convert's previous tracker, which requested up to 50 stored jobs every 2.5 s, together with its "Recent jobs" and "conversions in progress" panels.
- **Preserve metadata:** a checkbox (checked by default) replaces the technical Preserve/Remove select wherever a provider exposes the `0`/`-1` metadata option, in both Convert and Compress. It writes the same serialized values, so saved presets, queued jobs and API clients are unchanged; providers without that option show no control.
- **Less copy:** removed the redundant output-format sentence, the "Choose profile settings…/how your files are processed" subtitles, the generic encoder-control hint, and shortened the advanced-override and quality hints. Size-limit, privacy and error guidance was kept.

Verified locally: `tsc`, production build, `ui-smoke.cjs` (progress from processing to a single download, partial batch with zip and per-file list, failed file without a download, expired result without a download, metadata default and both serialized values, no horizontal overflow at 390 px), `theme-smoke.cjs`, `public-smoke.cjs`. **Not verified:** the Docker browser suite with real conversions and guest downloads; no screen-reader pass; the toast "Batch created…" is kept because the Docker suite asserts it, so it still duplicates the inline summary.

## Confirmed issues at reviewed HEAD — address before general public launch

1. **[P0] Nginx upload size mismatch — fixed and ingress-verified.** The continuation working tree sets `client_max_body_size 1025m`, matching Core's 1 GiB plus 1 MiB multipart request bound. The upload-proxy regression passed config validation, forwarded an actual 1 GiB multipart file and a 106 MiB chunked file to an isolated byte-counting upstream, and rejected a declared request above 1025 MiB with HTTP 413. CI run #98 passed this regression, the Docker browser suite, and the related cancellation/guest-storage paths. Deployment-specific outer proxies and host capacity remain operational checks; GitHub issue #1 stays open for that environment-specific follow-up.
2. **[P0] Static `/readyz` — fixed, unit-verified, and deployment-verified.** `/healthz` is process liveness. `/readyz` checks PostgreSQL schema access and Core-managed storage, returns HTTP 503 with safe diagnostics when unavailable, and returns HTTP 200 for a fresh unconfigured instance so setup can proceed before a Worker or converter profile exists. CI run #98 passed the complete Go, Compose, document-runtime and browser gates; `/api/v1/operations` remains the authenticated source for Worker/profile `readyToConvert` diagnostics. GitHub issue #2 stays open for an independently exercised live dependency outage/recovery.
3. **[P0] Embedded compatibility catalog / documentation drift — fixed and CI-verified.** `catalog/targets/catalog/index.json` is now the authoritative release list. `scripts/sync-compatibility-catalog.py` validates all target manifest identities, generates `catalog/index.json` and `server/internal/catalog/official.json`, preserves the legacy `catalog/profiles/` paths, and runs in CI. CI run #98 passed the catalog drift check and document runtime verification; README and catalog-format documentation describe the same managed LibreOffice/Poppler and compression entries. Signed production publication remains the separate owner-controlled #4 trust-root gate. GitHub issue #3 is reconciled as completed.
4. **[P0 pre-public-release] No pinned official trust root — ceremony prepared, publication outstanding.** `server/internal/repositorymanager/official-root.json` remains empty by design. `docs/security/trust-root-ceremony.md` records offline custody, independent fingerprint verification, rotation, recovery, and publication steps. Production signing keys and root metadata require an owner-controlled release operation; no AI should create or check in signing secrets automatically.

## Risks/decisions needing targeted audits

- **Worker isolation:** `scripts/verify-compose-security.sh` and live inspection confirm an unprivileged Worker with only the internal database network, read-only engine/input mounts, writable results, dropped capabilities, read-only root, and CPU/memory/process bounds. The managed deployment now uses a dedicated non-superuser Worker database role with compatibility DML grants; existing installations must run the explicit additive migration. The Worker still shares the application result volume, and hostile-parser/public-demo threat-model work remains open.
- **Five-character password policy:** intentional compatibility behavior in current auth code; assess risk, throttling, warnings and rollout without breaking accounts. Owner approval for a policy change.
- **Mutable image/base dependencies:** production Compose and build-stage references are now pinned to recorded digests, with exact document package revisions. Verify those digests in the remote CI/build environment and retain rollback images. `sslmode=disable` operates on the isolated Docker DB network; risk depends on deployment, not an automatic universal defect.
- **`no-new-privileges` absent by design:** documented compatibility behavior with snap-packaged Docker; retest host variants and consider opt-in hardening, not blind change.
- **Real hardware/device support, full HDR preservation, image target-size and combined batch budgets:** not yet verified as general product features.
- **Frontend modularity:** `web/src/App.tsx` remains large; recent feature extraction is partial. Split by workspace/service rather than another broad rewrite.

## Planned, not yet implemented as generally available

- P1 capability/provider migration and saved presets: implemented in the completion commit and verified by CI run #98, including browser/mobile acceptance. The deployment continues to retain legacy profile IDs as a migration boundary. GitHub issues #6 and #7 are reconciled as completed; broader modularization remains tracked by #8.
- Metadata-based automation conditions, richer media controls, and engine-specific optional GPU execution.
- Document Creator: the first Build editor now provides non-destructive page ordering, thumbnails, ordered preview, and browser-local PDF generation for raster image pages; server-side PDF/Office expansion and merging, saved/expiring projects, and fidelity-tested exporter output remain planned.
- Full visual Automation branches and broader blueprint library beyond the supported typed flow.
- Executable/remote extension packages, external application probing, and the Immich connector.
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
  sign-in links, a compact catalog-backed image/media/document capability
  summary, a copyable Docker quick start, responsive markup, keyboard skip
  navigation, and no external analytics or scripts. It also ships a concise
  credential-free `llms.txt` index for crawler and assistant discovery.
- The README now carries a matching linked catalog matrix for GitHub-first
  discovery, with explicit document, media, animation, metadata, and fidelity
  caveats, plus a generated public-page screenshot.
- The authenticated SPA shell is marked `noindex,nofollow`; `robots.txt`
  allows only the public information page and `llms.txt`. No new brand,
  canonical domain, sitemap publication, or public conversion demo has been
  selected.
- The production frontend build now runs `web/public-smoke.cjs` in CI to
  protect the static route, robots/no-index boundary, Schema.org metadata,
  workspace descriptions, quick-start content, and absence of remote page
  assets; it also checks the README screenshot and `llms.txt` for unsafe
  credential strings.

Evidence: `web/public/about/index.html`, `web/public/robots.txt`,
`web/index.html`, `web/public-smoke.cjs`, and
`docs/architecture/public-discoverability-p5.md`.

## P6 progress in the current worktree (not deployment evidence)

- Automations now expose a bounded typed graph alongside the existing flat C.1
  definition. The graph validates node kinds, block pins, configuration limits,
  acyclic/reachable connections, and rejects executable-looking configuration.
  New revisions project the graph into the existing safe folder executor, while
  legacy definitions receive a generated graph view without rewriting history.
- The Automations screen now has a mobile-friendly visual flow view for trigger,
  condition, action, and output blocks, with the existing detailed editor,
  preview, run-now, history, and immutable-revision behavior retained.
- Declarative extension manifests have a retained version table and admin-only
  install/activate/rollback APIs. API compatibility is checked before
  activation; incompatible versions stay retained and visible, and active
  blocks are added to the typed block catalog. No extension package code is
  downloaded or executed.

Evidence: `server/internal/automation/graph.go`,
`server/internal/automation/extensions.go`, the additive automation migration,
`server/internal/httpapi/automation.go`, `web/src/App.tsx`, and
`docs/architecture/automations-c1.md`. Focused Go and HTTP tests, frontend
lint/build, theme smoke, and UI smoke pass. CI run #98 also passed the full Go
suite and browser/mobile gates; production deployment verification and the
remaining extension-security scope are separate release concerns.

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

### Focused image-capability follow-up — 2026-10-21

The current worktree also corrects the image compression capability boundary:

- A separate pinned-FFmpeg image-compression profile exposes same-format JPEG
  recompression plus PNG, WebP, and AVIF outputs. Its rules publish
  codec-specific quality/level ranges, metadata preservation/removal, and
  bounded target-size controls where the encoder supports adaptive search.
- Canonical capability options are now rule-scoped, so a PNG mapping does not
  display JPEG quality and a WebP mapping uses its own quality direction.
  Convert and Compress derive formats and automatic-provider summaries from
  the capability catalog; profile selection remains an Advanced override.
- The pinned FFmpeg verification script now exercises the image encoder
  arguments, signatures, dimensions, same-format JPEG size reduction, and
  corrupt-input rejection. Local host FFmpeg smoke output was also produced,
  but it used the host's 7.1.5 binary rather than the catalog's pinned 9.x
  artifact.
- Catalog generation and shell/JSON consistency checks pass in this
  worktree. CI run #98 passed the pinned FFmpeg matrix, full Go suite, browser
  acceptance and restore conversion. The local host smoke used FFmpeg 7.1.5
  rather than the pinned catalog artifact; pinned-artifact CI is the release
  evidence.

Evidence: `server/internal/worker/runner.go`,
`server/internal/analysis/service.go`, `web/src/features/Inspector.tsx`,
`catalog/targets/profiles/ffmpeg-compression-linux-amd64.json`,
`docs/architecture/compression-p2.md`, and the focused/full package test runs
performed in this worktree. Production deployment and broad codec-specific
HDR10+/Dolby Vision compatibility remain unverified release risks.

## Owner-approved backlog additions — 2026-10-21 (not implemented)

These items document product UX and reporting requests rather than verified deployed functionality:

- **[FR-019](FEATURE_REQUESTS.md#fr-019--clear-profileengine-updates-and-catalog-lifecycle-ux) / [#23](https://github.com/Timisah/101conv/issues/23):** clearly distinguish new converter-profile installation from an available newer version, with admin update discovery, preview/compatibility, safe version retention and rollback. Existing catalog/version APIs are not a complete intuitive update experience; the reported JPG→JXL-only instance required installation of a **separate** FFmpeg Image Compression profile, not an update to JPEG XL.
- **[FR-020](FEATURE_REQUESTS.md#fr-020--unified-filterable-jobs-and-history) / [#24](https://github.com/Timisah/101conv/issues/24):** a filterable Jobs & history page for independent/grouped-batch Convert/Compress jobs is implemented and CI-verified at [CI #107](https://github.com/Timisah/101conv/actions/runs/39597290879); deployment remains unverified. A visual thumbnail collage/details experience is now requested as FR-023; Batches remains secondary to unified history.
- **[FR-021](FEATURE_REQUESTS.md#fr-021--admin-only-lifetime-processing-statistics) / [#25](https://github.com/Timisah/101conv/issues/25):** admin-only durable lifetime and per-user processed-data stats with reliable counting, retention-independent storage, privacy and honest legacy backfill.
- **[FR-023](FEATURE_REQUESTS.md#fr-023--safe-upload-and-jobs-history-file-previews) / [#28](https://github.com/Timisah/101conv/issues/28):** requested image previews upon file selection, a recognizable thumbnail for single jobs, four-tile batch collages (three thumbnails +N) and expandable per-file original/result previews with individual/ZIP downloads. Privacy-safe thumbnail lifecycle and large-batch performance remain design/implementation tasks; **not implemented**.
- **[FR-022](FEATURE_REQUESTS.md#fr-022--simple-metadata-controls-quiet-helper-copy-and-inline-downloads) / [#27](https://github.com/Timisah/101conv/issues/27):** the Preserve metadata checkbox, reduction of repeated explanatory copy and completed single-file/batch downloads on Convert/Compress are now implemented and CI-verified by [CI #112](https://github.com/Timisah/101conv/actions/runs/39600102181) and [main CI #114](https://github.com/Timisah/101conv/actions/runs/39601103435). Deployment, accessibility and real-file edge-case verification remain distinct.
- **[FR-009](FEATURE_REQUESTS.md#fr-009--rich-image-compression-and-combined-batch-size-goal) / [#9](https://github.com/Timisah/101conv/issues/9) follow-up:** optional maximum size must be initially blank; when explicitly set, maximize feasible quality under the cap rather than over-compressing. The owner's 5 MB source / 2 MB requested maximum / ~0.5 MB result is a reproduction target; the existing implementation is not accepted as fully meeting this UX.

These additions do not change P0 release gates. Newer [main CI #114](https://github.com/Timisah/101conv/actions/runs/39601103435) has now passed the full ten-job suite at main commit `2d55e2d`; this is code/CI verification, not evidence that the user's own deployed instance has upgraded or is configured with the newest profiles. The visual preview request (FR-023), lifecycle clarity (FR-019), and lifetime stats (FR-021) remain future work.

## Immediate implementation queue

**Next:** execute the owner-controlled trust-root ceremony when release ownership is ready, then continue the targeted Worker compatibility-grant and public-demo threat-model audits. Keep the deployment classified as trusted single tenant/small team until those gates are accepted.

The 2026-10-20 local release pass rebuilt the pinned Core, Worker, and Web images, migrated the managed Worker login, and passed the local upload ingress, Compose security, catalog, FFmpeg and document checks. Remote CI run #98 subsequently passed the full PostgreSQL/race/vet suite, document runtime, Docker browser acceptance and isolated backup/restore conversion. The earlier disposable PostgreSQL `ENOSPC` and browser/restore preflight blockers are resolved in verified CI; no owner-controlled trust-root ceremony or general public-demo approval is implied.

The status ledger should be updated whenever a significant task is implemented **and verified**. Record the completion SHA, test URLs, compatibility impacts, completed/requested feature IDs and explicit outstanding risks. Current tracking reconciliation: GitHub issues #3, #6 and #7 are completed; #1 remains open for deployment-specific ingress/quota verification; #2 remains open for a live dependency outage/recovery exercise; #4, #5 and the later milestone issues remain open by design. The remaining P0 release gates are the owner-controlled TUF trust-root ceremony/publication and the public-demo threat-model decision.
