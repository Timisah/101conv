# Optional hardware acceleration (P4 — FR-013)

Hardware video acceleration is strictly opt-in and preserves the Worker's
least-privilege isolation boundary. The base Compose deployment remains
CPU-only by default.

## Trust boundary and reviewed Compose overrides

A Worker sees a GPU only when an administrator opts in with both an explicit
device allowlist environment variable and a reviewed Compose override:

- **Intel VAAPI / Quick Sync or AMD VAAPI:** `deploy/compose.gpu-vaapi.yaml`
  maps only `${GPU_RENDER_NODE:-/dev/dri/renderD128}` and sets `GPU_RENDER_NODES`.
- **NVIDIA NVENC / NVDEC:** `deploy/compose.gpu-nvidia.yaml` maps only
  `${GPU_NVIDIA_DEVICE:-/dev/nvidia0}`, `/dev/nvidiactl`, and `/dev/nvidia-uvm`,
  and sets `GPU_NVIDIA_DEVICES`.

Both overrides keep `read_only: true`, `cap_drop: [ALL]`, non-root UID
`65532:65532`, internal-only `worker-db` network, and zero Docker socket or
unrestricted `/dev` access. `scripts/verify-compose-security.sh` enforces these
invariants in CI for both the CPU-only base stack and both GPU overrides.

## Discovery and FFmpeg capability probe

1. `acceleration.Detect` verifies each allowlisted render node via read-only
   sysfs vendor/driver checks (`i915`/`xe` for Intel `0x8086`, `amdgpu` for AMD
   `0x1002`) and NVIDIA devices via `/dev/nvidiactl` + `nvidia-smi`.
2. When hardware is configured, Worker startup also runs
   `acceleration.ProbeFFmpegCapabilities` against the installed FFmpeg engine
   (`-hide_banner -hwaccels` and `-hide_banner -encoders`) to discover the
   actual verified hardware encoders (`h264`, `hevc`, `vp9`, `av1`) and
   `hwaccels` (`vaapi`, `qsv`, `cuda`).
3. Host device paths (`DevicePath`) are never serialized to the Operations API
   or job responses; only safe basenames, driver names, verified codecs, and
   diagnostic reasons are exposed to administrators on **Admin → Operations**.

## Deterministic selection, HDR safety, and CPU fallback

Jobs freeze an explicit `hardwareAcceleration` policy (`auto`, `cpu`,
`prefer_hardware`, `require_hardware`) into their immutable `ExecutionSpec`:

- **Allowlisted translation only:** `acceleration.BuildFFmpegArgs` rewrites only
  validated software video encoders (`libx264`, `libx265`, `libvpx-vp9`,
  `libaom-av1`, `libsvtav1`) to their allowlisted hardware counterparts
  (`*_vaapi`, `*_qsv`, `*_nvenc`) and injects only allowlisted device/filter
  flags. Arbitrary user-supplied FFmpeg flags are never accepted.
- **HDR preservation invariant:** hardware encoders do not claim verified HDR
  preservation. When `preserveHDR` is enabled, `auto` and `prefer_hardware`
  deterministically select CPU (`CPU fallback; HDR preservation is not verified
  for hardware encoders`), and `require_hardware` fails explicitly.
- **Transparent runtime fallback:** if a hardware-accelerated FFmpeg run fails
  due to a device/driver initialization error (classified by
  `acceleration.IsHardwareDeviceError`, never misclassifying corrupt input) and
  the policy is not `require_hardware` and `preserveHDR` is false, the Worker
  re-runs the job on CPU and records `fallback: true` and `deviceError` in
  `conv_jobs.acceleration_info` (returned as `job.acceleration`).

## Quality and performance considerations

Hardware encoders prioritize throughput and power efficiency, not universally
higher compression efficiency or visual fidelity than CPU software encoders.
Use `go test -v ./internal/acceleration/... ./internal/worker/...` to run the
deterministic capability, fallback, and selection benchmark harness, and
compare output sizes and visual quality on target hardware before enabling
`prefer_hardware` or `require_hardware` in production.
