# Full backup and isolated restore test

Run from a host with Docker, Python 3, OpenSSL, and access to the same Docker daemon as the stack. The destination must be a trusted filesystem outside the live Go data directory. The backup includes personal files, credentials in management state, and a PostgreSQL dump; restrict access and encrypt it before off-host transfer.

```sh
./scripts/backup.sh /path/to/private/backups 101conv-go /path/to/secure/deployment.env
./scripts/verify-backup.sh /path/to/private/backups/101conv-backup-YYYYMMDDTHHMMSSZ
./scripts/test-restore.sh /path/to/private/backups/101conv-backup-YYYYMMDDTHHMMSSZ
```

Pass the actual Compose project name as the second backup argument for a Portainer stack (often `101conv`). The script locates containers by Compose labels, stops web/Core/Worker to freeze file writers, keeps PostgreSQL running for `pg_dump`, archives Go data, uploads, and results, writes SHA-256 checksums, verifies all artifacts, and restarts the stopped containers even if backup fails. `compose.yaml` is included from the repository checkout. If the deployment uses a local `.env`, it is included automatically; otherwise pass an exported environment file as the third argument. Portainer keeps stack variables in its own database, so the script cannot retrieve them through Docker. Export the complete environment table into a protected file before backup, including database password, bootstrap/admin credential settings, data paths, image tags, and public port. Mark that file mode `0600`, store the backup on a trusted filesystem with restricted access, and encrypt it before off-host transfer. The manifest states which configuration files are included and explicitly records that Portainer variables are not included automatically.

The backup pauses the application writers. `docker stop` uses the runtime's normal bounded shutdown timeout; if a container cannot stop, the backup aborts and attempts to restart anything it stopped. Expect a service interruption and verify the stack becomes healthy again afterward. For Portainer, use the stack's exact Compose project identity (shown in Compose labels); do not assume its display name is the project name. Do not prune volumes.

`verify-backup.sh` validates checksums, archive readability, and the database dump format. `test-restore.sh` restores to temporary Docker volumes and a PostgreSQL container with no published port or Docker network, checks referenced original/result files, and removes the test resources. It never writes to the live stack. This is not yet an application-level restore smoke test: it does not boot Core/Worker or run a conversion. It also does not prove that every converter binary can execute on another CPU/OS. A new host restore requires the correct Compose project, the included Compose file, exported Portainer variables or `.env`, compatible image versions, and preserved volume mapping. First restore into a separately named, isolated stack and verify accounts, installed profiles/engines, file references, and an actual conversion before considering a production cutover. Never unpack over a running instance.

The current tooling produces full backups only. Automatic scheduling, retention of backup sets, encryption, and a guided production restore are future work.
