# Full backup and isolated restore test

Run from a host with Docker, Python 3, OpenSSL, and access to the same Docker daemon as the stack. The destination must be a trusted filesystem outside the live Go data directory. The backup includes personal files, credentials in management state, and a PostgreSQL dump; restrict access and encrypt it before off-host transfer.

```sh
./scripts/backup.sh /path/to/private/backups 101conv-go
./scripts/verify-backup.sh /path/to/private/backups/101conv-backup-YYYYMMDDTHHMMSSZ
./scripts/test-restore.sh /path/to/private/backups/101conv-backup-YYYYMMDDTHHMMSSZ
```

Pass the actual Compose project name as the second backup argument for a Portainer stack (often `101conv`). The script locates containers by Compose labels, stops web/Core/Worker to freeze file writers, keeps PostgreSQL running for `pg_dump`, archives Go data, uploads, and results, writes SHA-256 checksums, verifies all artifacts, and restarts the stopped containers even if backup fails. Expect a service interruption while the backup runs. Do not prune volumes. Check that the stack becomes healthy again afterward.

`verify-backup.sh` validates checksums, archive readability, and the database dump format. `test-restore.sh` restores to temporary Docker volumes and a PostgreSQL container with no published port or Docker network, checks referenced original/result files, and removes the test resources. It never writes to the live stack. It does not prove that every converter binary can execute on another CPU/OS, or test a production cutover. A real restore must use a stopped, separate deployment and preserve its `.env`/Compose secrets; never unpack over a running instance.

The current tooling produces full backups only. Automatic scheduling, retention of backup sets, encryption, and a guided production restore are future work.
