# P1 capability/provider compatibility boundary

The first P1 slice adds a canonical read model beside the persisted profile
model. `server/internal/capability` adapts installed `profile.Definition` and
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

This is a migration seam, not a replacement for profiles. Jobs, preferences,
automations, and installation APIs still use profile IDs and immutable job
execution specifications. Presets, file analysis, novice UI migration, and
the final provider precedence policy remain later P1 work.
