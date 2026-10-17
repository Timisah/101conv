# Product maturity audit and Milestone A map

The October 2026 main branch already has a Go Core, a separate restricted Worker role, PostgreSQL jobs and accounts, React web, engine/profile/repository managers, conversion and compression jobs, preferences, and roles/groups/quotas. Jobs record an execution specification, uploads and results have distinct persistent mounts, and the Worker validates output before registering a result. Compose exposes only the web service on 5656.

| Area | Before this iteration | This iteration | Still needed |
|---|---|---|---|
| First run | Manual Compose variables, token login, no claim flow | Local installer generates secrets; atomic first-account claim and resumable storage review | Portainer secret generation outside the UI; guided trusted converter test on every supported platform |
| Storage | Durable files, no lifecycle view | Container capacity and database-recorded usage; safe bounded cleanup of unsubmitted uploads | Engine/temporary/age/category exact inventory; policy for results and submitted originals |
| Jobs/diagnostics | Durable leases, but synthetic running percent | Worker heartbeat and truthful indeterminate progress; low-space rejection | Human-readable per-engine failure taxonomy; batch model |
| Operations | Basic health endpoints | Admin readiness view with Worker heartbeat, lease count, storage, engines and repository status | Alert delivery and measured Worker temp usage |
| Backup | Named volumes and bind, no verified full backup | Stopped-writer backup, checksums, archive verification and isolated restore test | Automated schedules, incremental/config-only backup, migration matrix |

Data dependencies: account/setup/retention/cleanup/heartbeat state is in PostgreSQL; installed engine binaries and repository trust state are in Go data; uploads/results are named volumes. The backup tool freezes Core and Worker writers while dumping PostgreSQL and copying all three file stores. Existing `DATA_PATH`/`GO_DATA_PATH`, Compose service names, and named volumes remain unchanged. New SQL tables are additive and migrations are idempotent.

Security boundaries: the bootstrap passphrase must be random and at least 24 characters for first claim; account creation is serialized by a PostgreSQL row lock. The existing administrator passphrase remains usable after claim for compatibility and must be protected as a full administrator credential. Repository trust does not make executables safe. The Worker still has no network or Docker socket. The documented snap Docker `no-new-privileges` entrypoint failure remains unresolved; the setting is not silently enabled or replaced with a weaker Worker model.

On the cloud test Docker host, `docker run --network none --security-opt no-new-privileges:true --entrypoint /bin/true postgres:17-alpine` succeeded. This does not establish that the previously reported Ubuntu snap Docker host accepts that option. A production Compose change would require a host-specific entrypoint and conversion test first.

Retention starts in manual mode. Automated retention, if enabled, touches only uploads never referenced by any job and older than the configured age. Originals tied to completed, failed, cancelled, or queued jobs and every registered result are preserved. Capacity measurements come from Core mounts; Worker `/work` is a 1 GiB tmpfs and its live free space cannot be measured from Core. PostgreSQL size sums are estimates, not a filesystem crawl. Backup and retention have no access to arbitrary host paths through the browser.

Next priority within Milestone A: complete exact and age-bucket inventory, result/original retention with download/retry synchronization, stronger diagnostic categories, and automated backup scheduling. Milestone B is durable batches, marketplace details, and profile smoke tests. Watch folders and scheduling remain later work.
