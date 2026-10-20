# P0 release audit

**Review date:** 2026-10-20  
**Scope:** the managed Docker deployment and the repository configuration used to build it.  
**Suitability:** trusted single tenant or small team. The managed deployment now uses a dedicated non-superuser Worker login; existing installations must run the explicit migration below before switching to it. The unsigned official catalog and hostile multi-tenant threat model still block a general public deployment.

This document records the P0 checks that can be performed from the repository and attached environment. It is an audit record, not a claim that a public deployment is safe against hostile multi-tenant workloads.

## Completed checks

- Nginx accepts the advertised 1 GiB file ceiling plus multipart overhead, forwards real 1 GiB and chunked uploads, and rejects requests above 1025 MiB. Core quotas and storage reservations remain authoritative.
- Core `/healthz` is liveness-only. `/readyz` checks PostgreSQL schema access and Core-managed storage, remains ready for first-run setup, and is the Compose healthcheck. The managed deployment returned HTTP 200 from both endpoints and all four services became healthy.
- The target catalog is authoritative. The compatibility indexes are generated and checked for manifest identity/version drift; the deployed catalog exposed eight profiles, including LibreOffice, Poppler, and compression profiles.
- The Compose isolation contract is checked by `scripts/verify-compose-security.sh` and by live inspection. Core, Worker, and Web are non-root, read-only, capability-dropped, resource-bounded, and non-privileged. Worker has only the internal database network, no host port, no automation mount, read-only engine/uploads mounts, and a writable result mount.
- Production image references are pinned to immutable digests for PostgreSQL, Go build stages, Node build stage, Nginx, and the Debian document Worker base. Package versions in the document Worker remain exact and are checked during the image build.
- Forwarded-header and origin-boundary behavior has focused Go coverage. Core trusts forwarded scheme/address information only on the bundled proxy boundary; Core should not be exposed directly.
- Password compatibility remains explicit: five to 256 characters are accepted so existing accounts continue to work; Argon2id hashing and login/setup rate limits are enabled. A stronger minimum is a product-owner decision because it affects existing credentials.
- The managed PostgreSQL volume was migrated to `101conv_worker`. Live verification confirmed `rolsuper`, `rolcreatedb`, `rolcreaterole`, `rolreplication`, `rolbypassrls`, schema `CREATE`, and database `TEMPORARY` are all false for that login; Worker became healthy after restart. The role currently has DML on application tables as a compatibility grant, with table-specific tightening remaining future work.
- Backup manifests, archive checksums, PostgreSQL dumps, isolated restore references, and a real conversion restore path are covered by `scripts/verify-backup.sh`, `scripts/test-restore.sh`, and the browser/restore CI jobs. The local browser/restore preflight was not started because the attached workspace had 1.57 GiB free against the script's required 8 GiB threshold. The document runtime unit matrix passed, but its disposable HTTP/PostgreSQL stage stopped when the Docker daemon reported no space; remote CI remains required for those capacity-heavy gates.

## Findings requiring an owner decision or follow-up

### Existing installations and the Worker database role

Fresh installs created by `scripts/install.sh` set `POSTGRES_WORKER_USER` and a separate random `POSTGRES_WORKER_PASSWORD`; the official PostgreSQL init hook creates that login without superuser, database-creation, role-creation, replication, bypass-RLS, schema-create, or temporary-table privileges. Existing volumes require an explicit additive migration so an upgrade never changes database authority implicitly:

```sh
set -a; . ./.env; set +a
scripts/migrate-worker-db-role.sh
```

Set the same `POSTGRES_WORKER_USER` and `POSTGRES_WORKER_PASSWORD` in the Portainer stack before restarting Worker. The migration grants application-table DML for compatibility and can be narrowed after reviewing the deployed schema. Keep the previous Core credentials for Core migrations and retain a database backup before changing an existing installation. Rollback is to the previous Worker environment and role; the migration does not drop or rewrite data.

### Official publisher trust

`server/internal/repositorymanager/official-root.json` is intentionally empty. The ceremony is documented in [trust-root-ceremony.md](trust-root-ceremony.md), but production private keys must be generated on an owner-controlled trusted machine, kept offline or in the approved signing system, independently fingerprinted, and never written by CI or an AI agent. The unsigned official catalog remains browse-only unless an administrator explicitly approves it.

### Public anonymous demo

Do not expose the current stack as an unrestricted public service. A separate deployment needs a threat model, dedicated data and volumes, conservative guest quotas, global rate/compute/storage caps, short retention, abuse response, an outer proxy with matching upload limits, and the least-privilege database work above. The current ingress and guest controls provide useful bounds but do not turn the Worker into a hostile multi-tenant sandbox.

## Release-gate evidence

The local managed deployment passed the liveness/readiness/catalog, Compose isolation, Worker-role, upload-ingress, and pinned-image checks on 2026-10-20. The repository's full Go test suite, `go vet`, catalog preflight, generated-index checks, pinned FFmpeg matrix, and document runtime unit matrix passed. Remaining evidence before a general public release is the remote CI run URL/result, capacity-heavy document HTTP and browser/restore gates, the owner-controlled TUF publication, and an explicit public-demo threat-model decision.
