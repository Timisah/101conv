# Converter catalogs

The marketplace uses a versioned catalog at `catalog/metadata` and `catalog/targets`, verified with TUF when the repository trust root is provisioned. The current GitHub catalog index is also available for explicitly approved unsigned browsing/installation; unsigned packages are clearly identified in the UI.

## Official catalog status

The official repository is hosted as static files on GitHub and appears by default in browse-only mode. An administrator can explicitly approve unsigned official installs from Administration. That choice persists in Go data storage and enables package previews/install/update using HTTPS, declared sizes, SHA-256 checks, compatibility checks, and safe extraction. These checks detect corruption but do not authenticate the publisher; anyone able to alter the GitHub catalog can replace the manifests and hashes. Revoke the approval in Administration at any time. A verified TUF root remains the recommended way to enable installs. The service never downloads artifacts while browsing. The root slot is intentionally empty until production signing is provisioned.

The signed target catalog at `catalog/targets/catalog/index.json` is the authoritative release list. The legacy compatibility index at `catalog/index.json` and Core's embedded `server/internal/catalog/official.json` are generated from it; they are checked into the repository so older clients and first-run installs do not need to fetch the signed repository just to browse capabilities. Run `python3 scripts/sync-compatibility-catalog.py --check` in CI, or `--write` after reviewing a target-catalog change. The older files under `catalog/profiles/` remain in place for clients that already reference those immutable manifest paths.

The Go catalog exposes separate conversion and compression profiles:

- **FFmpeg Audio/Video Conversion** and **FFmpeg Audio/Video Compression** share one FFmpeg engine installation.
- **JPEG XL Image Conversion** and **JPEG XL Image Compression** share one JPEG XL engine installation.
- **FFmpeg Image Conversion 1.1.0** uses the same pinned LGPL FFmpeg engine for JPG/JPEG, PNG, WebP, TIFF, BMP, GIF, and AVIF mappings verified against the selected build. It is published at a new immutable target path while 1.0.0 remains available for older installations and clients. Rules are target-specific: JPEG uses FFmpeg quantizer quality, PNG uses compression level, WebP uses libwebp quality, AVIF uses libaom CRF, and TIFF/BMP/GIF expose no controls that their encoders ignore. Still-image targets select the first frame; WebP and GIF preserve animation; AVIF is encoded as one still image.

Their engine/profile manifests live under `catalog/targets/`. The package SHA-256 values were checked against the upstream release bytes. FFmpeg is the LGPL BtbN build; JPEG XL is the upstream BSD-3-Clause build. Review the upstream notices and local legal obligations before publishing.

## Provision the official signing identity

This is an explicit maintainer action. The command creates four Ed25519 keys and the public root; it does not run during build or application startup. Use a new, empty directory outside the repository on a trusted machine, then move `root.key` to offline backup/custody. Keep the remaining role keys in a protected signing location. Never commit the key directory.

```bash
mkdir -p /secure/101conv-tuf
chmod 700 /secure/101conv-tuf
cd server
go run ./cmd/catalog-repo init \
  --keys-dir /secure/101conv-tuf \
  --root-file ../catalog/metadata/root.json \
  --embed-root internal/repositorymanager/official-root.json
```

`init` refuses to replace a non-empty key directory or an existing public root. It prints the exact root fingerprint. Have the project owner verify and record that fingerprint before publishing the root. The initial root currently uses a single-maintainer threshold; use separate custody for the offline root key and online publishing keys. Do not run this command in CI.

## Publish catalog metadata

Run this when manifests or metadata need publishing. The publisher checks the upstream package URL, size, and SHA-256 before signing. It uses the maintained `go-tuf` metadata/signature implementation, requires signing keys authorized by the root, and increments metadata versions from the existing files.

```bash
cd server
go run ./cmd/catalog-repo publish \
  --catalog-root ../catalog \
  --keys-dir /secure/101conv-tuf
```

The command writes `catalog/metadata/{targets,snapshot,timestamp}.json`. Commit metadata and targets in one Git commit so the static repository changes atomically. Timestamp metadata expires in seven days; publish again before expiry. Keep private keys out of CI logs, Git, the application image, and `DATA_PATH`.

The target manifests are already browseable from the existing GitHub repository without provisioning a signing identity. After owner review, the public `catalog/metadata/root.json`, embedded `official-root.json`, and signed metadata can be committed and pushed there to enable installation. No separate catalog host, additional Portainer repository entry, or manual root pin will be needed.

## Repository and manifest format

The repository base URL is `https://raw.githubusercontent.com/Timisah/101conv/main/catalog`. TUF metadata is under `/metadata`; target paths are under `/targets`. The signed catalog index target is `catalog/targets/catalog/index.json`.

The signed index has schema version 1 and lists engine/profile manifest target paths. Engine manifests contain `{"engine": <engine.Definition>}` and profile manifests contain `{"profile": <profile.Definition>}`. A profile names its explicit `type` (`conversion` or `compression`), engine compatibility range, required capabilities, formats, and argv templates. Keep conversion and compression in separate profile manifests and catalog entries even when they use the same engine. The matching profile type is enforced at job submission, and an engine is installed once and shared by all profiles that depend on it. Older installed definitions without a type remain readable through the legacy compatibility path. Older repository manifests are classified as conversion-only when they have no compression block, or as compression-only when they do; new manifests should declare `type` explicitly.

Packages remain hosted by upstream GitHub Releases; they are not committed to this repository. An artifact has a logical TUF target path, upstream HTTPS URL, size, format, and SHA-256. TUF targets metadata signs the exact length and digest and includes the upstream URL in signed custom metadata. The Go downloader only accepts GitHub release URLs and a fixed set of GitHub asset redirect hosts, then verifies bytes against signed TUF metadata before extraction. The manifest digest and TUF target digest must agree.

Supported archives are `tar.gz`, `tar.xz`, `tar.lz`, and `zip`. Extraction rejects traversal, links, device files, unsupported entries, excessive entry counts, and expanded size over 2 GiB. XZ and LZip dictionary requests are capped at 128 MiB. Package downloads are capped at 1 GiB. Converter execution is handled by the restricted Worker; signature verification does not sandbox a converter.

## Authoring and validating a profile

Repository packages use stable engine IDs and profile IDs. A profile version is independent of an engine version. Prefer a tested compatibility range and required capabilities over pinning one engine patch release. A profile rule refers to the engine's stable executable name, never an executable path or shell command. `arguments` is an argv array: only standalone `{source}` and `{target}` tokens are substituted by the Worker. Rules may cover multiple source or target formats, but no two rules in one profile may match the same normalized input/output pair. Unsupported placeholders and ambiguous pairs are rejected when the repository is read.

For a quick profile-only example:

```json
{
  "profile": {
    "schemaVersion": 1,
    "type": "conversion",
    "id": "png-to-jxl",
    "version": "1.0.0",
    "name": "PNG to JPEG XL",
    "license": "BSD-3-Clause",
    "engines": [{
      "engineId": "jpeg-xl",
      "versions": {"minimum": "0.12.0", "maximumExclusive": "1.0.0"},
      "capabilities": ["image.encode"]
    }],
    "rules": [{
      "engineId": "jpeg-xl",
      "executable": "cjxl",
      "sources": ["png"],
      "targets": ["jxl"],
      "arguments": ["{source}", "{target}", "-q", "85"]
    }]
  }
}
```

The referenced engine manifest must declare `id: "jpeg-xl"`, provide capability `image.encode`, and map executable name `cjxl` to a safe package-relative path. The catalog index entry's ID and version must match the manifest. Version range and capabilities are checked while previewing dependencies; the repository author tool also checks included engine candidates and executable names. If JPEG XL also publishes compression controls, create another profile entry with `type: "compression"` and the same engine requirement instead of mixing both workflows into one new profile.

Use this repository layout:

```text
catalog/
  targets/catalog/index.json
  targets/engines/jpeg-xl-linux-amd64.json
  targets/profiles/png-to-jxl.json
```

Add an index entry with a unique ID/version pair and a path relative to `targets/`, then run preflight before publishing. This validates index/manifest agreement, schemas, ambiguous rules, argument placeholders, and included engine compatibility. It does not download or execute converter binaries.

```bash
cd server
go run ./cmd/catalog-repo validate --catalog-root ../catalog
go test ./cmd/catalog-repo ./internal/profile ./internal/repositorymanager
```

Only after validation should maintainers run `catalog-repo publish`. The publish step verifies upstream package bytes and signs the TUF metadata. Test an actual conversion with each new profile and supported engine build before release: structural validation cannot prove that converter-specific flags, codecs, or output behavior are correct. For the pinned FFmpeg image profile, run `scripts/verify-ffmpeg-image.sh`; it checks every declared source/target pair, signatures, dimensions, animation behavior, and corrupt-input failure against the manifest's exact artifact digest.

Conversion rules may declare `options` using the same bounded `number`, `select`, or `boolean` schema used by compression profiles. `arguments` can contain `{option:key}` placeholders, including inside one argv token; Core validates the declared key and range/choice, expands the value into the immutable execution specification, and the Worker never invokes a shell. Existing profiles without options keep their previous behavior.

Compression profiles may mark one numeric option with `targetSizeMb`. A
duration-based profile must declare `probeExecutable`, `audioBitrateKbps`, and
`minimumVideoKbps`; its rules may use the bounded
`{compression.videoKbps}`, `{compression.audioKbps}`, and
`{compression.bufferKbps}` placeholders. An image-style profile may instead
declare `targetQualityOption`, set `targetQualityHigherIsBetter` when larger
values mean better quality, and use `{compression.quality}`. The Worker then
performs a bounded quality search and hard-checks the final artifact size.
`targetSizeAttempts` is capped at twelve and defaults to eight. These controls
do not promise exact sizes for every codec, and unsupported or infeasible
targets must fail rather than publish an oversized result.

Video compression profiles may additionally set `preserveResolution` and
`preserveHDR`. Either flag requires `probeExecutable`; the Worker uses one
bounded JSON probe for the input and output and rejects a resized, SDR,
lower-bit-depth, or metadata-downgraded first video stream. These flags do not
claim multi-stream, subtitle/attachment, HDR10+, or Dolby Vision equivalence.

Declared compatibility is a manifest claim. Verified compatibility is limited to the source/target pairs covered by the exact engine artifact test. An unsupported or unverified pair must not be added to `sources` or `targets`. Metadata option `0` requests converter metadata mapping and `-1` requests removal; it does not promise that output formats can carry every input EXIF field, orientation tag, timestamp, or ICC/color-profile record. Profile descriptions must state format-specific limitations.

The target catalog also ships managed LibreOffice Office-to-PDF and Poppler PDF rendering/text extraction adapters for supported Linux AMD64 deployments. Their small adapter archives are pinned and checked against the exact Worker runtime; they do not imply arbitrary Office fidelity, OCR, editable PDF output, or support on other architectures. Keep the document profiles separate: LibreOffice publishes Office-to-PDF rules, while Poppler publishes PDF page rendering and text extraction rules.

The manifest schema and validation behavior are versioned by `schemaVersion`. Additive optional fields can be introduced without a Core patch; changing placeholder semantics or execution behavior requires a schema/Worker compatibility change and should be documented as such. Third-party repositories should test against the oldest supported 101conv version named by their catalog bounds.

Community repositories still require an administrator to pin their TUF root by an independently checked fingerprint. The marketplace reads the API trust value `pinned`; the React client now uses that same value when deciding which repositories to browse.
