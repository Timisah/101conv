# 101conv

[![CI](https://github.com/Timisah/101conv/actions/workflows/ci.yml/badge.svg)](https://github.com/Timisah/101conv/actions/workflows/ci.yml)

101conv is a self-hosted file conversion application. The supported application is a React web client, Go Core and restricted Worker, and PostgreSQL, deployed together from [`compose.yaml`](compose.yaml).

The official catalog currently includes pinned LGPL FFmpeg audio/video profiles, JPEG XL profiles, and an FFmpeg image profile for JPG/JPEG, PNG, WebP, TIFF, BMP, GIF, and AVIF mappings supported by the selected build. Image resize, aspect-ratio, quality, compression-level, and metadata controls are validated by Core and stored in each job's immutable execution specification. The catalog does not currently claim LibreOffice or PDF rasterization support; those require separately reviewed, pinned converter packages.

## New installation

On a Docker host with Compose and OpenSSL, run:

```sh
git clone https://github.com/Timisah/101conv.git && cd 101conv && ./scripts/install.sh
```

The installer creates private, random database and bootstrap credentials in `.env`, prepares `data/go-data`, and starts the existing Compose stack. Open `http://<docker-host>:5656/`, enter the bootstrap passphrase from `.env`, and create the first administrator account. Keep `.env` private and include it in your secure configuration backup. The setup page checks writable storage and shows the path to installing a first profile; it does not download converter binaries without your action. PostgreSQL, uploads, and results use Docker named volumes. The local `data/go-data` bind contains engine binaries, profile/repository management state, and trust settings. Worker scratch files use a nonpersistent 1 GiB tmpfs. The installer refuses to adopt an existing `data/go-data` without explicit configuration.

## Deploy with Portainer

Create or update one Git stack from `https://github.com/Timisah/101conv`, branch `main`, Compose path `compose.yaml`. The web interface is published on port `5656` by default. Only the web service publishes a host port; Core and PostgreSQL stay private.

Set these variables in the Portainer stack's Environment variables table:

- `DATA_PATH`: host directory whose `go-data` subdirectory contains Go management state and installed engines. For an existing installation, retain its current value. The directory `${DATA_PATH}/go-data` must already exist and be writable by UID/GID `65532:65532`.
- `POSTGRES_PASSWORD`: strong PostgreSQL password. When retaining an existing database volume, keep its original database credentials.
- `ENGINE_ADMIN_TOKEN`: strong administrator login/API token.
- `PORT`: optional published web port; defaults to `5656`.

Optionally set `GO_DATA_PATH` to the exact existing Go data directory instead of using `${DATA_PATH}/go-data`. Do not delete or replace the PostgreSQL, uploads, results, or Go data storage when updating the stack. Compose builds the frontend and backend from the checked-out commit without build cache, while HTML revalidation makes the newly built interface visible after deployment.

For a new Portainer deployment, create the Go data directory before deploying and supply a random `ENGINE_ADMIN_TOKEN` of at least 24 characters for first-run claim. For an existing installation, preserve the current path and named volumes. See the [Portainer deployment guide](docs/deployment/portainer.md) for storage, variables, and update details.

Administrators can configure separate retention for unsubmitted uploads, terminal-job originals, successful results, failed originals, and canceled originals. Defaults preserve files. Time-based or after-success cleanup requires a category preview; deleted files become explicit expired states while job history remains. The Storage overview uses database-recorded sizes; an administrator-triggered scan separately measures managed files and reports discrepancies. Filesystem capacity is measured inside Core, while the Worker's private temporary usage remains unavailable to Core. The Storage page labels those limits. A stopped-writer backup and isolated application-level restore rehearsal are documented in [`docs/deployment/backup.md`](docs/deployment/backup.md). The rehearsal boots restored Core and Worker privately and validates a real conversion when run through the browser smoke test.

## Anonymous conversion

Fresh installations enable guest conversion after the first administrator finishes secure setup. Visitors then land on Convert and can convert, compress, submit small batches, and download results without an account. Guest credentials are unique random HttpOnly cookies; each browser session owns separate jobs and files. Guest results and successful originals expire automatically (24 hours by default), unsubmitted and failed uploads expire separately, and old guest history is purged after the configured metadata interval. Closing a browser can lose access to its jobs; it does not stop scheduled cleanup. Guests do not receive personal preferences, account features, or any management access.

An existing deployment remains private after upgrade. An administrator must explicitly enable guests in **Admin → Anonymous access**. That screen controls conversion/compression, file and batch limits, per-session rates, active guest job and total storage capacity, and guest lifetimes. The total capacity counts retained guest originals and results plus a worst-case output reservation for each queued or running job; this bounds result growth before a converter starts. These controls are independent from the Worker's concurrency and output-size limits. The browser smoke suite exercises guest conversion, compression, batch download, session isolation, admin-denial, and phone layout in a separate browser context.

The browser uses the versioned API documented in [`docs/api/v1/openapi.yaml`](docs/api/v1/openapi.yaml). Frontend development instructions are in [`web/README.md`](web/README.md); the browser conversion smoke test is [`scripts/phase5-browser-smoke.sh`](scripts/phase5-browser-smoke.sh).

## Mounted drives and automations

Compose exposes one explicit host directory to **Core only** at `/mounts`; by default the host source is `${DATA_PATH}/automation-mounts`. To use an existing directory, set `AUTOMATION_HOST_PATH` in Portainer to its absolute Docker-host path (for example `/mnt/truenas/media`). The directory must be visible to Docker and readable by Core's unprivileged UID/GID `65532:65532`; destinations must also be writable. For NFS with root squashing, set ownership/ACLs on the NAS. The browser cannot create host mounts or change permissions. In **Admin → Mounted drives**, register subdirectories using their container paths, such as `/mounts/incoming` and `/mounts/converted`, and choose read-only or read/write access. Docker remains responsible for binding the host folder; the conversion Worker never mounts these folders.

In **Admin → Automations**, install either built-in blueprint, select the registered folders and an installed compatible profile, preview the current top-level files, then enable the rule. Conditions can combine extension, filename, and size blocks with AND, OR, or NOT. Folder checks run on a bounded polling interval, only accept stable regular files, process at most 250 directory entries/512 MiB per scan and submit at most 20 files per pass. Per-automation concurrency defaults to one (maximum eight). Existing files are skipped on the first scan unless explicitly selected. Results are staged and published without overwriting; originals are retained. Configuration, revisions and run history are persisted in PostgreSQL. Include PostgreSQL and the mounted host directories in backup planning; the normal backup script backs up app-managed databases/files but does not archive arbitrary host mounts. See the [automation model and current limits](docs/architecture/automations-c1.md).

## Continuous integration and verification

GitHub Actions runs five checks on pushes to `main`, pull requests, and manual `workflow_dispatch`: PostgreSQL-backed Go tests; Go race tests plus `go vet`; React lint/type checking and production build; the real Docker/Chromium browser suite; and an isolated backup/restore boot with a real fixture conversion. The restore check depends on the browser suite's disposable backup, so a skipped or failed browser stage cannot turn restore into a green check. CI uses temporary credentials and disposable volumes; it does not access deployment secrets or production data.

To run the non-container checks locally:

```sh
# Docker or a local PostgreSQL 17 service is needed for PostgreSQL integration tests.
env 'TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5432/101conv_test?sslmode=disable' bash -c 'cd server && go test -p 1 ./... -count=1'
env 'TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5432/101conv_test?sslmode=disable' bash -c 'cd server && go test -race -p 1 ./... -count=1 && go vet -p 1 ./...'
(cd web && npm ci --ignore-scripts && npm run lint && npm run build)
```

The full browser and restore run requires Docker Engine with Compose v2, at least 8 GiB free on the checkout, temporary-file, and Docker storage filesystems, at least 2 GiB available memory (and at least 2 GiB total memory visible to the daemon), and two visible CPUs. The browser and Worker run in real containers; the smoke profile converts a PPM fixture into PGM, while separate cases exercise batches, cancellation, retries, ZIP downloads, permissions, retention, restart persistence, and mobile navigation. Run it with:

```sh
./scripts/ci-resource-check.sh 8 2048
./scripts/phase5-browser-smoke.sh
```

The runner respects the selected Docker context and `DOCKER_HOST`, including rootless daemons. A remote Docker daemon must be given independently measured `SMOKE_DOCKER_FREE_GIB` and `SMOKE_DOCKER_AVAILABLE_MEM_MIB`; the script does not assume the client can measure remote capacity. The 8 GiB minimum is enforced and cannot be lowered. The script records filesystem and Docker image/volume usage before and after, retains redacted logs and browser screenshots on failure when `SMOKE_ARTIFACT_DIR` is set, and removes only its uniquely named Compose project and tagged test images. It never runs Docker prune.

The restore rehearsal takes a backup of the synthetic browser deployment, checks its manifest/checksums/archives/PostgreSQL dump, restores into new uniquely named volumes on an internal-only network, checks retained account/job/profile/engine references and expired-result tombstones, starts Core and Worker, and performs and downloads a PPM-to-PGM conversion. It demonstrates this test fixture's application-level restore path; it does not prove production-host permissions, external backup storage, every converter's behavior, disaster-recovery objectives, or a restore over a live Portainer installation. Production restore must remain a deliberate, separate operation.

When preflight fails, first inspect the printed checkout, temp, and Docker-root `df -h` output plus `docker system df`. Free space only by reviewing resources you own; the test scripts intentionally do not prune unrelated Docker content. A failed GitHub check includes the failing job logs and, for the browser/restore stages, uploads redacted Compose logs and screenshots as short-lived artifacts. If a stage is not run because the runner is below threshold, the job fails with the observed capacity and the end-to-end status remains unverified.
