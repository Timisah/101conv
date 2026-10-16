# Phase 4: conversion jobs and restricted Worker

Phase 4 adds a first real conversion path to the Go service while keeping the existing Java application and its deployment intact. The Go stack is defined in `compose.go.yaml`; it does not replace the root Java `compose.yaml`.

## Runtime boundary

The Core is the only writer for engine/profile and repository state. It accepts an upload, validates its filename, extension, size, and digest, resolves an enabled profile rule against an installed engine version, and stores a fixed execution specification in PostgreSQL. The specification contains a managed engine ID/version, a package-relative executable, argv tokens, and source/target formats. The client never selects a binary path or shell command.

The separate Worker claims queued jobs transactionally with `FOR UPDATE SKIP LOCKED`, creates an attempt row, renews a lease, and records terminal state and bounded diagnostics. A lost process is recovered after lease expiry and retried up to three attempts. Idempotency keys are unique; the same key and identical request return the original job. Inputs are consumed once. Cancellation is cooperative through polling plus OS process-group termination. Converter invocations use `exec.CommandContext` and argv directly, with a deadline, minimal environment, private workspace, input digest recheck, and output type/size check.

PostgreSQL is the durable job/attempt authority. The existing management service keeps its single-process JSON writer lock for engine/profile state. The Worker does not mount that state or TUF repository trust material. It only mounts managed engine files read-only, uploads read-only, and results writable.

## Deployment and API

`compose.go.yaml` runs Core, Worker, and PostgreSQL. Core publishes configurable host port `PORT` (default `5656`) and can access the default outbound network for repository metadata. Worker has only an internal network to PostgreSQL, a read-only root filesystem, dropped capabilities, `no-new-privileges`, PID/memory/CPU limits, and a 1 GiB no-exec tmpfs workspace. It receives no Docker socket. The Compose file retains the existing deployment's named storage for PostgreSQL/uploads/results and requires `DATA_PATH` to exist on the Docker host for engine management state and packages.

Set `DATA_PATH`, `POSTGRES_PASSWORD`, and `ENGINE_ADMIN_TOKEN` in Portainer's stack environment table. `PORT` defaults to `5656`. The Go API currently uses the admin bearer token for all endpoints. Upload with `POST /api/v1/uploads` multipart field `file`; submit JSON to `POST /api/v1/jobs` with `uploadId`, `profileId`, `targetFormat`, and `idempotencyKey`; poll `GET /api/v1/jobs/{id}`; cancel via `POST /api/v1/jobs/{id}/cancel`; fetch `GET /api/v1/jobs/{id}/result`. The OpenAPI document is `docs/api/v1/openapi.yaml`.

To use a manual engine, import its executable beneath `DATA_PATH/imports`, install it through the management API, then install a compatible profile. The Worker only resolves binaries beneath the managed `DATA_PATH/engines` hierarchy. The one-click repository package pathway remains governed by Phase 3 TUF trust and previews; Phase 4 does not weaken that trust boundary.

## Verification

The test suite includes a real, self-contained PPM P3 to PGM P2 converter in `server/testdata/ppm-pgm`. `scripts/phase4-compose-smoke.sh` builds this converter and the Go service, starts the Core, Worker, and PostgreSQL as containers, submits an actual sample image, and checks the returned grayscale pixels. The same run verifies unsupported and malformed inputs, bearer authorization, idempotent duplicate submission, cancellation, execution timeout, oversized output rejection, and lease recovery after forcibly killing and restarting the Worker. It inspects the Worker container's read-only root, dropped capabilities, no-new-privileges setting, resource limits, volume modes, and sole internal network. It uses a unique Compose project and removes only its own test volumes on exit.

`go test ./...`, `go test -race ./...`, `go vet ./...`, and the Compose smoke test are the Phase 4 checks. Results from this implementation run are recorded in the delivery summary; Docker controls observed through Compose are subject to the host Docker daemon's enforcement and are not a substitute for a production host security audit.

## Limits and next steps

- Authentication is one administrator bearer token, not per-user authorization or tenant isolation. A production public API needs user/session auth and per-job ownership checks.
- The default scratch image contains no dynamic libraries. Many FFmpeg/ImageMagick distributions need a curated runtime image and allowlisted shared libraries; do not assume arbitrary installed binaries will execute in this image.
- CPU and memory cgroups plus PID/disk bounds are configured at the Worker container, but no syscall profile, seccomp profile beyond Docker defaults, or per-job cgroup exists. This is process isolation, not a complete hostile-code sandbox.
- The Worker has no public egress; its database is reachable only on the internal worker network. Compose isolation was tested locally, but Portainer/Swarm translation and host kernel enforcement need verification on the deployment host.
- Jobs are globally administrator-visible, uploads are one-use, and there is no retention/garbage-collection policy yet. Result and upload disk usage requires operational monitoring and cleanup policy.
- Progress is lease/heartbeat progress rather than converter-reported percent. Retry policy covers worker loss/interruption and is intentionally bounded.
- The API returns bounded stdout/stderr in attempt details, but it has no structured converter-specific diagnostic mapping yet.

Recommended Phase 5: establish user identity and authorization, retention quotas, engine-specific runtime packaging (including FFmpeg), richer progress/diagnostics, and a production container hardening review. Keep repository signing/publishing and the future isolated-worker threat model distinct from conversion scheduling or UI redesign.
