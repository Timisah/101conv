# Document Creator / Build Architecture

The Document Creator workspace (`web/src/features/build/`, `server/internal/document/`, `server/internal/httpapi/documents.go`, `server/internal/jobs/document_build.go`, `server/internal/worker/document_build.go`) implements non-destructive multi-page document assembly across images, multi-page PDFs, and supported Office documents.

## Non-destructive Manifest & Persisted Projects

The typed v1 manifest lives in `server/internal/document/manifest.go`:

- Schema version 1, at most 500 pages per project (`MaxPages`), rotations limited to `0`, `90`, `180`, or `270` degrees.
- Each page references an ownership-verified `uploadId`, a 1-based `sourcePage`, optional source grouping metadata (`sourceName`, `sourceKind`, `sourcePageCount`), and optional additive `layout` overrides.
- Page array order is the exact export order; filenames never determine page order.
- Projects are persisted in PostgreSQL (`conv_document_projects`):
  - Signed-in users receive persistent saved projects (`expires_at IS NULL`).
  - Guest projects automatically expire according to the instance guest retention window (`expires_at`), and expired guest projects are purged during scheduled guest cleanup.
  - Deleting a project never deletes source uploads.

## Source Inspection & Multi-page Expansion

`GET /api/v1/documents/sources/{id}` inspects an owned upload without mutating it:

- **Images (JPEG/PNG/GIF):** bounded header decode + EXIF orientation check -> 1 page with exact oriented dimensions.
- **PDFs:** bounded pure-Go PDF parser (`server/internal/document/pdf_parser.go`) supporting classic xref tables, incremental updates, PDF 1.5+ cross-reference streams, and compressed object streams (`/Type /ObjStm`). Encrypted PDFs are rejected explicitly. Returns exact page count and per-page MediaBox/Rotate dimensions so multi-page PDFs expand into individually rearrangeable pages.
- **Office documents (DOCX/ODT/PPTX/ODP/XLSX/ODS/RTF/DOC/XLS/PPT/TXT):** validated for ZIP traversal and decompression bombs (`ValidateOfficePackage`); expanded to PDF pages inside the isolated Worker via LibreOffice during export.

## Shared Page Geometry & Multi-format Assembly

`server/internal/document/geometry.go` mirrors `web/src/features/documentLayout.ts` (`A-028`) so server export geometry matches browser previews (`automatic`, `fit`, `fill`, `a4`, `letter`, `custom`, `marginMm`, `background`, `positionX/Y`).

Exports execute through the existing durable job queue (`mode = "build"`, `POST /api/v1/documents/export`) inside the isolated Worker (`server/internal/worker/document_build.go`), verifying every source upload's SHA-256 digest and size before assembly:

- **PDF (`pdf`):** Preserves native PDF vector text, fonts, paths, and streams via lossless object-closure copying and renumbering (zero rasterization) with `/Rotate` merging. Native JPEGs with EXIF orientation 1 are embedded verbatim via `/Filter /DCTDecode` (even when rotated via PDF CTM) with zero recompression; PNG/GIF images are embedded losslessly via `/Filter /FlateDecode` (+ `/SMask` alpha).
- **CBZ (`cbz`):** Ordered ZIP (`001.jpg`, `002.png`, ...) preserving original JPEG/PNG bytes for unrotated raster pages; PDF/Office pages rasterized via Poppler (`pdftoppm`) in the Worker.
- **Ordered Images ZIP (`zip`):** Numbered page images alongside `manifest.json`.
- **Multipage TIFF (`tiff`):** Pure-Go baseline TIFF 6.0 multi-IFD writer with bounded uncompressed RGB buffer cap (160 MiB).
- **DOCX (`docx`), PPTX (`pptx`), ODT (`odt`), ODP (`odp`):** Spec-compliant paginated OOXML / ODF archives placing one page image per page/slide at its physical dimensions. Honest fidelity warnings communicate that these are paginated image assemblies rather than editable flowing-text reconstructions.

The browser-local raster PDF path (`web/src/features/build/pdfLocal.ts`) remains available as a fast offline option when all pages are local raster images.
