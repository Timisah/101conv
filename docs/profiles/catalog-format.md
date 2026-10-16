# Converter profile catalogs

Converter profiles let an administrator add a conversion engine without rebuilding the 101conv image. The **101conv Official Catalog** is pre-added on every install; its current index is intentionally empty until official packages are published. Administrators can add community catalogs too. Catalogs are fetched only when an administrator opens them in **Profiles**.

## Catalog index

Serve a JSON document from a public HTTPS URL on port 443. Catalog URLs that resolve to loopback, link-local, private, or local-only addresses are rejected. Up to three HTTPS redirects are followed, with each redirect destination checked against the same URL rules.

```json
{
  "version": 1,
  "minimumAppVersion": "0.0.1",
  "maximumAppVersionExclusive": "2.0.0",
  "profiles": [
    {
      "id": "example-converter",
      "version": "1.0.0",
      "name": "Example Converter",
      "description": "Example format support",
      "manifestUrl": "https://downloads.example.org/profiles/example-converter-1.0.0.json",
      "minimumAppVersion": "0.1.0",
      "maximumAppVersionExclusive": "1.0.0"
    }
  ]
}
```

The catalog's application-version range applies to the entire index. Each entry can narrow the supported 101conv release range; incompatible entries are hidden and cannot be installed. The minimum is inclusive and the maximum is exclusive. Version comparison uses numeric major/minor/patch values; suffixes such as `-SNAPSHOT` do not change that release line. Omitted bounds mean unbounded compatibility. IDs and versions use letters, numbers, dots, underscores, and hyphens. The catalog document is limited to 1 MB.

## Profile manifest

The linked manifest must repeat the catalog ID and version, describe the license and package source, provide a ZIP URL and SHA-256, and define the converter's format rules. Its optional 101conv version range can further narrow the catalog entry's range. Every rule must use `{var:executable}` as its first argument. Arguments are passed directly to the process without shell expansion.

```json
{
  "schemaVersion": 1,
  "id": "example-converter",
  "version": "1.0.0",
  "name": "Example Converter",
  "description": "Example format support",
  "author": "Example Maintainers",
  "license": "MIT",
  "homepage": "https://example.org/converter",
  "minimumAppVersion": "0.1.0",
  "maximumAppVersionExclusive": "1.0.0",
  "artifact": {
    "url": "https://downloads.example.org/example-converter-1.0.0-linux-amd64.zip",
    "sha256": "<64 hexadecimal characters>",
    "format": "zip",
    "platform": "linux-amd64",
    "executable": "bin/example-converter"
  },
  "converter": {
    "id": "example-converter",
    "friendlyName": "Example Converter",
    "priority": 100,
    "rules": [
      {
        "sources": ["foo"],
        "targets": ["bar"],
        "command": ["{var:executable}", "--input", "{source}", "--output", "{target}"]
      }
    ]
  }
}
```

Package formats currently accepted are `zip` and `tar.xz`. Archives are streamed to disk, checksum-verified before extraction, and unpacked with strict path, link, entry-count, and expanded-size checks. A profile is built for the server platform; the first official package targets Linux x86-64.

The app accepts packages up to 512 MB, at most 10,000 ZIP entries, and expanded contents up to 2 GB. Paths that escape the package are rejected. The selected executable must exist in the extracted package and cannot be a `#!` script. Installer scripts are never run. The installed converter itself is still third-party software and runs when a conversion invokes it; only install profiles from sources you trust. Licenses remain the operator's responsibility.

The built-in catalog is hosted at `https://raw.githubusercontent.com/Timisah/101conv/main/catalog/index.json` and can be overridden with the `APP_PROFILES_CATALOG_URL` environment variable (Spring property `app.profiles.catalog-url`). The first official entry is a pinned BtbN FFmpeg LGPL build. 101conv distributes its catalog metadata and downloads the binary archive directly from the upstream GitHub release; it does not mirror or bundle the converter. The upstream checksum file supplies the expected SHA-256. An administrator reviews the manifest, source, license, and checksum in the UI and explicitly confirms installation. SHA-256 detects package mismatch; it does not establish who published a manifest. Signed catalog metadata remain future work. Remove an added community catalog by deleting its entry from `profile-catalogs.json` under the persistent app data directory; installed profiles are stored separately under `profiles/` and their generated converter definitions in `converter-configs/`.

### First official profile: FFmpeg LGPL on Linux x86-64

This profile uses BtbN's immutable `autobuild-2026-10-15-13-03` release asset rather than a floating `latest` URL. It is the static Linux x86-64 `lgpl` variant, not the `gpl` or `nonfree` build. Its included license file identifies LGPL version 3; the profile links to the upstream package, preserves `LICENSE.txt`, and discloses upstream dependency notices. The selected build excludes GPL-only x264 and x265 encoders. The profile supports common audio and video container conversions through FFmpeg's default codecs. Operators remain responsible for their local licensing and patent obligations.

## Admin API

All profile management endpoints require an authenticated administrator session:

- `GET /api/v1/profiles` lists catalog sources and installed profiles.
- `POST /api/v1/profiles/catalogs` with `{"url":"https://…"}` saves a catalog source.
- `GET /api/v1/profiles/catalogs/{sourceId}` fetches its profile list.
- `GET /api/v1/profiles/catalogs/{sourceId}/{profileId}/{version}` retrieves details for review.
- `POST /api/v1/profiles/catalogs/{sourceId}/{profileId}/{version}/install` verifies and installs the package, then refreshes the active converter registry.
