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
| FFmpeg audio/video, JPEG XL and FFmpeg image profile 1.2.0 | Catalog definitions and `scripts/verify-ffmpeg-image.sh`; runtime/encoder-specific limits apply |
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

The #9 follow-up is now source- and test-verified in this worktree: the Worker
searches small quality ranges completely, probes bounded interior points when
large-range endpoints are insufficient, and reports when no tested setting
fits instead of claiming universal infeasibility. A deterministic
non-monotonic encoder fixture, mixed-profile budget allocation, and a real
FFmpeg image no-cap invocation pass in the full Go suite; `go vet ./...` also
passes. The real image test uses the host FFmpeg 7.1.5 binary. Pinned FFmpeg
target-size Worker execution, `cjxl` no-cap execution, and AVIF/JXL 2 MB
comparisons remain unverified release checks.

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

## Converter update discovery ([#23](https://github.com/Timisah/101conv/issues/23)) — implemented, CI pending

Not deployment evidence. An administrator can now tell a *new* profile from an *update* and sees what an update changes before approving it:

- **One vocabulary** (`web/src/features/catalog/`): **New · not installed** (a profile ID that is not installed), **Update available vX → vY** (a higher version of an installed ID; a different profile is never called an update), **Installed vX**, and the existing Incompatible section. The same tag appears in the catalog, on installed profiles in Manage converters (linking to the review), and as a count badge on the "Repository catalog" navigation item.
- **One card per profile:** the catalog lists several releases of a profile (for example FFmpeg Image Compression 1.0.0 and 1.1.0). Install and update both act on the highest release, so the card shows that release and notes the older ones.
- **Change summary before approval** (backend, additive): the update plan response gains `installedVersion` and `profileChanges` — added/removed format mappings, new/removed options and changed option defaults (for the image-compression 1.1.0 releases: "Maximum output size (MB): default changes from 8 to none (optional)"), license-text and engine-requirement changes. The browser `confirm()` dialog is replaced by an inline plan showing the version change, these lines, publisher, trust, license and the engine each install would reuse or add. Nothing is installed or updated without that explicit step; a plan still expires.
- **Surfacing:** the catalog is loaded once per admin session and cached for 30 minutes in `sessionStorage` (a forced refresh is on the Refresh catalog button), because the repository fetch goes to GitHub each time. The badge counts available updates plus profiles that appeared since the administrator last opened the catalog; on first use everything is marked seen, so an existing catalog does not read as new.
- Failures to load the catalog degrade to the existing error state and never block the application.

Verified locally: `go test ./...` against PostgreSQL 16 (new `DescribeProfileChanges` tests use the real 1.0.0/1.1.0 manifests), `go vet`, `tsc`, production build, `ui-smoke.cjs` (new: update tag on an installed profile, nav badge count, one card for two releases, plan content, the approved plan id is what is applied, "New" banner and tag, no overflow at 390 px), `theme-smoke.cjs`, `public-smoke.cjs`. **Not verified:** a real install or update against the live GitHub catalog, and the Docker suite with the new pages. Not done from the issue: missing-engine states beyond "Also installs the X engine", rollback UI, and updating several profiles at once.

## Official profile app-version bound alignment & pinned engine artifact refresh ([#30](https://github.com/Timisah/101conv/issues/30)) — implemented, CI-verified

[CI run #44521306926](https://github.com/Timisah/101conv/actions/runs/44521306926) on `e2e88af` (`main`) passed all ten jobs, including PostgreSQL Go tests, `document-conversion` runtime verification, pinned FFmpeg image verification, Docker browser acceptance, and isolated backup/restore conversion; not deployment evidence. Three official profile releases (`ffmpeg-image-linux-amd64` `1.1.0`, `libreoffice-pdf-linux-amd64` `1.0.0`, and `poppler-pdf-linux-amd64` `1.0.0`) previously capped `maximumAppVersionExclusive` at `0.2.0` while `catalog/targets/catalog/index.json` and the remaining official profiles declared `1.0.0`, and two pinned upstream artifacts in the catalog/Worker image had aged out of rolling mirrors:

- **Immutable aligned releases:** published `ffmpeg-image-linux-amd64` `1.2.0`, `libreoffice-pdf-linux-amd64` `1.1.0`, and `poppler-pdf-linux-amd64` `1.1.0` at new immutable paths under `catalog/targets/profiles/` with `maximumAppVersionExclusive: "1.0.0"`, keeping all existing `1.0.0` and `1.1.0` target manifests and catalog entries intact.
- **Durable upstream FFmpeg & Debian LibreOffice pins:** updated `catalog/targets/engines/ffmpeg-linux-amd64.json` from the expired daily `autobuild-2026-10-15-13-03` asset to the two-year retained month-end release `FFmpeg-Builds/FFmpeg-Builds` `autobuild-2026-10-31-12-59` (`ffmpeg-n9.0.2-39-g7adfbe40dc-linux64-lgpl-9.0.tar.xz`, engine `9.0.2-39-g7adfbe40dc+101conv.1`, `136027168` bytes, SHA-256 `6046d6731b26f58887e953561551d254a6eec9fbca4100dfeab56e19af8328d5`), and synchronized `server/Dockerfile.worker`, `engines/document-tools/office-convert`, `scripts/package-document-engines.py`, `server/Dockerfile.documents-test`, and `catalog/targets/engines/libreoffice-linux-amd64.json` to Debian `trixie-security` `libreoffice-{core,writer,calc,impress}=4:25.2.3-2+deb13u12` (managed adapter `25.2.3+deb13u12.1`) while retaining `catalog/targets/packages/libreoffice-25.2.3+deb13u8.1-linux-amd64.tar.gz` at its immutable archive path and keeping `poppler-utils=25.03.0-5+deb13u4`.
- **Synchronized derived indexes & Marketplace filtering:** regenerated `catalog/index.json` and `server/internal/catalog/official.json` via `python3 scripts/sync-compatibility-catalog.py --write` (4 engines, 14 profile releases), verified `catalog.New("0.2.0")` retains the aligned releases while filtering out the `0.2.0`-capped historical releases, and updated `repositorymanager` (`loadUnsignedView` and `loadViewLocked`) so superseded historical releases with narrower app-version bounds are omitted from `IncompatibleProfiles` whenever a compatible release of the same profile ID exists (and genuinely incompatible multi-version profiles collapse to their highest semver release).
- **Author preflight ceiling guard:** `catalog-repo validate` (`server/cmd/catalog-repo/main.go`) now validates catalog index app-version bounds and rejects any profile whose latest (highest semver) release sets `maximumAppVersionExclusive` below `index.MaximumAppVersionExclusive` unless an explicit `maximumAppVersionReason` is recorded on the outer target manifest wrapper or catalog index entry; superseded historical releases continue to validate without modification.

Verified locally and in CI #44521306926: `python3 scripts/package-document-engines.py`, `python3 scripts/sync-compatibility-catalog.py --check`, `scripts/verify-ffmpeg-image.sh` (full 7×9 image matrix, compression invocations, signatures, dimensions, GIF animation, and corrupt-input rejection against the pinned `autobuild-2026-10-31-12-59` archive), `scripts/verify-documents.sh` Docker Worker build + runtime test, `go run ./cmd/catalog-repo validate --catalog-root ../catalog`, PostgreSQL-backed `go test ./...`, `go test -race -p 1 ./... -count=1`, `go vet -p 1 ./...`, and `npm run lint && npm run build && npm run test:public`; official TUF root signing remains the separate owner-controlled gate ([#4](https://github.com/Timisah/101conv/issues/4)).

## Frontend `App.tsx` decomposition ([#34](https://github.com/Timisah/101conv/issues/34)) — implemented, CI-verified

[CI run #44527977281](https://github.com/Timisah/101conv/actions/runs/44527977281) on `9c8e4cd` (`main`, PR #36) passed all jobs. Not deployment evidence. `web/src/App.tsx` previously held 15+ inline screens, domain types, and API hooks in 510 densely packed lines (~172 KB):

- **Shell-only `App.tsx` (245 readable multi-line lines):** `web/src/App.tsx` now owns only the authenticated/guest shell layout (`.sidebar`, `.topbar`, `.mobile-nav`), route guards and `<Routes>`, `CatalogNavBadge`, and `PageErrorBoundary`, delegating session/setup/readiness/update polling to `web/src/features/auth/useAppSession.ts`, pre-shell setup/visitor routing to `web/src/features/auth/AuthGate.tsx`, and navigation metadata to `web/src/features/auth/navigation.ts`.
- **Feature-owned modules:** extracted `web/src/features/useData.ts` (with `mergeDataDefaults` preserving initial non-null defaults when API responses omit or null top-level keys), `web/src/features/auth/{SetupClaim,SetupReview,LoginPage,VisitorConversion,types}`, `web/src/features/presets/PresetBar.tsx`, `web/src/features/convert/ConvertPage.tsx`, `web/src/features/compress/CompressPage.tsx`, `web/src/features/converters/{types,ConvertersPage,ProfilePreferences,ProfileDefaultsPage}`, `web/src/features/automations/{types,DrivesSection,AutomationsSection,ExtensionReleases,AutomationAdministration}`, `web/src/features/storage/StoragePage.tsx`, `web/src/features/admin/{InstanceSettingsPage,UserAdministrationPage,AccessControls,AccessModals,quotas}`, and `web/src/features/account/AccountPage.tsx`.
- **Typed contracts, unit coverage & cascade parity:** aligned `Profile.state.active?: boolean` with the backend response contract, removed dead `hasCompatiblePreference` and unused `lucide-react` imports, guarded list/quota fallbacks across extracted admin/account/automation screens, added `web/module-smoke.mjs` (`npm run test:unit` in CI `frontend`) covering navigation metadata, converter helpers, quota formatters, automation condition unpacking, and `mergeDataDefaults`, and preserved the exact stylesheet import cascade.

## P1 Catalog & Preview completion ([#31](https://github.com/Timisah/101conv/issues/31), [#32](https://github.com/Timisah/101conv/issues/32), [#28](https://github.com/Timisah/101conv/issues/28)) — implemented, CI-verified

Not deployment evidence. Completed the remaining P1 catalog architecture, catalog scaling, and visual preview milestones:

- **Side-by-side installed profile versions & rollback ([#31](https://github.com/Timisah/101conv/issues/31), PR #37, [CI run #45598396445](https://github.com/Timisah/101conv/actions/runs/45598396445)):** Additively added `state.active` to `profile.Installation` with automatic normalization of legacy `management.json` files. `UpdateProfile` and `InstallVerifiedProfile(..., update=true)` retain prior versions side-by-side (`active: false`) instead of overwriting in place. `ResolveConversionVersion`, `automation.Service.ValidateDefinition`, and `presets.Service` continue resolving pinned retained versions without drifting and surface informational notices (`Automation.profileNotice`, `Preset.compatibilityMessage`). Added `GET /api/v1/profiles?includeRetained=true`, `GET /api/v1/profiles/{id}/versions`, `PUT /api/v1/profiles/{id}/active`, and `DELETE /api/v1/profiles/{id}/versions/{version}` (HTTP 409 if active or referenced by active jobs, automations, or presets), plus `ProfileVersionPicker` in `ConvertersPage.tsx`. Resolves the side-by-side retention blocker in [#23](https://github.com/Timisah/101conv/issues/23).
- **Scaled Repository Catalog ([#32](https://github.com/Timisah/101conv/issues/32), PR #38, [CI run #45599400489](https://github.com/Timisah/101conv/actions/runs/45599400489)):** Indexed source/target formats, capabilities, and descriptions in Marketplace search with responsive media category filter pills (`All`, `Images`, `Audio`, `Video`, `Documents`, `Archives`) and deduplicated format chips with `+N more` / `Show fewer` disclosure. Added declarative `bundles` (`Essential Images`, `Office & PDF`, `Audio & Video`) to `catalog/targets/catalog/index.json` and `repositorymanager`, plus deduplicated multi-profile & **Review all updates** batch plans (`POST /api/v1/repositories/{id}/batch-preview` and `/batch-apply` in `server/internal/repositorymanager/batch.go`).
- **Safe upload thumbnails & visual Jobs/history batch previews ([#28](https://github.com/Timisah/101conv/issues/28), PR #39, [CI run #45601745954](https://github.com/Timisah/101conv/actions/runs/45601745954)):** Added pure-Go bounded, EXIF-oriented thumbnail renderer (`server/internal/thumbnails/`) with decompression-bomb bounds and ownership/retention-checked endpoints (`GET /api/v1/uploads/{id}/thumbnail`, `GET /api/v1/jobs/{id}/thumbnail?kind=result|original`). Hydrated up to 3 `previewItems` per batch in `GET /api/v1/history`, added reusable `web/src/features/previews/` (`LocalFileThumb` with `URL.revokeObjectURL` cleanup in Convert/Compress, `RemoteFileThumb` with explicit `Output` vs `Original` badges, and `BatchCollage` showing 1–3 tiles or 3 tiles + `+N` without fake `+0`).

## P2 Analytics, Media Expansion & Analysis Reuse ([#25](https://github.com/Timisah/101conv/issues/25), [#33](https://github.com/Timisah/101conv/issues/33), [#11](https://github.com/Timisah/101conv/issues/11), [#9](https://github.com/Timisah/101conv/issues/9), [#10](https://github.com/Timisah/101conv/issues/10)) — implemented, CI-verified

Not deployment evidence. Completed all five P2 milestones on `main` (`66da1c4`):

- **Durable admin analytics ledger & dashboard ([#25](https://github.com/Timisah/101conv/issues/25), PR #40, [CI run #45604098160](https://github.com/Timisah/101conv/actions/runs/45604098160)):** Added an additive PostgreSQL processing ledger (`conv_processing_ledger`, `conv_analytics_meta`, `SECURITY DEFINER` trigger `conv_jobs_processing_ledger_trg`, and idempotent startup backfill in `server/internal/analytics/service.go`) that records terminal jobs once and retains zero filenames/paths/tokens after retention cleanup or user deletion. Added admin-only `GET /api/v1/analytics/overview` and `GET /api/v1/analytics/users` (`server/internal/httpapi/analytics.go`, returning 403 for non-admins and guests) and a responsive `AnalyticsPage` at `/analytics` (`web/src/features/admin/AnalyticsPage.tsx`) with range selectors (`24h`, `7d`, `30d`, `All time`), KPI cards, daily trend chart, breakdown tables, and per-user breakdown.
- **Official media catalog expansion & real pinned FFmpeg verification ([#33](https://github.com/Timisah/101conv/issues/33), [#11](https://github.com/Timisah/101conv/issues/11), [#9](https://github.com/Timisah/101conv/issues/9), PR #41, [CI run #45604910233](https://github.com/Timisah/101conv/actions/runs/45604910233)):** Published 5 new immutable official profile releases while keeping all historical releases untouched: `jpeg-xl-linux-amd64` `1.3.0` (`quality`, `effort`, `lossless`), `ffmpeg-linux-amd64` `1.3.0` (MP4/MOV/MKV/WebM/MP3/WAV/FLAC/OGG/M4A/Opus + GIF/WebP with `videoFps`, `videoScale`, `audioBitrate`, `stripAudio`), `ffmpeg-compression-linux-amd64` `1.1.0` (LGPL `libvpx-vp9` default with optional `targetSizeMb`, HDR/resolution preservation, and bounded CRF search), `ffmpeg-audio-mastering-linux-amd64` `1.0.0` (Opus/AAC/MP3/FLAC/WAV/M4A/OGG with bitrate, sample rate, mono/stereo, and optional EBU R128 loudness normalization), and `ffmpeg-animated-web-linux-amd64` `1.0.0` (GIF/WebP/APNG/MP4/WebM loops with FPS, max width, quality, and loop count). Synchronized `catalog/index.json` and `server/internal/catalog/official.json` (4 engines, 19 profile releases) and extended `scripts/verify-ffmpeg-image.sh` against pinned FFmpeg 9.0.2 to verify VP9 video compression, Opus/AAC/MP3/FLAC audio mastering, animated GIF→MP4 loop conversion, and JPEG/WebP/AVIF quality-order scaling. HDR10+/Dolby Vision dynamic metadata remains explicitly rejected until tested.
- **Shared file analysis reuse & metadata-driven Automation conditions ([#10](https://github.com/Timisah/101conv/issues/10), PR #42, [CI run #45605474016](https://github.com/Timisah/101conv/actions/runs/45605474016)):** Added `SourceAnalysisHint` (`web/src/features/previews/SourceAnalysisHint.tsx`) wired into Convert and Compress to display ephemeral source dimensions, container/codec, HDR/color-space, and frame-rate hints via `POST /api/v1/inspect` with a one-click jump to Inspector. Added bounded metadata-driven Automation conditions (`min_width`, `min_height`, `hdr`, `codec`, `has_audio`) in `server/internal/automation/service.go` and `runner.go` (`matchesDefinitionFile`, `probeFileMetadata`, `evaluateConditionWithMetadata`), covered by `TestMetadataDrivenConditions`.

## P3 Document Creator completion ([#12](https://github.com/Timisah/101conv/issues/12)) — implemented, CI-verified

Not deployment evidence. Completed the full P3 multi-format visual Document Creator with non-destructive page editing (merged via PR #43):

- **Pure-Go PDF parser, native vector/text preservation & multi-format assembler (`server/internal/document/`):** Added bounded PDF parser (`pdf_parser.go`) supporting xref tables, incremental updates, PDF 1.5+ cross-reference streams, compressed object streams, and encrypted-PDF rejection; shared Go page geometry resolver (`geometry.go`, matching `web/src/features/documentLayout.ts`); pure-Go PDF assembler (`pdf_assembler.go`) preserving native PDF vector text/streams via object-closure renumbering and verbatim original JPEG bytes (`/DCTDecode`, zero recompression even when rotated via PDF CTM) plus lossless PNG (`/FlateDecode` + `/SMask`); and spec-compliant CBZ, ordered-images ZIP, multipage TIFF, DOCX, PPTX, ODT, and ODP assemblers (`raster_formats.go`, `office_formats.go`, `assembler.go`) with LibreOffice/Poppler Worker bridge and honest per-format fidelity metadata.
- **Persisted projects & durable Worker execution (`server/internal/document/store.go`, `server/internal/jobs/document_build.go`, `server/internal/worker/document_build.go`, `server/internal/httpapi/documents.go`):** Added additive `conv_document_projects` table with ownership isolation, persistent user projects, automatic guest project expiry/purge, source page inspection (`GET /api/v1/documents/sources/{id}`), formats endpoint (`GET /api/v1/documents/formats`), and durable `mode='build'` jobs (`POST /api/v1/documents/export`) verified end-to-end through the isolated Worker and surfaced in `GET /api/v1/history?mode=build`.
- **Modular Build workspace (`web/src/features/docbuilder/`):** Extracted `DocumentCreator.tsx`, `PageCard.tsx`, `pdfLocal.ts`, `api.ts`, `types.ts`, and `build.css` with saved/expiring project management, automatic multi-page PDF expansion into rearrangeable pages, drag-and-drop plus accessible keyboard/touch controls, flat vs source-grouped views, 8-format selector with honest fidelity warnings, inline server export polling/download, and preserved browser-local raster PDF shortcut.

## P4 GPU Acceleration, Visual Automations & Extension Marketplace / Immich ([#13](https://github.com/Timisah/101conv/issues/13), [#14](https://github.com/Timisah/101conv/issues/14), [#15](https://github.com/Timisah/101conv/issues/15)) — implemented, CI-verified (#13, #14), PR #47 awaiting CI (#15)

Not deployment evidence. Completed all three P4 milestones:

- **Optional hardware video acceleration with protected Worker isolation ([#13](https://github.com/Timisah/101conv/issues/13), PR #44, [CI run #45610751480](https://github.com/Timisah/101conv/actions/runs/45610751480)):** Added FFmpeg `-hwaccels` / `-encoders` capability probing on Worker startup (`server/internal/acceleration/ffmpeg.go`), codec- and HDR-aware selection policies (`auto`, `cpu`, `prefer_hardware`, `require_hardware`) frozen into `ExecutionSpec`, allowlist-only FFmpeg hardware argument translation for Intel VAAPI/QSV, AMD VAAPI, and NVIDIA NVENC/NVDEC, transparent CPU fallback on hardware device errors (preserving HDR CPU-only policy), additive `conv_jobs.acceleration_info` persistence, reviewed non-privileged GPU Compose overrides (`deploy/compose.gpu-vaapi.yaml`, `deploy/compose.gpu-nvidia.yaml`) enforced by `scripts/verify-compose-security.sh`, enriched Admin Operations worker diagnostics, and progressively disclosed video acceleration policy selectors in Convert/Compress.
- **Visual Automations, typed blocks & beginner-friendly blueprints ([#14](https://github.com/Timisah/101conv/issues/14), PR #46, [CI run #45612963240](https://github.com/Timisah/101conv/actions/runs/45612963240)):** Expanded the built-in block catalog (`trigger.schedule`, `condition.inspect-metadata`, `condition.branch`, `action.build`), added bounded conditional branching (`true`/`false` ports, 1..4 actions, acyclic DAG + reachability + no executable config) projected onto the existing durable C.1 automation engine via additive `Definition.fallbackAction` with explainable `skipped_branch` history and DryRun branch annotations, expanded `BuiltinBlueprints()` to 6 beginner templates, and added the mobile-first `BlueprintWizard` (`web/src/features/automations/BlueprintWizard.tsx`) plus metadata/branch controls in `AutomationsSection.tsx`.
- **Secure versioned extension marketplace & Immich connector ([#15](https://github.com/Timisah/101conv/issues/15), PR #47):** Added curated versioned extension catalog (`GET /api/v1/extensions/catalog`) with SHA-256 manifest integrity verification and explicit allowlisted permissions (`server/internal/automation/extension_marketplace.go`), external-app version compatibility checks (`ExternalVersionCompatible`), selective automation pausing (`PauseAutomationsForExtension` pausing only affected extension automations), bounded secret-safe Immich REST API client and store (`server/internal/immich/`, `/api/v1/connectors/immich*`) using documented `/api/server/about`, `/api/search/metadata`, `/api/assets/{id}/original`, and `POST /api/assets` endpoints with explicit honest fidelity disclosure, and admin UI in `ExtensionReleases.tsx` and `ImmichConnector.tsx`.

## P5 Discoverability, Isolated Public Demo & Brand Transition Plan ([#17](https://github.com/Timisah/101conv/issues/17), [#18](https://github.com/Timisah/101conv/issues/18), [#16](https://github.com/Timisah/101conv/issues/16)) — implemented, awaiting CI

Not deployment evidence. Completed the P5 engineering deliverables while preserving all owner-controlled branding and live-demo security gates:

- **Catalog-driven static GitHub Pages site & synchronized public docs ([#17](https://github.com/Timisah/101conv/issues/17)):** Added `scripts/build-public-site.mjs` generating a zero-JS, zero-tracker, responsive light/dark static site (8 substantive pages including `/formats/` generated directly from `catalog/targets/catalog/index.json`, plus `robots.txt`, `llms.txt`, and optional `sitemap.xml` + canonical tags when `SITE_BASE_URL` is configured) with opt-in `.github/workflows/pages.yml` publishing and CI verification (`node scripts/build-public-site.mjs --verify`). Synchronized `README.md`, `web/public/about/index.html`, and `web/public/llms.txt` with verified P2/P3/P5 capabilities.
- **Isolated resource-limited public demo mode ([#18](https://github.com/Timisah/101conv/issues/18)):** Added `server/internal/demo/` and `server/internal/httpapi/demo.go` (disabled by default via `DEMO_MODE=false`, zero impact on private self-hosted instances) with an administrator-toggleable overload kill switch (`PUT /api/v1/demo/kill-switch`), global hourly compute budgets, active-job ceilings, per-file/per-batch/profile allowlist bounds, and strictly aggregate non-sensitive self-hosting funnel counters (`conv_demo_funnel_events`, `POST /api/v1/demo/funnel`, admin-only `GET /api/v1/demo/metrics`). Added isolated `compose.demo.yaml` (zero NAS/host mounts, tighter resource caps, 90 s timeout, 15 min retention) verified in CI by `scripts/verify-demo-compose.sh`, modular frontend `web/src/features/demo/` (`DemoBanner` shown only when `demo.enabled`), and `docs/security/public-demo-p5.md`. Launching a live internet-facing demo remains gated on owner sign-off of P0 #4/#5.
- **Brand transition plan ([#16](https://github.com/Timisah/101conv/issues/16)):** Documented selection rubric, SVG/favicon asset specification, and non-breaking compatibility migration plan in `docs/architecture/brand-transition-p5.md`. `101conv` remains the provisional identifier pending owner brand selection (`DECISIONS.md` A-010, A-024).

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
- **Frontend modularity:** `web/src/App.tsx` is now decomposed into a 245-line shell delegating workspaces and admin screens to `web/src/features/*` ([#34](https://github.com/Timisah/101conv/issues/34)); broader UI consolidation remains tracked under [#8](https://github.com/Timisah/101conv/issues/8).

## Planned, not yet implemented as generally available

- P1 capability/provider migration and saved presets: implemented in the completion commit and verified by CI run #98, including browser/mobile acceptance. The deployment continues to retain legacy profile IDs as a migration boundary. GitHub issues #6 and #7 are reconciled as completed; broader modularization remains tracked by #8.
- Engine-specific optional GPU execution (hardware-probe foundation implemented in P4; codec-level FFmpeg arguments and deployment benchmarks remain open) and HDR10+/Dolby Vision dynamic metadata preservation.
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
`server/internal/httpapi/automation.go`, `web/src/features/automations/`, and
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
  real FFmpeg media target, real FFmpeg image no-cap behavior, deterministic
  adaptive-quality behavior including a non-monotonic encoder fixture, catalog
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

## Immediate implementation queue

**Next:** execute the owner-controlled trust-root ceremony when release ownership is ready, then continue the targeted Worker compatibility-grant and public-demo threat-model audits. Keep the deployment classified as trusted single tenant/small team until those gates are accepted.

The 2026-10-20 local release pass rebuilt the pinned Core, Worker, and Web images, migrated the managed Worker login, and passed the local upload ingress, Compose security, catalog, FFmpeg and document checks. Remote CI run #98 subsequently passed the full PostgreSQL/race/vet suite, document runtime, Docker browser acceptance and isolated backup/restore conversion. The earlier disposable PostgreSQL `ENOSPC` and browser/restore preflight blockers are resolved in verified CI; no owner-controlled trust-root ceremony or general public-demo approval is implied.

The status ledger should be updated whenever a significant task is implemented **and verified**. Record the completion SHA, test URLs, compatibility impacts, completed/requested feature IDs and explicit outstanding risks. Current tracking reconciliation: GitHub issues #3, #6 and #7 are completed; #1 remains open for deployment-specific ingress/quota verification; #2 remains open for a live dependency outage/recovery exercise; #4, #5 and the later milestone issues remain open by design. The remaining P0 release gates are the owner-controlled TUF trust-root ceremony/publication and the public-demo threat-model decision.
