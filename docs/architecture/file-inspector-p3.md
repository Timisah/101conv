# P3 File Inspector

The File Inspector is an ephemeral, read-only scan. Core exposes
`POST /api/v1/inspect`; the multipart source is held in a bounded temporary
file, never inserted into conversion storage, and removed after analysis.
The older owned-upload analysis route remains available for API clients that
already retain an upload and need an ownership-checked report.

## Analysis contract

`server/internal/analysis` owns the typed report contract. Image, PDF, and
Office inspection is performed in Core. Audio/video stream inspection is
delegated to a pinned installed probe with the same typed contract used by the
Worker. Analysis is bounded by a 4 MiB header read, 10,000 Office entries,
256 MiB of expanded Office data, and a 12 MiB bounded report containing the
full extracted metadata for the authorized requester.

The report includes detected type, file identity, image dimensions and profile
signals, PDF/package details, media streams, warnings, and bounded probe JSON.
Because the scan is authorized and ephemeral, the report may include GPS,
location, device, ownership, and other sensitive metadata; clients must treat
it as private and must not share it with other users.

An explicit `POST /api/v1/uploads/{id}/metadata-removal` action queues a
same-format conversion with the profile's `metadata=-1` option. It creates a
separate result, retains the original, requires the caller's submit capability,
and uses the normal job idempotency, quota, retention, and output validation
paths. A profile that does not expose the typed metadata option is rejected.

Expired, missing, cleanup-pending, symlinked, changed, digest-mismatched, or
otherwise invalid retained originals cannot be inspected. The implementation
is not an OCR service or universal media probe; unsupported or deeper
container details are reported explicitly. Metadata removal is never implicit.
