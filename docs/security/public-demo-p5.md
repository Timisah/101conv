# Isolated Public Demo Architecture & Threat Model (FR-018)

This document defines the deployment architecture, resource controls, privacy guarantees, and pre-launch release gates for an optional, resource-limited public demonstration of **101conv**.

Private self-hosting via `compose.yaml` remains the default. Public demo mode is **disabled by default** (`DEMO_MODE=false`) and has zero runtime effect on private installations.

## 1. Strict isolation boundary

A public demo must **never** share a host directory, PostgreSQL database, Docker volume, network, or secret with a private or NAS deployment:

- Dedicated stack: [`compose.demo.yaml`](../../compose.demo.yaml) (`name: 101conv-demo`).
- Zero host/NAS mounts: `AUTOMATION_MOUNT_ROOTS=""` and no `/mounts` bind mount.
- Isolated internal networks: `demo-core-db` and `demo-worker-db` (`internal: true`).
- Hardened containers: non-root UID `65532` (Core/Worker) and `101` (Web), `read_only: true`, `cap_drop: [ALL]`, bounded `mem_limit`, `cpus`, `pids_limit`, and small tmpfs `/work` (`256m`).
- Verified by CI via [`scripts/verify-demo-compose.sh`](../../scripts/verify-demo-compose.sh).

## 2. Resource, compute, and profile guardrails

Implemented in [`server/internal/demo/service.go`](../../server/internal/demo/service.go) and enforced by Core before upload storage and job queuing:

| Control | Environment variable | Default in `compose.demo.yaml` |
| --- | --- | --- |
| Enable demo mode | `DEMO_MODE` | `true` (disabled by default in Core) |
| Overload kill switch | `DEMO_KILL_SWITCH` / `PUT /api/v1/demo/kill-switch` | `false` |
| Max upload per file | `DEMO_MAX_FILE_BYTES` | 15 MiB (`15728640`) |
| Max files per batch | `DEMO_MAX_BATCH_FILES` | `3` |
| Global active jobs cap | `DEMO_MAX_ACTIVE_JOBS` | `4` |
| Global hourly job budget | `DEMO_MAX_GLOBAL_JOBS_PER_HOUR` | `60` |
| Max output per job | `DEMO_MAX_OUTPUT_BYTES` / `MAX_OUTPUT_BYTES` | 25 MiB (`26214400`) |
| Worker conversion timeout | `DEMO_CONVERSION_TIMEOUT` | `90s` |
| Short auto-deletion window | `DEMO_RETENTION_MINUTES` | `15` minutes |
| Allowed profiles | `DEMO_ALLOWED_PROFILES` | Verified image conversion & compression profiles only |

## 3. Runtime overload kill switch

Administrators can pause all demo uploads and job submissions immediately without restarting containers:

```sh
curl -X PUT https://<demo-host>/api/v1/demo/kill-switch \
  -H "Authorization: Bearer $DEMO_ENGINE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"active":true,"reason":"CPU saturation"}'
```

While active, Core rejects non-admin uploads and submissions with HTTP `503` (`demo_kill_switch_active`, `Retry-After: 300`), and the UI displays an alert banner directing visitors to self-host with Docker.

## 4. Non-sensitive self-hosting funnel metrics

To evaluate empirically whether demo visitors actually explore GitHub or self-hosting instructions without collecting personal data, Core persists strictly aggregate daily counters in `conv_demo_funnel_events` (`event_date`, `event_type`, `event_count`):

- **Allowed events:** `demo_visit`, `demo_conversion_started`, `demo_download`, `cta_github_click`, `cta_install_docs_click`, `cta_self_host_copy`, `demo_blocked_overload`, `demo_blocked_kill_switch`.
- **Never stored:** IP addresses, user agents, cookies, filenames, file sizes, or free-form strings.
- **Access:** `GET /api/v1/demo/metrics?days=30` is administrator-only (HTTP `401`/`403` for guests and non-admins) and computes aggregate CTR ratios (`githubCtrPercent`, `installDocsCtrPercent`, `selfHostCopyRatePercent`).

## 5. Pre-deployment security gates

Code support in this repository does **not** authorize launching a public internet-facing demo automatically. Before exposing any live public demo host, the project owner must verify:

1. **P0 #4 (Official TUF trust root)** — signed official catalog metadata published under owner custody.
2. **P0 #5 (Hostile-parser & public-exposure audit)** — outer reverse-proxy rate limiting, DDoS protection, host firewalling, and isolated VM/container host placement with predictable billing caps.
3. **Privacy & Terms disclosures** — configure `DEMO_PRIVACY_URL` and `DEMO_TERMS_URL` for the hosted operator entity.
4. **Retention schedule** — verify Core guest retention policy matches `DEMO_RETENTION_MINUTES` so uploads and results expire within 15 minutes.
