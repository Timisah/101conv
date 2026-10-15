# 101conv target system

**Status:** target direction with a partial first vertical slice implemented. This document distinguishes the current implementation from the remaining target.

## Product and architecture goals

101conv should let a person select files and an output capability without learning converter command lines. The same conversion capability should be available to the web client, API clients, and later automation. File contents and conversion execution stay on the operator's infrastructure by default.

Start with a **modular monolith**, not a fleet of services. Keep explicit boundaries between the HTTP/API layer, conversion and job domain, persistence, storage, catalog, and worker execution. Keep the UI as a client of the public API. Preserve Java 21 and Spring Boot during the first stages because they are already the starter's working stack; revisit that choice only if a measured requirement justifies a rewrite.

The intended deployment is one Compose project with Core, Worker, and PostgreSQL. Do not add Redis or a separate message broker for the first release: PostgreSQL is the durable job queue and source of truth. Core and Worker can begin as two runtime roles in the same codebase. Use named volumes for database state and managed files. Keep the Worker unprivileged, without Docker socket access, and with explicit CPU, memory, process, and disk limits.

## Current baseline

The supplied archive contains a Spring Boot application with a static web UI, authentication, upload/history APIs, H2 persistence, and a JSON-configured converter registry. It is a useful feature reference, not the required final architecture.

The current upload path performs conversion before returning the HTTP response. Conversion configuration can supply child-process commands; the current runner waits without a deadline and reads all process output into memory. Persistence models conversion sessions and uploads, but has no durable conversion-job lifecycle. The API routes are not versioned. The current password-session cookie is marked `HttpOnly` but sets `Secure` to false. These are migration targets and need a security review before production exposure.

The supplied test originally failed during Spring test bootstrap because `ApplicationTests` was in `com.schroeter._conv` while the application configuration is in `com.schroeter.conv`; the test package is now corrected. The app was started with a local H2 database and its home page returned HTTP 200. That smoke check does not establish production readiness.

## Domain boundaries

- **Identity and access:** users, administrator bootstrap, browser sessions, scoped API credentials, and authorization policy.
- **Capabilities and catalog:** format/capability discovery, converter manifests, compatibility, dependency and license metadata, trust state, and installation policy. A fresh install contains no third-party engines or profiles.
- **Jobs:** validated requests, idempotency, state transitions, progress, attempts, cancellation, priority, concurrency, and history.
- **Storage:** immutable source artifacts, per-attempt work directories, completed outputs, retention, and backup/restore.
- **Worker execution:** claim work, stage inputs, invoke an installed converter through a typed adapter, collect bounded results, and report completion.
- **Web client:** onboarding, drag-and-drop, capability selection, job progress, and history using only the public API.

These are code modules and interfaces first. They do not require separate deployable services beyond the Core and Worker roles.

## Job contract

A job records an opaque ID, owner or service principal, creation time, idempotency key, requested capability, immutable source artifact references, validated options, selected converter identity and version, priority, attempt count, progress, timestamps, and a bounded public error code/message. Never store raw credentials or converter output without a strict size limit in job records or logs.

Use explicit states:

`accepted -> queued -> running -> succeeded | failed | canceled`

`queued` and `running` jobs may be canceled. A worker claims a queued job with a time-limited lease and increments its attempt. On a worker crash, an expired lease makes the attempt recoverable. Retrying creates a new attempt directory and never overwrites a prior result. Commit output metadata only after the worker has verified the output and atomically moved it into managed storage. Cancellation is cooperative first, then terminates the converter process at its deadline. A retry policy is bounded and defined by failure class; invalid input and unsupported capabilities are not retried.

Use a PostgreSQL transaction to claim jobs (for example, a locked row with `SKIP LOCKED`) and to persist state transitions. PostgreSQL is both the job store and queue; do not rely on an in-memory queue for accepted work. Workers use lease renewal and compare-and-set state updates so duplicate delivery cannot publish two outputs. The initial deployment uses one Worker; the same contract supports more workers later.

## Versioned API

Publish an OpenAPI contract under `/api/v1`. UI and external clients use the same authorization and validation rules.

- `GET /api/v1/capabilities` lists installed, available conversion capabilities and relevant option schemas.
- `POST /api/v1/jobs` accepts source artifact IDs, a capability, options, and an optional idempotency key; it returns `202 Accepted` with a job resource.
- `GET /api/v1/jobs/{id}` returns state, progress, timestamps, and a safe error description.
- `POST /api/v1/jobs/{id}/cancel` requests cancellation.
- `GET /api/v1/jobs/{id}/events` streams state/progress updates using Server-Sent Events; polling remains available.
- `GET /api/v1/jobs/{id}/result` is available only for a successful job and authorized owner.

Upload and job submission may be separate operations so large files can be staged, validated, and retried without resending bytes. Keep existing `/api/*` routes behind compatibility adapters while the UI moves to v1; deprecate them only after parity and a documented migration window.

Return stable error codes, field validation details, and request IDs. Never expose local paths, command lines, stack traces, or raw converter logs through the API. Add scoped API credentials after browser-session and bootstrap behavior has a clear security model.

## Converter trust and execution

Treat converter manifests and executables as untrusted until verified and explicitly installed. Manifests describe identity/version, supported capabilities, compatibility, dependencies, checksums/signatures, licenses, installation guidance, and executable entry points. Resolve dependencies before install and verify artifact hashes/signatures. Installing a converter must not silently run arbitrary scripts; installation is a deliberate administrator action with a visible trust and license summary. Keep official, community, local-import, and manually configured sources distinguishable.

Only the Worker launches converters. Pass an argument vector, never a shell command. Require an installed converter identity/version selected by policy; do not accept executable paths or command fragments from job requests. Give each attempt a private input/output directory, read-only source, explicit environment, no network by default, a deadline, bounded stdout/stderr, and output size limits. Verify the output is a regular file inside the attempt directory before publishing it. Start with process timeouts and filesystem checks; deploy the Worker with OS/container resource limits and least privilege. Never mount the Docker socket or run privileged by default.

## Data and privacy

Store files in managed filesystem storage and metadata in PostgreSQL. Use opaque IDs in the API; keep original filenames as display metadata only. Normalize and validate every path, reject symlinks where they could escape managed roots, and scope each attempt to its own directory. Retention cleanup must not delete source files until success and an explicit policy permit it. Make deletion, backup, and restore observable and testable.

Do not send file content, filenames, conversion options, or usage telemetry to a hosted service. Catalog access is an explicit administrator action. Once engines and manifests are installed, ordinary conversions can work offline.

## Migration plan

1. **Foundation and contracts:** fix the test bootstrap mismatch; add an architecture decision record for PostgreSQL and storage layout; define job/capability/error contracts and OpenAPI; identify legacy API behavior to preserve. Audit auth bootstrap, cookie flags, CSRF, authorization, and default database configuration before making the UI/API production-facing.
2. **Persistent job vertical slice:** introduce schema migrations and durable Job/Attempt records. Submit one supported conversion as a job, return immediately, read state, and download the completed artifact. Keep a single local Worker role and a DB-backed queue; test restart recovery and idempotent submissions.
3. **Safe execution boundary:** replace direct converter calls in request handlers with a typed worker adapter and process runner. Add deadline, cancellation, output/log limits, per-attempt workspaces, strict output validation, and resource controls. Keep converter installation disabled unless an administrator installs a verified manifest.
4. **Compatibility and UI:** route the existing upload UI through v1 jobs, show progress and understandable errors, and retain legacy routes as adapters during migration. Preserve history and ownership semantics.
5. **Deployment and operations:** publish Compose for Core, Worker, and PostgreSQL with health checks, unprivileged containers, named volumes, backup/restore, and an empty converter catalog by default. Verify clean install, upgrade, offline conversion, and data recovery.
6. **Catalog and automation:** add signed/versioned repository indexes, dependency and license display, reviewed installation, then scheduling and folder monitoring. Add these only after the job lifecycle and recovery behavior are stable.

### First implementation slice

The initial code slice adds durable JPA job records, `/api/v1/jobs` submit/status/cancel/result endpoints, an owner-scoped session check, idempotent submission, one scheduled worker, bounded child-process execution, managed input/output paths, and an OpenAPI contract. Legacy routes remain available. Tests cover end-to-end submission through result download and owner isolation, plus converter process timeout and output bounds.

This is a local-development milestone, not the full durable deployment target: the checked-in default still uses H2 with schema auto-update, and the worker runs in the web process. Tests cover cancellation of a running process, but not process restart recovery. The next reliability step is a versioned schema on PostgreSQL with restart/lease recovery tests and a separately constrained Worker deployment. A real OS/container sandbox with CPU, memory, PID, disk, filesystem, and network controls also remains deployment work.

### Main risks and decisions

- PostgreSQL adds one service to the default Compose deployment, but provides durable concurrent job leasing without another broker. Reassess only if the supported single-node workflow proves embedded persistence materially simpler without losing recovery guarantees.
- Moving from H2 and filesystem paths into versioned PostgreSQL/storage schemas needs an explicit importer and backup plan. Do not discard existing sessions, history, or files during migration.
- Process isolation reduces blast radius but is not a complete sandbox by itself. Network, filesystem, CPU, memory, PID, and disk limits must be enforced by the deployment boundary as well as validated in application code.
- Existing clients may depend on synchronous responses and legacy identifiers. Keep adapters until actual UI and API usage is migrated.
- Converter catalogs introduce supply-chain and licensing risk. Do not enable community installation until signature/hash verification, explicit admin review, compatibility checks, and removal/rollback are implemented.

## Out of scope for the first release

Microservices, a separate broker, remote execution, automatic community-script execution, cloud file processing, telemetry, and folder automation are not prerequisites for the first durable conversion workflow.
