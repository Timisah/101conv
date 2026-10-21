# P2: bounded target-size compression

This document records the verified P2 increments. It covers per-file
target-size contracts, deterministic combined batch budgets, a bounded shared
media analysis path, explicit stream/metadata policy, and HDR-aware output
equivalence checks. The implementation is source and test verified; it is not
deployment evidence.

## Contract

Compression profiles may mark one numeric option with `targetSizeMb` and may
declare a numeric `targetQualityOption`. When both are present, the Core freezes
the selected quality range and attempt bound into the immutable job execution
specification. The Worker's adaptive path tries at most twelve converter runs
(eight by default), tests the best and worst quality endpoints, then uses a
bounded search for the best known quality that fits.

The Worker validates every candidate as a regular, non-empty artifact, checks
the format signature for supported image types, and applies the hard byte cap
before publishing the result. If even the lowest quality exceeds the cap, the
job fails with an explicit infeasibility error. `requireSmallerOutput` remains
an independent constraint. The original upload is never modified.

Profiles that use bitrate placeholders such as `{compression.videoKbps}` retain
the existing duration-based video path and media probe requirement. Adaptive
quality profiles use `{compression.quality}` and do not require a media probe.
The two modes are intentionally represented separately in the execution spec
so queued jobs remain compatible with the profile and engine versions resolved
at submission time.

## Current catalog coverage

The JPEG XL compression profile now exposes a quality ceiling and a maximum
output size. It supports JPEG XL output from its declared JPEG/PNG/APNG/GIF
sources, with the exact source/target and encoder behavior still limited by the
pinned `jpeg-xl` engine package. The pinned FFmpeg engine also has a separate
image-compression provider for JPEG, PNG, WebP, and AVIF output. Its
rule-scoped controls distinguish JPEG quantizer quality, WebP quality, AV1 CRF,
PNG compression level, metadata mapping, and bounded target-size search. The
provider keeps source pixel dimensions and intentionally encodes still images.
The frontend describes whether a target uses adaptive quality or video bitrate
estimation from the capability metadata.

This is not a promise that every codec can meet an exact byte target. The
target is hard for the artifact that is published, while quality selection is
bounded and codec behavior may make a target infeasible.

## Combined batch budgets

Compression batch requests may set `combinedTargetSizeMb`. Core resolves each
item's compatible compression profile before persistence, reads the uploaded
input sizes, and allocates the requested maximum proportionally. Profile
minimums and maximums are respected with a constrained largest-remainder
allocation; if the budget cannot satisfy all minimums, the batch is rejected
before any item is submitted. If profile maxima sum to less than the request,
the result is best effort below the requested maximum. The allocated
`targetSizeMb` values and resolved profile IDs are frozen in each durable batch
item request, so resume and retry do not recalculate the budget.

Combined budgets currently require one whole-megabyte numeric target-size
option per item and use the Worker output ceiling as an additional per-file
cap. Each item is independently resolved and frozen, so a retry cannot change
its allocation or profile. The combined field is a maximum, not an exact
total.

## Video fidelity validation

Compression rules may declare `streamSelection` (`all`, `first-video-audio`,
`first-video`, or `audio`) and `preserveMetadata`. Core validates and freezes
these values; the Worker expands them into allowlisted FFmpeg `-map` and
metadata/chapter arguments. Candidate output is rejected if the selected
stream layout, audio channel layout, or safe comparable metadata is lost.

`preserveResolution` and `preserveHDR` require a bounded `probeExecutable`.
Core freezes them, along with `hdrPolicy` (`preserve`, `allow-sdr`, or
`tone-map`) and `dynamicHDRPolicy` (`reject` or `verify`), into the immutable
execution specification. The Worker reuses the input probe for bitrate sizing
and validates every selected video stream in `all` mode. Preserve checks cover
dimensions, transfer/primaries/matrix/range, bit depth, mastering-display and
content-light side data, and detected dynamic HDR payload values. Unsupported
dynamic HDR can be rejected at input; verified dynamic metadata must match
exactly. Missing or downgraded properties fail publication.

The Core exposes `POST /api/v1/inspect` for an authenticated, rate-limited
Inspector workspace. It streams the multipart input into a short-lived scan
file, verifies its digest, applies bounded image/document checks, reuses the
typed media probe, deletes the file before responding, and never creates a
conversion upload record. Reports retain the fullest bounded metadata
available, including location and device tags. Image scans preserve native
JPEG APP payloads (EXIF/TIFF, XMP, IPTC/Photoshop, ICC, JFIF and comments),
PNG text/EXIF/ICC/chunks, WebP metadata chunks, TIFF/DNG tags, and AVIF/HEIC/
JXL metadata boxes; PDF scans inspect bounded first/last file windows for Info
and XMP metadata. Media scans request ffprobe private data, programs, streams,
chapters, container tags and the complete bounded JSON document. The response
is private because the report may contain that sensitive data.

## Verification

The Worker, Core analysis, HTTP API, profile, jobs, and shared media packages
have focused tests. Frontend lint, type checking, and a Vite production build
also pass. Catalog preflight validates the profile fields and placeholder
rules. These checks are source/test evidence; they do not establish deployment
or production-engine verification.

## Owner-requested maximum-size UX follow-up (2026-10-21; planned, not verified)

- The per-file **Maximum size** field and optional combined batch maximum start **blank/unset** unless the user explicitly applies a size-constrained preset. `unset` means no byte ceiling and does not imply a numeric default. A user changing JPG to WebP/AVIF/JXL can simply choose normal format/quality compression without a target size.
- When a cap is supplied (example: a 5 MB original, 2 MB maximum), optimize for **highest feasible quality** while remaining at/below 2 MB, not the smallest possible byte count. The search may aim near 2 MB when the codec allows it, but user intent is quality subject to an upper bound, **not** an exact-byte promise. Codec-specific search direction/granularity and non-monotonic results require bounded actual-size validation and selection among feasible candidates.
- Preserve separate strict/best-effort policies and honest impossible-limit errors. Never silently resize, corrupt or emit an invalid output to meet a cap. Do not expand unbounded CPU/time/batch budget.
- Add regression cases for empty default, explicit size preset, JPG/JPEG/JXL/WebP/AVIF mappings where supported, targets with a higher-quality candidate nearer the limit than the current chosen result, impossible caps, inputs already smaller than the cap, mixed batches, cancellation/retries and result size/quality comparison.
- This documents [FR-009](../FEATURE_REQUESTS.md#fr-009--rich-image-compression-and-combined-batch-size-goal) / [#9](https://github.com/Timisah/101conv/issues/9); it does **not** claim the current encoder/UI already satisfy those criteria.

## Metadata and result interaction follow-up (2026-10-21; planned)

- Where a provider has a typed metadata setting, render a compact accessible **Preserve metadata** toggle in Convert/Compress rather than a two-choice select. Map checked/unchecked to the established provider-specific option values (e.g. `metadata=0` for attempted preservation and `metadata=-1` for removal where that mapping is valid); do not change the immutable option schema, promise universal metadata preservation or display a nonfunctional switch for unsupported codecs.
- Eliminate redundant helper hints such as “The original format is selected when this profile supports it. Choose another format to compress and convert in one job.” Retain concise guidance when output is lossy, fidelity is limited, the size ceiling is infeasible or a safety/privacy consequence exists.
- Completed single results and successful batch archives belong on the same Convert/Compress page as a visible download action. Reuse existing permission-checked Core result endpoints and validated status states, including expired results and partial batches. Jobs & history remains the full archive. See [FR-022](../FEATURE_REQUESTS.md#fr-022--simple-metadata-controls-quiet-helper-copy-and-inline-downloads) / [#27](https://github.com/Timisah/101conv/issues/27).

This is a future UX requirement; no implementation or deployment proof is claimed here.
