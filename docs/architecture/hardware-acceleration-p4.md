# Optional hardware acceleration (P4 slice)

The Worker now reports explicitly configured acceleration devices in its
heartbeat. This is a discovery and policy foundation, not a claim that every
codec/driver combination is production-ready.

## Trust boundary

The base Compose deployment remains CPU-only. A Worker sees a GPU only when an
administrator supplies both:

- `GPU_RENDER_NODES`, a comma- or path-list-separated allowlist of Intel/AMD
  `/dev/dri/renderD*` nodes; or
- `GPU_NVIDIA_DEVICES`, an allowlist of `/dev/nvidiaN` nodes.

The deployment must map those exact devices in a reviewed Compose override.
There is no automatic `/dev` scan, Docker socket, `privileged` mode, or
unrestricted device mapping. The probe verifies the render-node vendor and
driver through read-only sysfs and verifies NVIDIA through `nvidia-smi`.

Example for a reviewed Intel/AMD override:

```yaml
services:
  worker:
    environment:
      GPU_RENDER_NODES: /dev/dri/renderD128
    devices:
      - /dev/dri/renderD128:/dev/dri/renderD128
```

The exact render node and driver mapping are host-specific. NVIDIA mappings
must include the selected `/dev/nvidiaN`, `/dev/nvidiactl`, and the driver
runtime devices required by the host. Do not copy an override without checking
the host's device ACLs and driver package.

## Capability and fallback contract

`server/internal/acceleration` emits a bounded report containing the vendor,
API, driver, encode/decode flags, verification level, and a safe diagnostic.
The report never includes arbitrary host paths in the Operations response.
CPU is always a selectable fallback. Hardware is selected only when the
requested encode/decode properties are available. A request that requires HDR
preservation currently selects CPU because hardware HDR preservation has not
yet been verified for the selected engine and codec. A future job-spec field
must freeze the resolved accelerator alongside the engine/provider before
enabling hardware execution.

The administrator-only `GET /api/v1/operations` response exposes each online
Worker's report. This makes unavailable devices and failed driver probes
visible without treating a green heartbeat as proof of codec fidelity.

## Remaining P4/P5 work

The probe does not yet select FFmpeg hardware arguments, benchmark codecs, or
publish per-codec HDR fidelity results. Those require engine-specific profiles,
actual device mappings, and output verification before hardware acceleration
can be advertised as generally available.
