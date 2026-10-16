# Deploying with Portainer

The repository includes a Dockerfile and a Compose stack. Portainer can clone the Git repository and build the image itself; you do not need to build and push an image registry first.

## Before opening Portainer

The current checkout changes must be committed and pushed to a Git remote before Portainer can see them. Portainer deploys a remote Git ref, not files that only exist in this workspace. Use the repository's `main` branch after the container files are present in that branch.

## Create the stack

In Portainer, open **Stacks → Add stack → Repository** and enter:

- Repository URL: `https://github.com/Timisah/101conv`
- Repository reference: `refs/heads/main`
- Compose path: `compose.yaml`
- Authentication: configure repository credentials or a deploy key if the repository is private.

Leave the `PORT` stack variable unset to run and publish the app on port **5656**. To use another port, add a stack environment variable such as `PORT=8080`; it changes both the app's listening port and the published host port. Then deploy the stack. Portainer builds from the repository's Dockerfile and starts the service.

### Choose where persistent data lives

The stack stores its data in a folder on the **Docker host** (the machine managed by this Portainer endpoint), mounted inside the container at `/var/lib/101conv`. The H2 database, uploads, user settings, converter configuration, and job files remain in that host folder when the container is replaced. At startup, the app creates a `tmp` subfolder inside the data folder for Tomcat and upload temporary files.

In the Portainer stack editor, add a stack environment variable named `DATA_PATH` and set it to your chosen folder's absolute path on the Docker host, for example `/mnt/storage/apps/101conv`. The path must be on the machine managed by the Portainer endpoint, not inside the app container or on your browser's computer. `DATA_PATH` is required; the stack does not assume a particular host folder.

Before deploying, create your chosen folder on the Docker host and give it to the container's unprivileged user (UID/GID `10001`). For example, if you set `DATA_PATH=/mnt/storage/apps/101conv`:

```sh
sudo mkdir -p /mnt/storage/apps/101conv
sudo chown 10001:10001 /mnt/storage/apps/101conv
```

The folder must already exist; the stack deliberately fails instead of silently creating an unexpected host folder. If your host uses a different UID mapping or filesystem permission model, make sure UID `10001` can write to the selected folder.

Open `http://<docker-host-address>:5656/`, or use the host port you selected.

The `PORT` variable controls both sides of the port mapping. For local Docker Compose, create and assign a local data folder first, then start the stack:

```sh
mkdir -p ./101conv-data
sudo chown 10001:10001 ./101conv-data
DATA_PATH=./101conv-data docker compose up -d --build
```

Set a custom port with `PORT=8080 DATA_PATH=./101conv-data docker compose up -d --build`.

## Current deployment limits

This first stack runs the app and job worker in one container and uses file-backed H2. It is suitable for a single-node test deployment. It does not yet provide PostgreSQL, separate Core/Worker services, automated backups, volume quotas, or an OS-level converter sandbox. Back up the configured host data folder before upgrades or host maintenance.

The image does not bundle converter executables. Sign in as an administrator, open **Profiles**, choose **Browse Official**, review a profile's package, checksum, platform and license, then install it. The 101conv Official Catalog currently offers FFmpeg and JPEG XL for Linux x86-64. Their pinned upstream packages are downloaded only when you choose to install them. The container includes `xz-utils` to unpack the official JPEG XL release.
