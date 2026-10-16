The current deployment and frontend behavior are documented in [phase5-web.md](phase5-web.md). This document describes the conversion runtime contract still used by the Go Core and Worker.

# Phase 4: conversion jobs and restricted Worker

The Go conversion path stores durable jobs and attempts in PostgreSQL, with one Core and one restricted Worker runtime role in the same Go codebase. The root `compose.yaml` defines the supported deployment with React/Nginx, Core, Worker, and PostgreSQL.

## Runtime boundary

The Core is the only writer for engine/profile and repository state. It accepts an upload, validates its filename, extension, size, and digest, resolves an enabled profile rule against an installed engine version, and stores a fixed execution specification in PostgreSQL. The specification contains a managed engine ID/version, a package-relative executable, argv tokens, and source/target formats. The client never selects a binary path or shell command.

The separate Worker claims queued jobs transactionally with `FOR UPDATE SKIP LOCKED`, creates an attempt row, renews a lease, and records terminal state and bounded diagnostics. A lost process is recovered after lease expiry and retried up to three attempts. Idempotency keys are unique; the same key and identical request return the original job. Inputs are consumed once. Cancellation is cooperative through polling plus OS process-group termination. Converter invocations use `exec.CommandContext` and argv directly, with a deadline, minimal environment, private workspace, input digest recheck, and output type/size check.

PostgreSQL is the durable job/attempt authority. The existing management service keeps its single-process JSON writer lock for engine/profile state. The Worker does not mount that state or TUF repository trust material. It only mounts managed engine files read-only, uploads read-only, and results writable.

## Deployment and API

The current production Compose deployment runs Core, Worker, and PostgreSQL. Core publishes configurable host port `PORT` (default `5656`) only through the web service. Worker has only an internal network to PostgreSQL, a read-only root filesystem, dropped capabilities, PID/memory/CPU limits, and a 1 GiB no-exec tmpfs workspace. It receives no Docker socket. `no-new-privileges` is omitted because the Ubuntu host's snap-packaged Docker denies execution of container entrypoints when this option is enabled (confirmed by the PostgreSQL startup log); app containers remain unprivileged and retain the other isolation controls. The Compose file retains named storage for PostgreSQL/uploads/results and requires `DATA_PATH` to exist on the Docker host for engine management state and packages.

Set `DATA_PATH`, `POSTGRES_PASSWORD`, and `ENGINE_ADMIN_TOKEN` in Portainer's stack environment table. `PORT` defaults to `5656`. The Go API currently uses the admin bearer token for all endpoints. Upload with `POST /api/v1/uploads` multipart field `file`; submit JSON to `POST /api/v1/jobs` with `uploadId`, `profileId`, `targetFormat`, and `idempotencyKey`; poll `GET /api/v1/jobs/{id}`; cancel via `POST /api/v1/jobs/{id}/cancel`; fetch `GET /api/v1/jobs/{id}/result`. The OpenAPI document is `docs/api/v1/openapi.yaml`.

To use a manual engine, import its executable beneath `DATA_PATH/imports`, install it through the management API, then install a compatible profile. The Worker only resolves binaries beneath the managed `DATA_PATH/engines` hierarchy. The one-click repository package pathway remains governed by Phase 3 TUF trust and previews; Phase 4 does not weaken that trust boundary.

## Verification

The test suite includes a real, self-contained PPM P3 to PGM P2 converter in `server/testdata/ppm-pgm`. `scripts/phase4-compose-smoke.sh` builds this converter and the Go service, starts the Core, Worker, and PostgreSQL as containers, submits an actual sample image, and checks the returned grayscale pixels. The same run verifies unsupported and malformed inputs, bearer authorization, idempotent duplicate submission, cancellation, execution timeout, oversized output rejection, and lease recovery after forcibly killing and restarting the Worker. It inspects the Worker container's read-only root, dropped capabilities, resource limits, volume modes, and sole internal network. It uses a unique Compose project and removes only its own test volumes on exit.

`go test ./...`, `go test -race ./...`, `go vet ./...`, and the Compose smoke test are the Phase 4 checks. Results from this implementation run are recorded in the delivery summary; Docker controls observed through Compose are subject to the host Docker daemon's enforcement and are not a substitute for a production host security audit.

## Runtime limits

- The default scratch image contains no dynamic libraries. Many FFmpeg/ImageMagick distributions need a curated runtime image and allowlisted shared libraries; do not assume arbitrary installed binaries will execute in this image.
- CPU and memory cgroups plus PID/disk bounds are configured at the Worker container, but no syscall profile, seccomp profile beyond Docker defaults, or per-job cgroup exists. This is process isolation, not a complete hostile-code sandbox.
- The Worker has no public egress; its database is reachable only on the internal worker network. Compose isolation was tested locally, but Portainer/Swarm translation and host kernel enforcement need verification on the deployment host.
- Jobs are scoped to their account owner, uploads are one-use, and there is no retention/garbage-collection policy yet. Result and upload disk usage requires operational monitoring and cleanup policy.
- Progress is lease/heartbeat progress rather than converter-reported percent. Retry policy covers worker loss/interruption and is intentionally bounded.
- The API returns bounded stdout/stderr in attempt details, but it has no structured converter-specific diagnostic mapping yet.

Next steps include backup/restore, retention and cleanup policy, engine-specific runtime packaging, richer progress/diagnostics, and a production container hardening review. Repository trust and signing remain separate from converter sandboxing.
