# Official catalog trust-root ceremony

This runbook is an owner-controlled release operation. It is intentionally not executed by CI or an AI agent. No private key belongs in Git, the application image, `DATA_PATH`, a chat transcript, or ordinary build logs.

## Prepare and verify

1. Choose a trusted, isolated release workstation and an encrypted offline backup location. Confirm the repository checkout and target catalog commit with a second maintainer.
2. Create a new empty key directory outside the repository with mode `0700`. Do not reuse a directory containing unrelated keys.
3. Run the repository's explicit initializer from the release workstation:

   ```sh
   mkdir -p /secure/101conv-tuf
   chmod 700 /secure/101conv-tuf
   cd server
   go run ./cmd/catalog-repo init \
     --keys-dir /secure/101conv-tuf \
     --root-file ../catalog/metadata/root.json \
     --embed-root internal/repositorymanager/official-root.json
   ```

4. Record the printed SHA-256 root fingerprint through an independent trusted channel. A second maintainer must compare the fingerprint and the root metadata before publication.
5. Move the root private key to offline custody. Keep online signing keys in the approved restricted signing location, with access and recovery recorded separately.

## Validate and publish

Run these checks against the same commit before signing:

```sh
python3 scripts/sync-compatibility-catalog.py --check
cd server
go run ./cmd/catalog-repo validate --catalog-root ../catalog
go test ./cmd/catalog-repo ./internal/catalog ./internal/profile ./internal/repositorymanager
```

Publish only from the approved signing workstation:

```sh
cd server
go run ./cmd/catalog-repo publish \
  --catalog-root ../catalog \
  --keys-dir /secure/101conv-tuf
```

Commit `catalog/metadata/` and `catalog/targets/` atomically. Publish the matching public root and metadata through the approved repository channel. Do not copy private keys into the checkout or deployment host.

## Rotation and recovery

- Rotate online targets/snapshot/timestamp keys on the documented schedule and after suspected compromise.
- Use TUF root rotation for root-key replacement; require independent fingerprint verification and the configured threshold before accepting a new root.
- Retain the previous trusted root, package metadata, and compatible target files long enough for the oldest supported client to update safely.
- Rehearse recovery on an isolated checkout: verify the root fingerprint, refresh metadata, reject stale/expired metadata, and install a known test package only after signature and artifact digest checks pass.
