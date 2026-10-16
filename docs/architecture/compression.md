# Compression workflow

Compression is a separate user workflow and a durable job mode. `POST /api/v1/compressions` uses the same authenticated upload, job ownership, cancellation, result, and quota boundaries as conversion. A request selects an installed profile, a supported output extension, and profile-declared option values; clients cannot submit executable paths or argument arrays.

## Profile contract

A profile exposes compression explicitly with a `compression` block containing typed options. Numeric options have server-enforced bounds; select options must match the declared choices. A rule with `compressionArguments` places one `{compression.args}` marker in its ordinary argv list wherever those additional flags belong for that engine. The Worker replaces the marker with the profile's argument array for compression jobs and omits it for conversions. This explicit insertion point preserves paired output flags such as `--output {target}`. Literal profile tokens are argv elements, and `{option:key}` references are expanded only after validating the value against the profile schema.

For media size targets, the profile marks a numeric option with `targetSizeMb`, declares an installed `probeExecutable`, and supplies bounded bitrate settings. Its compression arguments can use `{compression.videoKbps}`, `{compression.audioKbps}`, and `{compression.bufferKbps}`. The Worker runs the declared probe executable against the private input, derives an average bitrate from duration and target size, reserves 10% for muxing overhead, then passes the resulting arguments directly to the declared converter without a shell. It validates the final artifact against the requested size and rejects output that exceeds the target.

Profiles without compression declarations remain conversion-only and do not appear in the Compress workflow. The UI defaults to the source extension when the installed profile supports that pair. When it does not, the user explicitly chooses a compatible output format, which can combine compression and conversion in one job.

## Current catalog support and upgrade

The bundled FFmpeg profile release 1.1.0 adds target-size compression for its supported audio and video formats. It requires an FFmpeg engine release with the `media.probe` capability and declared `ffprobe` executable. The bundled engine release is represented as `9.0.2-28-g669f5ab083+101conv.1` to distinguish the packaging metadata that installs both FFmpeg tools while preserving the upstream build identifier. The JPEG XL profile release 1.1.0 exposes a bounded quality control while encoding JPEG, PNG, APNG, or GIF inputs to JXL; users choose JXL because this profile does not support same-extension recompression.

When an existing installation updates the FFmpeg profile, the dependency resolver installs the compatible engine package if its current engine lacks `media.probe`. Existing engine files and job history are preserved; old profile installations keep working until updated. Community profile authors can add compression settings by extending their manifests—no Core patch is needed for bounded numeric/select options or the declared target-size bitrate placeholders.

## Limits

Target size is estimated from duration and average bitrate, not guaranteed by the encoder. 101conv returns no artifact above the requested size; it currently reports that job as failed instead of automatically retrying at lower bitrates. Codec/container overhead and minimum viable bitrates can therefore make a very small target unattainable. FFmpeg provides target-size audio/video compression. JPEG XL provides a quality control rather than a target-size guarantee; other image profiles remain conversion-only until they declare compression rules and their encoder semantics are validated.
