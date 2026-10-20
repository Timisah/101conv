# P2: bounded target-size compression

This document records the verified P2 increments. It covers per-file
target-size contracts and deterministic combined batch budgets; shared file
analysis, stream selection, and full HDR-aware video policy remain open P2
work.

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
pinned `jpeg-xl` engine package. The frontend describes whether a target uses
adaptive quality or video bitrate estimation from the profile metadata.

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
cap. The combined field is a maximum, not an exact total, and it does not yet
share media analysis or coordinate stream selection across files.

## Verification

The Worker tests cover a real FFmpeg target-size media job, a deterministic
adaptive-quality fixture, input digest validation, output signatures, timeout
handling, and oversized/invalid results. Catalog preflight validates the new
profile fields and placeholder rules. These checks are source/test evidence;
they do not establish deployment or production-engine verification.
