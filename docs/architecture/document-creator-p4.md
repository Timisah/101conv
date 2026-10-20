# Document Creator foundation

The first Build slice establishes the non-destructive page model and a
mobile-friendly editor shell. Users can add source files, see image thumbnails,
open an ordered preview, reorder pages with accessible up/down controls,
rotate in 90-degree increments, duplicate or remove pages, and undo/redo those
edits. Preparing sources uploads each unique file through the normal ownership,
quota, and retention path; it does not replace or rewrite the original.

The typed manifest lives in `server/internal/document/manifest.go`:

- schema version 1;
- PDF is the first export target;
- at most 500 pages;
- each page references an owned upload and a bounded source-page number;
- rotations are limited to 0/90/180/270 degrees;
- page array order is the export order, so filenames never determine layout.

For raster image pages, the editor can generate a PDF directly in the browser:
each page is rotated, fitted to an A4-sized page without cropping, and emitted
as a downloadable PDF while the originals remain unchanged. This is an initial
local exporter, not a replacement for the server worker. PDF/Office source
expansion and merging, saved projects, guest expiration, and
fidelity-tested server PDF assembly remain the next layers. The server
exporter must preserve native PDF/vector content where possible and must not
silently resize image sources.

The prepared manifest can still be downloaded as a durable handoff for that
upcoming server exporter.
