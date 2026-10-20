# P1 capability/provider compatibility boundary

P1 adds a canonical read model beside the persisted profile model.
`server/internal/capability` adapts installed `profile.Definition` and
`engine.Definition` records into three safe concepts:

- a **Capability** is a normalized operation and source/target mapping;
- a **Provider** is a versioned profile implementation bound to a selected
  engine installation; and
- a provider capability carries only typed, bounded option descriptors.

The adapter never returns executable paths, argument templates, or other
Worker execution details. It chooses a pinned engine when the legacy profile
has one, otherwise the highest compatible installed engine version, and marks
the provider unavailable when the installation or named executable is absent.
Input record order does not affect the catalog or provider choice.

`GET /api/v1/capabilities` exposes this model to authenticated conversion or
compression users while preserving the legacy `/api/v1/profiles` contract.
Guests see enabled providers only. `GET /api/v1/capabilities/resolve` accepts
an operation and normalized source/target formats, honors an explicit provider
preference when available, and otherwise selects by stable profile/version/
engine ordering. The response includes candidate reasons so a UI can explain
disabled, missing, or unavailable providers.

Jobs adapt automatic selection through this resolver and still freeze the
legacy profile, profile version, engine, and executable details into the
immutable execution specification at submission time. A supplied legacy
profile ID remains an advanced override, while the default Convert and
Compress paths submit no technical provider choice.

`server/internal/presets` stores user intent separately from executable
manifests. Presets contain a format mapping, optional provider/profile
preference, profile version, and bounded typed option values. Every read
rechecks compatibility against the live catalog, so an installed profile or
engine update produces an explicit warning instead of silently changing a
saved choice. Personal presets are scoped to an account; administrators can
create shared presets and guests can read shared compatible presets.

`/api/v1/presets` provides list, create, edit, duplicate, delete, and default
operations. The web workspaces use a reusable Saved preset control and keep
the implementation selector inside an Advanced disclosure. Batch items carry
the selected typed options, and automation revisions can pin a preset's
validated options into their immutable action definition before execution.
Legacy profile, preference, automation, and installation APIs remain available
as migration boundaries.
