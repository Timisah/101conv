# Converter catalogs

101conv keeps the legacy Java catalog files for rollback compatibility. The Go marketplace uses a signed TUF repository at `catalog/metadata` and `catalog/targets`; do not replace the legacy `catalog/index.json`.

## Official catalog status

The official repository appears by default. It becomes enabled and trusted automatically when a real TUF root is embedded in `server/internal/repositorymanager/official-root.json`. That file is intentionally empty in this source tree until the project owner provisions the production root. The running service therefore reports `trust_unavailable` and will not install remote packages yet. It does not trust a checksum by itself.

The Go catalog already defines two packages:

- **FFmpeg Audio and Video** uses one shared FFmpeg engine for common audio/video formats.
- **JPEG XL Images** uses the JPEG XL engine for JPEG/PNG/APNG/GIF → JXL and JXL → JPEG/PNG.

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

After owner review, the public `catalog/metadata/root.json`, embedded `official-root.json`, signed metadata, and target manifests can be published to the existing GitHub repository. The web app will browse the catalog automatically after the application is rebuilt with that embedded root. No additional Portainer repository entry or manual root pin will be needed.

## Repository and manifest format

The repository base URL is `https://raw.githubusercontent.com/Timisah/101conv/main/catalog`. TUF metadata is under `/metadata`; target paths are under `/targets`. The signed `catalog/index.json` target is `catalog/targets/catalog/index.json`, separate from the legacy Java index at `catalog/index.json`.

The signed index has schema version 1 and lists engine/profile manifest target paths. Engine manifests contain `{"engine": <engine.Definition>}` and profile manifests contain `{"profile": <profile.Definition>}`. A profile names an engine, compatible version range, required capabilities, formats, and argv templates. An engine is installed once and can be shared by multiple profiles.

Packages remain hosted by upstream GitHub Releases; they are not committed to this repository. An artifact has a logical TUF target path, upstream HTTPS URL, size, format, and SHA-256. TUF targets metadata signs the exact length and digest and includes the upstream URL in signed custom metadata. The Go downloader only accepts GitHub release URLs and a fixed set of GitHub asset redirect hosts, then verifies bytes against signed TUF metadata before extraction. The manifest digest and TUF target digest must agree.

Supported archives are `tar.gz`, `tar.xz`, `tar.lz`, and `zip`. Extraction rejects traversal, links, device files, unsupported entries, excessive entry counts, and expanded size over 2 GiB. XZ and LZip dictionary requests are capped at 128 MiB. Package downloads are capped at 1 GiB. Converter execution is handled by the restricted Worker; signature verification does not sandbox a converter.

Community repositories still require an administrator to pin their TUF root by an independently checked fingerprint. The marketplace reads the API trust value `pinned`; the React client now uses that same value when deciding which repositories to browse.
