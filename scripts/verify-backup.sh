#!/usr/bin/env bash
set -euo pipefail
[[ $# -eq 1 ]] || { echo 'Usage: scripts/verify-backup.sh BACKUP_DIRECTORY' >&2; exit 2; }
backup_dir="$(realpath "$1")"
BACKUP_DIR="$backup_dir" python3 - <<'PY'
import hashlib,json,os,sys
from pathlib import Path
root=Path(os.environ['BACKUP_DIR'])
manifest=json.loads((root/'manifest.json').read_text())
required={'postgres.dump','go-data.tar','uploads.tar','results.tar'}
allowed=required|{'compose.yaml','deployment.env'}
listed=set(manifest.get('files',{}))
if manifest.get('application')!='101conv' or manifest.get('formatVersion')!=1 or manifest.get('scope')!='full' or not required.issubset(listed) or not listed.issubset(allowed):
    sys.exit('Unsupported or incomplete backup manifest')
if manifest.get('environmentFileIncluded') != ('deployment.env' in listed) or manifest.get('deploymentConfigIncluded') != ('compose.yaml' in listed):
    sys.exit('Deployment configuration flags do not match the manifest')
for name,entry in manifest['files'].items():
    path=root/name
    digest=hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''):
            digest.update(block)
    if digest.hexdigest()!=entry['sha256'] or path.stat().st_size!=entry['bytes']:
        sys.exit(f'Checksum or size mismatch: {name}')
    if name in ('deployment.env','compose.yaml') and path.stat().st_mode & 0o077:
        sys.exit(f'Sensitive deployment file has overly broad permissions: {name}')
PY
for archive in go-data.tar uploads.tar results.tar; do
  tar -tf "$backup_dir/$archive" >/dev/null
done
docker run --rm -i --network none --entrypoint pg_restore postgres:17-alpine --list < "$backup_dir/postgres.dump" >/dev/null
echo 'Backup checksums, archives, and PostgreSQL dump verified.'
