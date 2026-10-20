# Architectural and Product Decisions

This is a lightweight decision log, not a record of every implementation detail. **Accepted** decisions describe the existing system or explicit product direction. **Proposed** decisions need design/owner review before implementation. **Open** means no final choice has been made.

| ID | Status | Decision | Reason / safeguards |
| --- | --- | --- | --- |
| A-001 | Accepted | Go Core + PostgreSQL durable jobs + separate restricted Worker | Transactional attempts, retry, ownership, immutable execution and bounded converter processes. Do not replace for convenience. |
| A-002 | Accepted | Managed, versioned engines and immutable job specs | Existing queued work must never silently adopt new executables or settings. Retain compatible older releases. |
| A-003 | Accepted | Core owns engine/repository management, Worker only executes approved specs | Avoid arbitrary binary installation or user-supplied arguments in Worker. Do not grant Docker socket/unrestricted mounts. |
| A-004 | Accepted | Safe Mounted Drives | Mounts are configured in deployment and registered in Core; do not expose them wholesale to Worker. Bounded scans and atomic non-overwrite publication with receipts remain invariants. |
| A-005 | Accepted | Private-first self-hosting with optional limited public demo | Do not weaken isolation or increase quotas for marketing. Public demo requires separate deployment and threat model. |
| A-006 | Accepted | Basic tasks must be quick on mobile | Progressive disclosure, contextual defaults, touch/keyboard accessibility and responsive parity. UX regression tests required for feature milestones. |
| A-007 | Accepted | Modular, maintainable code is a release requirement | Small typed feature modules/services, explicit contracts, test seams. Incrementally extract from large React App; no unrelated rewrites. |
| A-008 | Accepted | Non-destructive file handling | Preserve originals and user projects by default; explicit cleanup/retention policies and confirmation for irreversible operations. |
| A-009 | Accepted | AI updates project status, owner owns scope and brand | Plans are durable goals, not authorization for agents to silently change or remove them. |
| A-010 | Accepted | Current name is provisional | Keep `101conv` implementation identifiers until original name/logo and compatibility migration are approved. |
| A-011 | Accepted | User-facing **Capability → Provider → Engine** model | Stops competing technical profiles in novice UX; explicit provider resolution. Existing technical profiles remain behind compatibility adapters until migrated safely. |
| A-012 | Accepted | Separate saved **Preset** model | Save user intent/settings, not binary manifests. Personal/shared presets, deterministic compatibility and version warnings. |
| A-013 | Proposed | Shared typed file analysis / inspection | Single validated metadata service feeds file Inspector, format choices, compression/HDR policy and automation conditions. Must enforce privacy, parser limits and ownership. |
| A-014 | Proposed | Adaptive size constraints with explicit infeasibility | Per-file and combined batch budgets, bounded retries, truthful results; never promise mathematically impossible image size/quality. |
| A-015 | Proposed | HDR fidelity as an explicit constraint | Do not silently downgrade HDR to SDR, bit depth or important streams; validate outputs. Dynamic HDR formats are conditional on tested support. |
| A-016 | Proposed | Optional and secure hardware-enabled Worker | Only trusted device mapping, driver/encoder probes, resource controls and fallback when constraints hold; never blanket privileges. |
| A-017 | Proposed | Non-destructive Document Creator page manifest | Arrange source pages, preserve native PDF/text/vector and image bytes where possible, no unnecessary resizing/resampling; multiple output formats must be individually verified. |
| A-018 | Proposed | Visual Automations built on the existing backend | Typed block graph/blueprints, mobile-friendly simple start, immutable revisions and safe publications; not a second workflow engine. |
| A-019 | Proposed | Versioned external connectors and compatible older releases | Preserve settings and older package versions; pause only incompatible blocks; no arbitrary executable plugin sandbox. |
| A-020 | Proposed | GitHub-first distribution plus truthful static website | SEO benefits from substantive documented tasks, not generated low-value conversion pages. Public demonstration remains optional and separately isolated. |
| A-021 | Open | Publisher root custody/rotation and signing rollout | Requires maintainer-controlled offline root generation, fingerprint verification, authorized signing keys and documented rotation. Never auto-generate secrets in CI or by an AI agent. |
| A-022 | Accepted | Readiness contract | `/healthz` is process liveness; `/readyz` requires PostgreSQL schema access and Core-managed storage but does not require a Worker, installed converter, or completed first-run setup; authenticated operations reports conversion capability separately. |
| A-023 | Open | Password minimum / compatibility | Five-character current minimum is weak; decide rollout via threat analysis with existing-account compatibility and no silent reset. |
| A-024 | Open | Brand, logo, repository/domain migration | Check trademark/domain availability, plan redirects/versioned identifiers/volume compatibility. No speculative name finalized. |
| A-025 | Accepted | Provider selection policy | Normalize the operation and format mapping, honor a compatible personal preference, then an administrator preference, then a compatible personal/shared default preset, and finally choose the stable profile/version/engine provider order. An unavailable saved choice is reported and never silently executed; every submitted job freezes the selected profile/version/engine execution specification. |
| A-026 | Open | Document Builder editable-output semantics | PDF/CBZ/page-image exports differ from DOCX/PPTX/ODT. Determine honest content models, page fidelity, and when native content can be preserved. |

## Decision-making process

For a consequential design change, record: context/problem, proposed alternatives, backward compatibility, security and privacy consequences, mobile UX consequences, migration/rollback plan, test evidence and owner decision. Change an entry's status only after the choice is accepted. Do not quietly relabel proposals as shipped decisions.

For detailed runtime behavior and implementation specifics, link to existing docs in `docs/architecture/`, `docs/deployment/`, `docs/security/` and `docs/profiles/` rather than duplicating entire technical manuals here.
