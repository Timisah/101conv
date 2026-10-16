# Local accounts, permissions, quotas, and API keys

101conv keeps one deployment administrator and adds ordinary user accounts for conversion. The administrator remains the trusted operator of the instance; user accounts cannot install engines, edit profiles, or change repositories.

## Account model

- `ENGINE_ADMIN_TOKEN` is still the global administrator credential. Existing integrations keep working; it is never sent to the browser. Use it only in trusted automation and keep it in a server-side secret store.
- An administrator creates regular accounts through Administration. There is no public registration route. Passwords must be 12–256 characters and are stored as Argon2id hashes with random salts.
- A regular account has two independent permissions: `canConvert` and `canReadJobs`. Disabling an account rejects its sessions and revokes all its API keys. Deleting through the API is deliberately a disable operation so historical jobs and ownership data are retained.
- Regular users can see only their own jobs, attempts, and results. An administrator can see all jobs. Existing records are migrated to the legacy administrator owner so current history remains accessible.

## Quotas

Administrators can set a per-file byte limit (up to the instance's 100 MiB maximum), a monthly conversion count, and monthly uploaded input bytes. A zero monthly limit means unlimited. The Administration form starts new accounts at 100 conversions and 5 GiB of uploaded data per month.

Uploaded bytes are charged when the upload is durably accepted, including files that are not later submitted. This reserves storage usage early and prevents users bypassing the data allowance with abandoned uploads. Conversion count is charged transactionally when a job is submitted; failed and canceled jobs still count. Both counters reset on the first day of each UTC month. Concurrent requests serialize quota checks per account in PostgreSQL, so parallel uploads or submissions cannot overspend the allowance.

## API keys

Users create and revoke keys under Account & API keys. A key is a high-entropy bearer secret with a `101c_` prefix. The raw value is returned once; the database keeps only its SHA-256 digest. Each key gets a chosen subset of these scopes:

- `conversion:upload`
- `conversion:submit`
- `jobs:read`
- `jobs:cancel`
- `results:download`

Scopes intersect with the account permissions and quotas. Disabling conversion or history access therefore also blocks older keys. Keys cannot perform administrator operations. Keys are revocable immediately, but currently have no built-in expiry date; users should rotate them when their integration's credential store supports rotation.

An external client can use its own user's key:

```sh
curl -H "Authorization: Bearer $CONVERTER_API_KEY" \
  -F "file=@input.png" \
  https://converter.example/api/v1/uploads
```

The upload response supplies `id`. Submit that ID with an installed `profileId`, `targetFormat`, and unique `idempotencyKey` to `POST /api/v1/jobs`; use the same key on retry. Poll `GET /api/v1/jobs/{id}` and download `GET /api/v1/jobs/{id}/result`. Keys can be limited to the endpoints an integration needs. A shared administrator integration may continue to use `ENGINE_ADMIN_TOKEN`, which has full privileges and bypasses user quotas.

## API and persistence

The API is versioned under `/api/v1` and described in [`../api/v1/openapi.yaml`](../api/v1/openapi.yaml). User administration uses `GET/POST /users`, `PATCH/DELETE /users/{id}`. `DELETE` disables the account and revokes its keys. A user inspects their account and usage at `GET /me`, manages keys at `/me/api-keys`, and changes their password at `/me/password`. Key creation/list/revocation requires an interactive password-backed browser session; a leaked API key cannot manage the account's other credentials.

On Core startup, additive PostgreSQL migrations create `conv_users` and `conv_api_keys`, then add owner IDs to the existing upload/job tables with `admin` as the default. The configured database and existing job rows are preserved. No Compose environment variable or manual data-path migration is required. If `DATABASE_URL` is not configured, account management remains unavailable, as do durable jobs.

## Current boundaries

This is a deliberately small role model: one administrator and regular users. There are no teams, custom roles, profile-specific entitlements, concurrent-job caps, external identity providers, API key expiry, or usage exports yet. Browser sessions are process-local and expire after 12 hours; users sign in again after a Core restart. Password reset changes the stored password but does not yet revoke other active browser sessions. The reverse proxy should serve the UI and API over HTTPS whenever credentials travel across an untrusted network.
