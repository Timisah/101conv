# Document conversion and PDF support (D.2)

## Use

Rebuild the stack from `compose.yaml` (Portainer: pull the repository and rebuild
the Worker). Worker now uses `server/Dockerfile.worker`; Core remains on its
minimal scratch image. Keep the existing engine, uploads and results mounts and
internal Worker network. No new public ports, privileged containers, database
migrations, or credentials are required.

In Marketplace, refresh the official catalog and install **Office Documents to
PDF** (`libreoffice-pdf-linux-amd64`, 1.0.0). The normal repository preview,
trust approval and dependency installation apply. Existing unsigned-source
approval remains explicit; checksums alone do not authenticate a publisher.
Enable the profile if disabled. Guests and authenticated users can then upload
these inputs and choose PDF:

| Family | Supported inputs |
| --- | --- |
| Writer | DOCX, DOC, ODT, RTF, TXT |
| Calc | XLSX, XLS, ODS |
| Impress | PPTX, PPT, ODP |

The same profile works with mixed document batches, automatic profile selection,
administrator/user preferences, and a mounted-drive automation with a `convert`
action targeting `pdf`. Each file is an ordinary persisted job; retries,
cancellation, quotas, owner isolation, result retention, ZIP downloads and
publication receipts use the existing infrastructure. Automations use profile
defaults and pin the profile release as before. Interactive jobs/batches may
select PDF image quality, tagged PDF, and standard PDF or PDF/A-2b export.
PDF/A is a LibreOffice export setting, not independently certified conformance.

Install **PDF Rendering & Text Extraction** (`poppler-pdf-linux-amd64`, 1.0.0)
for PDF to PNG/JPG/JPEG or TXT. Rendering produces **one selected page** (default
1), with a bounded maximum edge (512–4096 px; default 2048). Text extraction
produces UTF-8 with plain or preserved layout. Select the profile explicitly in
the conversion panel to expose its options; automatic selection uses defaults.
There is no OCR, all-pages image archive, PDF editing, or editable Office output
in this release. Scanned/blank PDFs with no extractable text fail with an explicit
message. Password-protected Office documents and encrypted PDFs are unsupported.

## Runtime and managed-package boundary

LibreOffice is not a static executable. Its filters, shared libraries, data,
fontconfig, fonts, and loader are required together. Shipping a random standalone
`soffice` into the old scratch Worker would not work.

The runtime is therefore an explicit part of the Worker image:

* Debian `13.1-slim` is pinned by its multi-platform OCI digest in
  `Dockerfile.worker`.
* LibreOffice Core, Writer, Calc and Impress are all pinned to Debian revision
  **4:25.2.3-2+deb13u8** (upstream 25.2.3 with Debian security patches).
* Poppler utilities are pinned to **25.03.0-5+deb13u4**.
* APT authenticates Debian's signed Release metadata and verifies Packages and
  `.deb` SHA-256 hashes. The build records the installed package versions in
  `/usr/share/101conv/runtime-packages.tsv` and upstream engine package metadata,
  including archive URLs and SHA-256, in `engine-provenance.txt` in the image.
  Transitive runtime dependency revisions are recorded, not all separately pinned;
  retain the built image digest for a deployment rollback.
* Liberation 2.1.5 and DejaVu 2.37 fonts are included. LibreOffice/fontconfig
  substitutes missing fonts. Proprietary Office fonts are not redistributed.
  A layout requiring additional licensed fonts can use a read-only font mount at
  `/usr/local/share/fonts`; rebuild the font cache in a custom image for stable
  performance. Test non-Latin scripts with appropriate licensed fonts before use.

The marketplace installs **small managed adapter packages**, not a second copy
of the system runtime. Their engine definitions are explicit about this split:
`libreoffice` 25.2.3+deb13u8.1 and `poppler` 25.3.0+deb13u4.1. The installed
package contains an executable adapter and its MIT license. These deterministic
archives live under `catalog/targets/packages`, with exact sizes/SHA-256 in the
engine manifests; `scripts/package-document-engines.py` regenerates them.

Adapters resolve through the existing managed engine installation and immutable
job execution snapshot. Each checks the exact runtime package revisions before
execution and fails closed on a mismatch. Nothing discovers an arbitrary
`soffice` in PATH, downloads a binary at job time, uses `latest`, or delegates to
an external conversion service. Installing the profile on an old/custom Worker
without this runtime fails; rebuild the Worker before enabling the profile.
Removing/disabling the managed profile/engine prevents new selection as usual;
it does not uninstall shared runtime libraries from the image. Already queued
jobs retain their normal immutable execution definitions.

Signed repositories can ship adapter archives as normal TUF targets. The built-in
unsigned fallback can fetch relative artifacts only below its hard-coded
same-origin official targets root, without cross-origin redirects, and still
checks length/SHA-256. External GitHub release downloads retain their existing
allowlist. No repository trust checks are relaxed.

The initial catalog is **Linux AMD64** only. Debian's pinned base and package
families support ARM64, and adapters contain no architecture-specific code. To
add ARM64, verify availability and provenance of the same revisions, build the
Worker for ARM64, run the full runtime and platform tests on that architecture,
and publish separate `linux-arm64` manifests/catalog entries with their own
archive identities. Do not mark AMD64 entries as platform-neutral or rely on
emulation as validation.

## Security, resources and errors

Worker retains its non-root user, read-only root filesystem and engine mount,
dropped capabilities, private database network, PID/memory/CPU limits, bounded
stdout/stderr, timeout/cancellation and size-limited attempt workspace. It never
mounts source automation drives. Engine storage must permit executable files;
`/work` and `/tmp` remain `noexec` in deployment.

Each Office attempt copies its verified upload to a fixed filename retaining the
source extension, starts headless LibreOffice with a unique user profile, disables
macro execution and sets macro security to its most restrictive level, disables Writer/Calc automatic
link updates, disables recovery/first-start UI and OpenCL, and uses a unique
output directory. There is no shared office listener or profile lock. Worker
cancellation kills the process group and removes the attempt workspace.
These controls reduce document attack surface; they do not make a document parser
a security sandbox. The container's existing network and resource restrictions
remain necessary.

Before launching LibreOffice, Worker verifies Office ZIP structure and required
members/MIME types, rejects unsafe paths/symlinks, limits packages to 10,000
entries and 256 MiB declared expanded contents, and checks legacy CFB and RTF
signatures. Encrypted OOXML/ODF and incorrectly renamed containers are rejected.
TXT encoding detection remains LibreOffice's responsibility. The extension must
match the actual Office container; changing a filename is not format conversion.

LibreOffice can exit zero without creating output; the adapter requires a
nonempty PDF and a successful Poppler `pdfinfo` parse. Worker independently
checks PDF header/trailer and normal regular-file/size limits before atomic
publication. A header/trailer check alone is not a complete PDF parser.
Poppler validates input PDFs, rejects encryption and checks text extraction
actually contains non-whitespace text. Page-out-of-range, malformed documents,
missing runtime, empty output and output-limit errors use ordinary failed jobs;
there is no silent successful download of an empty or mislabelled artifact.

Large/complex files can exceed the existing 1 GiB workspace or memory ceiling;
Office imports and the copy consume additional space beyond upload size.
Preserve conservative concurrency (default 1) and tune deployment limits based
on representative workloads. Spreadsheet print areas/page breaks, charts,
embedded objects, animations, tracked changes and unavailable fonts may render
differently from Microsoft Office. The fixture matrix verifies basic fidelity,
not arbitrary document equivalence. Review converted output when layout matters.

## Licensing, updates and verification

Debian's LibreOffice copyright file identifies MPL-2.0 for the main source;
components also have LGPL and other terms. Poppler core is GPL-2.0-only OR GPL-3.0-only, with GPL-2.0-or-later contributions and
component notices; font and dependency terms are separate. The image retains
Debian copyright notices under `/usr/share/doc` and common license texts.
Debian package `Source` metadata and the Debian source archive provide matching
source references. Anyone redistributing the image must meet the applicable
source and notice obligations; retain the package inventory and source versions
with the release. The MIT adapter license does not replace runtime licenses.

When updating runtimes, change the exact Debian pins and adapter version checks,
publish **new** engine/archive identities and compatible profile releases, and
retain existing artifacts for queued jobs. Never overwrite a released adapter
archive or change its checksum in place. Old images and adapters must remain
paired during rollback. If a Debian security revision disappears from live
mirrors, the build intentionally fails; update to a reviewed revision or use an
authenticated Debian archive/snapshot rather than dropping version checks.

Run `./scripts/verify-documents.sh` with Docker and Python 3. It builds production
Worker and test images, verifies deterministic packages, tests every requested
Office input against real LibreOffice and checks extracted PDF text, exercises
PDF rendering/text extraction, invalid/encrypted/no-text inputs, PDF/A export,
concurrency, size limits, publication guards and attempt cleanup. It then starts
an isolated temporary PostgreSQL on an internal network and verifies guest and
authenticated HTTP batches/downloads, owner isolation, managed engine installation,
and actual folder automation publication. Only its own temporary DB/network are
removed; no deployment data is touched. CI runs the same script. The existing
full PostgreSQL Go suite and frontend type/build checks cover regressions.

Reference sources used for the integration:

* [Debian LibreOffice binary releases](https://deb.debian.org/debian/pool/main/libr/libreoffice/)
  and [Poppler binary releases](https://deb.debian.org/debian/pool/main/p/poppler/).
* [LibreOffice 25.2 PDF command-line export parameters](https://help.libreoffice.org/25.2/en-US/text/shared/guide/pdf_params.html).
* LibreOffice 25.2.3.2 [macro security schema](https://github.com/LibreOffice/core/blob/libreoffice-25.2.3.2/officecfg/registry/schema/org/openoffice/Office/Common.xcs)
  and [Calc link-update modes](https://github.com/LibreOffice/core/blob/libreoffice-25.2.3.2/sc/inc/global.hxx).
  The corresponding registry properties were also inspected in the built runtime.

Document runtime revisions are image-wide in this first release. Drain pending
Office/PDF jobs before replacing the image with a different runtime revision;
side-by-side document runtime versions and job routing by runtime are not
implemented. The runtime mismatch check deliberately fails instead of silently
executing a queued snapshot with a different LibreOffice/Poppler revision.
