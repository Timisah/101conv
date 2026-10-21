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
each page keeps its oriented pixel dimensions and receives a bounded physical
page size. Automatic mode follows each image's aspect ratio (with a 150 DPI
mapping and 2–15 inch long-edge bounds); Fit and Fill use A4, Letter, or a
validated custom page with automatic landscape/portrait orientation. Margins,
background, focal position, and a per-page mode override are represented in the
typed layout contract. The same geometry resolver drives the thumbnail,
ordered preview, and browser PDF MediaBox/draw matrix, so preview and export
do not silently fall back to a rigid portrait page.

The browser exporter preserves native JPEG bytes only when no rotation or EXIF
orientation correction is required. Other raster pages are encoded at their
original pixel dimensions for this local PDF path; server export remains the
place to preserve native PNG/PDF/vector content without rasterization.
Images are decoded with EXIF orientation applied where supported, and browser
work is bounded at 80 million pixels. This is an initial local exporter, not a
replacement for the server worker. PDF/Office source expansion and merging,
saved projects, guest expiration, and fidelity-tested server PDF assembly
remain the next layers. Unsupported source types are shown honestly and remain
available for source preparation/manifest handoff rather than pretending that
the browser exporter can merge them.

The prepared manifest can still be downloaded as a durable handoff for that
upcoming server exporter.
