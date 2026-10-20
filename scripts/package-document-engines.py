#!/usr/bin/env python3
"""Rebuild small deterministic managed adapters; never download runtime binaries.

The pinned Debian runtime is shipped in Dockerfile.worker. These packages expose
it to the existing managed-engine lifecycle, with runtime revision checks.
"""
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parents[1]
for name, version, executable, capability, license_name in [
    ('libreoffice', '25.2.3+deb13u8.1', 'office-convert', 'document.pdf', 'MPL-2.0; some components LGPL-3.0-or-later'),
    ('poppler', '25.3.0+deb13u4.1', 'pdf-convert', 'pdf.process', 'GPL-2.0-only OR GPL-3.0-only; contributions GPL-2.0-or-later'),
]:
    archive = io.BytesIO()
    with gzip.GzipFile(fileobj=archive, mode='wb', filename='', mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as tar:
            for member, mode in [(executable, 0o755), ('LICENSE', 0o644)]:
                data = (root / 'engines/document-tools' / member).read_bytes()
                info = tarfile.TarInfo(member)
                info.size, info.mode, info.mtime = len(data), mode, 0
                tar.addfile(info, io.BytesIO(data))
    data = archive.getvalue()
    filename = f'{name}-{version}-linux-amd64.tar.gz'
    (root / 'catalog/targets/packages' / filename).write_bytes(data)
    manifest = {'engine': {
        'schemaVersion': 1, 'id': name, 'version': version,
        'name': f'{dict(libreoffice="LibreOffice", poppler="Poppler")[name]} document runtime (Linux x86-64)',
        'description': 'Managed adapter for the exact Debian runtime in the document Worker image. Requires Dockerfile.worker; installation does not download a system package.',
        'license': license_name + '; adapter: MIT; dependency notices in the Worker /usr/share/doc.',
        'platform': 'linux-amd64', 'capabilities': [capability],
        'executables': {executable: executable},
        'artifact': {'url': 'packages/' + filename, 'sizeBytes': len(data),
                     'sha256': hashlib.sha256(data).hexdigest(), 'format': 'tar.gz'},
    }}
    (root / f'catalog/targets/engines/{name}-linux-amd64.json').write_text(json.dumps(manifest, indent=2) + '\n')
