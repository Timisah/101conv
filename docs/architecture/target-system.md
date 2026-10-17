# 101conv architecture

101conv is a Go modular monolith with a React client. The production Compose stack runs a web/Nginx frontend, Go Core, restricted Go Worker, and PostgreSQL. Nginx is the only service with a published host port, defaulting to `5656`.

## Component boundaries

- **Web:** React uses the versioned REST API; it contains presentation and workflow state, not authorization decisions.
- **Core:** owns account identity, effective permissions, quotas, engine/profile/repository management, uploads, job creation, and result access.
- **Worker:** claims durable jobs and runs only Core-resolved engine/profile arguments in a constrained container.
- **PostgreSQL:** durable accounts, groups, roles, permissions, API keys, jobs, attempts, usage reservations, and audit events.
- **Managed storage:** converter state/engines use the persistent Go data directory; uploads and results use separate named volumes.
- **Catalog:** versioned engine and typed profile manifests declare supported formats, capabilities, license, engine compatibility, and validated argument arrays.

## Access policy

Roles provide reusable permission grants. Users may belong to multiple groups; a group can combine a role with explicit permission rules and quota policies. Group grants combine, and a denial from any group wins over another group's grant. An individual permission override takes precedence over role/group policy. The server calculates effective access for every authenticated request; group, user, or key changes therefore apply without waiting for a session refresh.

Quota values are explicit `limited`, `unlimited`, or `inherit` objects. An individual limit overrides group values; across groups, the tightest limited value wins, otherwise an unlimited policy applies, then the server account default. Worker and instance upload/output caps remain separate hard safety boundaries. PostgreSQL locks usage reservations so concurrent submissions cannot overrun account limits.

## Job execution

The browser uploads a file and submits a versioned job referencing an installed profile and output format. Core validates permissions, profile type, formats, quotas, and engine compatibility, then persists an immutable execution specification. The Worker claims jobs transactionally with leases, creates private attempt workspaces, invokes the managed executable with an argv array (never a shell), and enforces deadline, output, and result validation. Job ownership and API key scopes are checked on reads, cancellation, and downloads.

## Repository and package trust

Repositories distribute versioned engine and profile manifests. TUF verifies signed metadata, expiry, rollback protection, target digests, and key rotation when a root is provisioned. Previews bind installation plans to exact metadata and artifact digests. Metadata browsing does not execute packages; signatures do not sandbox converter binaries. The Worker remains a separate execution boundary.

## Persistence and deployment

PostgreSQL schema changes are additive. Account migration converts the previous `can_convert`, `can_read_jobs`, max-file, and monthly quota fields into role/permission and explicit quota fields without dropping the source columns. The current Go state directory and named database/upload/result volumes must be preserved across updates. Compose does not delete host directories or named volumes.

The current worker model expects one Core writer for JSON repository/engine management state. Do not run multiple Core writers against the same state directory. The stopped-writer full-backup script covers PostgreSQL, the Go data directory, uploads, and results; automatic scheduling and production restore orchestration remain follow-ups. See [backup instructions](../deployment/backup.md).
