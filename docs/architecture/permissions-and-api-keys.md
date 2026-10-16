# Accounts, groups, permissions, quotas, and API keys

The Go Core is authoritative for access and quota decisions. React controls only how administrators and users interact with that policy.

## Policy model

The deployment administrator authenticates with `ENGINE_ADMIN_TOKEN`. Administrators create local accounts; public registration is not exposed. Reusable roles grant permissions. A user may belong to multiple named groups; each group may reference a role and add explicit allow/deny rules. Grants from roles and groups combine, but a deny in any group wins over other inherited grants. An individual allow/deny override is highest priority. The user inspector lists effective permission values and the contributing role, group, and user override sources.

Permission names are stable API identifiers:

- `conversion.use`, `compression.use`
- `jobs.read`, `jobs.cancel`
- `converters.manage`, `repositories.manage`
- `users.manage`, `groups.manage`, `roles.manage`
- `api_keys.manage`

Every protected API request checks the current effective permissions. Sessions and API keys do not cache an authorization grant: changes to group membership, role policy, user overrides, or enabled state take effect on the next request. API keys cannot administer the instance.

## Quotas

Each account and group has independent max-file bytes, monthly job count, and monthly uploaded bytes settings. Each field has an explicit `limited`, `unlimited`, or `inherit` mode; zero is never used as the new model's sentinel. An individual limited/unlimited setting overrides group settings. If the account inherits, the lowest limited value across its groups wins; absent a limited value, any unlimited group setting grants unlimited; otherwise the instance account default applies. Current instance upload/output/Worker safety bounds remain hard ceilings independent of account quotas.

Monthly uploads are charged when persisted, including abandoned uploads. Jobs are charged when submitted, including failed or canceled work. Both values reset on the first day of the UTC month. PostgreSQL advisory transaction locks serialize quota reservations per user, preventing concurrent requests from exceeding limits.

## API keys

Users create or revoke keys under Account & API keys. A key is a high-entropy bearer secret with the `101c_` prefix. Its raw value is shown once; PostgreSQL stores only a SHA-256 digest. Scopes include upload, conversion submission, compression submission, job reads/cancellation, and result downloads. Scopes intersect with the owner's current permissions and quotas. Disabling an account revokes its keys while preserving job ownership and history.

## API and migration

The OpenAPI contract is [`../api/v1/openapi.yaml`](../api/v1/openapi.yaml). Account, bulk update, group/membership, and role operations are under `/api/v1/users`, `/api/v1/groups`, and `/api/v1/roles`. A delegated `users.manage` account can list accounts, create a restricted custom account with bounded default quotas, and enable/disable other users. Assigning roles or groups, changing permission/quota overrides, and resetting passwords require an administrator. Delegated group/role managers cannot change policies or memberships that affect their own account. Bulk requests return successful user IDs plus per-user failures; each user update and its audit event is transactional. The operation is intentionally partial-success across users, so a failure does not undo unrelated successful account changes.

Additive PostgreSQL migrations retain old account columns and backfill their conversion/history flags and numeric quota values into permission overrides and explicit quota modes. A previous zero monthly limit is migrated as unlimited; a positive value becomes limited. Existing account/job ownership and history are retained. Account disablement is soft and does not delete jobs or uploaded/result files.

Administrative changes are recorded in `conv_audit_events`. The first version does not expose an audit search/export screen, expiring API keys, concurrent-job limits, or per-profile entitlements.
