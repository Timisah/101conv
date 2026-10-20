# Feature Requests & Implementation Backlog

**Product plan:** [MASTER_PLAN.md](MASTER_PLAN.md) · **Verified status:** [PROJECT_STATUS.md](PROJECT_STATUS.md) · **Decisions:** [DECISIONS.md](DECISIONS.md)

This is a curated **request register**, not a feature-completion list. `Requested` means a user goal has been captured, **not** that the code implements it. Priorities are indicative and can be changed by the owner. GitHub issues may track individual implementation tasks; do not open dozens of duplicates or copy entire documents into every issue.

## Universal acceptance checks

Every feature: (1) simple mobile first path and accessible advanced controls, (2) typed, modular, tested API/UI, (3) no arbitrary CLI/execution injection or destructive state migration, (4) real functional/output tests, (5) truthful unsupported states, (6) documentation and status update, (7) safe upgrade from the existing Portainer Compose deployment. “No regression” applies to jobs, guest sessions, accounts, storage, mounted drives, automation revisions and installed historical engine/profile releases.

## P0 — correctness / pre-release engineering

### FR-001 — Correct advertised upload ingress limit

**Problem:** Nginx rejects requests over 105 MiB despite advertised app 1 GiB ceiling and configurable account quotas.  
**Acceptance:** reconcile Nginx and reverse proxy request size with app setting; test a real upload above 105 MiB and boundary cases, guest smaller quota, multipart overhead, cancellation, storage/reservation and response errors. Never interpret 1 GiB as a promise of available RAM/Worker output space.  
**Status:** Confirmed at baseline `6dc091c`.

### FR-002 — True dependency-aware readiness

**Problem:** `/readyz` always returns ready; health details exist only in admin operations.  
**Acceptance:** liveness vs Core API readiness vs ready-to-convert semantics documented; database/storage failures reflected appropriately; new unconfigured installation can complete setup; Worker/profile status exposed accurately without flapping or leaking admin data. Compose/healthchecks tested.  
**Status:** Confirmed at baseline.

### FR-003 — One accurate official capability catalog and docs

**Problem:** legacy embedded catalog and profile docs differ from shipped Office/PDF target catalog.  
**Acceptance:** specify authoritative source or generate compatibility index from it; verify all official entries against real manifests, appropriate release/version compatibility; update README/docs/API. CI fails on drift. Preserve old compatible manifest paths.  
**Status:** Confirmed at baseline.

### FR-004 — Production publisher trust and reproducible builds

**Problem:** empty official TUF trust root, manual unsigned approval. Image pinning/reproducibility needs audit.  
**Acceptance:** maintainer-controlled offline root ceremony, independent fingerprint verification, signed artifact metadata, key rotation/recovery/runbook, immutable package releases and verified source/licensing. No private key ever committed or published by automation. Test older compatible client resolution.  
**Status:** Incomplete; owner-controlled security ceremony needed.

### FR-005 — Worker/public deployment threat-model hardening

**Problem:** existing sandbox restrictions are meaningful but Worker still shares database and result storage and is not full multi-tenant isolation; weak minimum passwords and deployment constraints require review.  
**Acceptance:** least-privilege DB and FS analysis, targeted hardening, documented public/private suitability, compatibility-aware password decision, forwarded-header/rate-limit tests, base image pinning where appropriate, nonprivileged runtime. No generic “sandbox is secure against all malicious parsers” claims.  
**Status:** Audit request.

## P1 — foundation and usable presets

### FR-006 — Capability/provider architecture and deterministic selection

**Goal:** one user-facing format operation regardless of how many engines implement it; no “best profile” guessing for everyday tasks.  
**Acceptance:** typed capability/option descriptors, provider compatibility/resolver/explanations, advanced implementation override, deterministic failure/fallback, immutable job resolution, migration adapters for existing profile API/versions and automation pins; unit/integration tests including provider conflicts. UI default hides technical engine/profile pickers.  
**Dependencies:** FR-003; design approval and migration plan.

### FR-007 — User presets and defaults

**Goal:** user can save “JPEG → JXL, my quality/metadata/dimensions rules” while compressing, then apply it next time with one tap.  
**Acceptance:** personal named presets, duplicate/edit/delete, favorites/default per operation or compatible input, optional admin shared presets, validation against capability/provider version, explicit incompatibility warnings, usage across Convert/Compress/batches/Automate. Guest flows remain simple without forced accounts.  
**Dependencies:** FR-006.

### FR-008 — Modular frontend/backend and mobile UX gates

**Goal:** constant feature development without a monolithic React app or huge backend service.  
**Acceptance:** incremental extraction of workspace features, shared typed API client, scoped state and reusable controls, clear service boundaries and fast tests; behavioral parity with old app; browser automation at narrow phone viewport, keyboard/touch and accessibility. No unrelated rewrite, no growth of `App.tsx` as feature dumping ground.  
**Dependencies:** continuous, work alongside FR-006/FR-007.

## P2 — compression, metadata and video fidelity

### FR-009 — Rich image compression and combined batch size goal

**Goal:** compress image(s) to a maximum per-file size, combined total, or both, while keeping original pixel dimensions by default.  
**Acceptance:** canonical quality/size controls, format/encoder-specific options, bounded iterative quality search, fair batch budget allocation, hard vs best-effort policies, impossible-target errors, real output validation and before/after data. Do not assume every codec provides exact target-size encoding.  
**Dependencies:** FR-006, FR-010.

### FR-010 — Shared file analysis and File Inspector workspace

**Goal:** upload any supported file and see useful metadata without technical knowledge; data also drives other workspaces.  
**Acceptance:** scoped, bounded inspection; MIME/content/container checks; video/audio streams/bitrate/HDR, image EXIF/ICC, PDF/Office properties where supported, readable summary and expandable raw metadata/JSON; ownership/GPS privacy, no content leaks, no metadata modification by inspection. Tests for corrupt inputs.  
**Dependencies:** FR-006 recommended; safe parsers first.

### FR-011 — Video compression with independent bitrate/resolution and HDR constraints

**Goal:** “keep 4K but reduce bitrate”, or keep streams/HDR as needed; supported metadata/fidelity must survive unless explicitly changed.  
**Acceptance:** codec/bitrate/CRF/quality modes, frame rate, stream/audio/subtitle handling; detect color primaries/transfer/matrix, bit depth, static HDR fields and conditional dynamic HDR. Verify outputs with probe and report unsupported HDR10+/Dolby Vision cases. Separate remux, HDR-preserving re-encode and explicit SDR tone map.  
**Dependencies:** FR-010, FR-006. No silent SDR fallback.

## P3 — Document Creator

### FR-012 — Visual Document Creator / Build workspace

**Goal:** upload JPG/PNG/PDF/multipage PDFs and supported Office files; reorder individual pages, rotate and export one document.  
**Acceptance:** real thumbnails, accessible reorder + drag, rotation, remove/duplicate, multipage flatten/group, undo/redo, mobile one-column/touch editor, non-destructive manifest, expiring guest and saved logged-in projects, PDF export preserving vector text/images and original bytes where possible; no needless resizing/resampling. Export formats beyond PDF (DOCX/ODT/PPTX/ODP/CBZ/multipage TIFF/ZIP) shipped only with honest fidelity and format-specific tests. Default one input page per output page.  
**Dependencies:** PDF/Office engines, FR-010 and design of safe project lifecycle.

## P4 — GPU, automations and extensions

### FR-013 — Optional hardware acceleration

**Goal:** speed up supported video operations without eroding quality/security.  
**Acceptance:** detect Intel Quick Sync/VAAPI, AMD VAAPI, NVIDIA encode/decode capability where configured; allowlisted GPU render nodes/driver requirements, never privileged Worker by default; transparent CPU fallback consistent with HDR/fidelity constraints; performance/fidelity benchmarks and failure reporting.  
**Dependencies:** FR-011, provider model.

### FR-014 — Visual Automations and blueprint library

**Goal:** easy “choose blueprint → configure” plus optional powerful LEGO-style block flow.  
**Acceptance:** typed triggers, conditional branches, Inspect/Convert/Compress/Build/Output blocks, explainable dry-run/history, schedules where safe, revision pinning and dedup; mobile functional version without desktop-only graph demands. Preserve existing automation engine and safe publication guarantees.  
**Dependencies:** FR-006/010, current C.1 runtime.

### FR-015 — Extensions marketplace and Immich

**Goal:** install/update compatible connector packages independently of app version; Immich workflows through supported API.  
**Acceptance:** manifests with app/provider compatibility, old compatible downloads, verified signatures/integrity, constrained permissions, secrets protection, configuration migration/rollback, only affected blocks paused when incompatible. Immich supported API media discovery/export/import only; no guaranteed in-place asset ID replacement or preservation of every tag/face relationship.  
**Dependencies:** FR-014 and explicit extension security design.

## P5 — identity, discoverability and optional public demo

### FR-016 — Original name and logo with safe migration

**Goal:** replace provisional `101conv` with distinct name and logo suitable for a complete file workspace.  
**Acceptance:** domain/trademark/GitHub availability research, distinct identity, favicon/mobile/light/dark assets, owner selection, redirect/legacy identifier transition and backwards compatible API/data/Compose path plan. No spontaneous repo rename.  
**Status:** Identity open, not yet chosen.

### FR-017 — GitHub-centered discoverability and SEO

**Goal:** GitHub as source/early landing page; accurate web discovery of self-hosted tools and supported conversions.  
**Acceptance:** README screenshots, quick start, format matrix, useful docs, static SEO pages through GitHub Pages or equivalent, proper titles/sitemaps/robots/canonicals/schema when justified, crawler accessibility and privacy. Only verified features; no spammy bulk landing pages or promises of ChatGPT/Google ranking.  
**Dependencies:** honest catalog FR-003, brand FR-016 for custom domain.

### FR-018 — Resource-limited public demo

**Goal:** visitors try actual conversions and discover self-hosting.  
**Acceptance:** separate isolated deployment without personal/NAS production data, conservative guest quotas, global compute/storage/output budgets, rate/abuse protection, short retention and privacy disclosures, limited verified engines, clear links to GitHub/installation, aggregate cost/conversion/referral metrics and kill switch. Evaluate benefits empirically; never unlimited free processing.  
**Dependencies:** P0 security/release gates, verified format capabilities.

## Workflow

A requested feature moves through: **requested → designed/approved → implemented → automated verification → production/deployment verification → complete**. An AI agent may update verified status and propose issues, but must not interpret this backlog as permission to implement all requests, rebrand, or weaken security. Keep feature descriptions and issue scopes small enough to implement without fragile massive PRs.
