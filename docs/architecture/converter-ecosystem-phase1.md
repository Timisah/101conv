# Converter ecosystem model (Phase 1)

Phase 1 establishes separate Go domain models and a legacy adapter. It does not
download or install packages, persist engine/profile state, verify signatures,
or run converters in an isolated worker.

## Models and boundaries

- `server/internal/engine` defines an engine release, its artifact, named
  executables, capabilities, local installation state, compatible version
  ranges, and the future engine manager interface. The manager contract is
  idempotent for the same engine, version, platform, and artifact digest.
- `server/internal/profile` defines conversion rules independently. A profile
  declares one or more engine requirements, including an inclusive minimum,
  exclusive maximum, and required capabilities. A rule references an engine
  and named executable rather than embedding an executable path. Profile
  installation state is a separate type.
- `server/internal/repository` defines sources, index references for engines and
  profiles, trust records, a metadata verifier seam, a trust store seam, and a
  metadata-only client. It has no installation behavior. The built-in Go
  catalog currently reports its source as `unverified` because signatures are
  not implemented yet.
- `server/internal/worker` defines an invocation/result boundary. It allows a
  restricted process or container runner to be added later without changing
  repository or profile models; no isolated runner is implemented in this
  phase.
- `server/internal/legacyadapter` reads the existing Java
  `ConverterFileDefinition` JSON shape and maps its executable variables to a
  manual engine installation, while keeping conversion rules in a profile.
  Existing executable paths remain local manual paths and are not mistaken for
  repository package paths.

The Go catalog uses aliases to the new repository source/index reference types.
Its version-1 profile-only index remains readable, and the schema can also
carry an `engines` list. The existing catalog index and Java profile manifests
are unchanged in this phase.

## Compatibility and limits

The legacy adapter is tested against multi-executable converter configs,
literal manual executable paths, default/disabled state, argument order, and
missing executable references. It is a translation seam, not a live registry
or data migration: the Go service does not yet load old converter directories
or Java `data/profiles` metadata. No user data is rewritten or removed.

Version-range comparisons retain the current catalog behavior: numeric
major/minor/patch components determine compatibility, and suffixes do not
change the release line. This avoids changing existing catalog filtering while
engine compatibility is introduced.

Repository signatures, trust onboarding and key rotation, install-once package
storage, profile CRUD, persisted migration, rollback, and worker isolation are
later phases. Until those are implemented, the Go code is architecture and
compatibility groundwork rather than a production converter installer.
