# 101conv — Product Master Plan

**Status:** Living product charter and architecture direction, as of 2026-10-21.  
**Temporary project name:** 101conv. Branding, public name, logo and domains are **undecided**.  
**Current implementation state:** [PROJECT_STATUS.md](PROJECT_STATUS.md).  
**Tracked requests:** [FEATURE_REQUESTS.md](FEATURE_REQUESTS.md).  
**Architecture decisions:** [DECISIONS.md](DECISIONS.md).

This document captures long-term intent, not a claim that all described features exist. The project owner must approve material changes in direction. A new AI session must use current code and tests to distinguish shipped behavior from plans.

## 1. Vision

Create an open-source, privacy-conscious, self-hostable file workspace that helps individuals, homelabs and small teams **convert, compress, assemble, inspect and automate** files. It should be useful in seconds for a casual visitor, yet offer fine-grained, repeatable workflows for experienced users.

The product is not merely a list of extension pairs, a general-purpose untrusted-code/plugin host, a video editor, a Word replacement, or an unlimited free public SaaS. Public hosting may be offered as a strictly resource-limited demonstration of the self-hosted application.

### Five Workspace experiences

1. **Convert** — upload, detect, choose output, run; supported source/target conversions and compatible settings.
2. **Compress** — simple quality/size goals, optional target size, resolution/bitrate/HDR preservation constraints and saved presets; reports actual savings.
3. **Build / Document Creator** — visually assemble files and individual pages into an ordered, non-destructive project and export a supported document.
4. **Inspect / File Inspector** — readable file and stream information, advanced metadata and structured export; optional explicitly invoked metadata-removal job.
5. **Automate** — approachable blueprints with optional visual block editor, triggers, metadata conditions and reusable processing actions.

Jobs & history are one primary, filterable destination for individual tasks and grouped batches across Convert and Compress; specialized batch handling belongs in the detail view rather than a competing first-class page. The administrative interface (engines, providers, packages, storage, permissions, repositories and system health) must remain separate from everyday task selection.

## 2. UX contract — must survive every milestone

- **Fast path:** on mobile, a user can choose/upload files, choose a supported outcome (or accept a sensible default), and start with minimal decisions; no mandatory engine/profile selection.
- **Progressive disclosure:** common controls are understandable and reachable; expert options are available but do not dominate. Never hide genuinely common settings (resolution, target size, HDR preservation) behind a technical maze.
- **File-aware:** analyze source properties, show only compatible formats/operations/settings, explain disabled options and show safe defaults. Do not ask users to know codecs or CLI flags.
- **Preset-friendly:** a sophisticated configuration becomes one-tap reuse; never confuse a user preset with an executable provider.
- **Understandable upgrades:** distinguish a distinct new capability/profile needing installation from a newer version of an existing installed profile. Show version, availability, compatibility, update indicators, changelog and safe migration paths; upgrading application code must not be presented as having installed converter packages.
- **One history:** users can find and filter Convert and Compress work, independently submitted jobs and grouped batches, from one Jobs & history page; advanced batch actions are available through details, not a competing prominent section.
- **Mobile-first:** touch targets, readable type, single-column flows, no horizontal scrolling at narrow widths, visible primary action, accessible reordering alternatives, bottom sheets/details where useful. Test real touch and keyboard flows. Desktop may use richer previews but must preserve functional parity.
- **Non-destructive:** originals and projects remain intact by default. Undo page edits; explicit confirmation for irreversible actions. Treat privacy and retention as first-class UX.
- **Meaningful outcomes:** display progress and clear errors, before/after sizes, validation warnings (e.g. lost HDR), downloads and next actions. Avoid a false “100% successful” for invalid or incomplete output.
- **Consistent file lifecycle:** an uploaded/owned file can be passed from Inspect to Convert/Compress/Build without unnecessary reupload where authorization/retention allow.
- **Bounded batches:** bulk actions have per-item overrides only when useful; defaults and results remain understandable.

## 3. Domain model and modular architecture

### Existing execution model

Browser/API/mounted directory → Go Core → PostgreSQL durable jobs → isolated Worker → validated stored result. React/TypeScript frontend. Preserve this model and existing Portainer Compose data/paths.

### Target abstractions

- **Engine**: versioned executable/runtime and its verified provenance/licensing/platform constraints (FFmpeg, JPEG XL tools, LibreOffice, Poppler, etc.).
- **Capability**: an operation and canonical semantic constraints: e.g. image encode to JXL, video transcode preserving HDR, PDF page assembly, PDF-to-image, metadata probe; declares valid inputs, outputs and options.
- **Provider**: a versioned implementation of a capability backed by a particular engine, with supported options, quality/performance/hardware characteristics and limitations.
- **Preset**: user/admin-saved named intent and validated option values; may prefer a provider or allow automatic resolution. Not arbitrary executable arguments.
- **Resolution policy**: finds all providers satisfying input/output, installed state, explicit fidelity requirements, licensing and hardware availability; selects predictably and explainably. Do not have providers “fight” for visibility by raw priority.
- **Immutable job specification**: freezes resolved provider and engine versions, settings, relevant source analysis and expected output checks. Later updates must not change queued or historical execution.
- **Project/page manifest**: non-destructive ordered input-page references, transforms and export constraints for Document Creator.
- **File analysis**: reusable typed, bounded metadata extraction shared by Inspect, Convert, Compress, Build and Automate.

The current `profiles` are a **legacy implementation/compatibility boundary**. Migrate incrementally with adapters and additive schemas. Preserve old releases, pinned automations, existing installations and API clients. Do not delete the old model before equivalent, tested behavior exists.

### Modularity rule

Separate application concerns into cohesive packages/services and frontend feature directories; prefer small typed modules with unit/integration tests and explicit boundaries. In particular, do not keep adding unrelated features to `web/src/App.tsx` or one giant HTTP/controller component. Extract incrementally while maintaining the design system and regression coverage. Avoid wholesale rewrites justified solely by architectural fashion.

Suggested domains: `capabilities`, `providers`, `presets`, `analysis`, `document-builder`, `media`, `automations`, `repositories`, `auth`, `jobs`, `storage`, corresponding UI feature packages and common API/UI primitives. These are organizational examples, not a mandated exact directory tree.

## 4. Requested capabilities and behavior

### A. Conversion and compression

Extend file format support only with tested engine/runtime builds; use target-specific encoder behavior, not a universal flag set. Preserve existing FFmpeg, JPEG XL, Office and PDF workflows.

- Basic: keep original dimensions unless the user explicitly requests otherwise; choose a reasonable quality/size goal.
- Advanced: image quality/lossless where feasible, metadata policy, format-specific encoder options, audio codec/bitrate/channels, video codec, bitrate or quality mode, resolution, frame rate, pixel format, color properties and selected streams.
- Target-size compression: per-file limit, **combined maximum size across the selected batch**, or both; both limits are **optional and unset by default** (unless the user explicitly applies a preset that includes one). Empty means no cap: ordinary format conversion or general compression does not secretly adopt a numeric target. When a cap is requested, favor the highest feasible quality subject to the bound, preferring a result close to (but never above) the requested maximum rather than gratuitously shrinking much further. Use codec-specific bounded search/analysis, intelligently allocate batch budgets, validate actual outputs and explain infeasible limits; never claim an exact target size can always be reached.
- “Keep 4K resolution, reduce bitrate” must be independently selectable. Resolution and video bitrate are not the same control.
- HDR: preserve source transfer, primaries, matrix/range, bit depth and applicable static metadata where supported; validate resulting streams. Explicit SDR tone mapping is separate. HDR10+/Dolby Vision preservation is conditional, never silently promised.
- Report actual input/output size, saved bytes/percent and relevant quality/fidelity warnings.

### B. User presets

Named personal presets, optionally admin-shared defaults, linked to typed capabilities and validated settings. Save directly from a successful Convert/Compress configuration; apply to compatible inputs, batches and automations. Support edit, duplicate, rename, delete, preferred/default per operation or file type, and safe import/export as appropriate. Preserve saved presets when engines update; mark incompatible presets for review instead of silently remapping.

### C. Document Creator / Build

Upload multiple JPG/PNG/images, PDFs and supported Office documents; expand multipage sources into individually rearrangeable pages with optional source grouping. Drag-and-drop ordering plus accessible controls, 90° rotation, remove/duplicate, preview, undo/redo, add-more flow, layout/orientation choices only when needed. Prioritize **assembly, not resampling or resizing**: preserve original image bytes and native PDF text/vector content when possible; page *placement* on paper must not imply changing image pixel dimensions.

Use a non-destructive page/project manifest with saved projects for signed-in users and expiring guest projects; export a single coherent file. Target formats beyond PDF may include DOCX, ODT, PPTX/ODP, CBZ, multipage TIFF and ordered-image ZIP **only where content semantics can be correctly represented**. Communicate potential fidelity loss; a paginated image collection is not automatically an editable Word document. No arbitrary complex layout engine in first release. Default: one input page → one output page; multi-image page layouts are later optional enhancements.

### D. File Inspector

Readable overview for common images/audio/video/PDF/Office data; selected advanced metadata including dimensions, streams, codecs, bit depth, HDR signals, EXIF/ICC, document properties, chapters and subtitle tracks. Safe probing via bounded, vetted tools (ffprobe and optionally ExifTool/other parsers). Raw JSON export and metadata comparison before/after conversion where useful. Do not expose GPS/private metadata to other users. Metadata stripping is a separate explicit processing operation, never an accidental side effect.

### E. Hardware acceleration

Explicitly opt-in hardware capabilities: Intel Quick Sync/VAAPI, AMD VAAPI and NVIDIA NVENC/NVDEC when supported by verified builds, drivers and device mappings. Probe actual usable encode/decode and format support; use CPU fallback only when it meets mandatory fidelity/format constraints. Hardware encoding is not inherently higher quality. Preserve container least privilege; no blanket `--privileged`, unrestricted device mapping or Docker socket. A separate tightly configured hardware-enabled Worker may be appropriate.

### F. Automations and extensions

Existing mounted-folder polling/blueprints are the foundation. Build a visual, typed block editor: trigger → inspect/conditions → actions → output, optional branching, tests/dry run and explainable execution history. Blueprints provide a fast entry route; advanced graph editing must be optional and mobile usable. Preserve immutable revisions, deduplication, safe publication, permissions and version pins. Version extension packages and compatibility ranges independently of app releases; retain older compatible versions. When an external app version becomes incompatible, pause only affected blocks, preserve configuration and report remediation.

Prioritized possible connector: Immich through supported APIs; never promise unsupported in-place asset replacement or complete metadata preservation.

### G. Public discoverability and distribution

Keep GitHub as code/distribution source and early landing page. Improve README/screenshot onboarding and accurate supported-format inventory. Consider a static GitHub Pages site with truthful, substantive searchable tool/use-case pages and appropriate markup/crawler access. Avoid automatically generated doorway content or guaranteed search/AI recommendations. Optional limited public demo on isolated infrastructure with conservative global/session rate, storage, compute, duration and abuse caps. Measure real users' discovery of self-hosting rather than assume free conversions convert to installs.

### H. Brand and identity

Current name `101conv` is provisional; a similarly named company/product exists. Choose an original legal/trademark/domain-checked name and scalable logo/symbol, with both light/dark and favicon usage. Do not automatically rename repository, package paths, API or volumes until a non-breaking rollout plan is approved. Brand must represent Convert / Compress / Build / Inspect / Automate, not only file extension swapping.

### I. Converter update discovery and history / admin statistics

- **Converter lifecycle UX:** automatically surface *available* catalog changes for admins without automatically installing them; clearly label `New profile — Install` for a new logical profile ID and `Update available vX → vY` only for a higher version of the same installed ID. Explain missing engines, unsigned trust, changed settings, pinned jobs/presets and safe rollback before explicit user-approved updates. Treat the existing backend install/update-preview machinery as a foundation, not as a completed user experience ([FR-019](FEATURE_REQUESTS.md#fr-019--clear-profileengine-updates-and-catalog-lifecycle-ux)).
- **Jobs & history:** a unified searchable/filterable primary page spans individual and batched Convert/Compress operations, with operation/type/status/date filters and batch expansion. Preserve existing deep links and APIs; admin-only cross-user filters must be permission-checked server-side ([FR-020](FEATURE_REQUESTS.md#fr-020--unified-filterable-jobs-and-history)).
- **Admin-only lifetime statistics:** server and per-user aggregates can report historical processed input bytes, output bytes, job and batch counts, operation split, failures and savings. Count each completed processing item once (not batch parent *and* children or retried attempts); persist totals independently of retained files/jobs, disclose uncertain historical baselines, and protect guest/deleted-user privacy. Only authorized admins may view even per-user lifetime metrics ([FR-021](FEATURE_REQUESTS.md#fr-021--admin-only-lifetime-processing-statistics)).

## 5. Security, deployment and release requirements

- Runtime shall not execute arbitrary user expressions, commands or unsafe converter arguments. Parser input is hostile. Maintain non-root Worker, mounts/network isolation, resource bounds and strict process cleanup. Strong multi-tenant isolation would require additional architecture; do not oversell current guarantees.
- Signed, authenticated official publisher metadata with offline root custody/rotation is a release requirement for general public installs; retain trusted previous packages and reproducible pinned runtime/package identities.
- Real host proxy/uploads and dynamic health/readiness must match advertised behavior. Separate Core service readiness from “has a configured usable conversion pipeline”, so initial setup remains possible.
- Review password policy and brute-force protection explicitly; avoid breaking existing credentials. Secure forwarded-header trust boundary.
- Migrations must be additive or thoroughly versioned; protect PostgreSQL state, installed engines, mounted folders, profiles, automations, result references and retention.
- Release gate: representative real-converter and corrupted-file tests, CI, restore rehearsal, mobile browser tests and independent verification of important output properties (not only HTTP 200).
- Document whether a deployment is appropriate for trusted single tenant/small team versus openly exposed anonymous processing; public demo requires a separate threat model.

## 6. Milestone roadmap and dependencies

**P0 — Stabilization and release correctness** (first next implementation work):
- Fix Nginx/request-body ceiling vs advertised 1 GiB and test actual large uploads; respect guest/user quotas and resource constraints.
- Implement meaningful Core dependency readiness without blocking brand-new unconfigured instances; distinguish operational ability to convert.
- Unify compatibility API/catalog indexes/docs with actually shipped Office/PDF support and add drift validation.
- Define official TUF signing ceremony/root, trusted initial publication and rotation plan, owner approval and secure key custody.
- Audit Worker shared DB/storage privileges, runtime isolation, container image pins, password policy and public-demo readiness. Prioritize demonstrable risks.

**P1 — Clean capability/provider/preset architecture**:
- Introduce file analysis and canonical typed operation descriptions; provider resolution and explainable defaults.
- Remove competing technical profile choices from novice UIs (retain advanced override); migrate safely without removing legacy support.
- First-class personal presets and admin-shared defaults. Keep all new code in feature-focused modules, with mobile UX tests.
- **P1 follow-up after the architecture baseline:** improve new-profile vs profile-update discovery, safe install/upgrade UX ([FR-019](FEATURE_REQUESTS.md#fr-019--clear-profileengine-updates-and-catalog-lifecycle-ux)), and make filterable, unified Jobs & history the main entry point for singles and grouped batches ([FR-020](FEATURE_REQUESTS.md#fr-020--unified-filterable-jobs-and-history)).

**P2 — Rich compression and media fidelity**:
- Per-image target size and combined batch size budgets, bounded iterations and result comparisons. Maximum size is optional and initially blank; when set, choose the highest feasible quality under the ceiling rather than an unnecessarily tiny output (FR-009).
- Keep resolution while reducing bitrate, stream selection, metadata handling.
- HDR-aware CPU transcoding before GPU-specific features; output verification and explicit unsupported states.
- **P2 admin reporting follow-up:** retention-independent, permission-gated lifetime per-user and server processing metrics, with transparent counting and legacy/guest privacy rules ([FR-021](FEATURE_REQUESTS.md#fr-021--admin-only-lifetime-processing-statistics)).

**P3 — File Inspector**:
- Reusable safe metadata analysis service, readable UI, JSON/export and privacy controls; use same analysis in processing flows.

**P4 — Document Creator**:
- Non-destructive page assembly, previews/reorder/rotation, PDF export without needless rasterization, saved projects/mobile UX; then additional verified formats.

**P5 — Optional hardware acceleration**:
- Safe GPU device access, probing, CPU/GPU provider selection, codec/HDR-aware fallback, performance/quality tests.

**P6 — Visual automations and extension lifecycle**:
- Blueprint wizard and typed block editor; extension installer/version compatibility/rollback; later Immich.

**P7 — Brand, public pages and controlled demo**:
- Branding can be researched in parallel; deploy new identity only through approved compatibility plan. Publish a truthful static public site and optional resource-limited public demo after public security gates.

Milestones are dependencies and direction, **not blanket permission for an agent to start the next one**. Reprioritize only with owner agreement. The P0 release blockers take precedence over flashy feature development.

## 7. Acceptance standards for every feature

A feature is done only when:
1. A coherent minimal UX works on phones and desktops with accessibility and error states.
2. Backend contracts are explicit, typed, validated, testable and modular.
3. It uses existing auth/quotas/storage/jobs appropriately with documented privacy and failure behavior.
4. Actual output and metadata meet advertised guarantees with real tests.
5. Existing data/versions/automations/deployment remain compatible.
6. Relevant CI, tests and deployment checks pass or blockers are disclosed.
7. Feature status, technical docs and user-facing supported-capability descriptions are synchronized.

## 8. Out-of-scope / deferred decisions

- No general-purpose arbitrary-code plugin marketplace in Core/Worker.
- No promise of lossless format-to-format transformations, universal Dolby Vision preservation or arbitrary editable document fidelity.
- No destructive rename, package migration, version replacement or privileged GPU access.
- No automatic remote document upload to third parties or unbounded public hosting.
- No giant all-in-one UI requiring every feature to be configured before simple tasks work.

Use [PROJECT_STATUS.md](PROJECT_STATUS.md) for the verified baseline and [FEATURE_REQUESTS.md](FEATURE_REQUESTS.md) for actionable requests.
