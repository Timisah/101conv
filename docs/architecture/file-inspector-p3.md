# P3 File Inspector

The File Inspector is a read-only workspace for an owned upload. Core exposes
`GET /api/v1/uploads/{id}/analysis`; the endpoint resolves the upload through
the normal ownership and retention checks, verifies its digest and filesystem
identity, and returns a bounded report without file content.

## Analysis contract

`server/internal/analysis` owns the typed report contract. Image, PDF, and
Office inspection is performed in Core. Audio/video stream inspection is
delegated to a pinned installed probe with the same typed contract used by the
Worker. Analysis is bounded by a 4 MiB header read, 10,000 Office entries,
256 MiB of expanded Office data, and a 1 MiB sanitized JSON report.

The report includes detected type, file identity, image dimensions and safe
profile signals, PDF/package details, media streams, warnings, and sanitized
probe JSON. Location, GPS, serial-number, and ownership metadata are omitted
by design. The Inspector can download the sanitized JSON report; it never
modifies the original upload.

Expired, missing, cleanup-pending, symlinked, changed, digest-mismatched, or
otherwise invalid originals cannot be inspected. The implementation is not an
OCR service, metadata-removal job, or universal media probe; unsupported or
deeper container details are reported explicitly.
